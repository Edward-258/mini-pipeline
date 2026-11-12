package exec

import (
	"fmt"

	"pipeline/pkg/models"
)

// 远程主机解析（2026-11-08，与 registry 推送目标解析同一时序）：
// 解析发生在 driver 编译前（runK8sJobOnce 每 attempt 一次）——私钥不落任务档案
// （任务 API 不泄明文），retry/clone 拿到的是 host 记录的当前值（换 key 重试可用）。
// 名字不存在 → 报错点名 → job 失败（同 registry 显式引用缺失的语义）。
// 解析依赖经 Ctx.FindHost 注入（engine 装配持久层真身，单测传 stub，不摸持久层）。

// resolveHosts —— 名字簿 → 记录列表；重复引用去重（同名两次投影无害但没必要，
// config 条目与私钥文件各只留一份）
func resolveHosts(c *Ctx, names []string) ([]*models.Host, error) {
	seen := map[string]bool{}
	out := make([]*models.Host, 0, len(names))
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		h, err := c.FindHost(n)
		if err != nil {
			return nil, fmt.Errorf("resolve host %q: %w", n, err)
		}
		out = append(out, h)
	}
	return out, nil
}
