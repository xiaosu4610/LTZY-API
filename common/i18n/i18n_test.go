package i18n

import (
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// placeholderLangs 是「尚未人工翻译、先用英文兜底占位」的语言。
// 之所以把标记写进测试：占位内容如果被误当成成品翻译，测试会立刻失败，
// 提醒后来者要么补人工翻译，要么继续保留 [xx] 标记。
var placeholderLangs = []string{"fr", "ru", "es", "ar"}

func loadLocale(t *testing.T, lang string) map[string]string {
	t.Helper()
	content, err := localesFS.ReadFile("locales/" + lang + ".json")
	if err != nil {
		t.Fatalf("读取词条文件 %s 失败: %v", lang, err)
	}
	translation := make(map[string]string)
	if err := json.Unmarshal(content, &translation); err != nil {
		t.Fatalf("解析词条文件 %s 失败: %v", lang, err)
	}
	return translation
}

func newGinContext(lang, acceptLanguage string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/api/status", nil)
	if acceptLanguage != "" {
		c.Request.Header.Set("Accept-Language", acceptLanguage)
	}
	if lang != "" {
		c.Set(ContextKey, lang)
	}
	return c
}

func TestInitLoadsAllLocaleFiles(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatalf("Init() 失败: %v", err)
	}

	want := []string{"ar", "en", "es", "fr", "ru", "zh-CN"}
	got := AvailableLanguages()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("已加载语言 = %v，期望 %v（新增语言文件应当被自动加载）", got, want)
	}
}

func TestLocaleKeySetsAreIdentical(t *testing.T) {
	reference := loadLocale(t, FallbackLang)
	for _, lang := range AvailableLanguages() {
		if lang == FallbackLang {
			continue
		}
		current := loadLocale(t, lang)
		for key := range reference {
			if _, ok := current[key]; !ok {
				t.Errorf("语言 %s 缺少键 %s", lang, key)
			}
		}
		for key := range current {
			if _, ok := reference[key]; !ok {
				t.Errorf("语言 %s 多出键 %s（其它语言没有，说明漏补了）", lang, key)
			}
		}
	}
}

var formatVerbPattern = regexp.MustCompile(`%[a-zA-Z]`)

func TestTranslationsAreNonEmptyAndPlaceholdersMatch(t *testing.T) {
	reference := loadLocale(t, DefaultLang)
	for _, lang := range AvailableLanguages() {
		for key, value := range loadLocale(t, lang) {
			if strings.TrimSpace(value) == "" {
				t.Errorf("语言 %s 的键 %s 是空文案", lang, key)
			}
			// 占位符数量/顺序不一致会导致 %!s(MISSING) 之类的噪声文案被回给调用方
			want := formatVerbPattern.FindAllString(reference[key], -1)
			got := formatVerbPattern.FindAllString(value, -1)
			if strings.Join(want, ",") != strings.Join(got, ",") {
				t.Errorf("语言 %s 的键 %s 占位符为 %v，期望与 %s 的 %v 一致", lang, key, got, DefaultLang, want)
			}
		}
	}
}

func TestPlaceholderLanguagesKeepMarker(t *testing.T) {
	for _, lang := range placeholderLangs {
		marker := "[" + lang + "] "
		for key, value := range loadLocale(t, lang) {
			if !strings.HasPrefix(value, marker) {
				t.Errorf("语言 %s 的键 %s 缺少占位标记 %q（当前值 %q）：要么补人工翻译，要么保留占位标记", lang, key, marker, value)
			}
		}
	}
}

var hanPattern = regexp.MustCompile(`\p{Han}`)

func TestEnglishLocaleHasNoChineseText(t *testing.T) {
	for key, value := range loadLocale(t, DefaultLang) {
		if hanPattern.MatchString(value) {
			t.Errorf("英文词条 %s 混入了中文：%q", key, value)
		}
	}
}

func TestLegacyKeysKeepOriginalValues(t *testing.T) {
	// 这 3 个键在改造前就已被 controller 引用，值必须保持原样，避免既有调用方文案发生变化
	legacy := map[string]string{
		"invalid_parameter": "无效的参数",
		"invalid_input":     "无效的输入，请检查您的输入",
		"send_email_failed": "发送邮件失败：",
	}
	zh := loadLocale(t, FallbackLang)
	for key, want := range legacy {
		if got := zh[key]; got != want {
			t.Errorf("中文词条 %s = %q，期望 %q", key, got, want)
		}
	}
}

// TestFallbackChainWithMissingEntries 直接替换内存词条表，
// 逐层验证「请求语言 -> 默认语言 -> 兜底语言 -> 通用错误」的回退顺序。
func TestFallbackChainWithMissingEntries(t *testing.T) {
	mu.Lock()
	saved := translations
	translations = map[string]map[string]string{
		DefaultLang:  {UnknownKey: "english unknown", "only.en": "only english"},
		FallbackLang: {UnknownKey: "中文兜底", "only.zh": "只有中文"},
		"fr":         {UnknownKey: "fr unknown"},
	}
	mu.Unlock()
	defer func() {
		mu.Lock()
		translations = saved
		mu.Unlock()
	}()

	cases := []struct {
		lang, key, want string
	}{
		{"fr", "only.zh", "只有中文"},               // 目标语言与英文都没有 -> 回退中文
		{"fr", "only.en", "only english"},        // 目标语言没有、英文有 -> 回退英文
		{"de-DE", "only.zh", "只有中文"},            // 未加载的语言 -> 回退中文
		{"fr", "no.such.key", "fr unknown"},      // 整条链都没有该键 -> 该语言的通用错误文案
		{"de-DE", "no.such.key", "english unknown"}, // 未加载语言 -> 默认语言的通用错误文案
	}
	for _, tc := range cases {
		if got := T(tc.lang, tc.key); got != tc.want {
			t.Errorf("T(%q, %q) = %q，期望 %q", tc.lang, tc.key, got, tc.want)
		}
	}
}

