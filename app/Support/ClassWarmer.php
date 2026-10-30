<?php
declare(strict_types=1);

namespace Ltzy\Support;

/**
 * 类预加载器。
 *
 * ⚠️ 存在的唯一原因：**Swoole 协程内触发 autoload 会崩溃**（0xC0000409）。
 *
 * 现象：接口 handler 里第一次用到某个尚未加载的类时，PHP 走 spl_autoload_register
 * 回调去 require 文件，进程直接退出，且 stderr 没有任何输出 —— 极难定位。
 *
 * 实测对比：
 *   - handler 里 new 一个已定义的类 → 正常
 *   - handler 里引用一个需要 autoload 的类 → 崩溃
 *   - 同一个类先在协程外加载过 → 正常
 *
 * 所以：**所有类必须在协程启动前加载完毕**。
 * 这个类遍历 app/ 目录，按文件名推断类名，逐个 class_exists() 触发 autoload。
 *
 * 代价是启动时多几十毫秒，换来的是运行期不再有 autoload 风险。
 */
final class ClassWarmer
{
    /**
     * @return array{loaded:int,failed:string[]}
     */
    public static function warm(string $appDir, string $prefix = 'Ltzy\\'): array
    {
        $loaded = 0;
        $failed = [];

        $it = new \RecursiveIteratorIterator(
            new \RecursiveDirectoryIterator($appDir, \FilesystemIterator::SKIP_DOTS)
        );

        $classes = [];
        foreach ($it as $file) {
            if (!$file->isFile() || $file->getExtension() !== 'php') {
                continue;
            }
            $path = $file->getPathname();
            $rel = substr($path, strlen($appDir) + 1, -4); // 去掉目录前缀与 .php
            $rel = str_replace(['/', '\\'], '\\', $rel);
            $classes[] = $prefix . $rel;
        }

        sort($classes);

        foreach ($classes as $class) {
            try {
                if (class_exists($class) || interface_exists($class) || trait_exists($class)) {
                    $loaded++;
                } else {
                    // class_exists 返回 false 说明既没加载到也不存在
                    $failed[] = $class;
                }
            } catch (\Throwable $e) {
                $failed[] = $class . ' (' . $e->getMessage() . ')';
            }
        }

        return ['loaded' => $loaded, 'failed' => $failed];
    }
}
