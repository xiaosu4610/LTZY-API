package model

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// 单渠道多密钥轮询（LTZY-API 新增）。
//
// 为什么需要：像英伟达这类渠道，同一个上游要配几百把免费密钥，
// 它们的模型列表完全一致，只是额度与限流各自独立。
// 如果按「一把密钥 = 一条渠道」来建，几百条渠道会让渠道列表无法维护，
// 而且每把密钥的模型列表要重复维护几百遍。
//
// 所以这里让一个渠道的 Key 字段可以放多行密钥，请求时轮询选一把：
//   - 轮询：均匀分摊，避免总打同一把把额度打爆
//   - 冷却：某把密钥报 401/403/429 时，把它单独放进冷却期，
//     后续请求自动跳过它，但不影响同一渠道里其它密钥
//   - 兜底：当所有密钥都在冷却期时，仍然返回一把去试 ——
//     「全都冷却」时让请求直接失败，比让它去试一把偶发失败的密钥更糟。
//
// 状态只放在内存里（进程重启后冷却状态清零）。这是有意的取舍：
// 密钥失效通常是可以自愈的（限额刷新、上游抖动），
// 若落库反而会把一次性的失败永久固化下来。

const (
	// KeyCooldownAuthFailed 密钥失效（401/403）后的冷却时长
	KeyCooldownAuthFailed = int64(30 * 60)
	// KeyCooldownRateLimited 被限流（429）后的冷却时长。
	// 取 60 秒是经验值：上游限流窗口通常是每分钟级别，太短会一直撞墙，太长会浪费额度。
	KeyCooldownRateLimited = int64(60)
)

var (
	channelKeyCounter  sync.Map // channelId -> *uint64，轮询游标
	channelKeyFailLock sync.RWMutex
	channelKeyFailures = map[int]map[string]int64{} // channelId -> key -> 冷却截止时间（Unix 秒）
)

// SplitChannelKeys 把渠道的 Key 字段拆成多把密钥。
// 支持换行与英文/中文逗号分隔，自动去空、去重并保持原顺序。
func SplitChannelKeys(raw string) []string {
	if raw == "" {
		return nil
	}
	normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n", ";", "\n", ",", "\n", "，", "\n").Replace(raw)
	seen := make(map[string]struct{})
	keys := make([]string, 0, 4)
	for _, part := range strings.Split(normalized, "\n") {
		key := strings.TrimSpace(part)
		if key == "" {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

// nextKeyIndex 取该渠道的下一个轮询下标。
func nextKeyIndex(channelId int, total int) int {
	if total <= 1 {
		return 0
	}
	value, _ := channelKeyCounter.LoadOrStore(channelId, new(uint64))
	counter := value.(*uint64)
	return int(atomic.AddUint64(counter, 1)-1) % total
}

// PickChannelKey 为本次请求挑选一把密钥。单密钥渠道直接返回原值。
func PickChannelKey(channel *Channel) string {
	if channel == nil {
		return ""
	}
	keys := SplitChannelKeys(channel.Key)
	if len(keys) == 0 {
		return channel.Key
	}
	if len(keys) == 1 {
		return keys[0]
	}
	now := time.Now().Unix()
	channelKeyFailLock.RLock()
	cooldowns := channelKeyFailures[channel.Id]
	channelKeyFailLock.RUnlock()

	start := nextKeyIndex(channel.Id, len(keys))
	for offset := 0; offset < len(keys); offset++ {
		index := (start + offset) % len(keys)
		if until := cooldowns[keys[index]]; until > now {
			continue
		}
		return keys[index]
	}
	// 全部处于冷却期：还是返回一把，让请求有机会成功
	return keys[start]
}

// MarkChannelKeyFailed 把某把密钥放进冷却期。
func MarkChannelKeyFailed(channelId int, key string, cooldownSeconds int64) {
	if channelId <= 0 || key == "" || cooldownSeconds <= 0 {
		return
	}
	channelKeyFailLock.Lock()
	defer channelKeyFailLock.Unlock()
	if channelKeyFailures[channelId] == nil {
		channelKeyFailures[channelId] = map[string]int64{}
	}
	channelKeyFailures[channelId][key] = time.Now().Unix() + cooldownSeconds
}

// FillChannelKeyStats 把多密钥统计填进渠道对象（非持久化字段），供前端展示
// 「共 N 把 / 冷却 M 把」，管理员据此判断是不是某几把密钥在拖后腿。
//
// 注意：渠道列表接口为了不泄露密钥，查询时是 Omit("key") 的，
// 所以这里对拿不到 key 的渠道单独补查一次（一条 IN 查询，不逐条打库）。
func FillChannelKeyStats(channels []*Channel) {
	if len(channels) == 0 {
		return
	}
	needLoad := make([]int, 0, len(channels))
	for _, channel := range channels {
		if channel.Key == "" {
			needLoad = append(needLoad, channel.Id)
		}
	}
	keyByChannel := map[int]string{}
	if len(needLoad) > 0 {
		type keyRow struct {
			Id  int
			Key string
		}
		var rows []keyRow
		if err := DB.Model(&Channel{}).Select("id", "key").Where("id IN ?", needLoad).Find(&rows).Error; err == nil {
			for _, row := range rows {
				keyByChannel[row.Id] = row.Key
			}
		}
	}
	now := time.Now().Unix()
	for _, channel := range channels {
		raw := channel.Key
		if raw == "" {
			raw = keyByChannel[channel.Id]
		}
		channel.KeyCount = len(SplitChannelKeys(raw))
		if channel.KeyCount == 0 {
			channel.KeyCount = 1
		}
		channel.KeyCooling = 0
		channelKeyFailLock.RLock()
		cooldowns := channelKeyFailures[channel.Id]
		channelKeyFailLock.RUnlock()
		for _, until := range cooldowns {
			if until > now {
				channel.KeyCooling++
			}
		}
	}
}
