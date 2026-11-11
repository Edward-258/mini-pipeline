package engine

import (
	"testing"

	"pipeline/pkg/models"
)

// 统一代码源（决断 #18）：BuildTask 的 checkout stage 合成——快照=可执行真相的表测

func TestBuildTaskSourceSynthesis(t *testing.T) {
	wf := &models.Workflow{
		Name:   "src-wf",
		Source: &models.SourceSpec{Repo: "https://github.com/o/r-demo.git", Branch: "dev", Token: "tok"},
		Stages: []*models.Stage{{Name: "build", Jobs: []*models.Job{{
			Name: "j", JobType: models.JobTypeFreestyle,
			Spec: &models.FreestyleSpec{Steps: []*models.StepSpec{{Name: "s", Type: models.StepShell, Script: "ls"}}},
		}}}},
	}
	tk := BuildTask(wf, 3)

	if len(tk.Stages) != 2 {
		t.Fatalf("stages = %d, want 2 (synthesized checkout + build)", len(tk.Stages))
	}
	co := tk.Stages[0]
	if co.Kind != "source" || co.Name != "checkout" || co.Status != models.StatusCreated {
		t.Errorf("checkout stage = kind=%q name=%q status=%q", co.Kind, co.Name, co.Status)
	}
	if len(co.Jobs) != 1 {
		t.Fatalf("checkout jobs = %d", len(co.Jobs))
	}
	cj := co.Jobs[0]
	if cj.Name != "clone" || cj.JobType != models.JobTypeFreestyle {
		t.Errorf("clone job = %+v", cj)
	}
	if len(cj.Steps) != 1 || cj.Steps[0].Type != models.StepGit {
		t.Errorf("clone steps = %+v", cj.Steps)
	}
	// 执行体 = CheckoutStep 的字段映射（渲染语义在 gitCtl 单源）
	st := cj.Spec.Steps[0]
	if st.Repo != wf.Source.Repo || st.Branch != "dev" || st.Token != "tok" {
		t.Errorf("clone step spec = %+v", st)
	}
	// 快照声明封存（clone/retry 重建的依据）
	if tk.WorkflowArgs.Source == nil || tk.WorkflowArgs.Source.Repo != wf.Source.Repo {
		t.Errorf("snapshot lost source: %+v", tk.WorkflowArgs.Source)
	}
	// 快照树不合成 checkout——合成体只在运行树（编辑器 JSON 干净）
	if len(tk.WorkflowArgs.Stages) != 1 {
		t.Errorf("snapshot stages must stay pure, got %d", len(tk.WorkflowArgs.Stages))
	}

	// 无 source = 老世界零合成
	legacy := BuildTask(&models.Workflow{Name: "x", Stages: wf.Stages}, 1)
	if len(legacy.Stages) != 1 || legacy.Stages[0].Kind != "" {
		t.Errorf("legacy workflow must not synthesize checkout, stages=%d", len(legacy.Stages))
	}
}
