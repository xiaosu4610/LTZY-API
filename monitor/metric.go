package monitor

import (
	"sync"

	"github.com/xiaosu4610/LTZY-API/common/config"
)

var store = make(map[int][]bool)
var storeRWMutex sync.RWMutex
var metricSuccessChan = make(chan int, config.MetricSuccessChanSize)
var metricFailChan = make(chan int, config.MetricFailChanSize)

func consumeSuccess(channelId int) {
	storeRWMutex.Lock()
	defer storeRWMutex.Unlock()
	if len(store[channelId]) > config.MetricQueueSize {
		store[channelId] = store[channelId][1:]
	}
	store[channelId] = append(store[channelId], true)
}

func consumeFail(channelId int) (bool, float64) {
	storeRWMutex.Lock()
	defer storeRWMutex.Unlock()
	if len(store[channelId]) > config.MetricQueueSize {
		store[channelId] = store[channelId][1:]
	}
	store[channelId] = append(store[channelId], false)
	successCount := 0
	for _, success := range store[channelId] {
		if success {
			successCount++
		}
	}
	successRate := float64(successCount) / float64(len(store[channelId]))
	if len(store[channelId]) < config.MetricQueueSize {
		return false, successRate
	}
	if successRate < config.MetricSuccessRateThreshold {
		store[channelId] = make([]bool, 0)
		return true, successRate
	}
	return false, successRate
}

// ChannelMetric 单个渠道的实时健康度快照。
type ChannelMetric struct {
	ChannelId   int     `json:"channel_id"`
	SuccessRate float64 `json:"success_rate"` // 最近窗口内的成功率，0~1
	SampleCount int     `json:"sample_count"` // 采样次数
}

// SnapshotMetrics 返回当前所有渠道的实时健康度快照（并发安全）。
// 说明：仅当 ENABLE_METRIC 打开且已有调用时才有数据。
func SnapshotMetrics() []ChannelMetric {
	storeRWMutex.RLock()
	defer storeRWMutex.RUnlock()
	metrics := make([]ChannelMetric, 0, len(store))
	for channelId, results := range store {
		if len(results) == 0 {
			continue
		}
		successCount := 0
		for _, success := range results {
			if success {
				successCount++
			}
		}
		metrics = append(metrics, ChannelMetric{
			ChannelId:   channelId,
			SuccessRate: float64(successCount) / float64(len(results)),
			SampleCount: len(results),
		})
	}
	return metrics
}

func metricSuccessConsumer() {
	for {
		select {
		case channelId := <-metricSuccessChan:
			consumeSuccess(channelId)
		}
	}
}

func metricFailConsumer() {
	for {
		select {
		case channelId := <-metricFailChan:
			disable, successRate := consumeFail(channelId)
			if disable {
				go MetricDisableChannel(channelId, successRate)
			}
		}
	}
}

func init() {
	if config.EnableMetric {
		go metricSuccessConsumer()
		go metricFailConsumer()
	}
}

func Emit(channelId int, success bool) {
	if !config.EnableMetric {
		return
	}
	go func() {
		if success {
			metricSuccessChan <- channelId
		} else {
			metricFailChan <- channelId
		}
	}()
}
