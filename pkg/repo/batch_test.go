package repo

import (
	"testing"
	"time"

	"pipeline/pkg/config"
	"pipeline/pkg/models"
)

// 批量删除的 repo 原语验收（0.7.9）：跨库任务列表 / 终态守卫删除 / 按图纸批量删。
// 编排层（BatchDeleteTasks/DeleteWorkflowFull）的语义测试在 pkg/engine/batch_test.go。
// 直连集群里的 Mongo，属于集成测试（门控同 counter_test.go）：
//
//	kubectl --context k3s-heyuan port-forward -n pipeline svc/mongo 27017:27017
//	MONGO_URI="mongodb://root:<password>@localhost:27017（密码 kubectl get secret mongo-auth -o jsonpath="{.data.password}" | base64 -d 取）" go test ./pkg/repo/ -run TestBatch -v

const batchTestWFA = "batchtest-wf-a"
const batchTestWFB = "batchtest-wf-b"

// batchTestSeed —— 铺两个 workflow 的任务档案（终态/非终态混合）+ 一条挂名 cron；
// 前置先清同名残留（上次跑挂的），测试收口再清一遍
func batchTestSeed(t *testing.T) {
	t.Helper()
	cleanupBatchTest(t)
	mk := func(id, wf string, taskID int64, st models.Status, at time.Time) {
		if err := CreateTask(&models.WorkflowTask{
			ID: id, TaskID: taskID, WorkflowName: wf, Status: st, Creator: "batchtest", CreatedAt: at,
		}); err != nil {
			t.Fatalf("seed task %s: %v", id, err)
		}
	}
	base := time.Now().Add(-time.Hour)
	mk(batchTestWFA+"-1", batchTestWFA, 1, models.StatusPassed, base)
	mk(batchTestWFA+"-2", batchTestWFA, 2, models.StatusRunning, base.Add(time.Minute))
	mk(batchTestWFB+"-1", batchTestWFB, 1, models.StatusFailed, base.Add(2*time.Minute))
	mk(batchTestWFB+"-2", batchTestWFB, 2, models.StatusQueued, base.Add(3*time.Minute))
	if err := CreateCron(&models.Cron{Name: "batchtest-cron", WorkflowName: batchTestWFA, Crontab: "0 3 * * *"}); err != nil {
		t.Fatalf("seed cron: %v", err)
	}
}

func cleanupBatchTest(t *testing.T) {
	t.Helper()
	for _, wf := range []string{batchTestWFA, batchTestWFB} {
		if _, err := DeleteTasksByWorkflow(wf); err != nil {
			t.Fatalf("cleanup tasks of %s: %v", wf, err)
		}
	}
	if err := DeleteCron("batchtest-cron"); err != nil && err != ErrNotFound {
		t.Fatalf("cleanup cron: %v", err)
	}
}

// TestListTasksCrossWorkflow —— 空 workflowName = 跨库总账本：不过滤图纸、created_at 降序
func TestListTasksCrossWorkflow(t *testing.T) {
	if err := Init(config.Load().MongoURI); err != nil {
		t.Skipf("mongo unavailable, skip: %v", err)
	}
	batchTestSeed(t)
	defer cleanupBatchTest(t)

	// 跨库：Creator 过滤隔离活体数据（本测试打的是真集群 mongo，455 条真任务在场），
	// 4 条 scratch 全出，且按 created_at 降序（B-1 比 A-2 晚创建，应排前面）
	items, total, _, err := ListTasksFiltered("", TaskFilter{Creator: "batchtest", Page: 1, Size: 100})
	if err != nil {
		t.Fatalf("cross-workflow list: %v", err)
	}
	if total != 4 {
		t.Fatalf("cross-workflow total = %d, want 4", total)
	}
	got := map[string]bool{}
	for _, it := range items {
		got[it.ID] = true
	}
	for _, want := range []string{batchTestWFA + "-1", batchTestWFA + "-2", batchTestWFB + "-1", batchTestWFB + "-2"} {
		if !got[want] {
			t.Errorf("cross-workflow list missing %s", want)
		}
	}
	if items[0].ID != batchTestWFB+"-2" {
		t.Errorf("cross-workflow sort: first = %s, want %s (created_at desc)", items[0].ID, batchTestWFB+"-2")
	}

	// 单库视图行为不变：只出本库
	items, total, _, err = ListTasksFiltered(batchTestWFA, TaskFilter{Page: 1, Size: 100})
	if err != nil {
		t.Fatalf("per-workflow list: %v", err)
	}
	if total != 2 {
		t.Fatalf("per-workflow total = %d, want 2", total)
	}
	for _, it := range items {
		if it.WorkflowName != batchTestWFA {
			t.Errorf("per-workflow list leaked %s", it.ID)
		}
	}
}

// TestDeleteTaskGuard —— 终态守卫：passed/failed 删得动，running/queued 条件删除不命中
func TestDeleteTaskGuard(t *testing.T) {
	if err := Init(config.Load().MongoURI); err != nil {
		t.Skipf("mongo unavailable, skip: %v", err)
	}
	batchTestSeed(t)
	defer cleanupBatchTest(t)

	ok, err := DeleteTask(batchTestWFA + "-1") // passed
	if err != nil || !ok {
		t.Fatalf("terminal task delete: ok=%v err=%v, want deleted", ok, err)
	}
	if _, err := FindTaskByID(batchTestWFA + "-1"); err != ErrNotFound {
		t.Errorf("deleted task still readable: %v", err)
	}
	ok, err = DeleteTask(batchTestWFA + "-2") // running：守卫必须拦下
	if err != nil {
		t.Fatalf("non-terminal delete errored: %v", err)
	}
	if ok {
		t.Fatal("running task was deleted — terminal guard failed")
	}
	if _, err := FindTaskByID(batchTestWFA + "-2"); err != nil {
		t.Errorf("guarded task vanished: %v", err)
	}
}

// TestDeleteByWorkflowSweep —— 按图纸批量删档案 + 按图纸摘 cron（级联清理的两个原语）
func TestDeleteByWorkflowSweep(t *testing.T) {
	if err := Init(config.Load().MongoURI); err != nil {
		t.Skipf("mongo unavailable, skip: %v", err)
	}
	batchTestSeed(t)
	defer cleanupBatchTest(t)

	n, err := DeleteTasksByWorkflow(batchTestWFA)
	if err != nil || n != 2 {
		t.Fatalf("DeleteTasksByWorkflow = %d, %v; want 2", n, err)
	}
	if _, err := FindTaskByID(batchTestWFA + "-2"); err != ErrNotFound {
		t.Errorf("task survived sweep: %v", err)
	}
	if _, err := FindTaskByID(batchTestWFB + "-1"); err != nil {
		t.Errorf("other workflow's task was collateral damage: %v", err)
	}
}
