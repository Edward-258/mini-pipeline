package k8s

import (
	"fmt"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"pipeline/pkg/models"
)

// TestJobName —— T1.6 抓的老雷回归：不同 workflow 的同号任务必须得到不同的 K8s Job 名
func TestJobName(t *testing.T) {
	a := JobName("t16capA", 1, "s1", "j")
	b := JobName("t16capB", 1, "s1", "j")
	if a == b {
		t.Fatalf("job name collision across workflows: %q == %q", a, b)
	}
	if a != "mp-t16capa-1-s1-j" {
		t.Errorf("jobName = %q", a)
	}
	// 非法字符清洗 + 超长截断
	if got := JobName("Wf With Space/Ümlaut", 2, "St age", "J*ob"); got != "mp-wf-with-space-mlaut-2-st-age-j-ob" {
		t.Errorf("sanitize: %q", got)
	}
	if got := JobName("averyveryverylongworkflownamethatkeepsgoing", 3, "s", "j"); len(got) > 63 {
		t.Errorf("truncate: len=%d %q", len(got), got)
	}
}

// bkFixture —— docker_build 步骤的 job 夹具（决断 #15 测试用）
func bkFixture(wf string, id int64) (*models.WorkflowTask, *models.JobTask) {
	wt := &models.WorkflowTask{WorkflowName: wf, TaskID: id}
	jt := &models.JobTask{
		Name: "build", JobType: "freestyle", K8sJobName: JobName(wf, id, "s", "build"),
		Spec: &models.FreestyleSpec{Steps: []*models.StepSpec{
			{Name: "img", Type: models.StepDockerBuild, Image: "mini-demo", Tag: "v1"},
		}},
	}
	return wt, jt
}

func envMap(job *batchv1.Job) map[string]string {
	m := map[string]string{}
	for _, e := range job.Spec.Template.Spec.Containers[0].Env {
		m[e.Name] = e.Value
	}
	return m
}

// TestBuildJobBuildkit —— buildkit 路线的 Pod 形状（决断 #15 演进）：MP_BUILDKIT_ADDR/CERTS
// env + 客户端证书卷三层 pem；dind 时代的 DOCKER_HOST/DOCKER_TLS_* 与 DOCKER_BUILDKIT=0
// 必须绝迹（classic builder 退役，provenance 防线移交 buildx --provenance=false）；
// fat 路线的 hostPath 卷必须绝迹；同 task 两次构建落同一 buildkit（亲和确定性）
func TestBuildJobBuildkit(t *testing.T) {
	t.Setenv("BUILDKIT_REPLICAS", "2")
	t.Setenv("BUILDKIT_ADDR", "")
	wt, jt := bkFixture("bkwf", 7)
	job := BuildJob("pipeline", wt, jt, "echo hi")
	env := envMap(job)

	want := fmt.Sprintf("tcp://buildkit-%d.buildkit:1234", buildkitIndex("bkwf", 7, 2))
	if env["MP_BUILDKIT_ADDR"] != want {
		t.Errorf("MP_BUILDKIT_ADDR = %q, want %q", env["MP_BUILDKIT_ADDR"], want)
	}
	if env["MP_BUILDKIT_CERTS"] != bkClientCertPath {
		t.Errorf("MP_BUILDKIT_CERTS = %q, want %q", env["MP_BUILDKIT_CERTS"], bkClientCertPath)
	}
	for _, gone := range []string{"DOCKER_HOST", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "DOCKER_BUILDKIT"} {
		if _, ok := env[gone]; ok {
			t.Errorf("stale env %s survived buildkit migration: %q", gone, env[gone])
		}
	}

	// 客户端证书卷：buildkit-tls-certs Secret + items 改名 ca.pem/cert.pem/key.pem
	// （buildx remote driver 经 driver-opt 显式引用，docker CLI 的 env 约定不再适用）
	var tls *corev1.SecretVolumeSource
	for _, v := range job.Spec.Template.Spec.Volumes {
		if v.VolumeSource.HostPath != nil {
			t.Errorf("hostPath volume survived migration: %s", v.Name) // fat 路线绝迹
		}
		if v.Name == "buildkit-client-tls" {
			tls = v.VolumeSource.Secret
		}
	}
	if tls == nil || tls.SecretName != "buildkit-tls-certs" {
		t.Fatalf("buildkit-client-tls secret volume missing: %+v", tls)
	}
	paths := map[string]bool{}
	for _, it := range tls.Items {
		paths[it.Path] = true
	}
	for _, p := range []string{"ca.pem", "cert.pem", "key.pem"} {
		if !paths[p] {
			t.Errorf("client cert items missing %q: %v", p, paths)
		}
	}

	// 亲和确定性：同 wf+taskID 两次构建必须落同一台 buildkit
	job2 := BuildJob("pipeline", wt, jt, "echo hi")
	if env["MP_BUILDKIT_ADDR"] != envMap(job2)["MP_BUILDKIT_ADDR"] {
		t.Errorf("affinity not deterministic: %q vs %q", env["MP_BUILDKIT_ADDR"], envMap(job2)["MP_BUILDKIT_ADDR"])
	}
}

