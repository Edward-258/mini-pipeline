package exec

import (
	"fmt"

	"pipeline/pkg/models"
)

// docker_build 推送目标解析（2026-11-07 registry 管理，全量制语义；决断 #16 修订档 3）。
// 解析发生在 driver 编译前（runK8sJobOnce 每 attempt 一次）而非快照时——与 deploy
// job 的 env 靶标解析同一时序：凭证不落任务档案（任务 API 不泄明文），retry/clone
// 永远拿到仓库记录的当前值（改密码后重试可用）。烘焙进驱动脚本的副本，任务树不动。
// 解析依赖经 Ctx 注入（FindRegistry / FindDefaultRegistry 字段，engine 装配持久层
// 真身，单测传 stub，不摸持久层）。
//
// 三档优先级（README/AGENTS 同款规则表）：
//  1. 显式 registry 引用   → 解析该记录（不存在 = job 失败，错误信息点名）
//  2. 裸名 + 全局默认存在  → 跟随 is_default 记录（⚠️ 设默认会改变所有裸名图纸的目标）
//  3. 全名（带仓库前缀）   → 原样直通（匿名直推——HTTPS 公信端点零配置；明文端点必须先登记，
//     引擎把它 reconcile 进构建守护进程白名单）
//     裸名无默认            → 报错点名（决断 #16：内置产物仓库退役后裸名无家可归，
//     不再回落节点本地端——语义回归"推送目标必须有人站"，登记并设默认即可）

// resolveRegistrySteps —— 返回一份可烘焙的步骤副本；无解析需求的步骤原指针直通（零拷贝）
func resolveRegistrySteps(c *Ctx, steps []*models.StepSpec) ([]*models.StepSpec, error) {
	out := make([]*models.StepSpec, len(steps))
	for i, ss := range steps {
		if ss.Type != models.StepDockerBuild {
			out[i] = ss
			continue
		}
		bare := !models.HasRegistryPrefix(ss.Image)
		var reg *models.Registry
		var err error
		switch {
		case ss.Registry != "": // 档 1：显式引用
			reg, err = c.FindRegistry(ss.Registry)
			if err != nil {
				return nil, fmt.Errorf("step %q: resolve registry %q: %w", ss.Name, ss.Registry, err)
			}
		case bare: // 档 2：裸名跟随默认（无默认 = 报错，见档 3）
			if reg, err = c.FindDefaultRegistry(); err != nil {
				return nil, fmt.Errorf("step %q: find default registry: %w", ss.Name, err)
			}
		}
		if reg == nil {
			if bare { // 档 3 报错分支：裸名无默认（决断 #16 起无家可归）
				return nil, fmt.Errorf("step %q: bare image name %q but no default registry registered — register one (POST /api/registries) and set is_default, or use a full image name", ss.Name, ss.Image)
			}
			out[i] = ss // 全名直通：零烘焙零拷贝
			continue
		}
		cp := *ss // 浅拷贝即深拷贝（烘焙字段全标量）
		cp.RegAddr, cp.RegUser, cp.RegPass = reg.Address, reg.Username, reg.Password
		out[i] = &cp
	}
	return out, nil
}
