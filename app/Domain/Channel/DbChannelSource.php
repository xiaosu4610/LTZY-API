<?php
declare(strict_types=1);

namespace Ltzy\Domain\Channel;

use Ltzy\Db\Connection;
use Ltzy\Support\Logger;

/**
 * 从 PostgreSQL 读取渠道。
 *
 * ── 为什么不每次请求都查库 ──
 *
 * 渠道和模型映射是「配置」，改动频率以天计，而请求频率以秒计。
 * 每请求两次 SQL（查 abilities + 查 channels）纯属浪费，所以这里做进程内缓存。
 *
 * 缓存策略：整份快照 TTL 60 秒。改配置最多 1 分钟生效，
 * 对内部系统完全够用，换来的是热路径零查询。
 *
 * ── 为什么不用 Swoole\Table ──
 *
 * Windows 版 OpenSwoole 没有 Swoole\Table（见 docs/04-环境实测报告）。
 * 但这不是妥协 —— 单进程模型下普通 PHP 数组就是最快的，
 * 而且不需要考虑跨进程一致性问题。
 */
final class DbChannelSource implements ChannelSourceInterface
{
    /** @var array<int, Channel>|null 按 id 索引的渠道快照 */
    private ?array $channels = null;

    /** @var array<string, int[]> 模型 → 渠道 id 列表 */
    private array $modelIndex = [];

    /** @var float 快照生成时间 */
    private float $loadedAt = 0.0;

    public function __construct(
        private readonly Connection $db,
        private readonly int $ttl = 60,
    ) {
    }

    public function findForModel(string $model, string $group = 'default'): array
    {
        $this->ensureFresh();

        $ids = $this->modelIndex[$model] ?? [];
        $out = [];
        foreach ($ids as $id) {
            $ch = $this->channels[$id] ?? null;
            if ($ch === null) {
                continue;
            }
            // 分组过滤：渠道的 grp 是 '*' 时对所有分组开放
            if ($ch->group !== '*' && $ch->group !== $group) {
                continue;
            }
            $out[] = $ch;
        }

        // 按 priority 降序、weight 降序排（数值大的优先）
        usort($out, static fn(Channel $a, Channel $b) => [$b->priority, $b->weight] <=> [$a->priority, $a->weight]);

        return $out;
    }

    public function findById(int $id): ?Channel
    {
        $this->ensureFresh();
        return $this->channels[$id] ?? null;
    }

    /** @return array<int, Channel> */
    public function all(): array
    {
        $this->ensureFresh();
        return $this->channels;
    }

    /**
     * 强制刷新缓存（管理接口改完配置后调用）。
     */
    public function invalidate(): void
    {
        $this->channels = null;
        $this->modelIndex = [];
        $this->loadedAt = 0.0;
    }

    private function ensureFresh(): void
    {
        if ($this->channels !== null && (microtime(true) - $this->loadedAt) < $this->ttl) {
            return;
        }
        $this->load();
    }

    private function load(): void
    {
        $t0 = microtime(true);

        // 一条 JOIN 拿齐渠道和它的模型映射 —— 比"先查渠道再逐个查模型"少 N 次往返。
        // 注意列名是 grp 不是 group：后者是 PG 保留字，建表时已规避。
        $rows = $this->db->query('
            SELECT c.id, c.name, c.type, c.status, c.base_url, c."key", c.grp,
                   c.priority, c.weight, c.model_mapping, c.endpoints, c.responses_mode,
                   a.model AS ability_model, a.category AS ability_category, a.endpoint AS ability_endpoint
            FROM channels c
            LEFT JOIN abilities a ON a.channel_id = c.id AND a.enabled = 1
            WHERE c.status = 1
            ORDER BY c.id, a.model
        ');

        $channels = [];
        $index = [];

        // 因为一条渠道会有多行（每个模型一行），先按 id 分组再构造对象，
        // 避免同一个渠道被 new 多次、以及 KeyPool 缓存失效（KeyPool 是按 id 缓存的，
        // 但如果 Channel 对象每次都是新的，manager 里会不停重建 pool，游标就丢了）。
        $grouped = [];
        foreach ($rows as $r) {
            $id = (int) $r['id'];
            $grouped[$id]['row'] = $r;
            if ($r['ability_model'] !== null && $r['ability_model'] !== '') {
                $model = (string) $r['ability_model'];
                $grouped[$id]['models'][] = $model;
                // 记录每个模型的类别和端点，供上层按类型过滤
                $grouped[$id]['categories'][$model] = (string) ($r['ability_category'] ?? 'chat');
                $grouped[$id]['endpoints'][$model] = (string) ($r['ability_endpoint'] ?? 'chat');
            }
        }

        foreach ($grouped as $id => $g) {
            $r = $g['row'];
            $models = $g['models'] ?? [];

            // models 用 abilities 里的（abilities 才是权威来源，channels.models 只是展示用的冗余）
            $ch = new Channel(
                id: $id,
                name: (string) $r['name'],
                type: (string) $r['type'],
                status: (int) $r['status'],
                baseUrl: (string) $r['base_url'],
                key: (string) $r['key'],
                models: $models,
                mapping: $this->decodeMapping((string) $r['model_mapping']),
                group: (string) $r['grp'],
                priority: (int) $r['priority'],
                weight: (int) $r['weight'],
                endpoints: $this->decodeEndpoints((string) ($r['endpoints'] ?? '')),
                responsesMode: (string) ($r['responses_mode'] ?? 'none'),
                categories: $g['categories'] ?? [],
            );

            $channels[$id] = $ch;
            foreach ($models as $m) {
                $index[$m][] = $id;
            }
        }

        $this->channels = $channels;
        $this->modelIndex = $index;
        $this->loadedAt = microtime(true);

        Logger::info('channel.cache_loaded', [
            'channels'   => count($channels),
            'models'     => count($index),
            'ms'         => (int) ((microtime(true) - $t0) * 1000),
        ]);
    }

    /**
     * @return array<string,string>
     */
    private function decodeMapping(string $json): array
    {
        if ($json === '' || $json === '{}') {
            return [];
        }
        $arr = json_decode($json, true);
        return is_array($arr) ? $arr : [];
    }

    /**
     * 解析 PG 的 text[] 数组字面量：{chat,responses,embeddings}
     *
     * 为什么不直接 json_decode：PG 的数组字面量格式不是 JSON
     * （元素没有引号），硬当 JSON 解析会全军覆没。
     *
     * @return string[]
     */
    private function decodeEndpoints(string $raw): array
    {
        $raw = trim($raw);
        if ($raw === '' || $raw === '{}') {
            return ['chat'];
        }
        $inner = trim($raw, '{}');
        if ($inner === '') {
            return ['chat'];
        }
        $out = [];
        foreach (explode(',', $inner) as $item) {
            $item = trim(trim($item), '"');
            if ($item !== '') {
                $out[] = $item;
            }
        }
        return $out === [] ? ['chat'] : $out;
    }
}
