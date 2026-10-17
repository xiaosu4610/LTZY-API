package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common"
)

// E4 请求签名中间件的单元测试。
//
// 说明：测试自己按协议约定的规范串独立算一遍签名（不调用被测代码的拼接逻辑），
// 这样一旦生产实现的拼接顺序或字段被改坏，测试会立刻失败，
// 而不是「和被测代码一起改坏却仍然通过」。

const testSignatureSecret = "unit-test-secret"

// setupSignatureTest 覆写包级配置并注册恢复，避免污染同包其他测试。
func setupSignatureTest(t *testing.T, enabled bool, ttlSeconds int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	originalEnabled, originalTTL, originalSecret := signatureEnabled, signatureTTLSeconds, signatureSecret
	originalRedis := common.RedisEnabled
	signatureEnabled = enabled
	signatureTTLSeconds = ttlSeconds
	signatureSecret = testSignatureSecret
	// 无 Redis 环境：走内存降级实现，测试不依赖外部服务
	common.RedisEnabled = false
	t.Cleanup(func() {
		signatureEnabled, signatureTTLSeconds, signatureSecret = originalEnabled, originalTTL, originalSecret
		common.RedisEnabled = originalRedis
	})
}

// newSignatureTestRouter 构造一个最小中继路由，并返回一个用于观察下游是否读到 body 的指针。
func newSignatureTestRouter() (*gin.Engine, *string) {
	seenBody := new(string)
	router := gin.New()
	router.Use(SignatureVerify())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		*seenBody = string(body)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return router, seenBody
}

// signForTest 按协议文档独立实现签名算法。
func signForTest(secret string, method string, path string, timestamp string, nonce string, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(method + "\n" + path + "\n" + timestamp + "\n" + nonce + "\n" + body))
	return hex.EncodeToString(mac.Sum(nil))
}

// doSignedRequest 发送一次带签名头的请求；timestamp 传空串表示不带签名头。
func doSignedRequest(router *gin.Engine, path string, body string, signature string, timestamp string, nonce string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if signature != "" {
		req.Header.Set(signatureHeader, signature)
	}
	if timestamp != "" {
		req.Header.Set(signatureTimestampH, timestamp)
	}
	if nonce != "" {
		req.Header.Set(signatureNonceH, nonce)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

// errorCodeOf 取出统一错误结构里的 error.code。
func errorCodeOf(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("响应不是合法 JSON: %v, body=%s", err, recorder.Body.String())
	}
	return payload.Error.Code
}

func nowTimestamp() string {
	return strconv.FormatInt(time.Now().Unix(), 10)
}

// TestSignatureValidRequestPasses 正确签名应当放行，且下游仍能读到完整 body。
func TestSignatureValidRequestPasses(t *testing.T) {
	setupSignatureTest(t, true, 300)
	router, seenBody := newSignatureTestRouter()

	path := "/v1/chat/completions"
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	ts, nonce := nowTimestamp(), "nonce-pass-1"
	sig := signForTest(testSignatureSecret, http.MethodPost, path, ts, nonce, body)

	recorder := doSignedRequest(router, path, body, sig, ts, nonce)
	if recorder.Code != http.StatusOK {
		t.Fatalf("正确签名应放行，实际状态码 %d，body=%s", recorder.Code, recorder.Body.String())
	}
	if *seenBody != body {
		t.Fatalf("签名中间件不应吞掉请求体，期望 %q，实际 %q", body, *seenBody)
	}
}

// TestSignatureTamperedBodyFails 篡改 body 后签名不再匹配，必须 401。
func TestSignatureTamperedBodyFails(t *testing.T) {
	setupSignatureTest(t, true, 300)
	router, seenBody := newSignatureTestRouter()

	path := "/v1/chat/completions"
	signedBody := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	tamperedBody := `{"model":"gpt-4o","messages":[{"role":"user","content":"hack"}]}`
	ts, nonce := nowTimestamp(), "nonce-tamper-1"
	sig := signForTest(testSignatureSecret, http.MethodPost, path, ts, nonce, signedBody)

	recorder := doSignedRequest(router, path, tamperedBody, sig, ts, nonce)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("篡改 body 应返回 401，实际 %d", recorder.Code)
	}
	if code := errorCodeOf(t, recorder); code != "signature_invalid" {
		t.Fatalf("错误码应为 signature_invalid，实际 %s", code)
	}
	if *seenBody != "" {
		t.Fatalf("被拒请求不应到达业务处理器，实际读到 body=%q", *seenBody)
	}
}

