package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/model"
)

// canaryTestKeys 灰度用到的全部配置键。每次用例都整体重置，
// 避免某个用例写完配置后污染后续用例的判断。
var canaryTestKeys = []string{OptionCanaryEnabled, OptionCanaryChannelId, OptionCanaryPercent, OptionCanaryGroups}

// setCanaryOptions 设置灰度配置并在用例结束后还原 OptionMap 原值。
func setCanaryOptions(t *testing.T, values map[string]string) {
	t.Helper()
	config.OptionMapRWMutex.Lock()
	if config.OptionMap == nil {
		config.OptionMap = make(map[string]string)
	}
	previous := make(map[string]string, len(canaryTestKeys))
	for _, key := range canaryTestKeys {
		previous[key] = config.OptionMap[key]
		config.OptionMap[key] = ""
	}
	for key, value := range values {
		config.OptionMap[key] = value
	}
	config.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		config.OptionMapRWMutex.Lock()
		defer config.OptionMapRWMutex.Unlock()
		for _, key := range canaryTestKeys {
			if previous[key] == "" {
				delete(config.OptionMap, key)
				continue
			}
			config.OptionMap[key] = previous[key]
		}
	})
}

// canaryStub 记录替身被调用的情况：断言「不该命中灰度时连随机数都不消耗」，
// 也就等于断言原有选渠道逻辑完全未被触碰。
type canaryStub struct {
	randCalls       int
	supportChecks   int
	getChannelCalls int
}

// stubCanary 用内存替身替换灰度判定依赖的外部能力（随机源、Ability 校验、渠道查询）。
// randValue < 0 表示让随机源返回区间最大值（即尽可能命中灰度）。
func stubCanary(t *testing.T, supported bool, channel *model.Channel, getErr error, randValue int) *canaryStub {
	t.Helper()
	stub := &canaryStub{}
	prevRand := canaryRand
	prevSupported := canaryChannelSupportsModel
	prevGet := canaryGetChannel

	canaryRand = func(n int) int {
		stub.randCalls++
		if randValue < 0 {
			return n - 1
		}
		return randValue
	}
	canaryChannelSupportsModel = func(group string, modelName string, channelId int) bool {
		stub.supportChecks++
		return supported
	}
	canaryGetChannel = func(id int, selectAll bool) (*model.Channel, error) {
		stub.getChannelCalls++
		if getErr != nil {
			return nil, getErr
		}
		return channel, nil
	}

	t.Cleanup(func() {
		canaryRand = prevRand
		canaryChannelSupportsModel = prevSupported
		canaryGetChannel = prevGet
	})
	return stub
}

func enabledCanaryChannel(id int) *model.Channel {
	return &model.Channel{Id: id, Status: model.ChannelStatusEnabled, Group: "default", Models: "gpt-4o"}
}

// TestCanaryDisabledKeepsOriginalSelection 未启用 / 比例 0 时必须完全不介入，
// 连随机数都不消耗，原有选渠道结果因而逐字节不变。
func TestCanaryDisabledKeepsOriginalSelection(t *testing.T) {
	cases := []struct {
		name    string
		options map[string]string
	}{
		{"未启用", map[string]string{OptionCanaryEnabled: "false", OptionCanaryChannelId: "9", OptionCanaryPercent: "50"}},
		{"开关缺失", map[string]string{OptionCanaryChannelId: "9", OptionCanaryPercent: "50"}},
		{"开关值非法", map[string]string{OptionCanaryEnabled: "yes", OptionCanaryChannelId: "9", OptionCanaryPercent: "50"}},
		{"比例为 0", map[string]string{OptionCanaryEnabled: "true", OptionCanaryChannelId: "9", OptionCanaryPercent: "0"}},
		{"比例非法", map[string]string{OptionCanaryEnabled: "true", OptionCanaryChannelId: "9", OptionCanaryPercent: "abc"}},
		{"比例负数", map[string]string{OptionCanaryEnabled: "true", OptionCanaryChannelId: "9", OptionCanaryPercent: "-10"}},
		{"渠道 ID 缺失", map[string]string{OptionCanaryEnabled: "true", OptionCanaryPercent: "50"}},
		{"渠道 ID 非法", map[string]string{OptionCanaryEnabled: "true", OptionCanaryChannelId: "abc", OptionCanaryPercent: "50"}},
		{"渠道 ID 为 0", map[string]string{OptionCanaryEnabled: "true", OptionCanaryChannelId: "0", OptionCanaryPercent: "50"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setCanaryOptions(t, tc.options)
			stub := stubCanary(t, true, enabledCanaryChannel(9), nil, -1)

			if got := pickCanaryChannel(context.Background(), "default", "gpt-4o"); got != nil {
				t.Fatalf("灰度不可用时不应接管选渠道，实际拿到渠道 #%d", got.Id)
			}
			if stub.randCalls != 0 || stub.supportChecks != 0 || stub.getChannelCalls != 0 {
				t.Fatalf("灰度不可用时不应触碰任何外部依赖，实际 rand=%d support=%d getChannel=%d",
					stub.randCalls, stub.supportChecks, stub.getChannelCalls)
			}
		})
	}
}

