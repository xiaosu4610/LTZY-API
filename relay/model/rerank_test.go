package model

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestRerankRequestParse_StringDocuments 验证 documents 为字符串数组时的解析与文本提取。
func TestRerankRequestParse_StringDocuments(t *testing.T) {
	body := `{"model":"rerank-v3","query":"什么是深度学习","documents":["深度学习是机器学习的分支","今天天气很好"],"top_n":1}`
	var req RerankRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if req.Model != "rerank-v3" {
		t.Errorf("model = %q, want rerank-v3", req.Model)
	}
	if req.Query != "什么是深度学习" {
		t.Errorf("query = %q", req.Query)
	}
	if len(req.Documents) != 2 {
		t.Fatalf("documents len = %d, want 2", len(req.Documents))
	}
	texts := req.RerankAllText()
	want := []string{"什么是深度学习", "深度学习是机器学习的分支", "今天天气很好"}
	if !reflect.DeepEqual(texts, want) {
		t.Errorf("RerankAllText() = %v, want %v", texts, want)
	}
}

// TestRerankRequestParse_ObjectDocuments 验证 documents 为对象数组（Cohere 新格式）时的文本提取。
func TestRerankRequestParse_ObjectDocuments(t *testing.T) {
	body := `{"model":"rerank-v3","query":"q","documents":[{"text":"doc-one"},{"text":"doc-two"}]}`
	var req RerankRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	texts := req.RerankAllText()
	want := []string{"q", "doc-one", "doc-two"}
	if !reflect.DeepEqual(texts, want) {
		t.Errorf("RerankAllText() = %v, want %v", texts, want)
	}
}

// TestRerankDocumentText 验证单文档文本提取的兼容性。
func TestRerankDocumentText(t *testing.T) {
	if got := RerankDocumentText("plain"); got != "plain" {
		t.Errorf("string doc = %q, want plain", got)
	}
	if got := RerankDocumentText(map[string]any{"text": "obj"}); got != "obj" {
		t.Errorf("object doc = %q, want obj", got)
	}
	if got := RerankDocumentText(123); got != "" {
		t.Errorf("invalid doc = %q, want empty", got)
	}
}