// TestSignatureTamperedSignatureFails 签名被改动一位也应失败（防止比较逻辑过宽）。
func TestSignatureTamperedSignatureFails(t *testing.T) {
	setupSignatureTest(t, true, 300)
	router, _ := newSignatureTestRouter()

	path := "/v1/chat/completions"
	body := `{"model":"gpt-4o"}`
	ts, nonce := nowTimestamp(), "nonce-tamper-2"
	sig := signForTest(testSignatureSecret, http.MethodPost, path, ts, nonce, body)
	tampered := "0" + sig[1:]
	if tampered == sig {
		tampered = "1" + sig[1:]
	}

	recorder := doSignedRequest(router, path, body, tampered, ts, nonce)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("签名被篡改应返回 401，实际 %d", recorder.Code)
	}
	if code := errorCodeOf(t, recorder); code != "signature_invalid" {
		t.Fatalf("错误码应为 signature_invalid，实际 %s", code)
	}
}

// TestSignatureExpiredTimestampFails 时间戳超出允许偏差（含未来时间）应判定过期。
func TestSignatureExpiredTimestampFails(t *testing.T) {
	setupSignatureTest(t, true, 300)
	router, _ := newSignatureTestRouter()

	path := "/v1/chat/completions"
	body := `{"model":"gpt-4o"}`
	cases := []struct {
		name string
		ts   string
	}{
		{"过于陈旧", strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)},
		{"未来时间", strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10)},
	}
	for _, tc := range cases {
		nonce := "nonce-expire-" + tc.name
		sig := signForTest(testSignatureSecret, http.MethodPost, path, tc.ts, nonce, body)
		recorder := doSignedRequest(router, path, body, sig, tc.ts, nonce)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s：应返回 401，实际 %d", tc.name, recorder.Code)
		}
		if code := errorCodeOf(t, recorder); code != "signature_expired" {
			t.Fatalf("%s：错误码应为 signature_expired，实际 %s", tc.name, code)
		}
	}
}

// TestSignatureNonceReplayFails 同一 nonce 在有效窗口内第二次出现必须判为重放。
func TestSignatureNonceReplayFails(t *testing.T) {
	setupSignatureTest(t, true, 300)
	router, _ := newSignatureTestRouter()

	path := "/v1/chat/completions"
	body := `{"model":"gpt-4o","stream":true}`
	ts, nonce := nowTimestamp(), "nonce-replay-1"
	sig := signForTest(testSignatureSecret, http.MethodPost, path, ts, nonce, body)

	if recorder := doSignedRequest(router, path, body, sig, ts, nonce); recorder.Code != http.StatusOK {
		t.Fatalf("首次请求应放行，实际 %d，body=%s", recorder.Code, recorder.Body.String())
	}
	// 完整重放：头与 body 一字不改
	replay := doSignedRequest(router, path, body, sig, ts, nonce)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("重放应返回 401，实际 %d", replay.Code)
	}
	if code := errorCodeOf(t, replay); code != "nonce_replayed" {
		t.Fatalf("错误码应为 nonce_replayed，实际 %s", code)
	}
}

// TestSignatureMissingHeadersFails 缺头按签名无效处理（客户端实现错误的常见形态）。
func TestSignatureMissingHeadersFails(t *testing.T) {
	setupSignatureTest(t, true, 300)
	router, _ := newSignatureTestRouter()

	recorder := doSignedRequest(router, "/v1/chat/completions", `{"model":"gpt-4o"}`, "", "", "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("缺少签名头应返回 401，实际 %d", recorder.Code)
	}
	if code := errorCodeOf(t, recorder); code != "signature_invalid" {
		t.Fatalf("错误码应为 signature_invalid，实际 %s", code)
	}
}

// TestSignatureInvalidTimestampFormatFails 时间戳非数字归为签名无效，而不是过期。
func TestSignatureInvalidTimestampFormatFails(t *testing.T) {
	setupSignatureTest(t, true, 300)
	router, _ := newSignatureTestRouter()

	path := "/v1/chat/completions"
	body := `{"model":"gpt-4o"}`
	ts, nonce := "not-a-number", "nonce-bad-ts"
	sig := signForTest(testSignatureSecret, http.MethodPost, path, ts, nonce, body)

	recorder := doSignedRequest(router, path, body, sig, ts, nonce)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("非法时间戳应返回 401，实际 %d", recorder.Code)
	}
	if code := errorCodeOf(t, recorder); code != "signature_invalid" {
		t.Fatalf("错误码应为 signature_invalid，实际 %s", code)
	}
}

