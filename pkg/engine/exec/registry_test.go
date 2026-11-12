package exec

import (
	"errors"
	"strings"
	"testing"

	"pipeline/pkg/models"
)

// TestResolveRegistrySteps —— 推送目标三档解析表测（Ctx stub，不摸持久层）：
//
//	显式引用 > 裸名跟随默认 > 全名直通；裸名无默认 = 报错（决断 #16）。
func TestResolveRegistrySteps(t *testing.T) {
	c := &Ctx{
		FindRegistry: func(name string) (*models.Registry, error) {
			if name == "ghost" {
				return nil, repoErrNotFound
			}
			return &models.Registry{Name: name, Address: "reg.example.com/ns", Username: "u", Password: "p'w"}, nil
		},
		FindDefaultRegistry: func() (*models.Registry, error) {
			return &models.Registry{Name: "def", Address: "def.example.com", Username: "", Password: ""}, nil
		},
	}

	in := []*models.StepSpec{
		{Name: "shell", Type: models.StepShell, Script: "echo hi"},
		{Name: "ref", Type: models.StepDockerBuild, Image: "app", Tag: "v1", Registry: "named"},
		{Name: "bare-with-default", Type: models.StepDockerBuild, Image: "mini-demo"},
		{Name: "prefixed", Type: models.StepDockerBuild, Image: "other.com/x:1"},
		{Name: "ghost-ref", Type: models.StepDockerBuild, Image: "app", Registry: "ghost"},
	}

	out, err := resolveRegistrySteps(c, in[:4])
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if out[0] != in[0] {
		t.Errorf("non-docker step must pass through untouched")
	}
	// 档 1：显式引用 → 烘焙该记录凭证（副本，原 spec 不动）
	got := out[1]
	if got == in[1] {
		t.Errorf("ref step must be baked into a copy, not mutate the spec")
	}
	if got.RegAddr != "reg.example.com/ns" || got.RegUser != "u" || got.RegPass != "p'w" {
		t.Errorf("ref step bake failed: %+v", got)
	}
	if in[1].RegAddr != "" {
		t.Errorf("original spec mutated: %+v", in[1])
	}
	// 档 2：裸名 + 默认存在 → 跟随默认（无凭证记录则只带地址）
	if out[2].RegAddr != "def.example.com" || out[2].RegUser != "" {
		t.Errorf("bare step should follow default registry: %+v", out[2])
	}
	// 档 3a：全名直通（零烘焙零拷贝）
	if out[3] != in[3] {
		t.Errorf("prefixed step must pass through: %+v", out[3])
	}

	// 档 1 失败路径：引用不存在 → 报错点名步骤与仓库
	if _, err = resolveRegistrySteps(c, in[4:]); err == nil {
		t.Errorf("ghost registry ref must fail resolution")
	}

	// 档 3b：裸名但无默认 → 报错点名（决断 #16：内置 registry 退役后裸名无家可归，
	// 语义回归"推送目标必须有人站"——登记并设默认，或改用全名）
	c.FindDefaultRegistry = func() (*models.Registry, error) { return nil, nil }
	_, err = resolveRegistrySteps(c, in[2:3])
	if err == nil {
		t.Fatalf("bare name without default must fail resolution")
	}
	if !strings.Contains(err.Error(), "bare image name") || !strings.Contains(err.Error(), "mini-demo") {
		t.Errorf("error must name the bare image: %v", err)
	}
	// 对照组：全名在无默认时依旧直通（匿名推，决断 #16 保留档）
	out2, err := resolveRegistrySteps(c, in[3:4])
	if err != nil {
		t.Fatalf("prefixed no-default resolve: %v", err)
	}
	if out2[0] != in[3] || out2[0].RegAddr != "" {
		t.Errorf("prefixed step without default must pass through: %+v", out2[0])
	}
}

// repoErrNotFound —— 与持久层 ErrNotFound 同形的哨兵（测试不 import repo）
var repoErrNotFound = errors.New("not found")
