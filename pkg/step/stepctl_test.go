package step

import (
	"encoding/base64"
	"strings"
	"testing"

	"pipeline/pkg/models"
)

// TestGitCtlPreRun —— git 片段渲染：token 注入、目录推断、默认分支
func TestGitCtlPreRun(t *testing.T) {
	get := func(s *models.StepSpec) string {
		frag, err := (&gitCtl{spec: s}).PreRun()
		if err != nil {
			t.Fatalf("PreRun: %v", err)
		}
		return frag
	}

	frag := get(&models.StepSpec{Name: "g", Type: models.StepGit, Repo: "https://github.com/o/r.git"})
	if !strings.Contains(frag, "git clone --depth 1 -b 'main'") { // 默认分支 main
		t.Errorf("default branch missing: %s", frag)
	}
	if !strings.Contains(frag, "'r'") { // 目录名从 URL 尾段推（剥 .git）
		t.Errorf("dir name not inferred: %s", frag)
	}
	if !strings.Contains(frag, "cd 'r'") { // clone 后留在仓库目录（后续步骤在源码根上跑）
		t.Errorf("cd missing: %s", frag)
	}

	frag = get(&models.StepSpec{Type: models.StepGit, Repo: "https://github.com/o/r.git", Token: "ghp_x"})
	if !strings.Contains(frag, "https://ghp_x@github.com/o/r.git") {
		t.Errorf("token not injected: %s", frag)
	}
	frag = get(&models.StepSpec{Type: models.StepGit, Repo: "https://github.com/o/r", Token: "user:pass"})
	if !strings.Contains(frag, "https://user:pass@github.com/o/r") {
		t.Errorf("user:pass token not injected: %s", frag)
	}
	frag = get(&models.StepSpec{Type: models.StepGit, Repo: "https://gitee.com/a/b.git", ContextDir: "src"})
	if !strings.Contains(frag, "'src'") || strings.Contains(frag, "'b'") {
		t.Errorf("context_dir override failed: %s", frag)
	}
}

// TestDockerBuildCtlPreRun —— 构建片段：buildx remote driver 形态 + 全名直通与裸名报错
// （决断 #16：内置本地档退役，裸名无默认可跟 = PreRun 报错——exec 解析层已挡，这里是防御兜底）
func TestDockerBuildCtlPreRun(t *testing.T) {
	// 裸名：报错点名（不再有 $MP_NODE_REGISTRY 前缀注入）
	if _, err := (&dockerBuildCtl{spec: &models.StepSpec{Type: models.StepDockerBuild, Image: "mini-demo"}}).PreRun(); err == nil {
		t.Errorf("bare image name must fail at PreRun (no built-in registry since decision #16)")
	}
	// 全名直通：buildx 序列 mp_bk 助手（builder 幂等重建）+ build --push（推送由 buildkitd
	// 直出，classic 的 docker build/push 双命令形态必须绝迹）；--provenance=false 是 ACR 防线（§9.9）
	frag, err := (&dockerBuildCtl{spec: &models.StepSpec{Type: models.StepDockerBuild, Image: "reg.example.com/x"}}).PreRun()
	if err != nil {
		t.Fatalf("full-name PreRun: %v", err)
	}
	if !strings.Contains(frag, `mp_bk && docker buildx build --builder mp --provenance=false --push -f 'Dockerfile' -t 'reg.example.com/x:latest' '.'`) {
		t.Errorf("buildx remote build form expected: %s", frag)
	}
	if !strings.Contains(frag, `mp_bk() {`) || !strings.Contains(frag, `--driver remote`) {
		t.Errorf("builder helper (rm+create remote driver) missing: %s", frag)
	}
	if strings.Contains(frag, "docker build ") || strings.Contains(frag, "docker push ") {
		t.Errorf("classic docker build/push must be gone: %s", frag)
	}
	// 构建成功宣告 image_full（&& 链尾：失败不宣告）
	if !strings.Contains(frag, `echo '===MP=== OUTPUT image_full=reg.example.com/x:latest'`) {
		t.Errorf("OUTPUT image_full banner expected at chain tail: %s", frag)
	}

	frag, _ = (&dockerBuildCtl{spec: &models.StepSpec{Type: models.StepDockerBuild, Image: "other.example.com:5000/x", Tag: "v2", Dockerfile: "build/Dockerfile", ContextDir: "cmd/app"}}).PreRun()
	if !strings.Contains(frag, "-f 'build/Dockerfile'") || !strings.Contains(frag, "'cmd/app'") {
		t.Errorf("dockerfile/context not rendered: %s", frag)
	}
	if !strings.Contains(frag, "-t 'other.example.com:5000/x:v2'") {
		t.Errorf("full name with tag not rendered: %s", frag)
	}
}

