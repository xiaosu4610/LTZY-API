<?php
declare(strict_types=1);

namespace Ltzy\Http\Controller;

use Ltzy\Http\Request;
use Ltzy\Http\Response;
use Ltzy\Install\Installer;

/**
 * 安装向导。
 *
 * 只在**未安装**时可访问。装完之后 Router 会把 /install 与 /api/install/*
 * 全部 404 掉，再进来就是这个页面不存在 —— 这是刻意的：
 * 一个能重复访问的安装入口等于把重装权限开放给全互联网。
 *
 * 并发保护：同一时刻只允许一个安装请求进入 run()。
 * 用一个简单的文件锁实现（flock），不是内存锁 —— 因为安装通常发生在
 * 多进程/多请求并存的环境，内存锁挡不住。
 */
final class InstallController
{
    public function __construct(private readonly Installer $installer)
    {
    }

    /**
     * GET /install
     */
    public function page(Request $req, Response $resp): void
    {
        $file = dirname(__DIR__, 3) . '/web/install.html';
        if (!is_file($file)) {
            $resp->html('<h1>install.html 缺失</h1><p>检查 web/ 目录</p>', 500);
            return;
        }
        $resp->html((string) file_get_contents($file));
    }

    /**
     * POST /api/install/check
     *
     * 环境自检。返回每项的名称、要求、当前值、是否通过。
     */
    public function check(Request $req, Response $resp): void
    {
        if ($this->installer->installed()) {
            $resp->fail('已经安装过了', 404, 'installed');
            return;
        }

        $rows = $this->installer->checkEnv();
        $ok = true;
        foreach ($rows as $r) {
            if (!$r['ok']) {
                $ok = false;
                break;
            }
        }

        $resp->ok([
            'ok'   => $ok,
            'items' => $rows,
            'php'  => PHP_VERSION,
            'sapi' => PHP_SAPI,
        ]);
    }

    /**
     * POST /api/install/test-db
     *
     * 只测连接，不建表。
     */
    public function testDb(Request $req, Response $resp): void
    {
        if ($this->installer->installed()) {
            $resp->fail('已经安装过了', 404, 'installed');
            return;
        }

        $cfg = [
            'host'     => (string) $req->input('host', '127.0.0.1'),
            'port'     => (int) $req->input('port', 5432),
            'database' => (string) $req->input('database', ''),
            'username' => (string) $req->input('username', ''),
            'password' => (string) $req->input('password', ''),
        ];

        if ($cfg['database'] === '' || $cfg['username'] === '') {
            $resp->fail('数据库名和用户名不能为空', 400, 'invalid');
            return;
        }

        $r = $this->installer->testDb($cfg);
        if ($r['ok']) {
            $resp->ok(['version' => $r['version'] ?? ''], '连接成功');
        } else {
            $resp->fail($r['message'], 400, 'db_failed');
        }
    }

    /**
     * POST /api/install/run
     *
     * 执行安装。这是唯一会写文件的接口。
     */
    public function run(Request $req, Response $resp): void
    {
        if ($this->installer->installed()) {
            $resp->fail('已经安装过了。如需重装，请先删除 var/install.lock', 404, 'installed');
            return;
        }

        // 文件锁：防止两个请求同时装，最后写成半截状态
        $lockDir = dirname(__DIR__, 3) . '/var';
        if (!is_dir($lockDir)) {
            @mkdir($lockDir, 0755, true);
        }
        $fp = @fopen($lockDir . '/install.lock.tmp', 'c');
        if ($fp === false) {
            $resp->fail('无法创建安装锁，检查 var/ 目录权限', 500, 'fs_error');
            return;
        }
        if (!flock($fp, LOCK_EX | LOCK_NB)) {
            fclose($fp);
            $resp->fail('另一个安装流程正在进行，请稍后再试', 409, 'busy');
            return;
        }

        try {
            $result = $this->installer->run([
                'db' => [
                    'host'     => (string) $req->input('host', '127.0.0.1'),
                    'port'     => (int) $req->input('port', 5432),
                    'database' => (string) $req->input('database', ''),
                    'username' => (string) $req->input('username', ''),
                    'password' => (string) $req->input('password', ''),
                ],
                'site' => [
                    'name' => (string) $req->input('site_name', 'LTZY API'),
                    'url'  => (string) $req->input('site_url', ''),
                    'logo' => (string) $req->input('site_logo', ''),
                ],
                'admin' => [
                    'username' => (string) $req->input('admin_user', 'root'),
                    'password' => (string) $req->input('admin_pass', ''),
                ],
            ]);
        } finally {
            flock($fp, LOCK_UN);
            fclose($fp);
            @unlink($lockDir . '/install.lock.tmp');
        }

        if (!$result['ok']) {
            $resp->fail($result['message'], 400, 'install_failed');
            return;
        }

        $resp->ok([
            'steps' => $result['steps'],
            'restart_required' => true,
        ], $result['message']);
    }
}
