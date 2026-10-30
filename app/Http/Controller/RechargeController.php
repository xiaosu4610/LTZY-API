<?php
declare(strict_types=1);

namespace Ltzy\Http\Controller;

use Ltzy\Http\Request;
use Ltzy\Http\Response;
use Ltzy\Payment\PaymentService;
use Ltzy\Security\RateLimiter;
use Ltzy\Support\Ctx;
use Ltzy\Support\Logger;

/**
 * 充值（用户端）。
 *
 * ── 路由 ──
 *
 * GET  /api/recharge/config   充值配置（档位、开关）—— 免登录，前端首屏要拿
 * POST /api/recharge/create   创建订单，返回 payurl —— 需登录
 * GET  /api/recharge/orders   我的充值记录 —— 需登录
 * GET  /api/recharge/notify   支付网关异步回调 —— 免登录，靠验签
 * GET  /api/recharge/return   支付完成同步跳转 —— 免登录，只做展示
 *
 * ── 为什么 notify 不做登录校验 ──
 *
 * 回调是支付机构发起的，它没有我们的用户 token。安全性完全由
 * MD5 签名 + 金额核对 + 订单幂等三道闸提供，不依赖登录态。
 * 反过来，如果给 notify 加登录校验，回调就永远进不来。
 */
final class RechargeController
{
    public function __construct(
        private readonly PaymentService $payment,
        private readonly array $cfg,
        private readonly RateLimiter $limiter,
    ) {
    }

    /** GET /api/recharge/config —— 免登录 */
    public function config(Request $req, Response $resp): void
    {
        $p = $this->cfg;

        $resp->ok([
            'enabled'     => (bool) $p['enabled'],
            'amounts'     => $p['amounts'],
            // ⚠️ 用 float 不要 (int)：最低 0.01 元强转成 int 就是 0，
            // 前端会显示「最低 0 元」。
            'min_amount'  => (float) $p['min_amount'],
            'max_amount'  => (int) $p['max_amount'],
            'pay_types'   => $p['pay_types'],
            'rate'        => (int) $p['rate'],
            // 让前端能显示「充 10 元 = 1000 万额度」，不用把换算逻辑写死两份
            'unit_hint'   => '1 元 = ' . number_format((int) $p['rate']) . ' 额度',
        ]);
    }

    /** POST /api/recharge/create —— 需登录 */
    public function create(Request $req, Response $resp): void
    {
        $userId = Ctx::int('user_id');
        if ($userId <= 0) {
            $resp->fail('请先登录', 401, 'login_required');

            return;
        }

        // ── 下单频控（安全评估 M-1）──
        //
        // 没有这层保护时，8 次连续下单全部成功。危害不在订单表膨胀，
        // 而在于每次下单都会向支付网关发真实请求 —— 被上游风控识别为
        // 异常下单量，可能直接封商户号，充值功能整体不可用，且解封不可控。
        //
        // 三层限制：
        //   ① 单用户 1 分钟最多 5 次（防脚本狂刷）
        //   ② 单用户 1 小时最多 20 次（防持续刷）
        //   ③ 未支付订单最多 3 笔（防堆积占网关额度）
        if (!$this->limiter->allow('recharge:m:' . $userId, 5, 60)) {
            Logger::warn('security.recharge_rate_limited', ['user_id' => $userId, 'window' => '1m']);
            $resp->fail('操作太频繁，请稍后再试', 429, 'rate_limited');

            return;
        }
        if (!$this->limiter->allow('recharge:h:' . $userId, 20, 3600)) {
            Logger::warn('security.recharge_rate_limited', ['user_id' => $userId, 'window' => '1h']);
            $resp->fail('下单次数已达上限，请 1 小时后再试', 429, 'rate_limited');

            return;
        }
        if ($this->payment->pendingCount($userId) >= 3) {
            $resp->fail('你已有多笔未支付订单，请先完成支付或等待订单过期', 429, 'too_many_pending');

            return;
        }

        $body   = $req->json() ?? [];
        $type   = (string) ($body['pay_type'] ?? 'alipay');

        // 金额按字符串处理再转「分」，不经过浮点运算 ——
        // 0.01、0.1、1.10 这些值用 float 相乘会出现 0.9999999 之类的误差，
        // 在钱上是不可接受的。前端传数字或字符串都能接受。
        $amountRaw = $body['amount'] ?? '';
        $amountCents = PaymentService::toCents(
            is_string($amountRaw) ? $amountRaw : (string) $amountRaw
        );

        if ($amountCents < 0) {
            $resp->fail('金额格式不正确', 400, 'bad_amount');

            return;
        }
        if ($amountCents === 0) {
            $resp->fail('请填写充值金额', 400, 'bad_amount');

            return;
        }

        $result = $this->payment->createOrder(
            $userId,
            $amountCents,       // 已经是「分」，不再乘 100
            $type,
            $req->clientIp()
        );

        if (!($result['ok'] ?? false)) {
            $resp->fail((string) ($result['message'] ?? '下单失败'), 400, 'create_failed');

            return;
        }

        $resp->ok([
            'order_no' => $result['order_no'],
            'payurl'   => $result['payurl'],
            'qrcode'   => $result['qrcode'] ?? '',
            'quota'    => $result['quota'] ?? 0,
            'amount'   => $amountCents / 100,
        ], '订单已创建');
    }

