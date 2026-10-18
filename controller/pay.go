package controller

import (
	"crypto"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/logger"
	"github.com/xiaosu4610/LTZY-API/model"
)

// 易支付（epay）在线充值（LTZY-API 新增）。
//
// 协议以「彩虹易支付系」为准（占绝大多数克隆站）：
//   - 下单：POST {api}/mapi.php，返回 JSON（code=1 成功，给出 payurl/qrcode/urlscheme）
//   - 通知：GET/POST 到 notify_url，参数在 query/form 里，需验签
//   - 入账：只认异步通知，且必须校验 trade_status=TRADE_SUCCESS 与金额一致
//
// 同时兼容「RSA（实际为 SHA256withRSA）+ code=0 成功」的 V2 变体：
// 签名算法与成功码都做成可配置，但**入账判定不看成功码**（通知里的判定统一是 trade_status）。

// ---------- 签名 ----------

// buildSignString 构造待签名串：剔除 sign/sign_type 与空值，按 key 的 ASCII 升序拼接。
// 注意：参数值**不做 URL 编码**，这是易支付对接最常见的坑。
func buildSignString(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params[k])
	}
	return strings.Join(parts, "&")
}

// normalizePEM 兼容三种输入：完整 PEM、去掉头尾的单行 base64、带换行的 base64。
func normalizePEM(value string, keyType string) []byte {
	trimmed := strings.TrimSpace(value)
	if strings.Contains(trimmed, "BEGIN") {
		return []byte(trimmed)
	}
	cleaned := strings.NewReplacer("\r", "", "\n", "", " ", "").Replace(trimmed)
	var lines []string
	for len(cleaned) > 64 {
		lines = append(lines, cleaned[:64])
		cleaned = cleaned[64:]
	}
	lines = append(lines, cleaned)
	body := strings.Join(lines, "\n")
	return []byte(fmt.Sprintf("-----BEGIN %s-----\n%s\n-----END %s-----\n", keyType, body, keyType))
}

func parseRSAPrivateKey(value string) (*rsa.PrivateKey, error) {
	if strings.TrimSpace(value) == "" {
		return nil, errors.New("未配置商户私钥")
	}
	block, _ := pem.Decode(normalizePEM(value, "PRIVATE KEY"))
	if block == nil {
		return nil, errors.New("商户私钥 PEM 解析失败")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("商户私钥不是 RSA 类型")
		}
		return rsaKey, nil
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

func parseRSAPublicKey(value string) (*rsa.PublicKey, error) {
	if strings.TrimSpace(value) == "" {
		return nil, errors.New("未配置平台公钥")
	}
	block, _ := pem.Decode(normalizePEM(value, "PUBLIC KEY"))
	if block == nil {
		return nil, errors.New("平台公钥 PEM 解析失败")
	}
	if pub, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("平台公钥不是 RSA 类型")
		}
		return rsaPub, nil
	}
	return x509.ParsePKCS1PublicKey(block.Bytes)
}

// signParams 按配置的签名算法生成 sign。
func signParams(params map[string]string) (string, error) {
	content := buildSignString(params)
	switch strings.ToUpper(config.EpaySignType) {
	case "MD5":
		sum := md5.Sum([]byte(content + config.EpayMerchantPrivateKey))
		return hex.EncodeToString(sum[:]), nil
	default:
		privateKey, err := parseRSAPrivateKey(config.EpayMerchantPrivateKey)
		if err != nil {
			return "", err
		}
		digest := sha256.Sum256([]byte(content))
		signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
		if err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(signature), nil
	}
}

// verifyNotifySign 用平台公钥（或 MD5 密钥）校验异步通知的签名。
func verifyNotifySign(params map[string]string) error {
	sign := params["sign"]
	if sign == "" {
		return errors.New("通知缺少 sign")
	}
	signType := strings.ToUpper(params["sign_type"])
	if signType == "" {
		signType = strings.ToUpper(config.EpaySignType)
	}
	content := buildSignString(params)
	if signType == "MD5" {
		sum := md5.Sum([]byte(content + config.EpayMerchantPrivateKey))
		if hex.EncodeToString(sum[:]) != strings.ToLower(sign) {
			return errors.New("MD5 验签不通过")
		}
		return nil
	}
	publicKey, err := parseRSAPublicKey(config.EpayPlatformPublicKey)
	if err != nil {
		return err
	}
	signature, err := base64.StdEncoding.DecodeString(sign)
	if err != nil {
		return errors.New("sign 不是合法 base64")
	}
	digest := sha256.Sum256([]byte(content))
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature); err != nil {
		return errors.New("RSA 验签不通过")
	}
	return nil
}

