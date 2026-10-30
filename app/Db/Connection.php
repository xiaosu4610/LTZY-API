<?php
declare(strict_types=1);

namespace Ltzy\Db;

use Ltzy\Support\Logger;
use Swoole\Coroutine\Channel;

/**
 * PostgreSQL 连接池。
 *
 * 为什么必须池化：实测新建连接 53.65 ms，复用连接单次查询 0.227 ms ——
 * 连接开销是查询的 237 倍。每请求新建连接等于给每个请求加 50ms 延迟。
 *
 * 为什么自己实现而不用 Swoole\Database\PDOPool：
 *   1. 需要「取出来时验活」。PG 会主动断开闲置过久的连接，
 *      池里可能躺着死连接，直接拿去 query 会报 "server closed the connection"。
 *   2. 需要能在连接损坏时自动换一条，而不是把异常抛给业务。
 * 自建的核心代码不到 100 行，多这点控制力是值得的。
 */
final class Connection
{
    private Channel $pool;
    private bool $closed = false;

    /**
     * 是否已做过首次预热。
     *
     * ⚠️ Channel::push 要求协程环境，所以**不能在构造函数里预热** ——
     * Connection 是服务启动时（协程外）创建的，那时 push 会抛
     * "API must be called in the coroutine"。预热只能推迟到第一次 get()。
     */
    private bool $warmed = false;

    /** 已创建的连接数（含使用中的），用于限制不超过池容量 */
    private int $created = 0;

    private int $size;

    /** @var array<string,mixed> */
    private array $dsn;

    /**
     * @param array{host:string,port:int,database:string,username:string,password:string} $conf
     */
    public function __construct(array $conf, int $size = 8)
    {
        if ($size < 1) {
            $size = 1;
        }
        $this->size = $size;

        $this->dsn = [
            'dsn'  => sprintf(
                'pgsql:host=%s;port=%d;dbname=%s',
                $conf['host'], $conf['port'], $conf['database']
            ),
            'user' => $conf['username'],
            'pass' => $conf['password'],
        ];

        // 容量 = size，push 满了会阻塞 —— 这正是我们要的：
        // 归还时如果池已满说明逻辑出错（重复归还），阻塞比静默泄漏好排查。
        $this->pool = new Channel($size);
    }

    private function createConnection(): \PDO
    {
        $pdo = new \PDO($this->dsn['dsn'], $this->dsn['user'], $this->dsn['pass'], [
            \PDO::ATTR_ERRMODE            => \PDO::ERRMODE_EXCEPTION,
            \PDO::ATTR_DEFAULT_FETCH_MODE => \PDO::FETCH_ASSOC,
            // 关闭模拟预处理，让 PG 真正做参数绑定（防注入 + 避免类型推断问题）
            \PDO::ATTR_EMULATE_PREPARES   => false,
        ]);
        // 会话级设置：让 PG 知道我们是 UTF8，避免客户端编码协商问题
        $pdo->exec("SET client_encoding TO 'UTF8'");
        return $pdo;
    }

    /**
     * 从池里取一条连接。
     *
     * @param float $timeout 等待超时（秒）。超时说明池被占满且业务没归还，
     *                       宁可快速失败也不要让请求挂住。
     */
    public function get(float $timeout = 2.0): \PDO
    {
        if ($this->closed) {
            throw new \RuntimeException('连接池已关闭');
        }

        // 首次取连接时才预热（此时一定在协程内）。
        // 只建 1 条而不是 size 条：建连 53ms，8 条要 424ms，
        // 第一个请求白等半秒不值得。其余连接按需增长，很快就能填满。
        if (!$this->warmed) {
            $this->warmed = true;
            $this->created = 1;
            $this->pool->push($this->createConnection());
        }

        $pdo = null;

        // length() 不阻塞，先看池里有没有空闲连接。
        if ($this->pool->length() > 0) {
            $pdo = $this->pool->pop($timeout);
        }

        // 池空且还能建：直接建一条新的（不必等 2 秒超时）
        if (!$pdo instanceof \PDO && $this->created < $this->size) {
            $this->created++;
            $pdo = $this->createConnection();
        }

        // 已达上限且全在忙：只能等别人归还
        if (!$pdo instanceof \PDO) {
            $pdo = $this->pool->pop($timeout);
        }

        if (!$pdo instanceof \PDO) {
            throw new \RuntimeException("从连接池取连接超时（{$timeout}s）");
        }

        // 验活：给 PG 一个最轻的探针。死连接会被丢弃并重建。
        try {
            $pdo->query('SELECT 1');
        } catch (\PDOException $e) {
            Logger::warn('db.stale_connection', ['error' => Logger::mask($e->getMessage())]);
            try {
                $pdo = $this->createConnection();
            } catch (\PDOException $e2) {
                // 重建也失败，把位置还回去（保持池容量），再把异常抛给业务
                $this->pool->push($pdo);
                throw $e2;
            }
        }

        return $pdo;
    }

