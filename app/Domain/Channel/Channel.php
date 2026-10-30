<?php
declare(strict_types=1);

namespace Ltzy\Domain\Channel;

/**
 * 渠道实体。
 *
 * 「一个渠道」= 一个上游 + 一组密钥。模型与渠道的对应关系在 abilities 里，
 * 这里只保留渠道本身的属性。
 */
final class Channel
{
    /**
     * @param string   $key       多把密钥，换行分隔
     * @param string[] $models    该渠道支持的模型清单
     * @param array    $mapping   用户看到的名字 → 上游真实名字
     * @param array    $config    其他配置（超时、认证方式等）
     * @param string[] $endpoints 该渠道支持的上游端点
     */
    public function __construct(
        public readonly int $id,
        public readonly string $name,
        public readonly string $type,        // 渠道类型：openai_compat（默认）/ 自定义适配标识
        public readonly int $status,         // 1=启用 2=禁用
        public readonly string $baseUrl,
        public readonly string $key,
        public readonly array $models = [],
        public readonly array $mapping = [],
        public readonly string $group = 'default',
        public readonly int $priority = 0,
        public readonly int $weight = 1,
        public readonly array $config = [],
        public readonly array $endpoints = ['chat'],
        /**
         * 上游 /v1/responses 的兼容模式：
         *   native = 标准 OpenAI 格式（input），可直通
         *   compat = 需要补齐字段（部分上游要额外传 messages）
         *   none   = 不支持，走 chat 翻译
         */
        public readonly string $responsesMode = 'none',
        /**
         * 模型 → 类别（chat / embedding / rerank / vision / audio / image / other）。
         *
         * 由 abilities.category 提供，用于「只展示能对话的模型」这类过滤。
         * 不在这里猜 —— 猜错过（GLM-ASR 被当成 chat，用户点了必然 400）。
         *
         * @var array<string,string>
         */
        public readonly array $categories = [],
    ) {
    }

    /**
     * 模型类别。
     */
    public function categoryOf(string $model): string
    {
        return $this->categories[$model] ?? 'chat';
    }

    /**
     * 该模型是否走 chat 端点（含 vision —— 视觉模型也是传图片进 chat）。
     */
    public function isChatModel(string $model): bool
    {
        return in_array($this->categoryOf($model), ['chat', 'vision'], true);
    }

    /**
     * 该渠道是否原生支持某个端点。
     *
     * 用于决定「直通还是翻译」：
     *   支持 → 直通（零转换，最快也最不容易出错）
     *   不支持 → 翻译成 chat
     */
    public function supportsEndpoint(string $endpoint): bool
    {
        return in_array($endpoint, $this->endpoints, true);
    }

    /**
     * 能否用 /v1/responses 直通。
     *
     * 只有 native 模式才算真正的直通 —— compat 模式要补字段，
     * 那已经是"改写请求"了，不算零转换。
     */
    public function canPassthroughResponses(): bool
    {
        return $this->supportsEndpoint('responses') && $this->responsesMode === 'native';
    }

    /**
     * 能否用 responses 端点（可以是 compat 模式）。
     */
    public function canUseResponses(): bool
    {
        return $this->supportsEndpoint('responses') && $this->responsesMode !== 'none';
    }

    public function isEnabled(): bool
    {
        return $this->status === 1;
    }

    /**
     * 上游真实模型名。
     *
     * 如果配了 mapping 就走映射，否则原样使用 ——
     * 网关不应该擅自改写模型名，只在运营显式配置时才映射。
     */
    public function upstreamModel(string $requested): string
    {
        return $this->mapping[$requested] ?? $requested;
    }

    /**
     * 该渠道能否处理这个模型。
     */
    public function supports(string $model): bool
    {
        if ($this->models === []) {
            // 模型清单为空表示不限制（少数透传渠道）
            return true;
        }
        return in_array($model, $this->models, true);
    }
}
