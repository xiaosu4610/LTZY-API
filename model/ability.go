package model

import (
	"context"
	"sort"
	"strings"

	"gorm.io/gorm"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/utils"
)

type Ability struct {
	Group     string `json:"group" gorm:"type:varchar(32);primaryKey;autoIncrement:false"`
	Model     string `json:"model" gorm:"primaryKey;autoIncrement:false"`
	ChannelId int    `json:"channel_id" gorm:"primaryKey;autoIncrement:false;index"`
	Enabled   bool   `json:"enabled"`
	Priority  *int64 `json:"priority" gorm:"bigint;default:0;index"`
}

func GetRandomSatisfiedChannel(group string, model string, ignoreFirstPriority bool) (*Channel, error) {
	ability := Ability{}
	groupCol := "`group`"
	trueVal := "1"
	if common.UsingPostgreSQL {
		groupCol = `"group"`
		trueVal = "true"
	}

	var err error = nil
	var channelQuery *gorm.DB
	if ignoreFirstPriority {
		channelQuery = DB.Where(groupCol+" = ? and model = ? and enabled = "+trueVal, group, model)
	} else {
		maxPrioritySubQuery := DB.Model(&Ability{}).Select("MAX(priority)").Where(groupCol+" = ? and model = ? and enabled = "+trueVal, group, model)
		channelQuery = DB.Where(groupCol+" = ? and model = ? and enabled = "+trueVal+" and priority = (?)", group, model, maxPrioritySubQuery)
	}
	if common.UsingSQLite || common.UsingPostgreSQL {
		err = channelQuery.Order("RANDOM()").First(&ability).Error
	} else {
		err = channelQuery.Order("RAND()").First(&ability).Error
	}
	if err != nil {
		return nil, err
	}
	channel := Channel{}
	channel.Id = ability.ChannelId
	err = DB.First(&channel, "id = ?", ability.ChannelId).Error
	return &channel, err
}

func (channel *Channel) AddAbilities() error {
	models_ := strings.Split(channel.Models, ",")
	models_ = utils.DeDuplication(models_)
	groups_ := strings.Split(channel.Group, ",")
	abilities := make([]Ability, 0, len(models_))
	for _, model := range models_ {
		for _, group := range groups_ {
			ability := Ability{
				Group:     group,
				Model:     model,
				ChannelId: channel.Id,
				Enabled:   channel.Status == ChannelStatusEnabled,
				Priority:  channel.Priority,
			}
			abilities = append(abilities, ability)
		}
	}
	return DB.Create(&abilities).Error
}

func (channel *Channel) DeleteAbilities() error {
	return DB.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
}

// UpdateAbilities updates abilities of this channel.
// Make sure the channel is completed before calling this function.
func (channel *Channel) UpdateAbilities() error {
	// A quick and dirty way to update abilities
	// First delete all abilities of this channel
	err := channel.DeleteAbilities()
	if err != nil {
		return err
	}
	// Then add new abilities
	err = channel.AddAbilities()
	if err != nil {
		return err
	}
	return nil
}

func UpdateAbilityStatus(channelId int, status bool) error {
	return DB.Model(&Ability{}).Where("channel_id = ?", channelId).Select("enabled").Update("enabled", status).Error
}

// AbilityExists 判断「分组 + 模型 + 渠道」这条能力记录是否存在且处于启用状态。
//
// 灰度路由（E10）需要知道「某个指定渠道能不能承载当前请求的模型」，
// 否则会把请求打到不支持该模型的上游上直接报错。内存缓存未开启时选渠道路径
// 也是查 Ability 表，这里保持同一口径，避免灰度与主路径判断不一致。
// 查询出错一律按「不支持」返回：宁可放弃灰度，也不能因此让请求失败。
func AbilityExists(group string, model string, channelId int) bool {
	if DB == nil {
		return false
	}
	groupCol := "`group`"
	trueVal := "1"
	if common.UsingPostgreSQL {
		groupCol = `"group"`
		trueVal = "true"
	}
	var count int64
	err := DB.Model(&Ability{}).
		Where(groupCol+" = ? and model = ? and channel_id = ? and enabled = "+trueVal, group, model, channelId).
		Count(&count).Error
	if err != nil {
		return false
	}
	return count > 0
}

func GetGroupModels(ctx context.Context, group string) ([]string, error) {
	groupCol := "`group`"
	trueVal := "1"
	if common.UsingPostgreSQL {
		groupCol = `"group"`
		trueVal = "true"
	}
	var models []string
	err := DB.Model(&Ability{}).Distinct("model").Where(groupCol+" = ? and enabled = "+trueVal, group).Pluck("model", &models).Error
	if err != nil {
		return nil, err
	}
	sort.Strings(models)
	return models, err
}

// ModelChannelSnapshot 「模型 → 该分组下可用渠道」的快照，供模型广场展示健康度。
//
// 只取渠道的 id / status / response_time：模型广场是用户可见页面，
// 绝不能把带 key 的渠道对象带进这条链路。
type ModelChannelSnapshot struct {
	// Channels 模型名 → 可用（启用状态）渠道 id 列表
	Channels map[string][]int
	// ResponseTime 渠道 id → 最近一次测速耗时（毫秒），0 表示还没测过
	ResponseTime map[int]int
}

// GetModelChannelSnapshot 汇总某分组下「每个模型由哪些渠道承载、这些渠道的响应时间」。
func GetModelChannelSnapshot(group string) (*ModelChannelSnapshot, error) {
	groupCol := "`group`"
	trueVal := "1"
	if common.UsingPostgreSQL {
		groupCol = `"group"`
		trueVal = "true"
	}
	abilities := make([]Ability, 0)
	err := DB.Where(groupCol+" = ? and enabled = "+trueVal, group).Find(&abilities).Error
	if err != nil {
		return nil, err
	}

	snapshot := &ModelChannelSnapshot{
		Channels:     make(map[string][]int, len(abilities)),
		ResponseTime: map[int]int{},
	}
	if len(abilities) == 0 {
		return snapshot, nil
	}

	channelIdSet := map[int]bool{}
	for _, ability := range abilities {
		channelIdSet[ability.ChannelId] = true
	}
	channelIds := make([]int, 0, len(channelIdSet))
	for id := range channelIdSet {
		channelIds = append(channelIds, id)
	}

	channels := make([]Channel, 0, len(channelIds))
	// Select 限定字段：这条查询不返回 key，避免上游凭据进入内存
	err = DB.Model(&Channel{}).Select("id", "status", "response_time").
		Where("id in ?", channelIds).Find(&channels).Error
	if err != nil {
		return nil, err
	}
	for _, channel := range channels {
		// 只统计启用渠道：被禁用/自动熔断的渠道不该出现在模型广场的「可用」口径里
		if channel.Status != ChannelStatusEnabled {
			continue
		}
		snapshot.ResponseTime[channel.Id] = channel.ResponseTime
	}
	for _, ability := range abilities {
		if _, ok := snapshot.ResponseTime[ability.ChannelId]; !ok {
			continue
		}
		snapshot.Channels[ability.Model] = append(snapshot.Channels[ability.Model], ability.ChannelId)
	}
	return snapshot, nil
}
