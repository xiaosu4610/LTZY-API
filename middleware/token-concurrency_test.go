package middleware

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/xiaosu4610/LTZY-API/common"
)

// TestInMemoryConcurrencyLimit 验证内存实现的并发上限语义（C2）
func TestInMemoryConcurrencyLimit(t *testing.T) {
	const tokenId = 90001
	const limit = 3
	for i := 0; i < limit; i++ {
		if !inMemoryConcurrencyAcquire(tokenId, limit) {
			t.Fatalf("第 %d 次占位应当成功", i+1)
		}
	}
	if inMemoryConcurrencyAcquire(tokenId, limit) {
		t.Fatal("已达上限仍占位成功")
	}
	// 释放一个槽位后应能重新占位
	inMemoryConcurrencyRelease(tokenId)
	if !inMemoryConcurrencyAcquire(tokenId, limit) {
		t.Fatal("释放槽位后占位应当成功")
	}
}

// TestConcurrencyIndependentPerToken 不同令牌的并发计数必须互相独立
func TestConcurrencyIndependentPerToken(t *testing.T) {
	if !inMemoryConcurrencyAcquire(91001, 1) {
		t.Fatal("令牌 A 首次占位应成功")
	}
	if inMemoryConcurrencyAcquire(91001, 1) {
		t.Fatal("令牌 A 已满，不应再占位成功")
	}
	if !inMemoryConcurrencyAcquire(91002, 1) {
		t.Fatal("令牌 B 不应受令牌 A 影响")
	}
}

// TestConcurrencyRaceExactlyLimit 并发抢占时成功数必须恰好等于上限。
// 这是 CAS 实现的核心保证：既不能超发（打爆上游），也不能少发（误伤容量）。
func TestConcurrencyRaceExactlyLimit(t *testing.T) {
	const tokenId = 92001
	const limit = 10
	const goroutines = 200

	var success int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // 同时起跑，制造最激烈的竞争
			if inMemoryConcurrencyAcquire(tokenId, limit) {
				atomic.AddInt64(&success, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if success != limit {
		t.Fatalf("并发抢占成功数应为 %d，实际 %d", limit, success)
	}
}

// TestAcquireConcurrencySlotWithoutRedis 验证无 Redis 时正确降级到内存实现
func TestAcquireConcurrencySlotWithoutRedis(t *testing.T) {
	original := common.RedisEnabled
	common.RedisEnabled = false
	defer func() { common.RedisEnabled = original }()

	acquired, release, err := acquireConcurrencySlot(93001, 1)
	if err != nil {
		t.Fatalf("占位不应报错: %v", err)
	}
	if !acquired || release == nil {
		t.Fatalf("首次占位应成功且返回释放函数: acquired=%v, release 是否为 nil=%v", acquired, release == nil)
	}
	if again, _, _ := acquireConcurrencySlot(93001, 1); again {
		t.Fatal("已达上限时不应占位成功")
	}
	release()
	again, rel, err := acquireConcurrencySlot(93001, 1)
	if err != nil || !again {
		t.Fatalf("释放后应能再次占位: acquired=%v err=%v", again, err)
	}
	rel()
}
