package k8s

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"pipeline/pkg/models"
)

// 决断 #17：任务级共享工作区（PVC 命名 / 幂等建删 / annotation 反查 / 卷源开关 /
// fail-fast 探测）——全 fake clientset，无需真集群

func TestWorkspacePVCName(t *testing.T) {
	a := WorkspacePVCName("wfa", 1)
	b := WorkspacePVCName("wfb", 1)
	if a == b {
		t.Fatalf("pvc name collision across workflows: %q", a)
	}
	if a != "mp-ws-wfa-1" {
		t.Errorf("name = %q", a)
	}
	if got := WorkspacePVCName("Wf Space/Ümlaut", 2); got != "mp-ws-wf-space-mlaut-2" {
		t.Errorf("sanitize: %q", got)
	}
	if got := WorkspacePVCName("averyveryverylongworkflownamethatkeepsgoing", 3); len(got) > 63 {
		t.Errorf("truncate: len=%d %q", len(got), got)
	}
}

func TestEnsureWorkspacePVC(t *testing.T) {
	t.Run("首建带label+annotation+RWO", func(t *testing.T) {
		cli := k8sfake.NewSimpleClientset()
		if err := EnsureWorkspacePVC(context.TODO(), cli, "pipeline", "wf", 5, "2Gi"); err != nil {
			t.Fatalf("ensure: %v", err)
		}
		pvc, err := cli.CoreV1().PersistentVolumeClaims("pipeline").Get(context.TODO(), "mp-ws-wf-5", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if pvc.Labels[WorkspacePVCLabel] != "true" {
			t.Errorf("sweep label missing: %v", pvc.Labels)
		}
		if pvc.Annotations[WorkspaceTaskAnno] != "wf/5" {
			t.Errorf("task annotation = %q", pvc.Annotations[WorkspaceTaskAnno])
		}
		if len(pvc.Spec.AccessModes) != 1 || pvc.Spec.AccessModes[0] != corev1.ReadWriteOnce {
			t.Errorf("access modes = %v", pvc.Spec.AccessModes)
		}
		if pvc.Spec.StorageClassName != nil {
			t.Errorf("storageClass must be nil (cluster default), got %q", *pvc.Spec.StorageClassName)
		}
	})

	t.Run("已存在→零操作", func(t *testing.T) {
		cli := k8sfake.NewSimpleClientset()
		_ = EnsureWorkspacePVC(context.TODO(), cli, "pipeline", "wf", 5, "2Gi")
		if err := EnsureWorkspacePVC(context.TODO(), cli, "pipeline", "wf", 5, "2Gi"); err != nil {
			t.Fatalf("second ensure: %v", err)
		}
		creates := 0
		for _, a := range cli.Actions() {
			if a.GetVerb() == "create" {
				creates++
			}
		}
		if creates != 1 {
			t.Fatalf("idempotent ensure must create exactly once, got %d", creates)
		}
	})

	t.Run("坏容量→报错不建", func(t *testing.T) {
		cli := k8sfake.NewSimpleClientset()
		if err := EnsureWorkspacePVC(context.TODO(), cli, "pipeline", "wf", 6, "not-a-qty"); err == nil {
			t.Fatal("bad quantity must error")
		}
	})
}

func TestDeleteWorkspacePVC(t *testing.T) {
	cli := k8sfake.NewSimpleClientset()
	if err := DeleteWorkspacePVC(context.TODO(), cli, "pipeline", "gone", 1); err != nil {
		t.Fatalf("delete missing must be idempotent success, got %v", err)
	}
	_ = EnsureWorkspacePVC(context.TODO(), cli, "pipeline", "wf", 5, "2Gi")
	if err := DeleteWorkspacePVC(context.TODO(), cli, "pipeline", "wf", 5); err != nil {
		t.Fatalf("delete existing: %v", err)
	}
	if _, err := cli.CoreV1().PersistentVolumeClaims("pipeline").Get(context.TODO(), "mp-ws-wf-5", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("pvc still present after delete: %v", err)
	}
}

func TestWorkspaceRefOf(t *testing.T) {
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
		Name:        "mp-ws-wf-5",
		Annotations: map[string]string{WorkspaceTaskAnno: "my-wf/5"},
	}}
	wf, id, ok := WorkspaceRefOf(pvc)
	if !ok || wf != "my-wf" || id != 5 {
		t.Errorf("ref = %q/%d ok=%v", wf, id, ok)
	}
	// 中文 wf 名（进不了 label 的字符集，annotation 必须扛得住）
	pvc2 := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
		Annotations: map[string]string{WorkspaceTaskAnno: "手动测试构建/12"},
	}}
	if wf, id, ok = WorkspaceRefOf(pvc2); !ok || wf != "手动测试构建" || id != 12 {
		t.Errorf("cjk ref = %q/%d ok=%v", wf, id, ok)
	}
	for _, bad := range []*corev1.PersistentVolumeClaim{
		{ObjectMeta: metav1.ObjectMeta{Name: "no-anno"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "bad-id", Annotations: map[string]string{WorkspaceTaskAnno: "wf/abc"}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "no-slash", Annotations: map[string]string{WorkspaceTaskAnno: "justname"}}},
	} {
		if _, _, ok = WorkspaceRefOf(bad); ok {
			t.Errorf("unparseable pvc %s must yield ok=false", bad.Name)
		}
	}
}

