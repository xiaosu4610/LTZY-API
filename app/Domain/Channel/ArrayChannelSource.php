<?php
declare(strict_types=1);

namespace Ltzy\Domain\Channel;

/**
 * 文件渠道源。
 *
 * 从 JSON 文件读渠道配置，用于「没有数据库」的场景：
 * 本地快速验证中继链路、或从既有配置批量导入。
 *
 * JSON 结构：
 * {
 *   "channels": [
 *     {
 *       "id": 1, "name": "我的上游", "base_url": "https://api.example.com",
 *       "status": 1, "keys": ["sk-xxx"], "models": ["model-a"],
 *       "model_mapping": "", "group": "default", "priority": 0, "weight": 1
 *     }
 *   ]
 * }
 *
 * 生产环境请用 DbChannelSource（渠道在管理后台维护，可热改）。
 */
final class ArrayChannelSource implements ChannelSourceInterface
{
    /** @var Channel[] */
    private array $channels = [];

    public function __construct(string $jsonFile)
    {
        $raw = @file_get_contents($jsonFile);
        if ($raw === false) {
            throw new \RuntimeException('渠道配置文件不存在: ' . $jsonFile);
        }
        $data = json_decode($raw, true);
        if (!is_array($data) || !isset($data['channels'])) {
            throw new \RuntimeException('渠道配置格式错误: ' . $jsonFile);
        }

        foreach ($data['channels'] as $c) {
            $this->channels[(int) $c['id']] = new Channel(
                id:       (int) $c['id'],
                name:     (string) $c['name'],
                type:     self::inferType((string) ($c['base_url'] ?? '')),
                status:   (int) $c['status'],
                baseUrl:  (string) ($c['base_url'] ?? ''),
                key:      implode("\n", $c['keys'] ?? []),
                models:   $c['models'] ?? [],
                mapping:  self::parseMapping($c['model_mapping'] ?? ''),
                group:    (string) ($c['group'] ?? 'default'),
                priority: (int) ($c['priority'] ?? 0),
                weight:   (int) ($c['weight'] ?? 1),
            );
        }
    }

    /**
     * 从 base_url 推断渠道类型。
     *
     * 导出文件里没有 type 字段时用这个兜底。默认按「原生 OpenAI 兼容」处理 ——
     * 市面上绝大多数上游都是这个形态，直通即可。
     *
     * 如果你要接入需要特殊适配的上游（例如要在请求体里补字段、
     * 或响应结构不是标准 OpenAI 格式），在这里按 base_url 特征返回自定义
     * type 标识，然后在 Relay 层实现对应分支。
     */
    private static function inferType(string $baseUrl): string
    {
        return 'openai_compat';
    }

    private static function parseMapping(string $raw): array
    {
        if ($raw === '') {
            return [];
        }
        $m = json_decode($raw, true);
        return is_array($m) ? $m : [];
    }

    public function findForModel(string $model, string $group = 'default'): array
    {
        $out = [];
        foreach ($this->channels as $ch) {
            if (!$ch->isEnabled()) {
                continue;
            }
            if (!$ch->supports($model)) {
                continue;
            }
            if ($ch->group !== $group && $ch->group !== 'default') {
                continue;
            }
            $out[] = $ch;
        }

        // 按 priority 降序、weight 降序，与现网选路口径一致
        usort($out, static function (Channel $a, Channel $b) {
            if ($a->priority !== $b->priority) {
                return $b->priority <=> $a->priority;
            }
            return $b->weight <=> $a->weight;
        });

        return $out;
    }

    public function findById(int $id): ?Channel
    {
        return $this->channels[$id] ?? null;
    }

    /** @return Channel[] */
    public function all(): array
    {
        return $this->channels;
    }
}
