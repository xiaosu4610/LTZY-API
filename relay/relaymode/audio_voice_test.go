package relaymode

import (
	"net/http"
	"testing"
)

// TestAudioVoicePathMapping 声音复刻端点必须映射到 AudioVoice，且不与既有 audio 前缀冲突（A5）
func TestAudioVoicePathMapping(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/v1/audio/voices", AudioVoice},
		{http.MethodPost, "/v1/audio/voices", AudioVoice},
		{http.MethodGet, "/v1/audio/voices/abc", AudioVoice},
		{http.MethodDelete, "/v1/audio/voices/abc", AudioVoice},
		{http.MethodPost, "/v1/audio/voices/abc/clone", AudioVoice},
		// 既有 audio 端点不能被抢走
		{http.MethodPost, "/v1/audio/speech", AudioSpeech},
		{http.MethodPost, "/v1/audio/transcriptions", AudioTranscription},
		{http.MethodPost, "/v1/audio/translations", AudioTranslation},
	}
	for _, tc := range cases {
		if got := GetByPath(tc.path); got != tc.want {
			t.Fatalf("%s %s 应映射为 %d，实际 %d", tc.method, tc.path, tc.want, got)
		}
	}
}

// TestAudioVoiceIsPassthrough 必须是透传家族成员（否则拿不到渠道分发与计费口径）
func TestAudioVoiceIsPassthrough(t *testing.T) {
	if !IsPassthrough(AudioVoice) {
		t.Fatal("AudioVoice 应属于透传家族")
	}
	if !IsPassthrough(Files) || !IsPassthrough(Threads) {
		t.Fatal("既有透传模式不应受影响")
	}
	if IsPassthrough(ChatCompletions) || IsPassthrough(AudioSpeech) {
		t.Fatal("文本/语音合成不是透传模式")
	}
}

// TestAudioVoiceNameRoundTrip 模式名可反查，令牌 scope 才能配置
func TestAudioVoiceNameRoundTrip(t *testing.T) {
	name := Name(AudioVoice)
	if name != "audio_voice" {
		t.Fatalf("模式名应为 audio_voice，实际 %q", name)
	}
	if !IsValidName(name) {
		t.Fatal("模式名应可通过校验")
	}
	allowed := ParseAllowedModes("chat,audio_voice")
	if !allowed[AudioVoice] {
		t.Fatal("解析 scope 时应包含 audio_voice")
	}
}
