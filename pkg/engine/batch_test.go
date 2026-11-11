package engine

import (
	"errors"
	"testing"
	"time"

	"pipeline/pkg/config"
	"pipeline/pkg/models"
	"pipeline/pkg/repo"
)

// 批量删除的编排语义验收（0.7.9）：终态才删 / 非终态拒绝 / 幂等 / 级联清理。
// repo 原语层的测试在 pkg/repo/batch_test.go；这里串起 engine.BatchDeleteTasks 与
// DeleteWorkflowFull 的完整行为。直连 Mongo 的集成测试（门控同 repo 侧）：
//
//	kubectl --context k3s-heyuan port-forward -n pipeline svc/mongo 27017:27017
//	MONGO_URI="mongodb://root:<password>@localhost:27017（密码 kubectl get secret mongo-auth -o jsonpath="{.data.password}" | base64 -d 取）" go test ./pkg/engine/ -run TestBatch -v

const batchTestWF = "batchtest-engine-wf"

func batchTestSeed(t *testing.T) {
	t.Helper()
	cleanupBatchTest(t)
	mk := func(id string, taskID int64, st models.Status) {
		if err := repo.CreateTask(&models.WorkflowTask{
			ID: id, TaskID: taskID, WorkflowName: batchTestWF, Status: st,
			// creator 与 repo 侧 batch_test 有意不同名：go test ./pkg/... 时两包进程并行
			// 打同一 mongo，repo 侧按 creator="batchtest" 数"恰 4 条"——同名会互相串台
			Creator: "batchtest-engine", CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("seed task %s: %v", id, err)
		}
	}
	mk(batchTestWF+"-1", 1, models.StatusPassed)
	mk(batchTestWF+"-2", 2, models.StatusRunning)
	mk(batchTestWF+"-3", 3, models.StatusSkipped)
	if err := repo.CreateCron(&models.Cron{Name: "batchtest-engine-cron", WorkflowName: batchTestWF, Crontab: "0 3 * * *"}); err != nil {
		t.Fatalf("seed cron: %v", err)
	}
}

func cleanupBatchTest(t *testing.T) {
	t.Helper()
	if _, err := repo.DeleteTasksByWorkflow(batchTestWF); err != nil {
		t.Fatalf("cleanup tasks: %v", err)
	}
	if err := repo.DeleteCron("batchtest-engine-cron"); err != nil && !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cleanup cron: %v", err)
	}
}

// TestBatchDeleteTasks —— 终态删 / 非终态拒绝点名 / 重复与不存在幂等
func TestBatchDeleteTasks(t *testing.T) {
	if err := repo.Init(config.Load().MongoURI); err != nil {
		t.Skipf("mongo unavailable, skip: %v", err)
	}
	batchTestSeed(t)
	defer cleanupBatchTest(t)

	res := BatchDeleteTasks([]string{
		batchTestWF + "-1",      // passed → 删
		batchTestWF + "-2",      // running → 拒绝（用户拍板：非终态项拒绝，先取消再删）
		batchTestWF + "-3",      // skipped（终态集成员）→ 删
		batchTestWF + "-1",      // 重复键 → 第二发幂等成功
		"batchtest-no-such-999", // 不存在 → 幂等成功
	})
	if len(res.Deleted) != 4 {
		t.Fatalf("deleted = %v, want 4 keys", res.Deleted)
	}
	if len(res.Failed) != 1 || res.Failed[0].Key != batchTestWF+"-2" {
		t.Fatalf("failed = %+v, want exactly %s", res.Failed, batchTestWF+"-2")
	}
	// 现场核对：running 的档案必须还在
	if _, err := repo.FindTaskByID(batchTestWF + "-2"); err != nil {
		t.Errorf("running task archive was deleted (must be rejected): %v", err)
	}
}

// TestDeleteWorkflowFullCascade —— 级联清理：任务档案（不分状态）+ 定时器一并消失，幂等重放不炸
func TestDeleteWorkflowFullCascade(t *testing.T) {
	if err := repo.Init(config.Load().MongoURI); err != nil {
		t.Skipf("mongo unavailable, skip: %v", err)
	}
	batchTestSeed(t)
	defer cleanupBatchTest(t)

	if err := DeleteWorkflowFull(batchTestWF); err != nil {
		t.Fatalf("DeleteWorkflowFull: %v", err)
	}
	for _, id := range []string{batchTestWF + "-1", batchTestWF + "-2", batchTestWF + "-3"} {
		if _, err := repo.FindTaskByID(id); !errors.Is(err, repo.ErrNotFound) {
			t.Errorf("task %s survived cascade: %v", id, err)
		}
	}
	if _, err := repo.FindCron("batchtest-engine-cron"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("cron survived cascade: %v", err)
	}
	// 幂等：图纸本就不在仓库里（测试没造 workflow 文档）+ 重放全链不报错
	if err := DeleteWorkflowFull(batchTestWF); err != nil {
		t.Errorf("cascade replay must be idempotent: %v", err)
	}
	// 批量形态：空名单 + 重复名单都不炸
	if res := BatchDeleteWorkflows([]string{batchTestWF, batchTestWF}); len(res.Failed) != 0 {
		t.Errorf("BatchDeleteWorkflows replay failed: %+v", res.Failed)
	}
}
