package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

//go:embed locales/*.json
var localesFS embed.FS

const (
	// DefaultLang 是请求端没有声明语言（或声明的语言完全没有对应词条）时使用的语言。
	// 保持 "en" 是为了不改变既有行为：middleware/language.go 一直把非中文请求都当作英文。
	DefaultLang = "en"
	// FallbackLang 是目标语言缺少某个词条时的第二兜底。
	// 这里刻意不再像旧实现那样把键名本身返回给调用方：本项目绝大多数调用方是中文用户，
	// 返回一句可读的中文，比让 "token.not_found" 这类内部键名泄漏到 API 响应里要好。
	FallbackLang = "zh-CN"
	// UnknownKey 是每种语言都必须提供的终极兜底词条，
	// 用于「请求语言、默认语言、兜底语言」三者都没有该键的极端情况。
	UnknownKey = "common.unknown_error"
	// unknownMessage 是连兜底词条都读不到时（词条文件损坏 / 未初始化）的最后一道防线，
	// 存在的意义同样是保证永远不把键名当文案回给调用方。
	unknownMessage = "Unknown error"
)

var (
	mu           sync.RWMutex
	translations = make(map[string]map[string]string)
	available    []string
	ContextKey   = "i18n"
)

// init 让本包在任何调用姿势下都可用：即便调用方忘了调用 Init()（例如单元测试直接调 Translate），
// 词条也已经加载，不会退化成把键名当文案返回。main.go 里的显式 Init() 仍然保留，用于校验词条文件合法性。
func init() {
	_ = Init()
}

// Init 加载 embed 进来的所有语言文件。
// 语言列表直接从 locales 目录推导，因此新增 fr/ru/es/ar 等语言文件不需要改动任何代码。
func Init() error {
	entries, err := localesFS.ReadDir("locales")
	if err != nil {
		return err
	}

	loaded := make(map[string]map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		langCode := strings.TrimSuffix(entry.Name(), ".json")
		content, err := localesFS.ReadFile("locales/" + entry.Name())
		if err != nil {
			return err
		}

		var translation map[string]string
		if err := json.Unmarshal(content, &translation); err != nil {
			return err
		}
		loaded[langCode] = translation
	}

	langs := make([]string, 0, len(loaded))
	for code := range loaded {
		langs = append(langs, code)
	}
	sort.Strings(langs)

	// 先解析再整体替换，避免读文件失败时把上一份可用词条清空
	mu.Lock()
	translations = loaded
	available = langs
	mu.Unlock()

	return nil
}

// AvailableLanguages 返回已加载的语言代码（升序），供测试与语言协商使用。
func AvailableLanguages() []string {
	mu.RLock()
	defer mu.RUnlock()

	out := make([]string, len(available))
	copy(out, available)
	return out
}

// GetLang 取本次请求使用的语言。
// 优先用中间件写入 Context 的结果；没有写（例如非 HTTP 场景或单测直接构造 gin.Context）时，
// 才退化为按请求头协商，最后回落到 DefaultLang。
func GetLang(c *gin.Context) string {
	if c == nil {
		return DefaultLang
	}
	if rawLang, ok := c.Get(ContextKey); ok {
		if lang, _ := rawLang.(string); lang != "" {
			return lang
		}
	}
	if c.Request != nil {
		if lang := Negotiate(c.GetHeader("Accept-Language")); lang != "" {
			return lang
		}
	}
	return DefaultLang
}

// Translate 按请求语言取词条。
func Translate(c *gin.Context, key string) string {
	return T(GetLang(c), key)
}

// Translatef 在 Translate 基础上做一次格式化，用于带参数的文案（如「无效的网段：%s」）。
// 约定：同一个键在所有语言里的格式化占位符数量与顺序必须一致，
// 否则会输出 %!s(MISSING) 之类的噪声（由 common/i18n 的键一致性测试兜住）。
func Translatef(c *gin.Context, key string, args ...any) string {
	return fmt.Sprintf(Translate(c, key), args...)
}

