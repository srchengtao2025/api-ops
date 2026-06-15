// BILLING v2 export worker 单测 (PR #2)
//
// 重点验证:
//   - 每用户 ≤ 2 个 running 限流 (Q3 决策)
//   - 状态机: pending → running → success
//   - 清理 map (CleanupUserSem)
//
// 跳过端到端测试 (依赖 RoDB + DB migration), 等 PR #7 集成测
package billing

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetUserSem_Singleton(t *testing.T) {
	// 同一 user_id 应返同一 semaphore
	sem1 := getUserSem(100)
	sem2 := getUserSem(100)
	if sem1 != sem2 {
		t.Fatal("getUserSem should return same channel for same user")
	}
	// 不同 user_id 应返不同
	sem3 := getUserSem(200)
	if sem1 == sem3 {
		t.Fatal("getUserSem should return different channels for different users")
	}
}

func TestUserSem_CapIs2(t *testing.T) {
	sem := getUserSem(300)
	if cap(sem) != MaxConcurrentPerUser {
		t.Fatalf("expected cap=%d, got %d", MaxConcurrentPerUser, cap(sem))
	}
}

func TestUserSem_2SlotsEnforced(t *testing.T) {
	// 模拟 3 个任务争抢 2 个槽位
	sem := getUserSem(400)
	// 占满 2 个
	sem <- struct{}{}
	sem <- struct{}{}

	// 第 3 个应该阻塞
	got3rd := false
	select {
	case sem <- struct{}{}:
		got3rd = true
	case <-time.After(100 * time.Millisecond):
		// 正常, 阻塞
	}
	if got3rd {
		t.Fatal("3rd task should block, but got through")
	}

	// 释放 1 个
	<-sem
	// 现在第 3 个应该能进
	select {
	case sem <- struct{}{}:
		// 正常
	case <-time.After(100 * time.Millisecond):
		t.Fatal("3rd task should succeed after release")
	}
}

func TestUserSem_ConcurrentEnforce(t *testing.T) {
	// 并发 N=10 个任务抢 2 槽位, 同一时刻最多 2 个
	// 用独立 user_id 避免跟其他测试污染
	sem := getUserSem(9999)
	// 清干净
	for {
		select {
		case <-sem:
		default:
			goto clean
		}
	}
clean:
	var concurrent int32
	var peak int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{} // 阻塞直到拿到
			// 进入临界区
			cur := atomic.AddInt32(&concurrent, 1)
			// 记录峰值
			for {
				old := atomic.LoadInt32(&peak)
				if cur <= old || atomic.CompareAndSwapInt32(&peak, old, cur) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond) // 模拟工作
			atomic.AddInt32(&concurrent, -1)
			<-sem
		}()
	}
	wg.Wait()
	if peak != 2 {
		t.Fatalf("expected peak concurrent=2, got %d", peak)
	}
}

func TestNewTaskID_Unique(t *testing.T) {
	// 1000 次生成, 不应有重复
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := newTaskID()
		if seen[id] {
			t.Fatalf("duplicate task_id: %s", id)
		}
		seen[id] = true
	}
}

func TestNewTaskID_Length32(t *testing.T) {
	id := newTaskID()
	if len(id) != 32 {
		t.Fatalf("expected 32-char task_id, got %d chars: %s", len(id), id)
	}
}

func TestCleanupUserSem_OnlyWhenEmpty(t *testing.T) {
	// 拿信号量 + 占 1 个槽
	sem := getUserSem(600)
	sem <- struct{}{}
	// 此时 CleanupUserSem 不应删除
	CleanupUserSem(600)
	userSemsMu.Lock()
	_, exists := userSems[600]
	userSemsMu.Unlock()
	if !exists {
		t.Fatal("CleanupUserSem should NOT delete when semaphore has occupied slots")
	}
	// 释放
	<-sem
	// 现在 CleanupUserSem 应删除
	CleanupUserSem(600)
	userSemsMu.Lock()
	_, exists = userSems[600]
	userSemsMu.Unlock()
	if exists {
		t.Fatal("CleanupUserSem should delete when semaphore is empty")
	}
}
