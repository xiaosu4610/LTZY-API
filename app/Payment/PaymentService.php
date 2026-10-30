<?php
declare(strict_types=1);

namespace Ltzy\Payment;

use Ltzy\Db\Connection;
use Ltzy\Support\Logger;

/**
 * 充值支付 —— 易支付（epay）协议。
 *
 * ── 协议要点 ──
 *
 * 下单：POST {gateway}/mapi.php
 *   pid / type / out_trade_no / notify_url / return_url
 *   name / money / sign / sign_type=MD5
 *   返回 {"code":1,"trade_no":"...","payurl":"..."}
 *
 * 回调：网关 GET 或 POST 到 notify_url
 *   pid / trade_no / out_trade_no / type / name / money
 *   trade_status=TRADE_SUCCESS / sign / sign_type
 *   我方回 "success"（纯文本）表示已收到，否则网关会一直重发
 *
 * ── 签名 ──
 *
 * 除 sign / sign_type 与空值外，全部参数按参数名 ASCII 升序拼成
 * k=v&k=v，末尾直接拼商户密钥，MD5 取小写。业界叫「V1 MD5 签名」。
 *
 * ── 幂等 ──
 *
 * 网关是「没收到 success 就重发」，同一笔单可能撞进来 N 次。
 * 用 `UPDATE ... WHERE status = 0` 让第二次影响 0 行 —— 天然幂等，
 * 不需要加锁，也不会重复加余额。
 */
final class PaymentService
{
    public function __construct(
        private readonly Connection $db,
        private readonly array $cfg,
    ) {
    }

    /**
     * 计算签名。
     *
     * 注意：$params 里值为空串的必须跳过 —— 网关那边也会跳过，
     * 不跳会导致「我方算出 A，网关算出 B」，表现为验签永远失败。
     */
    public static function sign(array $params, string $key): string
    {
        ksort($params, SORT_STRING);

        $pairs = [];
        foreach ($params as $k => $v) {
            if ($k === 'sign' || $k === 'sign_type') {
                continue;
            }
            // 下划线开头的是我们内部加的审计字段（如 _ip），
            // 网关那边不存在，参与计算会导致验签永远失败。
            if (str_starts_with((string) $k, '_')) {
                continue;
            }
            if ($v === '' || $v === null || is_array($v)) {
                continue;
            }
            $pairs[] = $k . '=' . $v;
        }

        return md5(implode('&', $pairs) . $key);
    }

    /** 校验签名（大小写不敏感比较，因为不同网关实现大小写不一致） */
    public function verify(array $params): bool
    {
        $given = strtolower(trim((string) ($params['sign'] ?? '')));
        if ($given === '') {
            return false;
        }
        $want = self::sign($params, (string) ($this->cfg['key'] ?? ''));

        return hash_equals($want, $given);
    }

    /**
     * 金额字符串转「分」。
     *
     * 全程字符串处理，不碰浮点 —— "0.1+0.2" 那套误差在钱上是不可接受的。
     *
     * ⚠️ 2026-10-29 安全测试中发现的实际缺陷：
     *   原正则要求小数位严格 1–2 位（`\.\d{1,2}`），于是 "0.010000" 这类
     *   「补零到多位」的金额会被判为非法，返回 -1，进而触发
     *   payment.amount_mismatch —— **用户实际付款成功但余额不入账**。
     *   数据库 numeric 列直接转 text 就会产出这种字符串，
     *   上游网关若也做同样的格式化，就是真实的丢单事故。
     *
     * 现在的规则（安全性与兼容性兼顾）：
     *   1. 整数部分最多 9 位（不超过 1 亿分，足够任何充值场景）
     *   2. 小数部分允许 1–6 位，但**第 3 位起必须全为 0**
     *      —— "0.0100" / "0.010000" 放行（等价 1 分）
     *      —— "0.019" 拒绝（1.9 分不是合法金额，绝不能四舍五入蒙混）
     *   3. 负数、空串、科学计数法、千分位一律拒绝
     * 任何一步不符就返回 -1，由调用方按「验签失败同级」处理，不入账。
     */
    public static function toCents(string $money): int
    {
        $money = trim($money);
        if ($money === '') {
            return -1;
        }
        // 小数位上限放到 20：PostgreSQL 的 numeric 转 text 会带满小数位
        // （实测 amount_cents/100.0::text → "0.01000000000000000000"），
        // 限制得太死会误杀合法金额。位数长不影响安全 —— 关键是下面
        // 「第 3 位起必须全 0」这条，它保证不会因截断而多算钱。
        if (!preg_match('/^(\d{1,9})(?:\.(\d{1,20}))?$/', $money, $m)) {
            return -1;
        }

        $int  = (int) $m[1];
        $frac = $m[2] ?? '';

        // 第 3 位及之后必须全 0，否则说明精度超出「分」，拒绝
        $extra = substr($frac, 2);
        if ($extra !== '' && trim($extra, '0') !== '') {
            return -1;
        }

        return $int * 100 + (int) substr(str_pad($frac, 2, '0'), 0, 2);
    }