    /** GET /api/recharge/orders —— 需登录 */
    public function orders(Request $req, Response $resp): void
    {
        $userId = Ctx::int('user_id');
        if ($userId <= 0) {
            $resp->fail('请先登录', 401, 'login_required');

            return;
        }

        $limit  = (int) ($req->query('limit') ?? 20);
        $offset = (int) ($req->query('offset') ?? 0);

        $rows = $this->payment->orders($userId, $limit, $offset);

        $items = array_map(static function (array $r): array {
            return [
                'order_no'  => $r['order_no'],
                'amount'    => round(((int) $r['amount_cents']) / 100, 2),
                'quota'     => (int) $r['quota'],
                'pay_type'  => $r['pay_type'],
                'status'    => (int) $r['status'],
                'status_cn' => match ((int) $r['status']) {
                    1       => '已到账',
                    2       => '已关闭',
                    default => '待支付',
                },
                'created_at' => $r['created_at'],
                'paid_at'    => $r['paid_at'],
            ];
        }, $rows);

        $resp->ok(['items' => $items, 'total' => count($items)]);
    }

    // ── 网关回调 ─────────────────────────────────────────

    /**
     * GET|POST /api/recharge/notify
     *
     * 易支付要求响应体是纯文本 "success"，不是 JSON。
     * 返回 JSON 会被判为「未确认」，然后网关会一直重发。
     */
    public function notify(Request $req, Response $resp): void
    {
        $params = $this->collectParams($req);
        $params['_ip'] = $req->clientIp();

        $result = $this->payment->handleNotify($params);

        if ($result['ok']) {
            $resp->text('success');

            return;
        }

        Logger::warn('payment.notify_rejected', [
            'reason' => $result['message'],
            'ip'     => $params['_ip'],
        ]);

        // 注意：即使拒绝也要回 200 + fail，不能回 4xx/5xx。
        // 网关看到非 200 会认为是「我们挂了」，按失败重试策略狂重发；
        // 回 "fail" 它知道是业务判定不通过，行为可预期。
        $resp->text('fail');
    }

    /** GET /api/recharge/return —— 用户付完跳回这里 */
    public function returnPage(Request $req, Response $resp): void
    {
        $orderNo = (string) ($req->query('out_trade_no') ?? '');
        $status  = (string) ($req->query('trade_status') ?? '');

        // 这里只做展示，不写库 —— 真正的入账一律以 notify 为准。
        // 原因：return_url 是浏览器跳转，用户可以手动改参数，
        // 拿它当入账依据等于把钱的判断权交给了前端。
        $ok  = $status === 'TRADE_SUCCESS';
        $msg = $ok ? '支付成功，额度已到账（如未刷新请稍候）' : '支付未完成';

        $esc = static fn (string $s): string => htmlspecialchars($s, ENT_QUOTES, 'UTF-8');

        $resp->html(<<<HTML
<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>充值结果 - LTZY API</title>
<style>
  body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
       background:#f5f6f8;font-family:system-ui,-apple-system,"Segoe UI",sans-serif;color:#222}
  .box{background:#fff;border-radius:14px;padding:36px 40px;box-shadow:0 2px 20px rgba(0,0,0,.06);
       text-align:center;max-width:420px}
  .ok{color:#16a34a;font-size:20px;font-weight:600;margin:0 0 10px}
  .no{color:#dc2626;font-size:20px;font-weight:600;margin:0 0 10px}
  p{color:#666;font-size:14px;line-height:1.7;margin:0 0 22px}
  a{display:inline-block;background:#2563eb;color:#fff;text-decoration:none;
    padding:10px 26px;border-radius:8px;font-size:14px}
</style>
</head>
<body>
  <div class="box">
    <p class="{$esc($ok ? 'ok' : 'no')}">{$esc($msg)}</p>
    <p>订单号：{$esc($orderNo)}</p>
    <a href="/console.html?tab=recharge">返回控制台</a>
  </div>
</body>
</html>
HTML);

        Logger::info('payment.return_page', ['order_no' => $orderNo, 'status' => $status]);
    }

    /**
     * 收集回调参数：GET 查询串 + POST 表单都要支持。
     *
     * 不同网关实现不一致，有的用 GET，有的用 POST，
     * 少数还会把参数塞进 JSON body。三种都收。
     */
    private function collectParams(Request $req): array
    {
        // GET 查询串 + POST 表单都要支持：不同网关实现不一致，
        // 有的用 GET，有的用 POST。三种都收，签名算法不关心来源。
        $params = $req->queryAll();

        foreach ($req->postAll() as $k => $v) {
            $params[$k] = $v;
        }

        if ($params === []) {
            $raw = $req->rawBody();
            if ($raw !== '') {
                $json = json_decode($raw, true);
                if (is_array($json)) {
                    foreach ($json as $k => $v) {
                        if (is_scalar($v)) {
                            $params[(string) $k] = (string) $v;
                        }
                    }
                } else {
                    parse_str($raw, $form);
                    foreach ($form as $k => $v) {
                        if (is_string($v)) {
                            $params[(string) $k] = $v;
                        }
                    }
                }
            }
        }

        return $params;
    }
}
