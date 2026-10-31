<?php
declare(strict_types=1);

namespace Ltzy\Relay;

/**
 * 中继环节的业务异常。
 *
 * 带 code 和 httpStatus，让上层能直接转成规范的错误响应，
 * 不用在 Controller 里做 if/else 猜是哪种失败。
 */
class UpstreamException extends \RuntimeException
{
    public function __construct(
        string $message,
        private readonly string $errorCode = 'upstream_error',
        private readonly int $httpStatus = 502,
        ?\Throwable $previous = null,
    ) {
        parent::__construct($message, 0, $previous);
    }

    public function errorCode(): string
    {
        return $this->errorCode;
    }

    public function httpStatus(): int
    {
        return $this->httpStatus;
    }
}
