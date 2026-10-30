<?php
declare(strict_types=1);

namespace Ltzy\Support;

/**
 * .env 文件加载器。
 *
 * 为什么需要它：安装向导要把数据库连接、站点地址这些写进配置文件，
 * 但不可能让用户在服务器上手动 export 一堆环境变量 —— 传统 PHP 项目
 * 的做法是生成一个 `.env`，程序启动时读进来。
 *
 * ── 优先级 ──
 *
 * **已有的环境变量永远赢**。也就是说：
 *   systemd 里 Environment=DB_HOST=... 注入了值 → .env 里同名的被忽略
 * 这样生产环境（用 systemd 注入）和开发环境（用 .env）可以共存，
 * 运维临时改一个变量也不需要动文件。
 *
 * ── 解析规则 ──
 *
 * 只支持最常见的那几种写法，不追求完整实现：
 *   KEY=value
 *   KEY="value with spaces"
 *   KEY='value'          # 单引号内不转义
 *   # 注释行
 *   空行忽略
 *
 * 不支持多行值、变量插值、`export` 前缀的复杂语法 —— 用不到，
 * 而且实现得越多越容易在解析上出安全漏洞。
 */
final class Env
{
    private static bool $loaded = false;

    /**
     * 加载 .env。重复调用只生效一次。
     *
     * @param string $file 绝对路径
     */
    public static function load(string $file): void
    {
        if (self::$loaded || !is_file($file)) {
            return;
        }
        self::$loaded = true;

        $lines = @file($file, FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES);
        if ($lines === false) {
            return;
        }

        foreach ($lines as $line) {
            $line = trim($line);
            if ($line === '' || $line[0] === '#') {
                continue;
            }
            // 允许 `export KEY=value` 写法（有人习惯从 shell 脚本复制过来）
            if (str_starts_with($line, 'export ')) {
                $line = ltrim(substr($line, 7));
            }
            if (!str_contains($line, '=')) {
                continue;
            }

            [$k, $v] = explode('=', $line, 2);
            $k = trim($k);
            if ($k === '' || !preg_match('/^[A-Za-z_][A-Za-z0-9_]*$/', $k)) {
                continue;
            }

            $v = trim($v);
            $len = strlen($v);
            if ($len >= 2) {
                $first = $v[0];
                $last = $v[$len - 1];
                if (($first === '"' && $last === '"') || ($first === "'" && $last === "'")) {
                    $v = substr($v, 1, -1);
                    // 双引号里允许 \n \" 这类转义
                    if ($first === '"') {
                        $v = str_replace(['\\n', '\\r', '\\t', '\\"'], ["\n", "\r", "\t", '"'], $v);
                    }
                }
            }

            // 已有环境变量不覆盖
            if (getenv($k) !== false) {
                continue;
            }
            putenv($k . '=' . $v);
            $_ENV[$k] = $v;
            $_SERVER[$k] = $v;
        }
    }

    /**
     * 把一组键值写成 .env 内容（不含写文件，纯拼字符串）。
     *
     * @param array<string,string|int|float|bool> $vars
     */
    public static function render(array $vars, string $header = ''): string
    {
        $out = '';
        if ($header !== '') {
            foreach (explode("\n", $header) as $h) {
                $out .= '# ' . $h . "\n";
            }
            $out .= "\n";
        }

        foreach ($vars as $k => $v) {
            if (!preg_match('/^[A-Za-z_][A-Za-z0-9_]*$/', (string) $k)) {
                continue;
            }
            $val = is_bool($v) ? ($v ? 'true' : 'false') : (string) $v;

            // 含空格、#、引号的值必须加引号，否则解析会截断
            $needQuote = $val === '' || preg_match('/[\s#"\'\\\\]/', $val) === 1;
            if ($needQuote) {
                $esc = str_replace(['\\', '"'], ['\\\\', '\\"'], $val);
                $val = '"' . $esc . '"';
            }
            $out .= $k . '=' . $val . "\n";
        }

        return $out;
    }
}
