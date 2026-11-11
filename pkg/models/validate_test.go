package models

import "testing"

// ValidateWorkflow 的 docker_build 镜像名/tag 字符集门（2026-11-11）：大写/空格/中文
// 曾一路活到 buildkit 才炸 invalid reference format（用户两连踩），拦在保存前。
// 与前端 collectErrors 的 IMG_REF_RE/IMG_TAG_RE 同规则，改这边必改那边。
func TestValidateWorkflowDockerBuildImage(t *testing.T) {
	base := func(image, tag string) *Workflow {
		return &Workflow{
			Name: "wf",
			Stages: []*Stage{{
				Name: "stage-1",
				Jobs: []*Job{{
					Name: "build",
					Spec: &FreestyleSpec{
						Steps: []*StepSpec{{Name: "img", Type: StepDockerBuild, Image: image, Tag: tag}},
					},
				}},
			}},
		}
	}

	pass := []struct{ image, tag string }{
		{"minio-test", "latest"},                         // 裸名（自建 registry 根路径档）
		{"codepipeline-learning/minio-test", ""},         // 命名空间/仓库（ACR 常态）
		{"a/b/c", "v1.0_beta"},                           // 多段路径 + tag 下划线点
		{"172.29.237.249:30500/scratch-inline-df", "t1"}, // 全名直通档（host:port，HasRegistryPrefix 语义）
		{"localhost/scratch", "L"},                       // localhost 前缀档
		{"{{.build.IMG}}", "{{.build.TAG}}"},             // 变量模板跳过
	}
	for _, c := range pass {
		if err := ValidateWorkflow(base(c.image, c.tag)); err != nil {
			t.Errorf("image=%q tag=%q: want pass, got %v", c.image, c.tag, err)
		}
	}

	reject := []struct{ image, tag string }{
		{"Minio-Test", ""}, // 大写
		{"minio test", ""}, // 空格（用户真踩过）
		{"镜像", ""},         // 中文（用户真踩过）
		{"ns/", ""},        // 尾斜杠（namespace 填了 name 还空着）
		{"-lead/name", ""}, // 段首符号
		{"mi/nio/x/", ""},  // 尾斜杠多段
		{"", "-x"},         // tag 首符非法
		{"", "a b"},        // tag 空格
		{"", ".9"},         // tag 首符为点
	}
	for _, c := range reject {
		if err := ValidateWorkflow(base(c.image, c.tag)); err == nil {
			t.Errorf("image=%q tag=%q: want reject, got pass", c.image, c.tag)
		}
	}
}

// 非法镜像名的报错必须点名步骤与规则（前端 toast 直接展示这条）
func TestValidateWorkflowDockerBuildImageMessage(t *testing.T) {
	wf := &Workflow{
		Name: "wf",
		Stages: []*Stage{{
			Name: "stage-1",
			Jobs: []*Job{{
				Name: "build",
				Spec: &FreestyleSpec{
					Steps: []*StepSpec{{Name: "构建镜像", Type: StepDockerBuild, Image: "minio test", Tag: ""}},
				},
			}},
		}},
	}
	err := ValidateWorkflow(wf)
	if err == nil {
		t.Fatal("want reject, got pass")
	}
	want := `step "构建镜像" image "minio test"`
	if got := err.Error(); !contains(got, want) {
		t.Errorf("error message want contains %q, got %q", want, got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