func TestPodUnschedulable(t *testing.T) {
	newPod := func(conds []corev1.PodCondition) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "pipeline", Labels: map[string]string{"job-name": "mp-j"}},
			Status:     corev1.PodStatus{Conditions: conds},
		}
	}
	cli := k8sfake.NewSimpleClientset()

	// Pod 没起（还没创建）= 未阻塞
	if _, _, blocked := PodUnschedulable(cli, "pipeline", "mp-j"); blocked {
		t.Fatal("no pod must not block")
	}

	stuck := metav1.NewTime(time.Now().Add(-3 * time.Minute))
	cli = k8sfake.NewSimpleClientset(newPod([]corev1.PodCondition{{
		Type:               corev1.PodScheduled,
		Status:             corev1.ConditionFalse,
		Reason:             corev1.PodReasonUnschedulable,
		Message:            "0/3 nodes are available: 1 node(s) were not ready",
		LastTransitionTime: stuck,
	}}))
	since, reason, blocked := PodUnschedulable(cli, "pipeline", "mp-j")
	if !blocked {
		t.Fatal("unschedulable pod must block")
	}
	if time.Since(since) < 2*time.Minute {
		t.Errorf("since = %v, want the condition's transition time (~3m ago)", time.Since(since))
	}
	if reason == "" {
		t.Error("scheduler reason message lost")
	}

	// 已调度（PodScheduled=True）= 未阻塞——正常运行不误伤
	cli = k8sfake.NewSimpleClientset(newPod([]corev1.PodCondition{{
		Type: corev1.PodScheduled, Status: corev1.ConditionTrue,
	}}))
	if _, _, blocked = PodUnschedulable(cli, "pipeline", "mp-j"); blocked {
		t.Fatal("scheduled pod must not block")
	}
}

// TestBuildJobWorkspace —— 卷源开关（决断 #17）：共享档 = 任务级 PVC + go 缓存 env +
// MP_SOURCE_DIR；false = 旧 emptyDir 世界零新增
func TestBuildJobWorkspace(t *testing.T) {
	wt := &models.WorkflowTask{
		WorkflowName: "wsfw", TaskID: 9,
		WorkflowArgs: &models.Workflow{Source: &models.SourceSpec{Repo: "https://github.com/o/r-demo.git"}},
	}
	jt := &models.JobTask{Name: "j", JobType: "freestyle", Spec: &models.FreestyleSpec{Scripts: []string{"echo hi"}}}

	t.Run("共享档=PVC卷+缓存env+SOURCE_DIR", func(t *testing.T) {
		t.Setenv("MP_WORKSPACE_SHARED", "true")
		job := BuildJob("pipeline", wt, jt, "echo hi")
		var pvc *corev1.PersistentVolumeClaimVolumeSource
		for _, v := range job.Spec.Template.Spec.Volumes {
			if v.Name == "workspace" {
				pvc = v.VolumeSource.PersistentVolumeClaim
			}
		}
		if pvc == nil || pvc.ClaimName != WorkspacePVCName("wsfw", 9) {
			t.Fatalf("workspace volume must be task PVC %q, got %+v", WorkspacePVCName("wsfw", 9), pvc)
		}
		env := envMap(job)
		if env["GOCACHE"] != "/workspace/.mp-cache/go-build" || env["GOMODCACHE"] != "/workspace/.mp-cache/go-mod" {
			t.Errorf("go cache env = %q / %q", env["GOCACHE"], env["GOMODCACHE"])
		}
		if env["MP_SOURCE_DIR"] != "r-demo" { // RepoDirName 派生（URL 尾段）
			t.Errorf("MP_SOURCE_DIR = %q", env["MP_SOURCE_DIR"])
		}
	})

	t.Run("关闭档=emptyDir旧世界", func(t *testing.T) {
		t.Setenv("MP_WORKSPACE_SHARED", "false")
		job := BuildJob("pipeline", wt, jt, "echo hi")
		var empty bool
		for _, v := range job.Spec.Template.Spec.Volumes {
			if v.Name == "workspace" {
				empty = v.VolumeSource.EmptyDir != nil
			}
		}
		if !empty {
			t.Fatal("shared=false must fall back to emptyDir workspace")
		}
		env := envMap(job)
		if _, ok := env["GOCACHE"]; ok {
			t.Error("shared=false must not pin GOCACHE to workspace volume")
		}
		// MP_SOURCE_DIR 是代码源契约（决断 #18），与工作区开关正交：目录名声明在任何
		// 卷模式下都注入（checkout job 自己也消费同名派生）
		if env["MP_SOURCE_DIR"] != "r-demo" {
			t.Errorf("MP_SOURCE_DIR = %q", env["MP_SOURCE_DIR"])
		}
	})
}
