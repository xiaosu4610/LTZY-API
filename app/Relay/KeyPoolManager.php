<?php
declare(strict_types=1);

namespace Ltzy\Relay;

use Ltzy\Domain\Channel\Channel;
use Ltzy\Support\Logger;

/**
 * 密钥池管理器。
 *
 * 每个渠道一个 KeyPool 实例，按渠道 ID 缓存。
 *
 * 为什么池实例要缓存而不是每次新建：KeyPool 内部持有轮询游标和冷却状态，
 * 每次新建就等于每次重置游标（永远只用第一把密钥）。
 */
final class KeyPoolManager
{
    /** @var array<int,KeyPool> */
    private array $pools = [];

    public function __construct(
        private readonly int $cooldownAuthFailed = 1800,
        private readonly int $cooldownRateLimited = 60,
    ) {
    }

    public function for(Channel $channel): KeyPool
    {
        if (!isset($this->pools[$channel->id])) {
            $this->pools[$channel->id] = new KeyPool(
                $channel->id,
                $channel->key,
                $this->cooldownAuthFailed,
                $this->cooldownRateLimited,
            );
            Logger::debug('keypool.created', [
                'channel_id' => $channel->id,
                'keys'       => $this->pools[$channel->id]->count(),
            ]);
        }
        return $this->pools[$channel->id];
    }

    /**
     * 渠道配置变更后要让池失效重建（密钥列表可能变了）。
     */
    public function invalidate(int $channelId): void
    {
        unset($this->pools[$channelId]);
    }

    /**
     * 别名：渠道被禁用时丢弃它的池，下次启用时重建。
     */
    public function forget(int $channelId): void
    {
        $this->invalidate($channelId);
    }

    /**
     * 清理所有池里过期的冷却记录。由定时器周期调用。
     */
    public function gc(): void
    {
        foreach ($this->pools as $pool) {
            $pool->gc();
        }
    }

    /** @return array<int,array> 各渠道池状态，供管理接口展示 */
    public function stats(): array
    {
        $out = [];
        foreach ($this->pools as $id => $pool) {
            $out[$id] = $pool->stats();
        }
        return $out;
    }
}