// TestBuildJobBuildkitExternal —— BUILDKIT_ADDR 外部直连档：地址原样透传、不带客户端证书
// （外部 buildkitd 自管安全，驱动脚本按 MP_BUILDKIT_CERTS 为空自适应裸连）
func TestBuildJobBuildkitExternal(t *testing.T) {
	t.Setenv("BUILDKIT_ADDR", "tcp://bk.example.com:1234")
	_, jt := bkFixture("bkext", 1)
	job := BuildJob("pipeline", &models.WorkflowTask{WorkflowName: "bkext", TaskID: 1}, jt, "echo hi")
	if env := envMap(job); env["MP_BUILDKIT_ADDR"] != "tcp://bk.example.com:1234" {
		t.Errorf("external addr = %q", env["MP_BUILDKIT_ADDR"])
	}
	for _, v := range job.Spec.Template.Spec.Volumes {
		if v.Name == "buildkit-client-tls" {
			t.Error("external mode must not carry client certs")
		}
	}
}

// TestBuildkitIndexSpread —— fnv 亲和的分布冒烟：同 key 恒同桶，不同 task 两个桶都出现
func TestBuildkitIndexSpread(t *testing.T) {
	if buildkitIndex("wf", 1, 2) != buildkitIndex("wf", 1, 2) {
		t.Fatal("same key diverged")
	}
	seen := map[int]bool{}
	for i := 0; i < 50; i++ {
		seen[buildkitIndex(fmt.Sprintf("wf-%d", i), int64(i+1), 2)] = true
	}
	if len(seen) != 2 {
		t.Errorf("expected both buckets used across 50 tasks, got %v", seen)
	}
	if buildkitIndex("wf", 1, 0) != 0 {
		t.Error("replicas < 1 must clamp to 1 (no mod-by-zero)")
	}
}

// TestBuildJobBusyboxNoBuildkit —— 纯 shell job 不带任何构建池 env / 证书卷
func TestBuildJobBusyboxNoBuildkit(t *testing.T) {
	wt := &models.WorkflowTask{WorkflowName: "plain", TaskID: 1}
	jt := &models.JobTask{Name: "j", JobType: "freestyle", Spec: &models.FreestyleSpec{Scripts: []string{"echo hi"}}}
	job := BuildJob("pipeline", wt, jt, "echo hi")
	for _, e := range job.Spec.Template.Spec.Containers[0].Env {
		if strings.HasPrefix(e.Name, "MP_BUILDKIT") || strings.HasPrefix(e.Name, "DOCKER_") {
			t.Errorf("busybox job got builder env %s", e.Name)
		}
	}
	for _, v := range job.Spec.Template.Spec.Volumes {
		if v.Name == "buildkit-client-tls" {
			t.Error("busybox job got buildkit client cert volume")
		}
	}
}