    // ── 下单 ─────────────────────────────────────────────

    /**
     * 创建充值订单并拿到支付链接。
     *
     * @return array{ok:bool, order_no?:string, payurl?:string, qrcode?:string, message?:string}
     */
    public function createOrder(int $userId, int $amountCents, string $payType, string $clientIp): array
    {
        $p = $this->cfg;

        // 安全评估 L-5：前端若带空白（"  wxpay  "）会被误判为不支持。
        // 这本身不是安全漏洞，但白名单校验前必须先归一化。
        $payType = strtolower(trim($payType));

        if (!$p['enabled']) {
            return ['ok' => false, 'message' => '充值通道未开启'];
        }
        if ($p['pid'] === '' || $p['key'] === '') {
            Logger::error('payment.not_configured', ['has_pid' => $p['pid'] !== '']);

            return ['ok' => false, 'message' => '支付配置缺失，请联系管理员'];
        }
        if (!in_array($payType, $p['pay_types'], true)) {
            return ['ok' => false, 'message' => '不支持的支付方式'];
        }

        // 最小 0.01 元（1 分）。配置项是浮点元，round 一次转成分。
        // 这里用 round 而不是 (int) 强转：(int)(0.01*100) 在某些浮点
        // 表示下会得到 0，直接把合法的 1 分判成非法金额。
        $minCents = (int) round(((float) $p['min_amount']) * 100);
        $maxCents = (int) round(((float) $p['max_amount']) * 100);
        if ($amountCents < $minCents || $amountCents > $maxCents) {
            $minYuan = number_format($minCents / 100, 2, '.', '');
            $maxYuan = number_format($maxCents / 100, 0, '.', '');

            return ['ok' => false, 'message' => "充值金额需在 {$minYuan} ~ {$maxYuan} 元之间"];
        }

        $rate      = (int) $p['rate'];
        $quota     = intdiv($amountCents * $rate, 100);
        $orderNo   = $this->newOrderNo();
        $expireMin = (int) $p['expire_minutes'];
        // 在 PHP 侧算好过期时间再传参 —— 不依赖 PG 的 make_interval() 命名参数
        // 语法，那个在 PDO 占位符下容易踩坑。
        $expireAt  = date('Y-m-d H:i:sP', time() + $expireMin * 60);

        // 先落库再调网关：如果网关超时但我们其实已经下单成功，
        // 至少本地有记录，回调来了能对上。
        $this->db->execute(
            'INSERT INTO recharge_orders
                (order_no, user_id, amount_cents, quota, pay_type, status, client_ip, expire_at)
             VALUES (?, ?, ?, ?, ?, 0, ?, ?)',
            [$orderNo, $userId, $amountCents, $quota, $payType, $clientIp, $expireAt]
        );

        $payload = [
            'pid'          => $p['pid'],
            'type'         => $payType,
            'out_trade_no' => $orderNo,
            'notify_url'   => $p['notify_url'] !== '' ? $p['notify_url'] : ($p['site_url'] ?? '') . '/api/recharge/notify',
            'return_url'   => $p['return_url'] !== '' ? $p['return_url'] : ($p['site_url'] ?? '') . '/console.html?tab=recharge',
            'name'         => '账户余额充值',
            'money'        => number_format($amountCents / 100, 2, '.', ''),

            // 实测（2026-10-29）：xnoo.cn 的 mapi.php 强制要求 clientip，
            // 不传直接回「用户IP地址(clientip)不能为空」。标准 epay 文档里
            // 这个字段是可选的，所以只能靠实打实的下单才能发现。
            // 它也参与签名（非空参数全参与）。
            'clientip'     => $clientIp !== '' ? $clientIp : '0.0.0.0',
        ];
        $payload['sign']      = self::sign($payload, (string) $p['key']);
        $payload['sign_type'] = 'MD5';

        $raw = $this->httpPost($p['gateway'] . '/mapi.php', $payload);
        if ($raw === null) {
            Logger::warn('payment.gateway_error', ['order_no' => $orderNo, 'reason' => 'http']);

            return ['ok' => false, 'order_no' => $orderNo, 'message' => '支付网关无响应，请稍后再试'];
        }

        $res = json_decode($raw, true);
        if (!is_array($res)) {
            Logger::warn('payment.bad_response', ['order_no' => $orderNo, 'raw' => substr($raw, 0, 200)]);

            return ['ok' => false, 'order_no' => $orderNo, 'message' => '支付网关返回异常'];
        }

        if ((int) ($res['code'] ?? 0) !== 1) {
            $msg = (string) ($res['msg'] ?? '未知错误');
            Logger::warn('payment.create_failed', ['order_no' => $orderNo, 'msg' => $msg]);

            return ['ok' => false, 'order_no' => $orderNo, 'message' => "下单失败：{$msg}"];
        }

        Logger::info('payment.order_created', [
            'order_no' => $orderNo,
            'user_id'  => $userId,
            'cents'    => $amountCents,
            'type'     => $payType,
        ]);

        return [
            'ok'       => true,
            'order_no' => $orderNo,
            'payurl'   => (string) ($res['payurl'] ?? ''),
            'qrcode'   => (string) ($res['qrcode'] ?? ''),
            'quota'    => $quota,
        ];
    }