    /**
     * 归还连接。
     *
     * ⚠️ 必须放在 finally 里调用，否则一次异常的请求会永久占掉一个池位，
     * 池满之后就再也取不到连接了（表现为整个服务不可用）。
     */
    public function put(\PDO $pdo): void
    {
        if ($this->closed) {
            return;
        }
        // 注意这里用 push 的默认超时（-1 = 阻塞等待）。
        // 池容量固定，正常归还不会阻塞；如果阻塞了说明有重复归还。
        $this->pool->push($pdo);
    }

    /**
     * 在连接上执行闭包，自动归还。
     *
     * 这是推荐用法 —— 手写 get/put 很容易漏掉 finally。
     */
    public function run(callable $fn, float $timeout = 2.0): mixed
    {
        $pdo = $this->get($timeout);
        try {
            return $fn($pdo);
        } finally {
            $this->put($pdo);
        }
    }

    /**
     * 执行查询并把结果全部取回。
     *
     * ⚠️ 一定要在这里 fetch 完再归还连接 —— PDOStatement 绑定在 PDO 上，
     * 连接被别的协程拿走执行新查询后，没读完的结果集就废了。
     * 所以本方法只返回数组，不返回 PDOStatement。
     *
     * @return array<int,array<string,mixed>>
     */
    public function query(string $sql, array $params = [], float $timeout = 2.0): array
    {
        return $this->run(static function (\PDO $pdo) use ($sql, $params) {
            $stmt = $pdo->prepare($sql);
            $stmt->execute($params);
            return $stmt->fetchAll();
        }, $timeout);
    }

    /**
     * 执行写入语句，返回影响行数。
     */
    public function execute(string $sql, array $params = [], float $timeout = 2.0): int
    {
        return $this->run(static function (\PDO $pdo) use ($sql, $params) {
            $stmt = $pdo->prepare($sql);
            $stmt->execute($params);
            return $stmt->rowCount();
        }, $timeout);
    }

    /**
     * 取单行，无结果返回 null。
     *
     * @return array<string,mixed>|null
     */
    public function first(string $sql, array $params = [], float $timeout = 2.0): ?array
    {
        return $this->run(static function (\PDO $pdo) use ($sql, $params) {
            $stmt = $pdo->prepare($sql);
            $stmt->execute($params);
            $row = $stmt->fetch();
            return $row === false ? null : $row;
        }, $timeout);
    }

    /**
     * 取单值（第一行第一列），无结果返回 null。
     */
    public function scalar(string $sql, array $params = [], float $timeout = 2.0): mixed
    {
        return $this->run(static function (\PDO $pdo) use ($sql, $params) {
            $stmt = $pdo->prepare($sql);
            $stmt->execute($params);
            $v = $stmt->fetchColumn();
            return $v === false ? null : $v;
        }, $timeout);
    }

    /**
     * 在一个事务里执行闭包。
     */
    public function transaction(callable $fn, float $timeout = 2.0): mixed
    {
        return $this->run(function (\PDO $pdo) use ($fn) {
            $pdo->beginTransaction();
            try {
                $result = $fn($pdo);
                $pdo->commit();
                return $result;
            } catch (\Throwable $e) {
                if ($pdo->inTransaction()) {
                    $pdo->rollBack();
                }
                throw $e;
            }
        }, $timeout);
    }

    public function close(): void
    {
        $this->closed = true;
        $this->pool->close();
    }

    public function stats(): array
    {
        return [
            'capacity' => $this->pool->capacity,
            'in_use'   => $this->pool->capacity - $this->pool->length(),
            'idle'     => $this->pool->length(),
        ];
    }
}