func TestMissingKeyNeverReturnsRawKey(t *testing.T) {
	bogusKeys := []string{"no.such.key", "token.not_found", "", "invalid_parameter_missing"}
	for _, lang := range append(AvailableLanguages(), "de-DE", "", "zh-Hans") {
		for _, key := range bogusKeys {
			got := T(lang, key)
			if got == key {
				t.Errorf("T(%q, %q) 直接把键名返回给了调用方", lang, key)
			}
			if strings.TrimSpace(got) == "" {
				t.Errorf("T(%q, %q) 返回空文案", lang, key)
			}
		}
	}
}

func TestTranslateFollowsContextLanguage(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatalf("Init() 失败: %v", err)
	}

	zh := loadLocale(t, FallbackLang)
	en := loadLocale(t, DefaultLang)
	fr := loadLocale(t, "fr")

	cases := []struct {
		name           string
		lang           string
		acceptLanguage string
		want           string
	}{
		{"中间件写入中文", "zh-CN", "", zh["user.not_found"]},
		{"中间件写入英文", "en", "", en["user.not_found"]},
		{"中间件写入法语", "fr", "", fr["user.not_found"]},
		{"没有上下文语言时按请求头协商", "", "zh-CN,zh;q=0.9", zh["user.not_found"]},
		{"未支持的语言回退英文", "", "de-DE,de;q=0.9", en["user.not_found"]},
		{"语言与请求头都没有时用默认语言", "", "", en["user.not_found"]},
		{"上下文语言为空字符串时按请求头协商", "", "zh-CN", zh["user.not_found"]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newGinContext(tc.lang, tc.acceptLanguage)
			if got := Translate(c, "user.not_found"); got != tc.want {
				t.Errorf("Translate() = %q，期望 %q", got, tc.want)
			}
		})
	}
}

func TestTranslateWithNilContext(t *testing.T) {
	if got := Translate(nil, "user.not_found"); got != loadLocale(t, DefaultLang)["user.not_found"] {
		t.Errorf("Translate(nil, ...) 应当回退到默认语言，实际 = %q", got)
	}
}

func TestTranslatefKeepsFormatArguments(t *testing.T) {
	c := newGinContext("en", "")
	got := Translatef(c, "token.invalid_subnet", "not-a-subnet")
	want := "Invalid subnet: not-a-subnet"
	if got != want {
		t.Errorf("Translatef() = %q，期望 %q", got, want)
	}

	c = newGinContext("", "zh-CN")
	got = Translatef(c, "token.invalid_subnet", "not-a-subnet")
	if !strings.Contains(got, "not-a-subnet") || strings.Contains(got, "%s") {
		t.Errorf("中文占位符未被正确替换：%q", got)
	}
}

func TestNegotiate(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatalf("Init() 失败: %v", err)
	}

	cases := []struct{ header, want string }{
		{"", DefaultLang},
		{"   ", DefaultLang},
		{"zh-CN", "zh-CN"},
		{"zh", "zh-CN"},
		{"zh-TW", "zh-CN"},
		{"zh-Hans-CN", "zh-CN"},
		{"zh_CN", "zh-CN"},
		{"en", "en"},
		{"en-US,en;q=0.9", "en"},
		{"fr-FR,fr;q=0.9,en;q=0.8", "fr"},
		{"fr", "fr"},
		{"ru-RU,ru;q=0.9", "ru"},
		{"es-419,es;q=0.8", "es"},
		{"ar-SA", "ar"},
		{"en;q=0.3,zh-CN;q=0.9", "zh-CN"},
		{"en;q=0.9,zh-CN", "zh-CN"},
		{"zh-CN;q=0.8,fr;q=0.9", "fr"},
		{"de-DE,de;q=0.9", DefaultLang},
		{"*", DefaultLang},
		{"garbage", DefaultLang},
		{"  zh-CN ; q=0.7 , en ; q=0.1 ", "zh-CN"},
	}
	for _, tc := range cases {
		if got := Negotiate(tc.header); got != tc.want {
			t.Errorf("Negotiate(%q) = %q，期望 %q", tc.header, got, tc.want)
		}
	}
}

func TestUnknownKeyExistsInEveryLocale(t *testing.T) {
	for _, lang := range AvailableLanguages() {
		value, ok := loadLocale(t, lang)[UnknownKey]
		if !ok || strings.TrimSpace(value) == "" {
			t.Errorf("语言 %s 缺少兜底词条 %s", lang, UnknownKey)
		}
	}
}
