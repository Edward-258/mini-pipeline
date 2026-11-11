package models

import "testing"

// 统一代码源（决断 #18）：校验门 / 目录派生 / git 步骤转换

func TestValidateWorkflowSource(t *testing.T) {
	wf := func(src *SourceSpec) *Workflow {
		return &Workflow{
			Name:   "wf",
			Source: src,
			Stages: []*Stage{{Name: "s", Jobs: []*Job{{Name: "j", Spec: &FreestyleSpec{
				Steps: []*StepSpec{{Name: "st", Type: StepShell, Script: "echo hi"}},
			}}}}},
		}
	}
	if err := ValidateWorkflow(wf(&SourceSpec{Repo: "https://github.com/o/r.git", Branch: "dev"})); err != nil {
		t.Errorf("valid source rejected: %v", err)
	}
	if err := ValidateWorkflow(wf(nil)); err != nil {
		t.Errorf("nil source must pass (legacy workflows): %v", err)
	}
	if err := ValidateWorkflow(wf(&SourceSpec{})); err == nil {
		t.Error("source without repo must be rejected")
	}
}

func TestRepoDirName(t *testing.T) {
	cases := map[string]string{
		"https://github.com/o/r.git":   "r",
		"https://github.com/o/r":       "r",
		"git@host:group/repo-name.git": "repo-name", // scp 形态按 ':' 切
		"https://git.example.com/a/b/": "repo",      // 尾斜杠取到空段 → 兜底（原 step 语义保真）
		"https://host/怪/名字.git":        "-",         // 非 [a-zA-Z0-9._-] 消压成 '-'（原 step 语义保真）
		"":                             "repo",      // 空串兜底
	}
	for repo, want := range cases {
		if got := RepoDirName(repo); got != want {
			t.Errorf("RepoDirName(%q) = %q, want %q", repo, got, want)
		}
	}
}

func TestSourceSpecDerived(t *testing.T) {
	// 显式 Dir 优先；缺省派生；CheckoutStep 字段映射齐全（渲染/token/branch 语义全在 gitCtl）
	src := &SourceSpec{Repo: "https://github.com/o/r.git", Token: "tok"}
	if got := src.CheckoutDir(); got != "r" {
		t.Errorf("derived dir = %q", got)
	}
	src.Dir = "custom"
	if got := src.CheckoutDir(); got != "custom" {
		t.Errorf("explicit dir = %q", got)
	}
	st := src.CheckoutStep()
	if st.Name != "clone" || st.Type != StepGit || st.Repo != src.Repo || st.Token != "tok" || st.ContextDir != "custom" {
		t.Errorf("checkout step = %+v", st)
	}
}
