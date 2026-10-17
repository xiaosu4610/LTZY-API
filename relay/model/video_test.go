package model

import (
	"encoding/json"
	"testing"
)

func TestVideoRequestParseAndForward(t *testing.T) {
	raw := `{
		"model": "kling-v1",
		"prompt": "一只在雨里奔跑的猫",
		"duration": 5,
		"size": "1280x720",
		"n": 2,
		"camera_control": {"type": "simple"},
		"cfg_scale": 0.7
	}`
	var req VideoRequest
	req.Extra = map[string]any{}
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if req.Model != "kling-v1" || req.Prompt != "一只在雨里奔跑的猫" {
		t.Fatalf("核心字段解析错误：%+v", req)
	}
	if req.Duration != 5 || req.Size != "1280x720" || req.N != 2 {
		t.Fatalf("扩展数值字段解析错误：%+v", req)
	}

	// 模拟 controller 收集未识别字段
	known := map[string]bool{
		"model": true, "prompt": true, "negative_prompt": true, "image": true,
		"duration": true, "size": true, "fps": true, "seed": true, "n": true,
		"response_format": true, "user": true,
	}
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &rawMap); err != nil {
		t.Fatalf("二次解析失败：%v", err)
	}
	for k, v := range rawMap {
		if known[k] {
			continue
		}
		var val any
		if err := json.Unmarshal(v, &val); err == nil {
			req.Extra[k] = val
		}
	}

	body := req.BuildForwardBody()
	if body["model"] != "kling-v1" {
		t.Fatalf("转发体缺少 model: %+v", body)
	}
	if body["duration"] != 5 {
		t.Fatalf("转发体 duration 错误: %+v", body)
	}
	if _, ok := body["camera_control"]; !ok {
		t.Fatalf("未识别字段 camera_control 应被透传: %+v", body)
	}
	if _, ok := body["cfg_scale"]; !ok {
		t.Fatalf("未识别字段 cfg_scale 应被透传: %+v", body)
	}
	if _, ok := body["fps"]; ok {
		t.Fatalf("未设置的 fps 不应出现在转发体中: %+v", body)
	}
}
