package engine

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"pipeline/pkg/engine/cancel"
	"pipeline/pkg/k8s"
	"pipeline/pkg/models"
	"pipeline/pkg/repo"
)

// 任务运维操作面：clone / retry / cancel / log（闸门与审批另立 gate.go——独立锁面
// gateMu，生命周期操作不碰它）。
// 与 run.go（状态机）、trigger.go（触发）分立——这是 API 层的领域后端，
// handler 只剩 HTTP 形状翻译（参数解析、状态码映射），不再碰 K8s/cancel 注册表。

// sentinel 错误：API 层 errors.Is 翻译成 404/409，其余一律 500
var (
	ErrNoSnapshot    = errors.New("task has no workflow snapshot (legacy archive?)")
	ErrTaskFinal     = errors.New("task is already finished")
	ErrTaskNotFinal  = errors.New("only finished tasks can be retried")
	ErrTaskMoved     = errors.New("task state changed, reload and retry")
	ErrStageNotFound = errors.New("stage not found")
	ErrJobNotFound   = errors.New("stage/job not found")
	ErrNotWaiting    = errors.New("stage is not waiting for release")
)

// ---------- T1.7：克隆 ----------

// CloneTask —— 一键原样重跑（历史即模板）：快照再快照（WorkflowArgs 是当时的完整
// 图纸快照，BuildTask 等价构造新号任务，两层快照互不共享指针，各是各的档案）、
// 全新 GlobalContext（不继承旧任务收割的 outputs——它们由新一轮运行重新产生）、
// 全新 created 树。克隆任何状态的任务都合法。
func CloneTask(src *models.WorkflowTask) (*models.WorkflowTask, error) {
	if src.WorkflowArgs == nil {
		return nil, ErrNoSnapshot
	}
	return spawn(src.WorkflowArgs, "clone:"+src.ID)
}

// ---------- T1.8：整任务重试 ----------

// RetryTask —— 从失败处续跑：仅终态任务可用（running/queued/pause 有看护人或排队
// 语义，返回 ErrTaskNotFinal）。树内手术见 resetForRetryRun；GlobalContext 保留：
// 上游 passed job 的 outputs 继续可用，失败 job 重跑后重新收割。
// ⚠️ 整树写回走条件更新（守卫=终态集）：双击 retry 的第二发在读到写回窗口里会看到
// 文档已被第一发翻走 → MatchedCount==0 → ErrTaskMoved（409），杜绝同一棵树两个看护人。
func RetryTask(workflowName string, taskID int64) (*models.WorkflowTask, error) {
	t, err := repo.FindTask(workflowName, taskID)
	if err != nil {
		return nil, err
	}
	if !models.StatusFinished(t.Status) {
		return nil, fmt.Errorf("%w (task is %s)", ErrTaskNotFinal, t.Status)
	}
	resetForRetryRun(t) // 重置+入队一体：直接落 queued，单次条件写回完成，不给竞态留两段窗口
	ok, err := repo.UpdateTaskIfStatus(t, models.FinishedStatuses...)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrTaskMoved
	}
	log.Printf("engine: task %s/%d queued for retry", t.WorkflowName, t.TaskID)
	return t, nil
}

// resetForRetryRun —— 整任务重试的树内手术（纯内存，可单测）。
// 与单 job 重试（exec.resetForRetry）的分工：那是 job 内一步重来的现场清理，这是任务级
// 选择性复活——passed 子树不动，其余全部拉回 created。
func resetForRetryRun(t *models.WorkflowTask) {
	for _, st := range t.Stages {
		if st.Status == models.StatusPassed {
			continue // passed stage 整棵保留（对照 runStages 的跳过逻辑）
		}
		st.Status = models.StatusCreated
		st.Error = ""
		st.EndTime = 0
		// StartTime 保留：它是"第一次触到这里"的历史（Run 的 StartTime 首标语义同源）
		for _, jt := range st.Jobs {
			if jt.Status == models.StatusPassed {
				continue // passed job 不重跑（runStage 的跳过逻辑）
			}
			jt.Status = models.StatusCreated
			jt.Error = ""
			jt.Retry = 0 // 重试预算重置
			for _, sp := range jt.Steps {
				sp.Status = models.StatusCreated
				sp.StartTime, sp.EndTime = 0, 0
				sp.Error = ""
				sp.Log = ""
				sp.TestResult = nil
			}
		}
	}
	t.Status = models.StatusQueued // 重试=单次条件写回，重置与入队一体（不再过手 Enqueue）
	t.EndTime = 0
	t.Error = ""
}

