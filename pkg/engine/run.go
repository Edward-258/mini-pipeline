// Package engine —— 编排核心：Run 状态机（run/stages/jobs/finish 四层切面）、
// 任务快照与触发（task/trigger）、派发队列（queue）、启动宣判（recover）、
// 变量渲染（vars）、任务运维操作面（ops）。
package engine

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"

	"pipeline/pkg/config"
	"pipeline/pkg/engine/cancel"
	"pipeline/pkg/k8s"
	"pipeline/pkg/models"
	"pipeline/pkg/repo"
)

// engine 包的任务执行核心按职责分四个文件（同一状态机的四层切面）：
//   run.go     —— Run 入口：装配环境、注册取消、panic 兜底（本文件）
//   stages.go  —— stage 级状态机：闸门/取消检查点/聚合/刹车 + 调度策略
//   jobs.go    —— job 级状态机：变量渲染、执行器三件套编排、输出收割
//   finish.go  —— 终态收口：markRestSkipped/jobsWorst/finish（含 IM 通知）

// taskCtx —— 任务运行期共享环境。
// mu 保 ack 互斥：并行 job 各自回写时，整棵树的字段写 + BSON 序列化 + ReplaceOne
// 必须串行（并行 goroutine 共享同一棵树，ack 全量写）。
type taskCtx struct {
	client   kubernetes.Interface
	ns       string
	t        *models.WorkflowTask // ack 的对象（整树写回的目标）
	mu       sync.Mutex
	cancelCh <-chan struct{} // T1.5 取消信号（close 广播；nil = 永不触发）
}

// ack —— 互斥版整树写回（并行场景下多 goroutine 共用）
func (tc *taskCtx) ack(t *models.WorkflowTask) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	ack(t)
}

// ackStrict —— 互斥版致命写回（与 ack 同锁面）：pause 闸门等"goroutine 即将退出"的
// 场景用——重试期间锁被持有没关系，此刻并行 job 已全部落地，没有第二个写者。
func (tc *taskCtx) ackStrict(t *models.WorkflowTask) error {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return ackStrict(t)
}

// Run —— 任务入口（runStages 状态机）。
// 铁律：每前进一步立即 ack（整树 ReplaceOne 落库）——进程随时可被杀，
// 已发生的进度不丢，这也是 M3 durable 暂停的地基。
func Run(t *models.WorkflowTask) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("engine: panic in task %s/%d: %v", t.WorkflowName, t.TaskID, r)
			finish(t, models.StatusFailed, fmt.Sprintf("engine panic: %v", r))
		}
	}()

	// T1.5：注册取消信号，goroutine 退出即注销（注册表只装活人）
	cancelCh, unregister := cancel.Register(t.ID)
	defer unregister()

	cfg := config.Load()
	client, err := k8s.NewClient(cfg.K8SContext)
	if err != nil {
		finish(t, models.StatusFailed, err.Error())
		return
	}

	t.Status = models.StatusRunning
	if t.StartTime == 0 { // 恢复路径不覆盖首次开始时间（M3：release 续跑 = 重新 Run 同一棵树）
		t.StartTime = time.Now().Unix()
	}
	ack(t)
	log.Printf("engine: task %s/%d running", t.WorkflowName, t.TaskID)

	runStages(&taskCtx{client: client, ns: cfg.K8SNamespace, t: t, cancelCh: cancelCh}, t)
}

// ack —— 任务每前进一步，整棵树写回 Mongo（全量写）。
// 也是注入给 exec.Ctx.Ack 的那一把（见 jobs.go runJobTask）——整树写回的互斥协议
// 归 engine 单点定义：持锁内才许写共享树字段，exec 侧经 Ctx.Mu/Ack 消费同一协议。
// ⚠️ best-effort：失败只记日志——中途写有 job 轮询每 2s 的全量重写兜底，下一拍自愈。
// 但 goroutine 即将消失的致命写（finish 终态 / Enqueue 入队 / pause 闸门）没有下一拍，
// 必须走 ackStrict（重试 + 上抛），否则档案停在过渡态无人补写。
func ack(t *models.WorkflowTask) {
	if err := repo.UpdateTask(t); err != nil {
		log.Printf("engine: ack task %s/%d failed: %v", t.WorkflowName, t.TaskID, err)
	}
}

// ackStrict 的注入点（单测用：updateTaskFn 换桩、ackSleep 缩拍免等退避）
var (
	updateTaskFn = repo.UpdateTask
	ackSleep     = time.Sleep
)

// ackStrict —— 致命写回：与 ack 同一落点（整树 ReplaceOne，幂等——"响应丢失其实
// 写成功"的重复写天然安全），但失败按 0.5s/1s/2s/4s 指数退避重试，共 5 次尝试
// （≈8s 封顶）。ErrNotFound 不重试立即返回——重试造不回被删的文档。更长的 Mongo
// 故障交给重启 Recover 兜底，引擎不做无限重试的守望者。
func ackStrict(t *models.WorkflowTask) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			ackSleep(time.Duration(1<<(attempt-1)) * 500 * time.Millisecond)
		}
		if err = updateTaskFn(t); err == nil {
			return nil
		}
		if errors.Is(err, repo.ErrNotFound) {
			return err
		}
	}
	return err
}