    // ── 回调 ─────────────────────────────────────────────

    /**
     * 处理异步回调。
     *
     * @return array{ok:bool, message:string, order_no?:string}
     */
    public function handleNotify(array $params): array
    {
        if (!$this->verify($params)) {
            Logger::warn('payment.notify_bad_sign', [
                'out_trade_no' => $params['out_trade_no'] ?? '',
                'ip'           => $params['_ip'] ?? '',
            ]);

            return ['ok' => false, 'message' => 'sign error'];
        }

        if (($params['trade_status'] ?? '') !== 'TRADE_SUCCESS') {
            // 只有成功状态才入账。其它状态（WAIT_BUYER_PAY 等）直接确认收到即可。
            return ['ok' => true, 'message' => 'ignored'];
        }

        $outTradeNo = (string) ($params['out_trade_no'] ?? '');
        if ($outTradeNo === '') {
            return ['ok' => false, 'message' => 'missing out_trade_no'];
        }

        $order = $this->db->first(
            'SELECT order_no, user_id, amount_cents, quota, status FROM recharge_orders WHERE order_no = ?',
            [$outTradeNo]
        );
        if ($order === null) {
            Logger::warn('payment.notify_unknown_order', ['out_trade_no' => $outTradeNo]);

            return ['ok' => false, 'message' => 'order not found'];
        }

        // 金额核对：网关说付了多少，必须与我们下单时一致。
        // 不一致说明有人在改包，或者网关串单了 —— 两种情况都不能入账。
        $paidCents = self::toCents((string) ($params['money'] ?? ''));
        if ($paidCents !== (int) $order['amount_cents']) {
            Logger::error('payment.amount_mismatch', [
                'order_no' => $outTradeNo,
                'expect'   => (int) $order['amount_cents'],
                'paid'     => $paidCents,
            ]);

            return ['ok' => false, 'message' => 'amount mismatch'];
        }

        $raw    = json_encode($params, JSON_UNESCAPED_UNICODE);
        $quota  = (int) $order['quota'];
        $userId = (int) $order['user_id'];

        // 幂等：只更新 status=0 的行。第二次回调 rowCount 为 0，直接跳过加余额。
        $done = $this->db->transaction(function (\PDO $pdo) use ($outTradeNo, $params, $raw, $quota, $userId) {
            $upd = $pdo->prepare(
                'UPDATE recharge_orders
                    SET status = 1, trade_no = ?, notify_raw = ?, paid_at = now()
                  WHERE order_no = ? AND status = 0'
            );
            $upd->execute([(string) ($params['trade_no'] ?? ''), $raw, $outTradeNo]);

            if ($upd->rowCount() === 0) {
                return false;
            }

            $pdo->prepare('UPDATE users SET quota = quota + ? WHERE id = ?')
                ->execute([$quota, $userId]);

            return true;
        });

        if ($done) {
            Logger::info('payment.paid', [
                'order_no' => $outTradeNo,
                'user_id'  => $userId,
                'quota'    => $quota,
            ]);
        }

        return ['ok' => true, 'message' => 'success', 'order_no' => $outTradeNo];
    }