// TestJunitGate —— 门禁判定表：解析（单表/包装层/拼接）+ 通过率翻转
func TestJunitGate(t *testing.T) {
	const oneFail = `<?xml version="1.0"?><testsuite name="t" tests="4" failures="1" errors="0" skipped="0" time="0.5"></testsuite>`
	const allPass = `<testsuite name="t" tests="2" failures="0" errors="0" skipped="0" time="0.1"></testsuite>`

	run := func(log string, rate float64) *models.StepTask {
		st := &models.StepTask{Name: "r", Type: models.StepJunitReport, Status: models.StatusPassed, Log: log}
		ctl := &junitReportCtl{spec: &models.StepSpec{Type: models.StepJunitReport, PassRate: rate}}
		if err := ctl.AfterRun(st); err != nil {
			t.Fatalf("AfterRun returned engine error: %v", err) // 门禁失败是步骤失败，不是引擎错误
		}
		return st
	}

	st := run(oneFail, 1.0) // 3/4 = 75% < 100% → 翻红
	if st.Status != models.StatusFailed || !strings.Contains(st.Error, "75.0%") {
		t.Errorf("gate should fail at 75%% < 100%%: status=%s err=%s", st.Status, st.Error)
	}
	if st.TestResult == nil || st.TestResult.Tests != 4 || st.TestResult.Failures != 1 || st.TestResult.Time != 0.5 {
		t.Errorf("TestResult wrong: %+v", st.TestResult)
	}

	st = run(oneFail, 0.7) // 75% ≥ 70% → 放行
	if st.Status != models.StatusPassed {
		t.Errorf("gate 70%% should pass: %s", st.Status)
	}

	// <testsuites> 包装层不计（防双算），内层两张表拼接累加
	wrapped := `<testsuites tests="6" failures="1"><testsuite tests="3" failures="0" errors="0" time="1"></testsuite><testsuite tests="3" failures="1" errors="0" time="1"></testsuite></testsuites>`
	st = run(wrapped, 0.5) // 5/6 ≈ 83% ≥ 50%
	if st.Status != models.StatusPassed || st.TestResult.Tests != 6 || st.TestResult.Failures != 1 || st.TestResult.Time != 2 {
		t.Errorf("wrapped/concat aggregation wrong: status=%s res=%+v", st.Status, st.TestResult)
	}

	st = run("no xml here at all", 1.0) // 没产出 XML：解析失败 = 步骤失败
	if st.Status != models.StatusFailed || !strings.Contains(st.Error, "junit parse") {
		t.Errorf("missing xml should fail step: %s %s", st.Status, st.Error)
	}
	_ = allPass
}

// TestDockerBuildCtlRegistryRef —— 仓库引用档（2026-11-07）：全名拼接 + stdin 登录 + 单引号转义
// + scheme 剥离（决断 #16：http:// 前缀是传输档声明不是地址的一部分，烘焙前必须剥掉）
func TestDockerBuildCtlRegistryRef(t *testing.T) {
	frag, err := (&dockerBuildCtl{spec: &models.StepSpec{
		Name: "img", Type: models.StepDockerBuild, Image: "ns/app", Tag: "v2",
		RegAddr: "reg.example.com", RegUser: "builder", RegPass: "p'a$$",
	}}).PreRun()
	if err != nil {
		t.Fatalf("PreRun: %v", err)
	}
	if !strings.Contains(frag, `-t 'reg.example.com/ns/app:v2'`) {
		t.Errorf("full image name addr/relative:tag expected: %s", frag)
	}
	if !strings.Contains(frag, `docker login -u 'builder' --password-stdin 'reg.example.com'`) {
		t.Errorf("stdin login expected: %s", frag)
	}
	// buildx 的 create/build 必须圈在 login 同一子 shell 里：buildx 收 auth 靠的就是
	// login 刚写进 $DOCKER_CONFIG/config.json 的凭证（remote driver 随 SolveRequest 传 buildkitd）
	if !strings.Contains(frag, `&& mp_bk && docker buildx build --builder mp --provenance=false --push -f 'Dockerfile'`) {
		t.Errorf("buildx sequence must chain after login inside the subshell: %s", frag)
	}
	// login 链路必须包在子 shell + 临时 DOCKER_CONFIG 里（凭证只活在 Pod 临时目录）
	if !strings.Contains(frag, "( export DOCKER_CONFIG=/tmp/.docker && ") || !strings.HasSuffix(frag, " )") {
		t.Errorf("login must be subshell-wrapped with temp DOCKER_CONFIG: %s", frag)
	}
	// 密码含单引号必须被 POSIX 转义（p'a$$ → 'p'\''a$$'），否则驱动脚本被掀翻
	if !strings.Contains(frag, "printf '%s' 'p'\\''a$$'") {
		t.Errorf("password must be single-quote escaped: %s", frag)
	}
	// 引用档同样宣告 image_full（下游 deploy 引用）
	if !strings.Contains(frag, `echo '===MP=== OUTPUT image_full=reg.example.com/ns/app:v2'`) {
		t.Errorf("OUTPUT image_full banner expected: %s", frag)
	}

	// scheme 声明剥离：http:// 前缀只进白名单判定（k8s/buildkit.go），不进 docker 引用
	frag, _ = (&dockerBuildCtl{spec: &models.StepSpec{
		Name: "img", Type: models.StepDockerBuild, Image: "app",
		RegAddr: "http://reg.local.svc:5000",
	}}).PreRun()
	if strings.Contains(frag, "http://") {
		t.Errorf("scheme must be stripped from baked refs: %s", frag)
	}
	if !strings.Contains(frag, "-t 'reg.local.svc:5000/app:latest'") {
		t.Errorf("normalized addr expected: %s", frag)
	}

	// 免认证仓库：无 username 则跳过 login（内网裸仓）
	frag, _ = (&dockerBuildCtl{spec: &models.StepSpec{
		Name: "img", Type: models.StepDockerBuild, Image: "app",
		RegAddr: "registry.local.svc:5000",
	}}).PreRun()
	if strings.Contains(frag, "docker login") {
		t.Errorf("credential-less registry must skip login: %s", frag)
	}
	if !strings.Contains(frag, "-t 'registry.local.svc:5000/app:latest'") {
		t.Errorf("default tag latest expected: %s", frag)
	}
}

