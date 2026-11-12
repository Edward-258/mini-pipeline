// Package api —— gin handler 层：HTTP 形状翻译（参数解析、状态码映射、JSON envelope）。
// 领域逻辑全部在 engine/models/repo，这里不碰 K8s 与取消注册表。
package api

import (
	"github.com/gin-gonic/gin"
)

// Deps —— 路由层外部依赖的注入点：集群侧副作用以函数字段经构造参数显式传入，
// api 层不 import 任何具体实现（与全局 var 钩子相比，装配关系在构造调用处一目
// 了然，测试也不再需要改全局再恢复）。nil 字段 = 未装配（单测路由）= 纯登记
// 降级：登记操作照常，副作用跳过。
type Deps struct {
	// EnsureNamespace 在 env 落库前确保目标 namespace 存在；失败则该次写操作中止
	// （不变量：落库的 env 其 namespace 必然已存在）
	EnsureNamespace func(string) error
	// BuildkitSync 在 registry CRUD 成功后触发一次白名单同步；best-effort——
	// 失败只记日志不回滚 CRUD（启动 reconcile + 下次 CRUD 自愈），错误处理在真身里
	BuildkitSync func()
}

func NewRouter(deps Deps) *gin.Engine {
	r := gin.Default()

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	api := r.Group("/api")
	{
		api.POST("/workflows", createWorkflow)
		api.GET("/workflows", listWorkflows)
		api.GET("/workflows/:name", getWorkflow)
		api.PUT("/workflows/:name", updateWorkflow)
		api.DELETE("/workflows/:name", deleteWorkflow)
		// 0.7.9：多选/全选删（前端 checkbox；级联清理任务档案+定时器，与单删同路径）
		api.POST("/workflows/batch-delete", batchDeleteWorkflows)

		// T3.3：IM 通知配置
		api.POST("/notifies", createNotifyCfg)
		api.GET("/notifies", listNotifyCfgs)
		api.PUT("/notifies/:name", updateNotifyCfg)
		api.DELETE("/notifies/:name", deleteNotifyCfg)
		// T3.2：git webhook 触发（GitHub/Gitee push，HMAC 验签）
		api.POST("/webhook/:workflow", handleWebhook)
		// T3.1：定时触发（cron 配置 + 手动点火）
		api.POST("/crons", createCron)
		api.GET("/crons", listCrons)
		api.PUT("/crons/:name", updateCron)
		api.DELETE("/crons/:name", deleteCron)
		api.POST("/crons/:name/fire", triggerCron)
		// T2.1：部署目标环境（env = 名字 → namespace，deploy job 的靶标）
		// createEnv/updateEnv 消费 deps.EnsureNamespace（nil = 纯登记降级），闭包注入
		api.POST("/envs", func(c *gin.Context) { createEnv(c, deps) })
		api.GET("/envs", listEnvs)
		api.PUT("/envs/:name", func(c *gin.Context) { updateEnv(c, deps) })
		api.DELETE("/envs/:name", deleteEnv)
		// 2026-11-07：镜像仓库管理（docker_build 的推送目标；is_default 决定裸名图纸推哪）
		// create/update/delete 消费 deps.BuildkitSync（nil 即跳过），闭包注入
		api.POST("/registries", func(c *gin.Context) { createRegistry(c, deps) })
		api.GET("/registries", listRegistries)
		api.PUT("/registries/:name", func(c *gin.Context) { updateRegistry(c, deps) })
		api.DELETE("/registries/:name", func(c *gin.Context) { deleteRegistry(c, deps) })
		// 2026-11-08：远程主机管理（freestyle job 的 ssh 靶标；私钥投影进 Pod 不落档案）
		api.POST("/hosts", createHost)
		api.GET("/hosts", listHosts)
		api.PUT("/hosts/:name", updateHost)
		api.DELETE("/hosts/:name", deleteHost)

		// M1：run / task 查询
		api.POST("/workflows/:name/run", runWorkflow)
		// 0.7.9：GET /tasks（无 :name）= 跨库总账本（?workflow= 可选过滤）；POST batch-delete = 档案真删除
		// （与 DELETE /tasks/:name/:id 的取消语义并存；gin v1.12 支持静态段与 :name 参数同级）
		api.GET("/tasks", listTasks)
		api.POST("/tasks/batch-delete", batchDeleteTasks)
		api.GET("/tasks/:name", listTasks)
		api.GET("/tasks/:name/:id", getTask)
		// T1.5：取消任务（活任务信号广播，pause/queued 纯数据收口）
		api.DELETE("/tasks/:name/:id", cancelTask)
		// T1.7/T1.8：克隆重跑 / 整任务重试
		api.POST("/tasks/:name/:id/clone", cloneTask)
		api.POST("/tasks/:name/:id/retry", retryTask)
		// T2.4：产物上传（Pod 内 archive 步骤 curl）与下载
		api.POST("/tasks/:name/:id/artifact/:step", putArtifact)
		api.GET("/tasks/:name/:id/artifact/:step", getArtifact)
		// CI/CD：分步日志（活任务透传 Pod 尾部，死任务回档案分步日志）
		api.GET("/tasks/:name/:id/log", getTaskLog)
		// M3：放行暂停的 stage（durable 暂停的灵魂端点）
		api.POST("/tasks/:name/:id/stages/:stage/release", releaseStage)
		// T3.4：原生审批（pause 闸门的多人版，any/all 策略；老图纸无 approvers 时等价 release）
		api.POST("/tasks/:name/:id/approve", approveTask)
	}
	return r
}
