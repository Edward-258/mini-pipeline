package engine

import (
	"context"
	"log"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"pipeline/pkg/config"
	"pipeline/pkg/k8s"
	"pipeline/pkg/models"
	"pipeline/pkg/repo"
)

// Recover —— M3.5 启动宣判（重启即清场：遗留 running 档案宣判死亡，revoker=system）。
//
// 为什么不复活：adopt 一棵半跑过的任务树（部分 stage 已完成、K8s Job 可能已终态、
// 轮询到一半）是脏活且不确定；宣判死亡是确定性的，CI 语义下用户重跑一次任务成本≈0。
// 补齐的不变量：running 的任务要么有活着的看护人，要么是终态——不许永远卡 running。
//
// ⚠️ 绝不动 pause：暂停 = 世上本无执行流（M3 灵魂），不需要看护人，跨重启存活。
// 已知边界：滚动发布时新 Pod 宣判的瞬间旧 Pod 可能还有残影 goroutine 在 ack——
// 档案最终仍会落到终态（cancelled 被旧 Pod 的最后一次 ack 覆盖成 failed/passed 而已），
// 明确接受的取舍（没有 cancel 消息队列基建，以最后一次 ack 为准）。
func Recover() {
	cfg := config.Load()
	client, err := k8s.NewClient(cfg.K8SContext)
	if err != nil {
		log.Printf("engine: recover skipped (k8s client): %v", err)
		return
	}

	tasks, err := repo.InCompletedTasks()
	if err != nil {
		log.Printf("engine: recover scan failed: %v", err)
		return
	}
	if len(tasks) == 0 {
		return
	}

	for _, t := range tasks {
		leaked := declareDead(t)
		for _, jobName := range leaked { // 清理执行物：K8s Job 是消耗品，档案才是真相源
			_ = client.BatchV1().Jobs(cfg.K8SNamespace).Delete(context.TODO(), jobName, k8s.DeleteOpts())
		}
		if err := repo.UpdateTask(t); err != nil {
			log.Printf("engine: recover task %s/%d ack failed: %v", t.WorkflowName, t.TaskID, err)
			continue
		}
		log.Printf("engine: recover: task %s/%d declared cancelled (engine restarted), %d k8s job(s) cleaned",
			t.WorkflowName, t.TaskID, len(leaked))
	}

	sweepWorkspacePVCs(client, cfg.K8SNamespace)
}

// sweepWorkspacePVCs —— 共享工作区泄漏清扫（决断 #17 的兜底半边）：label 扫出全部
// 任务工作区 PVC → annotation 反查任务 → 已终态或档案已删才删卷。覆盖三股泄漏：
// finish/取消时删卷失败的搁浅卷、宣判死亡任务（上一行刚落库 cancelled，扫得出）、
// 档案被批量删除后的遗孤。在跑/ pause 稳态的任务卷不动（StatusFinished 不含 pause
// ——durable 暂停的工作区跨重启存活是特性）。annotation 解析不动的卷留着不动
// （宁可漏删不可误删）。Recover 只在启动时跑一次，不构成周期性后台扫描。
func sweepWorkspacePVCs(client kubernetes.Interface, ns string) {
	pvcs, err := client.CoreV1().PersistentVolumeClaims(ns).List(context.TODO(), metav1.ListOptions{
		LabelSelector: k8s.WorkspacePVCLabel + "=true",
	})
	if err != nil {
		log.Printf("engine: recover workspace sweep: list pvc: %v", err)
		return
	}
	for _, pvc := range pvcs.Items {
		wfName, taskID, ok := k8s.WorkspaceRefOf(&pvc)
		if !ok {
			continue
		}
		t, err := repo.FindTask(wfName, taskID)
		if err == nil && !models.StatusFinished(t.Status) {
			continue // 活人（running 看护在场 / pause 稳态 / queued）：工作区是它的
		}
		if err := k8s.DeleteWorkspacePVC(context.TODO(), client, ns, wfName, taskID); err != nil {
			log.Printf("engine: recover workspace sweep: delete %s: %v", pvc.Name, err)
			continue
		}
		log.Printf("engine: recover: reclaimed workspace pvc %s (task %s/%d)", pvc.Name, wfName, taskID)
	}
}

// declareDead —— 把一棵被看护人抛下的任务树宣判为终态（纯内存操作，可单测）：
//   - running 的 stage/job → cancelled（被打断），running job 的 K8s Job 名收集进 leaked
//   - 从未启动的（created stage/job）→ skipped（无事发生）
//   - 已终态的原样保留——历史不许改写
func declareDead(t *models.WorkflowTask) (leaked []string) {
	now := time.Now().Unix()
	t.Status = models.StatusCancelled
	t.EndTime = now
	t.Error = "engine restarted while task was in flight (declared dead by startup recovery)"

	for _, st := range t.Stages {
		switch st.Status {
		case models.StatusRunning:
			st.Status = models.StatusCancelled
			st.EndTime = now
			for _, jt := range st.Jobs {
				switch jt.Status {
				case models.StatusRunning:
					jt.Status = models.StatusCancelled
					jt.Error = "engine restarted"
					if jt.K8sJobName != "" {
						leaked = append(leaked, jt.K8sJobName)
					}
				case models.StatusCreated:
					jt.Status = models.StatusSkipped
				}
			}
		case models.StatusCreated:
			st.Status = models.StatusSkipped
			st.EndTime = now
			for _, jt := range st.Jobs {
				jt.Status = models.StatusSkipped
			}
		}
	}
	return leaked
}
