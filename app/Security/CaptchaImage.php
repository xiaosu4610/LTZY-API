<?php
declare(strict_types=1);

namespace Ltzy\Security;

/**
 * 图形验证码渲染（GD + FreeType）。
 *
 * ── 为什么必须是位图，不能用 SVG ──
 *
 * SVG 是文本格式，攻击者把 data URI 里的 base64 解码就能直接读到 <text> 里的
 * 字符，等于没有验证码。必须是 PNG 这种像素图，识别成本才回到 OCR 级别。
 *
 * ── 字符集为什么去掉 0/O/1/I/l ──
 *
 * 这几个字符在小字号、带旋转、加干扰线的图里极易混淆。用户输错一次就要
 * 重新取图，体验损失远大于多出 5 个字符带来的熵增益（32^4 已经足够）。
 */
final class CaptchaImage
{
    /** 去除形近字符后的字符集：32 个字符，4 位 = 约 105 万种组合 */
    private const CHARS = '23456789ABCDEFGHJKLMNPQRSTUVWXYZ';

    private const FONT = '/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf';

    /**
     * 生成随机验证码（默认 4 位）。
     *
     * 用 random_int 而不是 mt_rand：验证码是安全凭证，
     * 可预测的随机源等于让人直接推算下一个。
     */
    public static function randomCode(int $len = 4): string
    {
        $s = '';
        $max = strlen(self::CHARS) - 1;
        for ($i = 0; $i < $len; $i++) {
            $s .= self::CHARS[random_int(0, $max)];
        }
        return $s;
    }

    /**
     * 渲染成 PNG 二进制。
     *
     * 配色跟站点亮色主题一致（浅底深字），嵌在页面里不突兀。
     */
    public static function png(string $code, int $w = 116, int $h = 42): string
    {
        $im = imagecreatetruecolor($w, $h);

        // 浅灰底（和 --panel2:#f1f3f5 接近）
        $bg = imagecolorallocate($im, 241, 243, 245);
        imagefilledrectangle($im, 0, 0, $w, $h, $bg);

        // 干扰线：6 条浅灰斜线
        for ($i = 0; $i < 6; $i++) {
            $c = imagecolorallocate(
                $im,
                random_int(190, 220),
                random_int(190, 220),
                random_int(200, 225)
            );
            imageline(
                $im,
                random_int(0, $w),
                random_int(0, $h),
                random_int(0, $w),
                random_int(0, $h),
                $c
            );
        }

        // 噪点：120 个像素
        for ($i = 0; $i < 120; $i++) {
            $c = imagecolorallocate(
                $im,
                random_int(160, 210),
                random_int(160, 210),
                random_int(170, 215)
            );
            imagesetpixel($im, random_int(0, $w - 1), random_int(0, $h - 1), $c);
        }

        // 字符：逐个随机角度、随机纵向偏移
        $len = strlen($code);
        $usableW = $w - 22;
        $step = $usableW / max(1, $len);

        for ($i = 0; $i < $len; $i++) {
            // 深蓝到深灰之间随机，保证和浅底对比度足够
            $col = imagecolorallocate(
                $im,
                random_int(25, 75),
                random_int(35, 85),
                random_int(70, 130)
            );
            $angle = random_int(-17, 17);
            $size = random_int(19, 23);
            $x = (int) (11 + $i * $step + random_int(-2, 2));
            $y = random_int(28, 35);

            imagettftext($im, $size, $angle, $x, $y, $col, self::FONT, $code[$i]);
        }

        ob_start();
        imagepng($im, null, 9);
        $data = (string) ob_get_clean();
        imagedestroy($im);

        return $data;
    }
}