// ---------- T1.5：取消（信号广播 / 纯数据收口的两条路径封装） ----------

// CancelTask —— 取消语义单点：
//   - 有看护 goroutine（注册表有号）：close 信号广播，看护人在一拍轮询内收口
//     → 返回 accepted=true（API 侧 202）
//   - 无看护人（created/queued/pause 稳态）：Tree 纯数据收口 + 条件写回落库
//     → 返回 accepted=false（API 侧 200）
//
// 终态任务返回 ErrTaskFinal（API 侧 409：终态不可取消，幂等语义交还给调用方）。
// 数据路径的写回带状态守卫：读取后文档被翻走（queued 被派发器抢成 running）→
// ErrTaskMoved（409），绝不拿过期副本覆盖新状态；此刻注册表已有看护人，
// 调用方再 cancel 一次自然走信号路径。
func CancelTask(workflowName string, taskID int64) (accepted bool, t *models.WorkflowTask, err error) {
	t, err = repo.FindTask(workflowName, taskID)
	if err != nil {
		return false, nil, err
	}
	if models.StatusFinished(t.Status) {
		return false, nil, fmt.Errorf("%w (%s)", ErrTaskFinal, t.Status)
	}
	if cancel.Request(t.ID) {
		return true, t, nil
	}
	cancel.Tree(t)
	ok, werr := repo.UpdateTaskIfStatus(t, models.StatusCreated, models.StatusQueued, models.StatusPause)
	if werr != nil {
		return false, nil, werr
	}
	if !ok {
		return false, nil, ErrTaskMoved
	}
	// 纯数据路径没有看护 goroutine，finish 钩子不会跑——工作区 PVC 在这里点名删
	//（queued 无卷 = 幂等 no-op；pause 的卷随取消收口）。有看护人的取消走信号路径，
	// 由 finish 统一清。删失败只记日志：Recover 清扫兜底（此时任务已终态，扫得出）
	if CleanupWorkspace != nil {
		CleanupWorkspace(t.WorkflowName, t.TaskID)
	}
	return false, t, nil
}

// ---------- 批量删除（0.7.9：前端多选/全选的后端面；全库第一个批量端点） ----------

// BatchItem —— 批量操作的逐项失败回执（deleted 是成功键列表，failed 只装失败的）
type BatchItem struct {
	Key   string `json:"key"`
	Error string `json:"error"`
}

// BatchResult —— 批量删除回执：部分成功如实上报（恒 200，不做全有全无——
// 多文档事务不在依赖面内，逐条独立删，失败项由前端 toast 点名）
type BatchResult struct {
	Deleted []string    `json:"deleted"`
	Failed  []BatchItem `json:"failed"`
}

// DeleteWorkflowFull —— 图纸删除 + 级联清理（任务档案 + 定时器）。单删与批量共用这一条路径。
// webhook 配置内嵌在图纸文档里随删自灭，无需另清；GridFS 产物属保留策略范畴
// （repo/workflow.go 既有立场），不追。图纸不存在不报错（幂等——批量重放/双击不炸）。
func DeleteWorkflowFull(name string) error {
	if err := repo.DeleteWorkflow(name); err != nil {
		return err
	}
	if _, err := repo.DeleteTasksByWorkflow(name); err != nil {
		return fmt.Errorf("delete task archives of %s: %w", name, err)
	}
	if _, err := repo.DeleteCronsByWorkflow(name); err != nil {
		return fmt.Errorf("delete crons of %s: %w", name, err)
	}
	return nil
}

