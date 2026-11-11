package models

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// regNameRe —— 仓库名形状（与 K8s namespace 命名同构，同 api/envs.go 的 nameRe；
// models 层放一份：ValidateWorkflow/BuildTask 都要用，不能反向 import api）
var regNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Registry —— 镜像仓库（2026-11-07）：
// docker_build 步骤的推送目标，凭证由引擎在 driver 编译前烘焙进步骤副本（数据直通，Pod 内
// docker login；不含云厂商动态换凭证/项目授权/DinD 同步）。
// 明文姿态与 mongo 密码同档（知情接受，凭证活库不进仓）。
//
// IsDefault 是全量制的核心语义（用户拍板 2026-11-07）：全局唯一默认仓库，
// 建立后所有【裸名】docker_build（无 registry 引用、镜像名不带仓库前缀）跟随它推送。
// 决断 #16 起内置 registry:2 退役——没有默认记录时裸名【报错点名】（无家可归，不再回落
// $MP_NODE_REGISTRY；回落语义随内置仓库一起退役，回归"推送目标必须有人站"）。
// ⚠️ 设置默认会静默改变所有裸名图纸的推送目标——前端表单与 README 都要写明。
//
// Address 支持可选 scheme 前缀作传输档声明（决断 #16，scheme 显式分派）：
//   "http://host[:port]" = 明文档（引擎 SyncBuildkitRegistries 把 host 写进 buildkitd 白名单）
//   "https://..." / 裸 host = buildkitd 默认 HTTPS 公信证书直通（不进白名单，零配置）
// 入库保留原样（声明不丢），消费点（烘焙/白名单键）一律经 NormalizeRegistryAddr 剥掉——
// docker 镜像引用不收 scheme。
type Registry struct {
	Name      string    `bson:"_id"        json:"name"`    // 仓库名即主键，图纸 registry 字段引用它
	Address   string    `bson:"address"    json:"address"` // [http(s)://]host[:port][/namespace]，scheme = 传输档声明（见上）
	Username  string    `bson:"username,omitempty" json:"username,omitempty"`
	Password  string    `bson:"password,omitempty" json:"password,omitempty"` // 空 = 免认证仓库（登录步骤跳过）
	IsDefault bool      `bson:"is_default,omitempty" json:"is_default,omitempty"`
	Desc      string    `bson:"desc,omitempty" json:"desc,omitempty"`
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
}

// Validate —— name/address 必填；address 允许 http(s):// 前缀（传输档声明，不剥——语义见
// Address 注释）或裸 host[:port]，其他 scheme（ftp:// 等）拒绝。2026-11-10 前的本函数语义
// 是"入库剥 scheme"，随决断 #16 翻转：scheme 现在是数据不是噪音。
func (r *Registry) Validate() error {
	if r.Name == "" {
		return fmt.Errorf("registry name is required")
	}
	if !regNameRe.MatchString(r.Name) {
		return fmt.Errorf("registry name must match [a-z0-9][a-z0-9-]{0,62}")
	}
	r.Address = strings.TrimSpace(r.Address)
	if r.Address == "" {
		return fmt.Errorf("registry address is required")
	}
	if i := strings.Index(r.Address, "://"); i > 0 {
		switch r.Address[:i] {
		case "http", "https": // 合法：传输档声明
		default:
			return fmt.Errorf("registry address scheme must be http:// or https:// (or bare host[:port]), got %q", r.Address[:i]+"://")
		}
	}
	return nil
}

// NormalizeRegistryAddr —— 剥 scheme 前缀与尾斜杠，返回 docker 认的 host[:port][/ns]。
// 消费点（step 烘焙 / buildkit 白名单键）统一走这里，入口形状（带不带声明）不关心。
func NormalizeRegistryAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	addr = strings.TrimPrefix(addr, "https://")
	addr = strings.TrimPrefix(addr, "http://")
	return strings.TrimSuffix(addr, "/")
}

// IsPlaintextRegistryAddr —— http:// 前缀 = 明文档声明（决断 #16 scheme 显式分派）：
// buildkitd 需要逐 host 白名单才能对它说 HTTP；https/裸名 = 默认 HTTPS 公信直通不进白名单。
func IsPlaintextRegistryAddr(addr string) bool {
	return strings.HasPrefix(strings.TrimSpace(addr), "http://")
}
