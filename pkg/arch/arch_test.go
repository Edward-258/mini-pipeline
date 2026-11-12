package arch

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// goRun 执行 go 子命令并返回 stdout；dir 为空 = 继承当前工作目录。
// 失败即 Fatal 并携带 stderr——包图本身解析不了时，分层断言无从谈起。
func goRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go %s (dir=%s): %v\n%s",
			strings.Join(args, " "), dir, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String()
}

// moduleRoot 返回 go.mod 所在目录。go test 的 cwd 恒为本包目录（必在模块内），据此解析模块根。
func moduleRoot(t *testing.T) string {
	t.Helper()
	return filepath.Dir(strings.TrimSpace(goRun(t, "", "env", "GOMOD")))
}

// pipelineDeps 返回 target 的依赖闭包（-deps -test：生产代码与测试文件一起算）
// 中模块内的包。外部依赖（标准库、第三方模块）与分层方向无关，一律滤掉。
func pipelineDeps(t *testing.T, root, mod, target string) []string {
	t.Helper()
	out := goRun(t, root, "list", "-deps", "-test", "-f", "{{.ImportPath}}", target)
	return dedupe(pipelineLines(out, mod))
}

// pipelineLines 从 go list 输出中取模块内的包路径；" [pkg/x.test]" 形态的括号注记
// （-test 下同包的测试变体条目）截掉，只留干净的导入路径。
func pipelineLines(out, mod string) []string {
	var pkgs []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, " ["); i >= 0 {
			line = line[:i]
		}
		if strings.HasPrefix(line, mod+"/") {
			pkgs = append(pkgs, line)
		}
	}
	return pkgs
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, p := range in {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// offenders 返回 pkgs 中踩线的包：exact 精确禁单个包，prefixes 连同其子包一起禁。
func offenders(pkgs []string, exact, prefixes []string) []string {
	var bad []string
	for _, p := range pkgs {
		hit := false
		for _, e := range exact {
			if p == e {
				hit = true
				break
			}
		}
		if !hit {
			for _, f := range prefixes {
				if p == f || strings.HasPrefix(p, f+"/") {
					hit = true
					break
				}
			}
		}
		if hit {
			bad = append(bad, p)
		}
	}
	return bad
}

// TestLayering 把分层方向纪律变成会咬人的测试：规则被踩即红灯，消息点名违规包。
// 断言全部基于 go 工具链的包图查询，本测试不 import 任何被检包。
// 口径按现状校准（2026-11-12）：engine 编排本体已不 import step，相关断言不存在；
// exec 是 engine 的子包，合法使用兄弟子包（如 engine/cancel），故只禁 engine 编排本体。
func TestLayering(t *testing.T) {
	root := moduleRoot(t)
	mod := strings.TrimSpace(goRun(t, root, "list", "-m"))
	pkg := func(name string) string { return mod + "/pkg/" + name }

	modelsDeps := pipelineDeps(t, root, mod, pkg("models"))
	repoDeps := pipelineDeps(t, root, mod, pkg("repo"))
	stepDeps := pipelineDeps(t, root, mod, pkg("step"))
	execDeps := pipelineDeps(t, root, mod, pkg("engine/exec"))

	// 规则3看直接 import（含测试文件）而非闭包：api 的依赖闭包经 engine 合法地含
	// k8s/step——钩子边界约束的是「api 自己伸手」，不是它驱动引擎去够。
	apiImports := pipelineLines(goRun(t, root, "list", "-f",
		`{{join .Imports "\n"}}{{"\n"}}{{join .TestImports "\n"}}{{"\n"}}{{join .XTestImports "\n"}}`,
		pkg("api")), mod)

	leaves := []struct {
		name string
		deps []string
	}{
		{"pkg/models", modelsDeps},
		{"pkg/repo", repoDeps},
		{"pkg/step", stepDeps},
	}

	t.Run("rule1-leaves-no-engine-api", func(t *testing.T) {
		for _, l := range leaves {
			bad := offenders(l.deps, nil, []string{pkg("engine"), pkg("api")})
			if len(bad) > 0 {
				t.Errorf("%s 的依赖闭包出现 engine/api 层的包（叶子层依赖只能单向向下）：\n  %s",
					l.name, strings.Join(bad, "\n  "))
			}
		}
	})

	t.Run("rule2-exec-no-engine-api", func(t *testing.T) {
		bad := offenders(execDeps, []string{pkg("engine")}, []string{pkg("api")})
		if len(bad) > 0 {
			t.Errorf("pkg/engine/exec 的依赖闭包出现 engine 编排本体或 api 层（exec 只被引擎驱动）：\n  %s",
				strings.Join(bad, "\n  "))
		}
	})

	t.Run("rule3-api-no-direct-k8s-step", func(t *testing.T) {
		bad := offenders(apiImports, nil, []string{pkg("k8s"), pkg("step")})
		if len(bad) > 0 {
			t.Errorf("pkg/api 直接 import 了 k8s/step（钩子边界：K8s 访问经钩子注入，api 只做 HTTP 形状翻译）：\n  %s",
				strings.Join(bad, "\n  "))
		}
	})

	t.Run("rule4-step-no-repo", func(t *testing.T) {
		bad := offenders(stepDeps, nil, []string{pkg("repo")})
		if len(bad) > 0 {
			t.Errorf("pkg/step 的依赖闭包出现 pkg/repo（脚本执行层只准依赖 models）：\n  %s",
				strings.Join(bad, "\n  "))
		}
	})
}
