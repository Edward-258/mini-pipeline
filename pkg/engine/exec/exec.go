// Package exec —— job 执行器：JobCtl 三件套接口（Run→SaveInfo→Clean 顺序铁律）与
// K8sJobCtl（freestyle）/DeployJobCtl（deploy rollout）两个实现。整树写回经 Ctx.Ack
// 由 engine 注入，绝不回 import engine 本体。
package exec

import (
	"sync"

	"k8s.io/client-go/kubernetes"

	"pipeline/pkg/models"
)

// 执行器子包（JobCtl 注册表）：engine 编排核心通过
// 三件套接口驱动每种 job 类型的生命周期，自己不碰 K8s 细节。
// 依赖方向：exec → step（脚本/日志协议）、k8s（资源 I/O）、engine/cancel（取消
// sentinel）、models——绝不回 import engine 本体（整树写回经 Ctx.Ack 由 engine 注入）。

// Ctx —— 单个 job 执行期的运行环境（engine.taskCtx 的导出视图）。
// Mu 与 engine 侧 taskCtx.mu 是同一把锁的指针：并行 job 的整树写回互斥
// 靠指针同一性保证，执行器内不得另立锁。
type Ctx struct {
	Client   kubernetes.Interface
	NS       string
	Task     *models.WorkflowTask // ack 的对象（整树写回的目标）
	Mu       *sync.Mutex
	Ack      func(*models.WorkflowTask) // 整树写回（engine 注入的无锁版；⚠️ 调用点必须已持 Mu）
	CancelCh <-chan struct{}            // 取消信号（close 广播；nil = 永不触发）

	// 外部记录的解析依赖：engine 构造 Ctx 时装配持久层真身，单测传 stub——签名即文档。
	// 解析发生在 driver 编译 / patch 前、每 attempt 一次：凭证只进执行物不进任务档案，
	// retry/clone 拿到的是集合里的当前值。
	FindEnv             func(name string) (*models.Env, error)      // deploy 靶标 env 名 → 记录（namespace 取其 Namespace 字段）
	FindHost            func(name string) (*models.Host, error)     // 远程主机名 → 记录（私钥经驱动脚本投影，档案只存名字引用）
	FindRegistry        func(name string) (*models.Registry, error) // 推送目标显式引用 → 记录（地址+凭证烘焙进驱动脚本）
	FindDefaultRegistry func() (*models.Registry, error)            // 全局默认仓库查询（nil,nil = 无默认记录）
}

// JobCtl —— 三件套（Run/Clean/SaveInfo）。
// 首发 K8sJobCtl（freestyle）+ DeployJobCtl（deploy）；新 job 类型在 NewJobCtl
// 的 switch 里长出来（注册表模式的"扩展性"真身：一张 switch 表）。
//
// ⚠️ 顺序铁律：Run → SaveInfo → Clean。SaveInfo 要拉 Pod 日志切分/门禁，
// Clean 一删 Pod 日志就没了——先收割再销毁（资源与数据分离）。
type JobCtl interface {
	// Run：创建 K8s Job → 轮询到终态；期间每拍顺手刮 Pod 日志维护步骤状态（live 分界面）。
	// 返回 job 级错误（创建失败/退出码/超时）。
	Run(c *Ctx, jt *models.JobTask) error
	// SaveInfo：终态善后：最后一次全量日志 → banner 补齐 → 日志切分 → 步骤收口（junit 门禁）。
	// 返回步骤级失败（门禁翻红）——job 级没失败但步骤失败也要把 job 拖红。
	SaveInfo(c *Ctx, jt *models.JobTask) error
	// Clean：删 K8s Job（Background 级联，含 Pod）。
	Clean(c *Ctx, jt *models.JobTask)
}

// NewJobCtl —— job 类型 → 执行器注册表
func NewJobCtl(jt *models.JobTask) JobCtl {
	switch jt.JobType {
	case models.JobTypeDeploy:
		return DeployJobCtl{}
	default:
		return K8sJobCtl{}
	}
}
