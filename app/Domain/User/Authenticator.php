<?php
declare(strict_types=1);

namespace Ltzy\Domain\User;

use Ltzy\Support\Ctx;
use Ltzy\Support\Logger;

/**
 * 请求身份解析。
 *
 * 三种身份：
 *   1. 无 Authorization          → 匿名（只能用免费模型）
 *   2. Bearer sk-xxx             → 令牌用户（走 tokens 表）
 *   3. Bearer <access_token>     → 网页会话（走 users.access_token）
 *
 * ── 为什么加缓存 ──
 *
 * 压测发现带鉴权的接口 QPS 只有无鉴权的一半（456 vs 1214）。
 * 根因不是查数据慢（执行 0.2ms），而是 PG 对分区表 JOIN 的
 * **规划时间** 要 4.6ms —— PDO 每次都要重新 prepare，规划一次。
 *
 * 加 5 秒的缓存后，同一 token 的高频访问只有 1/（并发×时长）的比例
 * 真正打到数据库。
 */
final class Authenticator
{
    public function __construct(
        private readonly UserRepository $users,
        private readonly ?AuthCache $cache = null,
    ) {
    }

    /**
     * 解析 Authorization 头，把身份写进 Ctx。
     *
     * @return array{user:?User,token_id:int,anonymous:bool}
     */
    public function resolve(?string $authorization): array
    {
        if ($authorization === null || $authorization === '') {
            return $this->anonymous();
        }

        $token = $this->extractToken($authorization);
        if ($token === '') {
            return $this->anonymous();
        }

        // sk- 前缀走令牌表；否则当网页会话 token 查 users.access_token
        if (str_starts_with($token, 'sk-')) {
            return $this->resolveToken($token);
        }

        return $this->resolveSession($token);
    }

    /**
     * 令牌鉴权（带缓存）。
     */
    private function resolveToken(string $token): array
    {
        // 先查缓存
        $hit = $this->cache?->get($token);
        if ($hit === null) {
            $hit = $this->users->findByTokenKey($token);
            if ($hit === null) {
                Ctx::set('auth_error', 'invalid_token');
                return $this->anonymous();
            }
            if ($hit['user']->isEnabled()) {
                $this->cache?->put($token, $hit);
            }
        }

        $user = $hit['user'];
        if (!$user->isEnabled()) {
            Ctx::set('auth_error', 'user_disabled');
            return $this->anonymous();
        }
        // 令牌限额用完也要拒（remain_quota = 0 表示不限）
        if ($hit['remain_quota'] !== 0 && $hit['remain_quota'] <= 0) {
            Ctx::set('auth_error', 'token_quota_exhausted');
            return $this->anonymous();
        }

        Ctx::set('user_id', $user->id);
        Ctx::set('token_id', $hit['token_id']);
        Ctx::set('group', $user->group);
        Ctx::set('token_models', $hit['models']);
        // 关键：把 User 对象也放进上下文，避免下游再查一次
        Ctx::set('user_obj', $user);

        return ['user' => $user, 'token_id' => $hit['token_id'], 'anonymous' => false];
    }

    /**
     * 网页会话鉴权（带缓存）。
     */
    private function resolveSession(string $token): array
    {
        $hit = $this->cache?->get('sess:' . $token);
        if ($hit === null) {
            $user = $this->users->findByAccessToken($token);
            if ($user === null) {
                Ctx::set('auth_error', 'invalid_session');
                return $this->anonymous();
            }
            if ($user->isEnabled()) {
                $this->cache?->put('sess:' . $token, ['user' => $user, 'token_id' => 0]);
            }
            $hit = ['user' => $user, 'token_id' => 0];
        }

        $user = $hit['user'];
        if (!$user->isEnabled()) {
            Ctx::set('auth_error', 'user_disabled');
            return $this->anonymous();
        }

        Ctx::set('user_id', $user->id);
        Ctx::set('group', $user->group);
        Ctx::set('user_obj', $user);

        return ['user' => $user, 'token_id' => 0, 'anonymous' => false];
    }

    /**
     * 登出时清缓存。
     */
    public function forget(string $authorization): void
    {
        $token = $this->extractToken($authorization);
        if ($token === '') {
            return;
        }
        $this->cache?->forget($token);
        $this->cache?->forget('sess:' . $token);
    }

    /**
     * 清掉某个用户的全部缓存（改密码/停用时用）。
     */
    public function forgetUser(int $userId): void
    {
        $this->cache?->forgetUser($userId);
    }

    private function anonymous(): array
    {
        Ctx::set('user_id', 0);
        Ctx::set('token_id', 0);
        Ctx::set('group', 'default');
        return ['user' => null, 'token_id' => 0, 'anonymous' => true];
    }

    /**
     * 从 Authorization 头取出凭证。
     *
     * 容错：有些客户端只发 token 不带 "Bearer " 前缀。
     */
    private function extractToken(string $header): string
    {
        $header = trim($header);
        if (stripos($header, 'Bearer ') === 0) {
            return trim(substr($header, 7));
        }
        return $header;
    }
}