// TestCanaryFullPercentAlwaysHits 比例 100（以及写超 100）等价于全部走灰度渠道。
func TestCanaryFullPercentAlwaysHits(t *testing.T) {
	for _, percent := range []string{"100", "150"} {
		t.Run("比例="+percent, func(t *testing.T) {
			setCanaryOptions(t, map[string]string{
				OptionCanaryEnabled:   "true",
				OptionCanaryChannelId: "9",
				OptionCanaryPercent:   percent,
			})
			stub := stubCanary(t, true, enabledCanaryChannel(9), nil, -1)

			got := pickCanaryChannel(context.Background(), "default", "gpt-4o")
			if got == nil || got.Id != 9 {
				t.Fatalf("比例 %s 应必定命中灰度渠道 #9，实际 %+v", percent, got)
			}
			if stub.randCalls != 1 || stub.getChannelCalls != 1 {
				t.Fatalf("随机源与渠道查询应各执行一次，实际 rand=%d getChannel=%d", stub.randCalls, stub.getChannelCalls)
			}
		})
	}
}

// TestCanaryUnsupportedModelFallsBack 灰度渠道不支持该模型时必须回退，
// 否则请求会被打到不支持该模型的上游上直接报错。
func TestCanaryUnsupportedModelFallsBack(t *testing.T) {
	setCanaryOptions(t, map[string]string{
		OptionCanaryEnabled:   "true",
		OptionCanaryChannelId: "9",
		OptionCanaryPercent:   "100",
	})
	stub := stubCanary(t, false, enabledCanaryChannel(9), nil, -1)

	if got := pickCanaryChannel(context.Background(), "default", "gpt-4o"); got != nil {
		t.Fatalf("渠道不支持该模型时不应接管选渠道，实际拿到渠道 #%d", got.Id)
	}
	if stub.supportChecks != 1 {
		t.Fatalf("应做且只做一次能力校验，实际 %d 次", stub.supportChecks)
	}
	if stub.getChannelCalls != 0 {
		t.Fatal("能力校验未通过就不应再去查询渠道")
	}
}

// TestCanaryChannelUnavailableFallsBack 渠道被删除 / 查询报错 / 已停用时都回退。
func TestCanaryChannelUnavailableFallsBack(t *testing.T) {
	cases := []struct {
		name    string
		channel *model.Channel
		getErr  error
	}{
		{"渠道查询报错", nil, errors.New("record not found")},
		{"渠道不存在", nil, nil},
		{"渠道已停用", &model.Channel{Id: 9, Status: model.ChannelStatusManuallyDisabled}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setCanaryOptions(t, map[string]string{
				OptionCanaryEnabled:   "true",
				OptionCanaryChannelId: "9",
				OptionCanaryPercent:   "100",
			})
			stubCanary(t, true, tc.channel, tc.getErr, -1)

			if got := pickCanaryChannel(context.Background(), "default", "gpt-4o"); got != nil {
				t.Fatalf("灰度渠道不可用时不应接管选渠道，实际拿到渠道 #%d", got.Id)
			}
		})
	}
}

