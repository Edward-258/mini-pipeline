package k8s

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"pipeline/pkg/models"
)

// buildkitd 配置运行时生成（决断 #16，registry 外部化）：白名单 = registry 集合的运行时
// 数据，运行时 reconcile 的同步对象——白名单进构建守护进程的 args + certs.d 卷是一条路；
// 我们只改 buildkitd.toml CM +
// pod-template hash 注解两刀，触发面收敛为启动 + registry CRUD（best-effort，同步失败下次
// 自愈）。buildkitd 只在启动时读 toml，所以内容变化必须跟着滚动（hash 注解实现）。
const (
	buildkitConfigName   = "buildkitd-config"
	buildkitStsName      = "buildkit"
	buildkitTomlKey      = "buildkitd.toml"
	buildkitTomlHashAnno = "mp/buildkitd-toml-hash"
)

// BuildBuildkitToml —— 全量生成 buildkitd.toml：mirror 段 + 明文 registry 白名单段。
// scheme 显式分派（models.IsPlaintextRegistryAddr）：http:// 前缀的记录 →
// [registry."host"] http=true；缺省/https:// = buildkitd 默认走 HTTPS 公信证书直通
// （不写段——给 HTTPS 端点写 http=true 反而把它打坏）。生成物确定化（host 排序去重）：
// 同输入必同输出，hash 注解才不打无谓的重启。
func BuildBuildkitToml(mirror string, regs []*models.Registry) string {
	var b strings.Builder
	b.WriteString("# 本文件由引擎 SyncBuildkitRegistries 全量生成（决断 #16）——手工改动会被下次同步覆盖。\n")
	b.WriteString("# 语义：[registry.\"host\"] http=true = buildkitd 对该 host 走明文 HTTP（registry\n")
	b.WriteString("# 记录地址带 http:// 前缀的）；缺省 = 默认 HTTPS 公信证书直通（ACR 等，零配置）。\n")
	if mirror != "" {
		fmt.Fprintf(&b, "\n[registry.\"docker.io\"]\n  mirrors = [\"%s\"]\n", mirror)
	}
	for _, h := range plaintextRegistryHosts(regs) {
		fmt.Fprintf(&b, "\n[registry.\"%s\"]\n  http = true\n", h)
	}
	return b.String()
}

// plaintextRegistryHosts —— 白名单 host 列表：只收 http:// 声明的记录，剥 scheme 后去重排序。
func plaintextRegistryHosts(regs []*models.Registry) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range regs {
		if r == nil || !models.IsPlaintextRegistryAddr(r.Address) {
			continue
		}
		h := models.NormalizeRegistryAddr(r.Address)
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// SyncBuildkitRegistries —— registry 集合 → buildkitd.toml 全量 reconcile：
//
//  1. 生成 toml；CM 内容无变化则只对齐 hash 注解（通常双无变化直接返回，registry CRUD
//     高频操作不惊动构建池）
//  2. CM 有变化 → Update（helm upgrade 会把 CM 重置回模板兜底段，引擎重启后这里写回——
//     自愈闭环，helm 的 three-way merge 与本写回天然收敛）
//  3. hash 注解落后 → patch STS pod-template 注解触发滚动（buildkitd 只在启动时读 toml）
//
// buildkit 池未部署（CM/STS NotFound）安静返回——tags.buildkit=false 的部署形态无事可做。
// 参数收 kubernetes.Interface（NewClient 的 *Clientset 与单测的 fake 都满足）。
func SyncBuildkitRegistries(ctx context.Context, cli kubernetes.Interface, ns, mirror string, regs []*models.Registry) error {
	toml := BuildBuildkitToml(mirror, regs)

	cm, err := cli.CoreV1().ConfigMaps(ns).Get(ctx, buildkitConfigName, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("get configmap %s: %w", buildkitConfigName, err)
	}
	if cm.Data == nil || cm.Data[buildkitTomlKey] != toml {
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data[buildkitTomlKey] = toml
		if _, err := cli.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("update configmap %s: %w", buildkitConfigName, err)
		}
	}

	sts, err := cli.AppsV1().StatefulSets(ns).Get(ctx, buildkitStsName, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("get statefulset %s: %w", buildkitStsName, err)
	}
	sum := sha256.Sum256([]byte(toml))
	hash := hex.EncodeToString(sum[:])
	if sts.Spec.Template.Annotations[buildkitTomlHashAnno] == hash {
		return nil
	}
	patch := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{%q:%q}}}}}`, buildkitTomlHashAnno, hash)
	if _, err := cli.AppsV1().StatefulSets(ns).Patch(ctx, buildkitStsName, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("patch statefulset %s: %w", buildkitStsName, err)
	}
	return nil
}