// BatchDeleteWorkflows —— 多选/全选删工作流：逐条 DeleteWorkflowFull，失败项点名
func BatchDeleteWorkflows(names []string) *BatchResult {
	res := &BatchResult{Deleted: []string{}, Failed: []BatchItem{}}
	for _, name := range names {
		if err := DeleteWorkflowFull(name); err != nil {
			res.Failed = append(res.Failed, BatchItem{Key: name, Error: err.Error()})
			continue
		}
		res.Deleted = append(res.Deleted, name)
	}
	return res
}

// BatchDeleteTasks —— 多选/全选删任务档案：**只删终态**（非终态拒绝——running 有看护
// goroutine 在 ack 回写，删了会被写回复活；pause 是 durable 稳态，绕过放行语义强删
// 等于拆闸门）。双保险：内存终态判定（给失败项可读原因）+ DeleteTask 条件删除
// （防读到删窗口的 retry 竞态）。不存在 = 幂等成功。
func BatchDeleteTasks(ids []string) *BatchResult {
	res := &BatchResult{Deleted: []string{}, Failed: []BatchItem{}}
	for _, id := range ids {
		t, err := repo.FindTaskByID(id)
		if errors.Is(err, repo.ErrNotFound) {
			res.Deleted = append(res.Deleted, id) // 幂等：没有就是删完了
			continue
		}
		if err != nil {
			res.Failed = append(res.Failed, BatchItem{Key: id, Error: err.Error()})
			continue
		}
		if !models.StatusFinished(t.Status) {
			res.Failed = append(res.Failed, BatchItem{
				Key: id, Error: fmt.Sprintf("task is not finished (%s), cancel it before deleting", t.Status)})
			continue
		}
		ok, err := repo.DeleteTask(id)
		if err != nil {
			res.Failed = append(res.Failed, BatchItem{Key: id, Error: err.Error()})
			continue
		}
		if !ok {
			res.Failed = append(res.Failed, BatchItem{Key: id, Error: ErrTaskMoved.Error()})
			continue
		}
		res.Deleted = append(res.Deleted, id)
	}
	return res
}

// ---------- CI/CD 分步日志（日志流=状态信道的对外分界面） ----------

// TaskLog —— 分步日志：活任务透传 Pod 日志尾部（live 分界面），终态回档案渲染
// 分步视图（banner 切分结果 + TestResult 摘要）。step 可空 = 该 job 全部步骤。
func TaskLog(t *models.WorkflowTask, stage, job, step string) (string, error) {
	jt := findJob(t, stage, job)
	if jt == nil {
		return "", ErrJobNotFound
	}

	// 活的：K8s Job 还在 → 实时尾部
	if txt, live := k8s.TailJobLog(jt.K8sJobName); live {
		return txt, nil
	}

	// 死的：档案里的分步日志（banner 切分结果，pkg/step/logs.go）
	var b strings.Builder
	for _, sp := range jt.Steps {
		if step != "" && sp.Name != step {
			continue
		}
		fmt.Fprintf(&b, "=== step %s [%s] %s", sp.Name, sp.Type, sp.Status)
		if sp.Error != "" {
			fmt.Fprintf(&b, " — %s", sp.Error)
		}
		b.WriteString(" ===\n")
		if sp.TestResult != nil {
			fmt.Fprintf(&b, "tests=%d failures=%d errors=%d skipped=%d time=%.2fs\n",
				sp.TestResult.Tests, sp.TestResult.Failures, sp.TestResult.Errors, sp.TestResult.Skipped, sp.TestResult.Time)
		}
		if sp.Log != "" {
			b.WriteString(sp.Log)
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

// ---------- 树内查找（log 域专用；闸门域的 findWaitingStage 在 gate.go） ----------

// findStage —— 按名精确查找 stage
func findStage(t *models.WorkflowTask, name string) *models.StageTask {
	for _, st := range t.Stages {
		if st.Name == name {
			return st
		}
	}
	return nil
}

// findJob —— stage/job 双层查找
func findJob(t *models.WorkflowTask, stage, job string) *models.JobTask {
	for _, st := range t.Stages {
		if st.Name != stage {
			continue
		}
		for _, jt := range st.Jobs {
			if jt.Name == job {
				return jt
			}
		}
	}
	return nil
}