// T 是与 gin 无关的取词入口，查找顺序：
// 先按「请求语言 -> 默认语言(en) -> 兜底语言(zh-CN)」找词条本身；
// 整条链都没有该词条时，再按同样的顺序找通用错误文案；
// 连通用错误文案都读不到（词条文件损坏）才用编译期常量兜底。
// 任何一步都不会返回原始键名，避免内部键泄漏到 API 响应。
func T(lang, key string) string {
	mu.RLock()
	defer mu.RUnlock()

	if value, ok := firstMatchLocked(lang, key); ok {
		return value
	}
	if value, ok := firstMatchLocked(lang, UnknownKey); ok {
		return value
	}
	return unknownMessage
}

func firstMatchLocked(lang, key string) (string, bool) {
	for _, candidate := range []string{lang, DefaultLang, FallbackLang} {
		if value, ok := lookupLocked(candidate, key); ok {
			return value, true
		}
	}
	return "", false
}

func lookupLocked(lang, key string) (string, bool) {
	trans, ok := translations[lang]
	if !ok {
		return "", false
	}
	value, ok := trans[key]
	if !ok || value == "" {
		return "", false
	}
	return value, true
}

// Negotiate 按 RFC 7231 解析 Accept-Language 并选出最合适的已加载语言。
// 放在 i18n 包里而不是中间件里：语言协商是 i18n 的职责，中间件只负责把结果写进 gin.Context。
func Negotiate(header string) string {
	langs := AvailableLanguages()
	if len(langs) == 0 || strings.TrimSpace(header) == "" {
		return DefaultLang
	}
	for _, tag := range parseAcceptLanguage(header) {
		if tag == "*" {
			return DefaultLang
		}
		if matched := matchLanguage(tag, langs); matched != "" {
			return matched
		}
	}
	return DefaultLang
}

type acceptLang struct {
	tag string
	q   float64
	seq int
}

// parseAcceptLanguage 解析请求头并按 q 值降序返回语言标签（q 相同时保持原文顺序）。
func parseAcceptLanguage(header string) []string {
	parts := strings.Split(header, ",")
	items := make([]acceptLang, 0, len(parts))
	for i, part := range parts {
		segments := strings.Split(part, ";")
		tag := strings.ToLower(strings.TrimSpace(segments[0]))
		if tag == "" {
			continue
		}
		q := 1.0
		for _, segment := range segments[1:] {
			segment = strings.TrimSpace(segment)
			if !strings.HasPrefix(strings.ToLower(segment), "q=") {
				continue
			}
			if value, err := strconv.ParseFloat(strings.TrimSpace(segment[2:]), 64); err == nil {
				q = value
			}
		}
		items = append(items, acceptLang{tag: tag, q: q, seq: i})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].q != items[j].q {
			return items[i].q > items[j].q
		}
		return items[i].seq < items[j].seq
	})

	tags := make([]string, 0, len(items))
	for _, item := range items {
		tags = append(tags, item.tag)
	}
	return tags
}

// matchLanguage 把请求语言标签匹配到已加载的语言上：
// 先精确匹配（en / zh-CN），再按主语言子标签匹配（en-US -> en、zh-Hans -> zh-CN、zh-TW -> zh-CN）。
func matchLanguage(tag string, langs []string) string {
	tag = strings.ReplaceAll(strings.ToLower(tag), "_", "-")
	for _, lang := range langs {
		if strings.EqualFold(lang, tag) {
			return lang
		}
	}

	primary := tag
	if idx := strings.Index(tag, "-"); idx > 0 {
		primary = tag[:idx]
	}
	for _, lang := range langs {
		lower := strings.ToLower(lang)
		if lower == primary || strings.HasPrefix(lower, primary+"-") {
			return lang
		}
	}
	return ""
}
