<?php
declare(strict_types=1);

namespace Ltzy\Support;

use Ltzy\Db\Connection;

/**
 * 站点配置（品牌信息）。
 *
 * 为什么单独做一层：
 *   这套系统要分发给不同用户部署，品牌名、Logo、描述都必须能在后台改，
 *   不能写死在代码或模板里。配置存在 options 表（key/value），
 *   与运行期可改的其它开关放在一起，管理员改完立刻生效、无需重启。
 *
 * 缓存策略：
 *   进程常驻，每次请求都查库没有必要。这里缓存 30 秒 ——
 *   改配置后最多半分钟生效，代价是每个进程多一次查询。
 */
final class SiteConfig
{
    /** options 里品牌相关的 key */
    public const KEYS = [
        'site_name'  => '站点名称',
        'site_desc'  => '站点描述',
        'site_footer'=> '页脚文字',
    ];

    /** @var array<string,string>|null */
    private ?array $cache = null;

    private float $cacheAt = 0.0;

    private int $ttl = 30;

    public function __construct(
        private readonly Connection $db,
        private readonly array $defaults = [],
    ) {
    }

    /**
     * 取全部品牌配置（含默认值兜底）。
     *
     * @return array{name:string,desc:string,footer:string}
     */
    public function brand(): array
    {
        $rows = $this->load();

        return [
            'name'   => $rows['site_name'] !== '' ? $rows['site_name'] : (string) ($this->defaults['name'] ?? 'LLM Gateway'),
            'desc'   => $rows['site_desc'] !== '' ? $rows['site_desc'] : (string) ($this->defaults['desc'] ?? '统一的大模型 API 网关'),
            'footer' => $rows['site_footer'] !== '' ? $rows['site_footer'] : (string) ($this->defaults['footer'] ?? ''),
        ];
    }

    /**
     * 保存配置（只接受白名单里的 key）。
     *
     * @param array<string,mixed> $data
     * @return int 实际保存的条数
     */
    public function save(array $data): int
    {
        $saved = 0;
        foreach (self::KEYS as $key => $_) {
            if (!array_key_exists($key, $data)) {
                continue;
            }
            $value = trim((string) $data[$key]);
            // 长度上限防止塞入超长内容把前端撑坏
            if (mb_strlen($value) > 120) {
                $value = mb_substr($value, 0, 120);
            }
            $this->db->execute(
                'INSERT INTO options (key, value) VALUES (?, ?)
                 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value',
                [$key, $value]
            );
            $saved++;
        }
        // 改完立即失效缓存，避免管理员点了保存还看到旧值
        $this->cache = null;

        return $saved;
    }

    /**
     * @return array<string,string>
     */
    private function load(): array
    {
        if ($this->cache !== null && (microtime(true) - $this->cacheAt) < $this->ttl) {
            return $this->cache;
        }

        $rows = [];
        try {
            $list = $this->db->query(
                'SELECT key, value FROM options WHERE key IN (?, ?, ?)',
                array_keys(self::KEYS)
            );
            foreach ($list as $r) {
                $rows[(string) $r['key']] = (string) $r['value'];
            }
        } catch (\Throwable) {
            // 表还没建好（首次安装）时不能因为读配置就 500
            $rows = [];
        }

        // 补全缺失的 key，后面取值不用再判 null
        foreach (array_keys(self::KEYS) as $k) {
            $rows[$k] ??= '';
        }

        $this->cache = $rows;
        $this->cacheAt = microtime(true);

        return $rows;
    }
}