// TestSignatureDisabledPassesThrough 未启用开关时必须完全放行（含无任何签名头的请求）。
func TestSignatureDisabledPassesThrough(t *testing.T) {
	setupSignatureTest(t, false, 300)
	router, seenBody := newSignatureTestRouter()

	body := `{"model":"gpt-4o"}`
	recorder := doSignedRequest(router, "/v1/chat/completions", body, "", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("未启用签名时应放行，实际状态码 %d，body=%s", recorder.Code, recorder.Body.String())
	}
	if *seenBody != body {
		t.Fatalf("未启用签名时不应改动 body，期望 %q，实际 %q", body, *seenBody)
	}
}

// TestSignatureConfigFollowsEnv 校验包级配置确实由环境变量驱动。
// 已设置的环境变量与包初始化结果必须一致：默认（未设置）时开关必须是关闭的，
// 这既是「默认关闭」这条硬性要求的回归测试，也用于在 CI 中带环境变量跑一次验证启用路径。
func TestSignatureConfigFollowsEnv(t *testing.T) {
	raw, present := os.LookupEnv("ENABLE_REQUEST_SIGNATURE")
	if !present || raw == "" {
		if signatureEnabled {
			t.Fatal("未设置 ENABLE_REQUEST_SIGNATURE 时签名校验必须默认关闭")
		}
	} else if raw == "true" && !signatureEnabled {
		t.Fatal("ENABLE_REQUEST_SIGNATURE=true 时应启用签名校验")
	}

	if _, present := os.LookupEnv("SIGNATURE_TTL_SECONDS"); !present {
		if signatureTTLSeconds != 300 {
			t.Fatalf("时间偏差默认值应为 300 秒，实际 %d", signatureTTLSeconds)
		}
	}
	if rawTTL, present := os.LookupEnv("SIGNATURE_TTL_SECONDS"); present && rawTTL != "" {
		if want, err := strconv.Atoi(rawTTL); err == nil && signatureTTLSeconds != want {
			t.Fatalf("SIGNATURE_TTL_SECONDS=%s 未被读取，实际 %d", rawTTL, signatureTTLSeconds)
		}
	}
	if _, present := os.LookupEnv("SIGNATURE_SECRET"); !present && signatureSecret != "" {
		t.Fatalf("未设置 SIGNATURE_SECRET 时应为空，实际 %q", signatureSecret)
	}
}

// TestSignatureZeroTTLFallsBackToDefault TTL 被配成 0 时既不能把所有请求判成过期，
// 也不能在 Redis 里留下永不过期的 nonce key（必须回落到默认窗口）。
func TestSignatureZeroTTLFallsBackToDefault(t *testing.T) {
	setupSignatureTest(t, true, 0)
	router, _ := newSignatureTestRouter()

	path := "/v1/chat/completions"
	body := `{"model":"gpt-4o"}`
	ts, nonce := nowTimestamp(), "nonce-zero-ttl"
	sig := signForTest(testSignatureSecret, http.MethodPost, path, ts, nonce, body)

	recorder := doSignedRequest(router, path, body, sig, ts, nonce)
	if recorder.Code != http.StatusOK {
		t.Fatalf("TTL 配成 0 时应回落到默认窗口并放行，实际 %d，body=%s", recorder.Code, recorder.Body.String())
	}
}

// TestInMemoryNonceStoreBehaviour 内存降级实现的去重与过期语义。
func TestInMemoryNonceStoreBehaviour(t *testing.T) {
	if !inMemoryNonceConsume("test:nonce:1", time.Minute) {
		t.Fatal("首次占用 nonce 应成功")
	}
	if inMemoryNonceConsume("test:nonce:1", time.Minute) {
		t.Fatal("窗口内重复 nonce 不应成功")
	}
	if !inMemoryNonceConsume("test:nonce:2", time.Minute) {
		t.Fatal("不同 nonce 之间不应互相影响")
	}
	if !inMemoryNonceConsume("test:nonce:3", 50*time.Millisecond) {
		t.Fatal("首次占用 nonce 应成功")
	}
	time.Sleep(80 * time.Millisecond)
	if !inMemoryNonceConsume("test:nonce:3", time.Minute) {
		t.Fatal("过期后的 nonce 应可重新占用")
	}
}

// TestInMemoryNonceConsumeConcurrency 并发抢占同一 nonce 时只能有一个成功，
// 否则重放窗口会出现竞态漏判。
func TestInMemoryNonceConsumeConcurrency(t *testing.T) {
	const goroutines = 100
	var success int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if inMemoryNonceConsume("test:nonce:race", time.Minute) {
				atomic.AddInt64(&success, 1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if success != 1 {
		t.Fatalf("同一 nonce 并发占用成功的次数应为 1，实际 %d", success)
	}
}
