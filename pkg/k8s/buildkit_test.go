package k8s

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stesting "k8s.io/client-go/kubernetes/fake"

	"pipeline/pkg/models"
)

// TestBuildBuildkitToml —— toml 全量生成的表测（决断 #16 scheme 显式分派）：
// http:// 声明 → 白名单段；https:// 与裸名 → 不写段（buildkitd 默认 HTTPS 公信直通）；
// host 去重 + 排序（生成物确定化，hash 注解不打无谓重启）。
func TestBuildBuildkitToml(t *testing.T) {
	regs := []*models.Registry{
		{Name: "local", Address: "http://registry.mp.svc:5000"},
		{Name: "local-dup", Address: "http://registry.mp.svc:5000"}, // 同 host 去重
		{Name: "acr", Address: "registry.example-acr.cn"},     // 裸名 = HTTPS 档
		{Name: "tls", Address: "https://reg.tls.example.com"},       // 显式 https = 不进白名单
		nil, // 防御：nil 记录不炸
	}
	toml := BuildBuildkitToml("https://docker.m.daocloud.io", regs)

	if !strings.Contains(toml, `[registry."docker.io"]`) || !strings.Contains(toml, `mirrors = ["https://docker.m.daocloud.io"]`) {
		t.Errorf("mirror section missing: %s", toml)
	}
	if n := strings.Count(toml, `[registry."registry.mp.svc:5000"]`); n != 1 {
		t.Errorf("plaintext host must appear exactly once (dedup), got %d: %s", n, toml)
	}
	if !strings.Contains(toml, "http = true") {
		t.Errorf("plaintext whitelist entry missing: %s", toml)
	}
	if strings.Contains(toml, "registry.example-acr.cn") || strings.Contains(toml, "reg.tls.example.com") {
		t.Errorf("https/bare registries must NOT be whitelisted (https default): %s", toml)
	}
	// 无明文记录 + 无 mirror = 纯注释空配置（断言行首段头——注释文案里的 [registry."host"] 字样不算）
	empty := BuildBuildkitToml("", nil)
	if strings.Contains(empty, "\n[registry.") || strings.HasPrefix(empty, "[registry.") {
		t.Errorf("no mirror + no plaintext regs must render no sections: %s", empty)
	}
}

// seedBuildkit —— fake clientset 播种 CM + STS（deploy_test 同款 fake 手法）
func seedBuildkit(t *testing.T, toml string) *k8stesting.Clientset {
	t.Helper()
	fc := k8stesting.NewSimpleClientset()
	if toml != "" {
		if _, err := fc.CoreV1().ConfigMaps("mp").Create(context.TODO(), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: buildkitConfigName, Namespace: "mp"},
			Data:       map[string]string{buildkitTomlKey: toml},
		}, metav1.CreateOptions{}); err != nil {
			t.Fatalf("seed cm: %v", err)
		}
	}
	if _, err := fc.AppsV1().StatefulSets("mp").Create(context.TODO(), &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: buildkitStsName, Namespace: "mp"},
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "buildkit"}}}},
		},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed sts: %v", err)
	}
	return fc
}

func bldTomlOf(t *testing.T, fc *k8stesting.Clientset) string {
	t.Helper()
	cm, err := fc.CoreV1().ConfigMaps("mp").Get(context.TODO(), buildkitConfigName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get cm: %v", err)
	}
	return cm.Data[buildkitTomlKey]
}

func stsAnnoOf(t *testing.T, fc *k8stesting.Clientset) string {
	t.Helper()
	sts, err := fc.AppsV1().StatefulSets("mp").Get(context.TODO(), buildkitStsName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get sts: %v", err)
	}
	return sts.Spec.Template.Annotations[buildkitTomlHashAnno]
}

var syncRegs = []*models.Registry{{Name: "local", Address: "http://registry.mp.svc:5000"}}

// TestSyncBuildkitRegistries —— reconcile 全链：未部署跳过 / 差异写回 + hash 滚动 / 幂等不滚动
func TestSyncBuildkitRegistries(t *testing.T) {
	ctx := context.TODO()

	// 池未部署：CM 不存在 → 安静返回
	fc := k8stesting.NewSimpleClientset()
	if err := SyncBuildkitRegistries(ctx, fc, "mp", "https://mirror.example", syncRegs); err != nil {
		t.Fatalf("absent CM must be a silent no-op: %v", err)
	}

	// 有池：CM 兜底段 ≠ 生成内容 → 写回 CM + patch hash 注解（滚动触发）
	fc = seedBuildkit(t, "PLACEHOLDER")
	want := BuildBuildkitToml("https://mirror.example", syncRegs)
	if err := SyncBuildkitRegistries(ctx, fc, "mp", "https://mirror.example", syncRegs); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := bldTomlOf(t, fc); got != want {
		t.Errorf("cm content mismatch:\n got %q\nwant %q", got, want)
	}
	if stsAnnoOf(t, fc) == "" {
		t.Errorf("hash annotation must be set after content change (roll trigger)")
	}

	// 再同步（同内容同 mirror）：幂等——CM 不变、注解不变（registry CRUD 高频不惊动池）
	anno := stsAnnoOf(t, fc)
	if err := SyncBuildkitRegistries(ctx, fc, "mp", "https://mirror.example", syncRegs); err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	if got := bldTomlOf(t, fc); got != want || stsAnnoOf(t, fc) != anno {
		t.Errorf("idempotent re-sync must not change anything")
	}

	// 记录变化（新增明文仓库）→ 内容变 → 注解变（滚动）
	regs2 := append(syncRegs, &models.Registry{Name: "extra", Address: "http://reg2.mp.svc:5000"})
	if err := SyncBuildkitRegistries(ctx, fc, "mp", "https://mirror.example", regs2); err != nil {
		t.Fatalf("sync after change: %v", err)
	}
	if !strings.Contains(bldTomlOf(t, fc), `[registry."reg2.mp.svc:5000"]`) {
		t.Errorf("new plaintext host must be whitelisted: %s", bldTomlOf(t, fc))
	}
	if stsAnnoOf(t, fc) == anno {
		t.Errorf("hash annotation must change after content change (roll trigger)")
	}

	// STS 不存在（CM 在但池被删）：安静返回
	fc2 := seedBuildkit(t, "PLACEHOLDER")
	if err := fc2.AppsV1().StatefulSets("mp").Delete(context.TODO(), buildkitStsName, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete sts: %v", err)
	}
	if err := SyncBuildkitRegistries(ctx, fc2, "mp", "https://mirror.example", syncRegs); err != nil {
		t.Fatalf("absent STS must be a silent no-op: %v", err)
	}
}
