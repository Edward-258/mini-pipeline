package main

import (
	"context"
	"log"

	"pipeline/pkg/api"
	"pipeline/pkg/config"
	"pipeline/pkg/engine"
	"pipeline/pkg/engine/cron"
	"pipeline/pkg/k8s"
	"pipeline/pkg/repo"
)

func main() {
	cfg := config.Load()

	if err := repo.Init(cfg.MongoURI); err != nil {
		log.Fatalf("connect mongodb failed: %v", err)
	}
	log.Printf("connected to mongodb")

	// 决断 #17：任务级共享工作区 PVC 清理钩子——finish 终态 / 取消纯数据路径消费，
	// Recover 泄漏清扫兜底。装配在 Recover 之前：启动宣判与运行期走同一个删卷实现。
	// best-effort：删失败响亮日志，漏卷由 Recover 扫描回收（此时任务已终态/已删）
	if cfg.WorkspaceShared {
		engine.CleanupWorkspace = func(wfName string, taskID int64) {
			cli, err := k8s.NewClient(cfg.K8SContext)
			if err != nil {
				log.Printf("[workspace] k8s client: %v", err)
				return
			}
			if err := k8s.DeleteWorkspacePVC(context.TODO(), cli, cfg.K8SNamespace, wfName, taskID); err != nil {
				log.Printf("[workspace] delete pvc of %s/%d failed (Recover 清扫兜底): %v", wfName, taskID, err)
			}
		}
	}

	engine.Recover()                      // M3.5 启动宣判：上一世遗留的 created/queued/running 档案宣判死亡（pause 不动）
	go engine.StartDispatcher()           // T1.6 派发器：1s 一拍扫 queued，CAS 抢占后起 Run
	go cron.Start(engine.TriggerWorkflow) // T3.1 定时触发：对齐整分钟扫 cron 配置
	// 前端已独立成 nginx 镜像 Pod（frontend/ + chart tags.web），经 ingress 统一入口
	// 反代 /api 到本引擎——引擎只服务 API（go:embed 时代 2026-11-06 收档）

	// env 随建 namespace（§4 决断 #13）：api 层钩子在此装配真身
	// （api 不 import K8s，router.go 教义不破）。每次现建 client = TailJobLog 同款
	// 自给风格；env 创建是低频操作，可接受。
	api.EnsureNamespaceHook = func(ns string) error {
		cli, err := k8s.NewClient(cfg.K8SContext)
		if err != nil {
			return err
		}
		return k8s.EnsureNamespace(cli, ns)
	}

	// 决断 #16（registry 外部化）：buildkitd 白名单 = registry 集合的运行时数据（运行时 reconcile 范式）。CRUD 后同步 + 启动即同步一次——helm upgrade 会把
	// buildkitd.toml CM 重置回模板兜底段，引擎重启（镜像变了必重启）后这里写回，自愈闭环。
	// best-effort：同步失败响亮日志不崩引擎（buildkit 池未部署时安静跳过），下次自愈。
	syncBuildkit := func() {
		cli, err := k8s.NewClient(cfg.K8SContext)
		if err != nil {
			log.Printf("[buildkit-sync] k8s client: %v", err)
			return
		}
		regs, err := repo.ListRegistries()
		if err != nil {
			log.Printf("[buildkit-sync] list registries: %v", err)
			return
		}
		if err := k8s.SyncBuildkitRegistries(context.Background(), cli, cfg.K8SNamespace, cfg.BKMirror, regs); err != nil {
			log.Printf("[buildkit-sync] FAILED (下次 registry CRUD / 引擎重启自愈): %v", err)
		}
	}
	api.BuildkitSyncHook = syncBuildkit
	syncBuildkit()

	r := api.NewRouter()
	log.Printf("mini-pipeline listening on %s", cfg.HTTPAddr)
	if err := r.Run(cfg.HTTPAddr); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
