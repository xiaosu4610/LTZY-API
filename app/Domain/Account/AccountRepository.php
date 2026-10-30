<?php
declare(strict_types=1);

namespace Ltzy\Domain\Account;

use Ltzy\Db\Connection;

/**
 * 经销商数据访问。
 *
 * 「经销商」= role=1 的账户，由我们手工创建。
 * 每个经销商有独立的模型清单和定价（account_models 表）。
 *
 * 为什么不用 group（分组）机制：
 *   分组是「一类用户共享一套规则」，而经销商是一人一价一清单。
 *   用 (user_id, model) 直接存最直观，查询也快，不用绕分组那一层。
 */
final class AccountRepository
{
    public function __construct(private readonly Connection $db)
    {
    }

    /**
     * 取该经销商的模型清单（含专属价格）。
     *
     * @return array<int,array{model:string,price:int}>
     */
    public function models(int $userId): array
    {
        return $this->db->query(
            'SELECT model, price FROM account_models WHERE user_id = ? AND enabled = 1 ORDER BY model',
            [$userId]
        );
    }

    /**
     * 某个模型对该经销商的价格。
     *
     * 查不到就是 null —— 调用方需要区分「价格为 0」和「没有这个模型」。
     * 这个区分很重要：价格 0 是免费能用，没有模型是不能用。
     */
    public function priceFor(int $userId, string $model): ?int
    {
        $row = $this->db->first(
            'SELECT price FROM account_models WHERE user_id = ? AND model = ? AND enabled = 1',
            [$userId, $model]
        );
        return $row === null ? null : (int) $row['price'];
    }

    /**
     * 该经销商是否被授权使用这个模型。
     */
    public function canUse(int $userId, string $model): bool
    {
        return $this->priceFor($userId, $model) !== null;
    }

    /**
     * 设置经销商的模型清单。
     *
     * 全量覆盖语义：先清空再插入。这样管理界面「勾选一堆模型保存」
     * 的行为最直观，不用担心残留旧记录。
     *
     * @param array<int,array{model:string,price:int}> $items
     */
    public function setModels(int $userId, array $items): int
    {
        return $this->db->transaction(function (\PDO $pdo) use ($userId, $items) {
            $del = $pdo->prepare('DELETE FROM account_models WHERE user_id = ?');
            $del->execute([$userId]);

            $ins = $pdo->prepare('INSERT INTO account_models (user_id, model, price) VALUES (?, ?, ?)');
            $n = 0;
            foreach ($items as $it) {
                $model = (string) ($it['model'] ?? '');
                if ($model === '') {
                    continue;
                }
                $ins->execute([$userId, $model, (int) ($it['price'] ?? 0)]);
                $n++;
            }
            return $n;
        });
    }

    /**
     * 经销商的调用日志。
     *
     * 默认 180 天（配置项 log_retention_days），按时间倒序。
     */
    public function logs(int $userId, int $limit = 50, int $offset = 0, int $days = 180): array
    {
        return $this->db->query(
            'SELECT id, created_at, model, quota, elapsed_ms, first_byte_ms, is_stream,
                    status_code, request_id, prompt_tokens, completion_tokens
             FROM logs
             WHERE user_id = ?
               AND created_at >= now() - (? || \' days\')::interval
             ORDER BY created_at DESC
             LIMIT ? OFFSET ?',
            [$userId, $days, $limit, $offset]
        );
    }

    /**
     * 经销商某段时间的汇总统计。
     */
    public function stats(int $userId, int $days = 180): array
    {
        $row = $this->db->first(
            'SELECT COUNT(*) AS requests,
                    COALESCE(SUM(quota), 0) AS quota,
                    COALESCE(AVG(elapsed_ms), 0) AS avg_ms,
                    COALESCE(AVG(first_byte_ms) FILTER (WHERE first_byte_ms > 0), 0) AS avg_ttft
             FROM logs
             WHERE user_id = ?
               AND created_at >= now() - (? || \' days\')::interval',
            [$userId, $days]
        ) ?? [];

        return [
            'requests' => (int) ($row['requests'] ?? 0),
            'quota'    => (int) ($row['quota'] ?? 0),
            'avg_ms'   => (int) ($row['avg_ms'] ?? 0),
            'avg_ttft' => (int) ($row['avg_ttft'] ?? 0),
        ];
    }

    /**
     * 按天聚合的用量（图表用）。
     */
    public function dailyUsage(int $userId, int $days = 180): array
    {
        return $this->db->query(
            'SELECT to_char(date_trunc(\'day\', created_at), \'YYYY-MM-DD\') AS day,
                    COUNT(*) AS requests,
                    COALESCE(SUM(quota), 0) AS quota
             FROM logs
             WHERE user_id = ?
               AND created_at >= now() - (? || \' days\')::interval
             GROUP BY 1 ORDER BY 1',
            [$userId, $days]
        );
    }

    /**
     * 按模型聚合（看哪个模型用得多）。
     */
    public function usageByModel(int $userId, int $days = 180): array
    {
        return $this->db->query(
            'SELECT model, COUNT(*) AS requests, COALESCE(SUM(quota), 0) AS quota
             FROM logs
             WHERE user_id = ?
               AND created_at >= now() - (? || \' days\')::interval
             GROUP BY model ORDER BY requests DESC LIMIT 20',
            [$userId, $days]
        );
    }
}
