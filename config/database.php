<?php
declare(strict_types=1);

/**
 * 数据库配置。
 *
 * 数据库连接全部从环境变量读，不写死任何主机名与凭据。
 * 切换前两边各自记账，互不影响。
 */

return [
    'host'     => getenv('DB_HOST') ?: '127.0.0.1',
    'port'     => (int) (getenv('DB_PORT') ?: 5433),
    'database' => getenv('DB_DATABASE') ?: 'ltzy',
    'username' => getenv('DB_USERNAME') ?: 'ltzy',
    'password' => (string) (getenv('DB_PASSWORD') ?: ''),

    // 连接池。每个池实例最多持有这么多连接。
    // 注意 PG 默认 max_connections = 100，多实例部署时总和不能超过它。
    'pool_size' => (int) (getenv('DB_POOL_SIZE') ?: 8),

    // 从池取连接的等待超时（秒）。超时说明池被占满，
    // 宁可快速失败也不要让请求挂住。
    'pool_wait_timeout' => (float) (getenv('DB_POOL_WAIT_TIMEOUT') ?: 2.0),
];