// TestCanaryGroupFilter 分组过滤：不在灰度分组内不生效；配置留空则全分组生效；
// 分组名允许前后空格与大小写差异。
func TestCanaryGroupFilter(t *testing.T) {
	cases := []struct {
		name       string
		groups     string
		group      string
		wantHit    bool
		wantNoRand bool // 分组未命中时应在掷骰子之前就退出
	}{
		{"未配置分组=全部分组", "", "default", true, false},
		{"分组命中", "vip,default", "default", true, false},
		{"分组未命中", "vip", "default", false, true},
		{"分组大小写与空格容错", " VIP , Default ", "default", true, false},
		{"中文逗号分隔", "vip，default", "default", true, false},
		{"用户分组为空且未配置", "", "", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setCanaryOptions(t, map[string]string{
				OptionCanaryEnabled:   "true",
				OptionCanaryChannelId: "9",
				OptionCanaryPercent:   "100",
				OptionCanaryGroups:    tc.groups,
			})
			stub := stubCanary(t, true, enabledCanaryChannel(9), nil, -1)

			got := pickCanaryChannel(context.Background(), tc.group, "gpt-4o")
			if tc.wantHit && (got == nil || got.Id != 9) {
				t.Fatalf("该分组应命中灰度，实际 %+v", got)
			}
			if !tc.wantHit && got != nil {
				t.Fatalf("该分组不应命中灰度，实际拿到渠道 #%d", got.Id)
			}
			if tc.wantNoRand && stub.randCalls != 0 {
				t.Fatalf("分组未命中时不应掷骰子，实际调用 %d 次", stub.randCalls)
			}
		})
	}
}

// TestCanaryProbabilityBoundary 用可控随机源对概率边界做确定性断言：
// 命中区间是 [0, percent)，所以 0% 永不命中、100% 必定命中、中间值可精确判定。
func TestCanaryProbabilityBoundary(t *testing.T) {
	cases := []struct {
		name      string
		percent   string
		randValue int
		wantHit   bool
	}{
		{"0% 即使随机源取最大值也不命中", "0", 99, false},
		{"100% 随机源取 0 必定命中", "100", 0, true},
		{"100% 随机源取 99 必定命中", "100", 99, true},
		{"30% 时随机值 29 命中", "30", 29, true},
		{"30% 时随机值 30 不命中", "30", 30, false},
		{"1% 时随机值 0 命中", "1", 0, true},
		{"1% 时随机值 1 不命中", "1", 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setCanaryOptions(t, map[string]string{
				OptionCanaryEnabled:   "true",
				OptionCanaryChannelId: "9",
				OptionCanaryPercent:   tc.percent,
			})
			stubCanary(t, true, enabledCanaryChannel(9), nil, tc.randValue)

			got := pickCanaryChannel(context.Background(), "default", "gpt-4o")
			if tc.wantHit && (got == nil || got.Id != 9) {
				t.Fatalf("比例 %s%%、随机值 %d 应命中灰度，实际 %+v", tc.percent, tc.randValue, got)
			}
			if !tc.wantHit && got != nil {
				t.Fatalf("比例 %s%%、随机值 %d 不应命中灰度，实际拿到渠道 #%d", tc.percent, tc.randValue, got.Id)
			}
		})
	}
}

// TestCanaryEnabledValueTolerance 开关值容错：大小写与空白都不应让开关失灵。
func TestCanaryEnabledValueTolerance(t *testing.T) {
	for _, value := range []string{"true", "True", " TRUE "} {
		t.Run(value, func(t *testing.T) {
			setCanaryOptions(t, map[string]string{
				OptionCanaryEnabled:   value,
				OptionCanaryChannelId: " 9 ",
				OptionCanaryPercent:   " 100 ",
			})
			stubCanary(t, true, enabledCanaryChannel(9), nil, -1)

			if got := pickCanaryChannel(context.Background(), "default", "gpt-4o"); got == nil || got.Id != 9 {
				t.Fatalf("开关取值 %q 应视为启用，实际 %+v", value, got)
			}
		})
	}
}

// TestCanaryOptionMissingMap OptionMap 尚未初始化时不能 panic，按未启用处理。
func TestCanaryOptionMissingMap(t *testing.T) {
	config.OptionMapRWMutex.Lock()
	previous := config.OptionMap
	config.OptionMap = nil
	config.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		config.OptionMapRWMutex.Lock()
		config.OptionMap = previous
		config.OptionMapRWMutex.Unlock()
	})

	stubCanary(t, true, enabledCanaryChannel(9), nil, -1)
	if got := pickCanaryChannel(context.Background(), "default", "gpt-4o"); got != nil {
		t.Fatalf("配置表未初始化时不应接管选渠道，实际拿到渠道 #%d", got.Id)
	}
}
