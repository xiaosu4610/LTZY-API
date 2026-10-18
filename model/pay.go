package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// 在线充值订单（LTZY-API 新增，对接易支付 epay）。
//
// 只在「异步通知验签通过」时才把订单置为已支付并给用户加额度；
// return_url（同步跳转）仅用于把用户送回前台，绝不作为入账依据。
const (
	PayOrderStatusPending = 0
	PayOrderStatusPaid    = 1
	PayOrderStatusClosed  = 2
)

type PayOrder struct {
	Id         int     `json:"id"`
	UserId     int     `json:"user_id" gorm:"index"`
	OutTradeNo string  `json:"out_trade_no" gorm:"uniqueIndex;type:varchar(64)"`
	TradeNo    string  `json:"trade_no" gorm:"type:varchar(64)"`
	Amount     float64 `json:"amount"` // 下单金额（元）
	Quota      int64   `json:"quota"`  // 应付额度（按当前每元额度折算）
	// 支付渠道费率（LTZY-API 新增）：下单时按渠道费率固化，便于对账与成本核算。
	// 事后改费率不会篡改历史订单，否则历史毛利会随配置漂移。
	FeeRate   float64 `json:"fee_rate"`   // 费率，如 0.06 表示 6%
	FeeAmount float64 `json:"fee_amount"` // 手续费（元）
	NetAmount float64 `json:"net_amount"` // 实收（元）= Amount - FeeAmount
	PayType   string  `json:"pay_type" gorm:"type:varchar(32)"`
	Status     int     `json:"status" gorm:"default:0;index"`
	CreateTime int64   `json:"create_time" gorm:"bigint"`
	PayTime    int64   `json:"pay_time" gorm:"bigint;default:0"`
	// NotifyRaw 保存最后一次通知原文，出问题时可直接对照平台侧记录排查
	NotifyRaw string `json:"notify_raw" gorm:"type:text"`
}

func InsertPayOrder(order *PayOrder) error {
	order.CreateTime = time.Now().Unix()
	return DB.Create(order).Error
}

func GetPayOrderByOutTradeNo(outTradeNo string) (*PayOrder, error) {
	order := &PayOrder{}
	err := DB.Where("out_trade_no = ?", outTradeNo).First(order).Error
	if err != nil {
		return nil, err
	}
	return order, nil
}

// MarkPayOrderPaid 幂等地把订单置为已支付：只有从「待支付」变更时才返回 true。
//
// 用一条带条件的 UPDATE 完成「检查 + 变更」，避免两个并发的重复通知同时通过
// 「先查后改」的判断而重复加额度。这是异步通知必须处理的场景（平台一定会重发）。
func MarkPayOrderPaid(outTradeNo string, tradeNo string, notifyRaw string) (bool, error) {
	result := DB.Model(&PayOrder{}).
		Where("out_trade_no = ? AND status = ?", outTradeNo, PayOrderStatusPending).
		Updates(map[string]any{
			"status":     PayOrderStatusPaid,
			"trade_no":   tradeNo,
			"pay_time":   time.Now().Unix(),
			"notify_raw": notifyRaw,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func UpdatePayOrderRaw(outTradeNo string, notifyRaw string) {
	_ = DB.Model(&PayOrder{}).Where("out_trade_no = ?", outTradeNo).
		Update("notify_raw", notifyRaw).Error
}

var ErrPayOrderNotFound = errors.New("订单不存在")

func IsPayOrderNotFound(err error) bool {
	return errors.Is(err, ErrPayOrderNotFound) || errors.Is(err, gorm.ErrRecordNotFound)
}