// TestDockerBuildInlineDockerfile —— 内联 Dockerfile 档（0.7.10）：正文 b64 烘焙、
// 现场落 .mp-inline-Dockerfile 再 -f 引用；内容里的单引号/换行/$ 全部原样幸存
// （b64 绕开 shell 引号协议——内容是数据不是脚本），路径档在 content 为空时零感知。
func TestDockerBuildInlineDockerfile(t *testing.T) {
	content := "FROM busybox\nRUN echo \"it's $HOME\" && printf '%s' ok\n"
	spec := &models.StepSpec{
		Name: "img", Type: models.StepDockerBuild, Image: "reg.example.com/mini-demo", Tag: "v1",
		Dockerfile:        "build/Dockerfile", // 路径档同填：内联必须赢
		DockerfileContent: content,
	}
	frag, err := (&dockerBuildCtl{spec: spec}).PreRun()
	if err != nil {
		t.Fatalf("PreRun: %v", err)
	}
	if !strings.Contains(frag, "-f '.mp-inline-Dockerfile'") {
		t.Errorf("inline content must switch -f to materialized file: %s", frag)
	}
	// b64 段解回原内容（roundtrip：引擎烘进去什么，Pod 里就该落出什么）
	i := strings.Index(frag, "printf '%s' '")
	j := strings.Index(frag[i:], "' | base64 -d >")
	if i < 0 || j < 0 {
		t.Fatalf("b64 materialize segment missing: %s", frag)
	}
	got, err := base64.StdEncoding.DecodeString(frag[i+len("printf '%s' '") : i+j])
	if err != nil {
		t.Fatalf("b64 decode: %v", err)
	}
	if string(got) != content {
		t.Errorf("content roundtrip broken:\n got %q\nwant %q", got, content)
	}
	if strings.Contains(frag, "'build/Dockerfile'") {
		t.Errorf("path-mode dockerfile must be overridden by inline content: %s", frag)
	}

	// 仓库引用档：helper 定义 → 落文件 → login 子 shell（三者顺序钉死——materialize 在
	// 子 shell 外跑一次即可，helper 是纯定义先铺）
	frag, _ = (&dockerBuildCtl{spec: &models.StepSpec{
		Name: "img", Type: models.StepDockerBuild, Image: "app",
		RegAddr: "reg.example.com", RegUser: "builder", RegPass: "p",
		DockerfileContent: content,
	}}).PreRun()
	h := strings.Index(frag, "mp_bk() {")
	m := strings.Index(frag, "printf '%s' '")
	s := strings.Index(frag, "( export DOCKER_CONFIG")
	if h < 0 || m < 0 || s < 0 || !(h < m && m < s) {
		t.Errorf("order must be helper < materialize < login subshell: %s", frag)
	}

	// 空内容 = 路径档原样（存量图纸零感知）
	frag, _ = (&dockerBuildCtl{spec: &models.StepSpec{
		Name: "img", Type: models.StepDockerBuild, Image: "reg.example.com/app", Dockerfile: "build/Dockerfile",
	}}).PreRun()
	if strings.Contains(frag, "base64") || !strings.Contains(frag, "-f 'build/Dockerfile'") {
		t.Errorf("empty content must keep pure path mode: %s", frag)
	}
}
