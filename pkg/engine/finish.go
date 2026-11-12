package engine

import (
	"log"
	"time"

	"pipeline/pkg/engine/notify"
	"pipeline/pkg/models"
)

// 终态收口：聚合、失败刹车的树内标注、任务收尾（含 IM 通知副作用）。

// jobsWorst —— stage 终态 = jobs 最差者
func jobsWorst(st *models.StageTask) models.Status {
	statuses := make([]models.Status, 0, len(st.Jobs))
	for _, jt := range st.Jobs {
		statuses = append(statuses, jt.Status)
	}
	return models.WorstOf(statuses...)
}

// markRestSkipped —— 失败刹车：failedStage 之后的所有 stage（含其 jobs）标记 skipped 并落库
func markRestSkipped(t *models.WorkflowTask, failedStage *models.StageTask) {
	after := false
	for _, st := range t.Stages {
		if st == failedStage {
			after = true
			continue
		}
		if !after {
			continue
		}
		st.Status = models.StatusSkipped
		st.EndTime = time.Now().Unix()
		for _, jt := range st.Jobs {
			jt.Status = models.StatusSkipped
		}
	}
	ack(t) // best-effort 足够：紧跟的 finish 全量写会把 skipped 标注一并带上
}

// CleanupWorkspace —— 任务级共享工作区 PVC 的清理钩子（决断 #17）：main.go 装配真身
// （var 钩子形态，装配点同 api 层 Deps 都收拢在 main），nil = 共享工作区未启用
// （单测/降级纯数据）。finish 是自然终态唯一汇点，删卷挂这里；钩子自己 best-effort
// （失败响亮日志）——漏删由 Recover 启动清扫兜底（label 扫出 → 任务已终态/已删才删）
var CleanupWorkspace func(workflowName string, taskID int64)

// finish —— 终态收口：置状态、记时间、最后一次 ack、IM 通知（fire-and-forget）。
// ⚠️ 通知只挂在这条引擎自然终态路径上；CancelTree 的纯数据收口（T1.5）不走这里
// （没有 goroutine 在场，通知让位给调用方 API——明确接受这个不对称）。
// 终态写是致命写（goroutine 即将消失，没有下一拍补写）——走 ackStrict 重试档；
// 耗尽时打响亮日志：档案停在 running，重启后由 Recover 宣判收口。
func finish(t *models.WorkflowTask, status models.Status, errMsg string) {
	t.Status = status
	t.EndTime = time.Now().Unix()
	t.Error = errMsg
	if err := ackStrict(t); err != nil {
		log.Printf("engine: TERMINAL ACK FAILED task %s/%d status=%s: %v — task orphaned until restart Recover",
			t.WorkflowName, t.TaskID, status, err)
	}
	notify.NotifyOnFinish(t)
	// 共享工作区（决断 #17）：任务终态即删——K8s 资源是消耗品，档案才是真相源（§6 原则2）。
	// pause 不经过 finish（durable 稳态工作区存活，release 后下游 job 接着用）
	if CleanupWorkspace != nil {
		CleanupWorkspace(t.WorkflowName, t.TaskID)
	}
}
