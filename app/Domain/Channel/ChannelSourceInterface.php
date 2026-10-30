<?php
declare(strict_types=1);

namespace Ltzy\Domain\Channel;

/**
 * 渠道数据源接口。
 *
 * 抽这一层是为了让 P0 阶段不依赖数据库就能先跑通中继链路 ——
 * 先用硬编码的数组验证「Swoole 流式转发是否可行」这个最关键的假设，
 * 验证通过后再换成数据库实现，业务代码一行不用改。
 */
interface ChannelSourceInterface
{
    /**
     * 找出能处理该模型的所有启用渠道。
     *
     * @param string $model 用户请求的模型名
     * @param string $group 用户所属分组
     * @return Channel[]
     */
    public function findForModel(string $model, string $group = 'default'): array;

    /**
     * 按 ID 取渠道。
     */
    public function findById(int $id): ?Channel;

    /**
     * 取全部渠道（含禁用的）。
     *
     * 公开接口（模型列表 / 模型广场 / 状态统计）需要遍历所有渠道，
     * 由调用方自行按 status 过滤 —— 而不是让数据源决定"要不要返回禁用的"，
     * 因为不同接口的过滤口径可能不同（状态统计要显示总数）。
     *
     * @return Channel[]
     */
    public function all(): array;
}
