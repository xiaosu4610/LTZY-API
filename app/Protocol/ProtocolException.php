<?php
declare(strict_types=1);

namespace Ltzy\Protocol;

/**
 * 协议层异常。
 *
 * 带 HTTP 状态码和机器可读的 code，方便上层直接转成对应协议的错误响应。
 */
final class ProtocolException extends \RuntimeException
{
    public function __construct(
        string $message,
        private readonly string $errorCode = 'bad_request',
        private readonly int $httpStatus = 400,
    ) {
        parent::__construct($message);
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
