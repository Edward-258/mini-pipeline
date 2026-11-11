package engine

import (
	"errors"
	"testing"
	"time"

	"pipeline/pkg/models"
	"pipeline/pkg/repo"
)

// ackStrict / Enqueue 的重试与回滚语义（0.7.11）：注入 updateTaskFn/ackSleep 桩，
// 无需 mongo。repo 层的 MatchedCount 守卫在 pkg/repo/task_test.go（mongo 门控）。

type ackStub struct {
	calls int
	slept []time.Duration
}

// stubAck —— 换 updateTaskFn 桩（按序吐错误、耗尽后恒 nil）+ ackSleep 空转并记录节拍
func stubAck(t *testing.T, errs ...error) *ackStub {
	t.Helper()
	s := &ackStub{}
	origFn, origSleep := updateTaskFn, ackSleep
	updateTaskFn = func(*models.WorkflowTask) error {
		i := s.calls
		s.calls++
		if i < len(errs) {
			return errs[i]
		}
		return nil
	}
	ackSleep = func(d time.Duration) { s.slept = append(s.slept, d) }
	t.Cleanup(func() { updateTaskFn, ackSleep = origFn, origSleep })
	return s
}

// 前两次失败第三次成功 → nil，共 3 次尝试
func TestAckStrictSucceedsAfterRetries(t *testing.T) {
	s := stubAck(t, errors.New("mongo down"), errors.New("mongo down"))
	if err := ackStrict(&models.WorkflowTask{ID: "wf-1"}); err != nil {
		t.Fatalf("ackStrict = %v, want nil", err)
	}
	if s.calls != 3 {
		t.Fatalf("updateTaskFn calls = %d, want 3 (fail, fail, success)", s.calls)
	}
}

// 恒失败 → 恰好 5 次尝试后返回最后错误，退避节拍 0.5s/1s/2s/4s
func TestAckStrictExhaustsRetries(t *testing.T) {
	boom := errors.New("mongo down")
	s := stubAck(t, boom, boom, boom, boom, boom, boom) // 多备一个防越界（恒失败语义）
	err := ackStrict(&models.WorkflowTask{ID: "wf-1"})
	if !errors.Is(err, boom) {
		t.Fatalf("ackStrict err = %v, want boom", err)
	}
	if s.calls != 5 {
		t.Fatalf("updateTaskFn calls = %d, want exactly 5", s.calls)
	}
	wantSlept := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second}
	if len(s.slept) != len(wantSlept) {
		t.Fatalf("slept %v, want %v", s.slept, wantSlept)
	}
	for i, d := range wantSlept {
		if s.slept[i] != d {
			t.Fatalf("slept[%d] = %v, want %v (backoff 0.5/1/2/4s)", i, s.slept[i], d)
		}
	}
}

// ErrNotFound 不重试立即返回——重试造不回被删的文档
func TestAckStrictNotFoundNoRetry(t *testing.T) {
	s := stubAck(t, repo.ErrNotFound)
	err := ackStrict(&models.WorkflowTask{ID: "wf-1"})
	if !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("ackStrict err = %v, want ErrNotFound", err)
	}
	if s.calls != 1 {
		t.Fatalf("updateTaskFn calls = %d, want 1 (no retry on not-found)", s.calls)
	}
}

// Enqueue 失败：错误上抛 + 内存态回滚 created（与 DB 实态对齐）
func TestEnqueueRollbackOnError(t *testing.T) {
	boom := errors.New("mongo down")
	s := stubAck(t, boom, boom, boom, boom, boom, boom)
	tk := &models.WorkflowTask{ID: "wf-1", WorkflowName: "wf", TaskID: 1, Status: models.StatusCreated}
	if err := Enqueue(tk); err == nil {
		t.Fatal("Enqueue = nil error, want failure after retries")
	}
	if tk.Status != models.StatusCreated {
		t.Fatalf("status = %s, want rolled back to created", tk.Status)
	}
	if s.calls != 5 {
		t.Fatalf("updateTaskFn calls = %d, want 5", s.calls)
	}
}

// Enqueue 成功：queued + nil
func TestEnqueueQueuedOnSuccess(t *testing.T) {
	stubAck(t) // 零错误序列 = 恒成功
	tk := &models.WorkflowTask{ID: "wf-1", WorkflowName: "wf", TaskID: 1, Status: models.StatusCreated}
	if err := Enqueue(tk); err != nil {
		t.Fatalf("Enqueue = %v, want nil", err)
	}
	if tk.Status != models.StatusQueued {
		t.Fatalf("status = %s, want queued", tk.Status)
	}
}