// ---------- 下单 ----------

func publicBaseURL(c *gin.Context) string {
	if addr := strings.TrimSpace(config.ServerAddress); addr != "" &&
		!strings.Contains(addr, "localhost") && !strings.Contains(addr, "127.0.0.1") {
		return strings.TrimSuffix(addr, "/")
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		scheme = strings.Split(proto, ",")[0]
	}
	return scheme + "://" + c.Request.Host
}

func generateOutTradeNo() string {
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("L%d%s", time.Now().Unix(), hex.EncodeToString(buf))
}

type createPayOrderRequest struct {
	Amount  float64 `json:"amount"`   // 元
	PayType string  `json:"type"`     // alipay / wxpay / qqpay，留空用默认
}

// CreatePayOrder 创建充值订单并向上游下单，返回可跳转的收银台地址。
// 路由：POST /api/user/pay （登录用户）
func CreatePayOrder(c *gin.Context) {
	if !config.EpayEnabled {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "管理员未开启在线充值"})
		return
	}
	if config.EpayApiUrl == "" || config.EpayPid == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "在线充值未配置完整（缺少网关地址或商户号）"})
		return
	}
	req := createPayOrderRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if req.Amount <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "充值金额必须大于 0"})
		return
	}
	if config.EpayMinTopup > 0 && req.Amount < config.EpayMinTopup {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("单笔最低充值 %.2f 元", config.EpayMinTopup)})
		return
	}
	if req.Amount > 1000000 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "充值金额过大"})
		return
	}
	payType := req.PayType
	if payType == "" {
		payType = config.EpayDefaultType
	}
	userId := c.GetInt("id")
	quotaPerUnit := config.QuotaPerUnit
	if quotaPerUnit <= 0 {
		quotaPerUnit = 500000
	}

	order := &model.PayOrder{
		UserId:     userId,
		OutTradeNo: generateOutTradeNo(),
		Amount:     req.Amount,
		Quota:      int64(req.Amount * quotaPerUnit),
		PayType:    payType,
		Status:     model.PayOrderStatusPending,
	}
	if err := model.InsertPayOrder(order); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	base := publicBaseURL(c)
	params := map[string]string{
		"pid":          config.EpayPid,
		"type":         payType,
		"out_trade_no": order.OutTradeNo,
		"notify_url":   base + "/api/pay/notify",
		"return_url":   base + "/api/pay/return",
		"name":         fmt.Sprintf("%s 充值", config.SystemName),
		// 金额必须是「元 + 两位小数」的字符串，1 与 1.00 在部分平台会被判为不同金额
		"money":    strconv.FormatFloat(req.Amount, 'f', 2, 64),
		"clientip": c.ClientIP(),
		"device":   "pc",
		"param":    order.OutTradeNo,
	}
	if config.EpaySitename != "" {
		params["sitename"] = config.EpaySitename
	}
	params["sign_type"] = strings.ToUpper(config.EpaySignType)
	sign, err := signParams(params)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "签名失败：" + err.Error()})
		return
	}
	params["sign"] = sign

	payURL, err := submitToEpay(params)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"out_trade_no": order.OutTradeNo,
			"amount":       order.Amount,
			"payurl":       payURL,
		},
	})
	return
}

// submitToEpay 调用 mapi.php 下单，返回可跳转的收银台地址。
func submitToEpay(params map[string]string) (string, error) {
	form := make([]string, 0, len(params))
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		form = append(form, k+"="+urlQueryEscape(params[k]))
	}
	endpoint := strings.TrimSuffix(config.EpayApiUrl, "/") + "/mapi.php"
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Post(endpoint, "application/x-www-form-urlencoded", strings.NewReader(strings.Join(form, "&")))
	if err != nil {
		return "", errors.New("请求支付网关失败：" + err.Error())
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", errors.New("读取支付网关响应失败：" + err.Error())
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		preview := string(raw)
		if len(preview) > 200 {
			preview = preview[:200]
		}
		return "", errors.New("支付网关返回不是 JSON：" + preview)
	}
	// code 兼容 int(1/0) 与字符串（ZPay 失败时是 "error"），因此不做强类型断言
	codeText := fmt.Sprintf("%v", result["code"])
	// V1 成功码是 1；部分 RSA 版本用 0。两者都认，真正入账以异步通知为准。
	if codeText != "1" && codeText != "0" {
		msg := fmt.Sprintf("%v", result["msg"])
		if msg == "" || msg == "<nil>" {
			msg = "支付网关下单失败"
		}
		return "", errors.New(msg)
	}
	for _, field := range []string{"payurl", "qrcode", "urlscheme"} {
		if v, ok := result[field].(string); ok && v != "" {
			return v, nil
		}
	}
	return "", errors.New("支付网关未返回可用支付地址")
}

func urlQueryEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '_' || ch == '.' || ch == '~' {
			b.WriteByte(ch)
			continue
		}
		b.WriteString(fmt.Sprintf("%%%02X", ch))
	}
	return b.String()
}

// ---------- 异步通知 / 同步跳转 ----------

// CollectEpayParams 兼容 GET 与 POST，把通知参数收成 map。
func CollectEpayParams(c *gin.Context) map[string]string {
	params := map[string]string{}
	for k, v := range c.Request.URL.Query() {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}
	if err := c.Request.ParseForm(); err == nil {
		for k, v := range c.Request.PostForm {
			if len(v) > 0 {
				params[k] = v[0]
			}
		}
	}
	return params
}

// EpayNotify 处理异步通知：验签 → 校验金额与状态 → 幂等入账 → 返回纯文本 success。
//
// 返回体必须是纯文本小写 success，否则网关会认为通知失败并持续重发。
func EpayNotify(c *gin.Context) {
	params := CollectEpayParams(c)
	rawJSON, _ := json.Marshal(params)
	raw := string(rawJSON)

	fail := func(reason string) {
		logger.SysError("epay notify rejected: " + reason + " params=" + raw)
		c.String(http.StatusOK, "fail")
	}
	if !config.EpayEnabled {
		fail("在线充值未开启")
		return
	}
	if err := verifyNotifySign(params); err != nil {
		fail(err.Error())
		return
	}
	if pid := params["pid"]; pid != "" && pid != config.EpayPid {
		fail("商户号不匹配")
		return
	}
	outTradeNo := params["out_trade_no"]
	if outTradeNo == "" {
		fail("缺少 out_trade_no")
		return
	}
	order, err := model.GetPayOrderByOutTradeNo(outTradeNo)
	if err != nil {
		fail("本地无此订单：" + outTradeNo)
		return
	}
	// 金额一致性校验：防止篡改金额的通知
	if money := params["money"]; money != "" {
		notifyAmount, parseErr := strconv.ParseFloat(money, 64)
		if parseErr != nil || fmt.Sprintf("%.2f", notifyAmount) != fmt.Sprintf("%.2f", order.Amount) {
			fail("金额不一致")
			return
		}
	}
	if status := params["trade_status"]; status != "" && status != "TRADE_SUCCESS" {
		// 非成功状态：确认收到即可，不做入账
		c.String(http.StatusOK, "success")
		return
	}
	if order.Status == model.PayOrderStatusPaid {
		// 重复通知：直接确认，避免重复加额度
		model.UpdatePayOrderRaw(outTradeNo, raw)
		c.String(http.StatusOK, "success")
		return
	}
	paid, err := model.MarkPayOrderPaid(outTradeNo, params["trade_no"], raw)
	if err != nil {
		fail("更新订单失败：" + err.Error())
		return
	}
	if !paid {
		// 并发通知下没抢到状态变更，说明另一个请求已入账
		c.String(http.StatusOK, "success")
		return
	}
	if err := model.IncreaseUserQuota(order.UserId, order.Quota); err != nil {
		// 已经把订单置为已支付却加额度失败：记录严重日志，需人工补发
		logger.SysError(fmt.Sprintf("epay order %s marked paid but failed to increase quota: %s", outTradeNo, err.Error()))
		fail("加额度失败")
		return
	}
	model.RecordTopupLog(c.Request.Context(), order.UserId,
		fmt.Sprintf("在线充值 ¥%.2f（订单 %s）", order.Amount, outTradeNo), int(order.Quota))
	logger.SysLog(fmt.Sprintf("epay order %s paid, user %d +%d quota", outTradeNo, order.UserId, order.Quota))
	c.String(http.StatusOK, "success")
}

// EpayReturn 同步跳转：只把用户送回前台，**不做任何入账**。
func EpayReturn(c *gin.Context) {
	c.Redirect(http.StatusFound, "/console/topup?paid=1")
}