    // ── 查询 ─────────────────────────────────────────────

    public function orders(int $userId, int $limit = 20, int $offset = 0): array
    {
        $limit  = max(1, min(100, $limit));
        $offset = max(0, $offset);

        return $this->db->query(
            'SELECT order_no, amount_cents, quota, pay_type, status, created_at, paid_at
               FROM recharge_orders
              WHERE user_id = ?
              ORDER BY created_at DESC
              LIMIT ? OFFSET ?',
            [$userId, $limit, $offset]
        );
    }

    public function orderByNo(string $orderNo, int $userId): ?array
    {
        return $this->db->first(
            'SELECT order_no, amount_cents, quota, pay_type, status, created_at, paid_at, expire_at
               FROM recharge_orders
              WHERE order_no = ? AND user_id = ?',
            [$orderNo, $userId]
        );
    }

    /** 关闭超时未支付的订单（由定时器调用） */
    public function closeExpired(): int
    {
        return $this->db->execute(
            'UPDATE recharge_orders SET status = 2 WHERE status = 0 AND expire_at < now()'
        );
    }

    /**
     * 该用户当前未支付的订单数。
     *
     * 安全评估 M-1：下单接口原来没有上限，攻击者可以脚本化刷单 ——
     * 每次都会向支付网关发真实请求，有被风控封商户号的风险。
     * 这里限制「未支付订单堆积量」，配合调用方的频控形成双层防护。
     */
    public function pendingCount(int $userId): int
    {
        return (int) $this->db->scalar(
            'SELECT COUNT(*) FROM recharge_orders WHERE user_id = ? AND status = 0 AND expire_at > now()',
            [$userId]
        );
    }

    // ── 内部 ─────────────────────────────────────────────

    private function newOrderNo(): string
    {
        return 'R' . date('YmdHis') . sprintf('%06d', random_int(0, 999999));
    }

    private function httpPost(string $url, array $data): ?string
    {
        $ch = curl_init($url);
        if ($ch === false) {
            return null;
        }

        curl_setopt_array($ch, [
            CURLOPT_POST           => true,
            CURLOPT_POSTFIELDS     => http_build_query($data),
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_TIMEOUT        => 15,
            CURLOPT_CONNECTTIMEOUT => 8,
            CURLOPT_SSL_VERIFYPEER => true,
            CURLOPT_SSL_VERIFYHOST => 2,
            CURLOPT_FOLLOWLOCATION => false,
            CURLOPT_USERAGENT      => 'llm-gateway/1.0',
        ]);

        $body = curl_exec($ch);
        $err  = curl_error($ch);
        @curl_close($ch);

        if ($body === false) {
            Logger::warn('payment.curl_error', ['url' => $url, 'error' => $err]);

            return null;
        }

        return (string) $body;
    }
}
