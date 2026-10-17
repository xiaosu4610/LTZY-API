package middleware

import (
	"testing"
	"time"
)

func TestInMemoryCounterIncrease(t *testing.T) {
	key := "test:counter:1"
	if v := inMemoryCounterIncrease(key, 1, time.Minute); v != 1 {
		t.Fatalf("首次计数应为 1，实际 %d", v)
	}
	if v := inMemoryCounterIncrease(key, 1, time.Minute); v != 2 {
		t.Fatalf("第二次计数应为 2，实际 %d", v)
	}
	if v := inMemoryCounterIncrease(key, 5, time.Minute); v != 7 {
		t.Fatalf("累加 5 后应为 7，实际 %d", v)
	}
}

func TestInMemoryCounterExpire(t *testing.T) {
	key := "test:counter:expire"
	inMemoryCounterIncrease(key, 3, 50*time.Millisecond)
	time.Sleep(80 * time.Millisecond)
	if v := inMemoryCounterIncrease(key, 1, time.Minute); v != 1 {
		t.Fatalf("过期后计数应重置为 1，实际 %d", v)
	}
}

func TestTokenRPMKeyWindow(t *testing.T) {
	k1 := tokenRPMKey(1)
	k2 := tokenRPMKey(2)
	if k1 == k2 {
		t.Fatal("不同令牌的限速 key 不应相同")
	}
	if tokenRPMKey(1) != k1 {
		t.Fatal("同一令牌在同一分钟内 key 应保持一致")
	}
	if tokenTPMKey(1) == k1 {
		t.Fatal("RPM 与 TPM 的 key 不应相同")
	}
}
