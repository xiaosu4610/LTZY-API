package model

// RerankRequest 是重排（Rerank）接口的请求体。
// 兼容 Cohere / Jina / BGE 等主流重排服务的入参格式。
type RerankRequest struct {
	Model           string `json:"model"`
	Query           string `json:"query"`
	Documents       []any  `json:"documents"`
	TopN            int    `json:"top_n,omitempty"`
	ReturnDocuments bool   `json:"return_documents,omitempty"`
	// 部分服务使用 max_chunks_per_doc 等字段，此处透传不做处理
}

// RerankDocumentText 从单个 document 中提取文本内容。
// 兼容两种格式：字符串 "text" 与对象 {"text": "..."}。
func RerankDocumentText(doc any) string {
	switch v := doc.(type) {
	case string:
		return v
	case map[string]any:
		if text, ok := v["text"].(string); ok {
			return text
		}
	}
	return ""
}

// RerankAllText 返回 query + 所有 documents 的文本，用于 token 统计与计费。
func (r RerankRequest) RerankAllText() []string {
	texts := make([]string, 0, len(r.Documents)+1)
	if r.Query != "" {
		texts = append(texts, r.Query)
	}
	for _, doc := range r.Documents {
		texts = append(texts, RerankDocumentText(doc))
	}
	return texts
}
