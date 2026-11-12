// Package models —— 领域模型与状态常量（leaf 包：workflow 图纸 / workflow_task 档案 /
// env / cron / notify 五域的结构体、Status 体系与 WorstOf 聚合、纯领域逻辑
// （ValidateWorkflow/CronMatch/Satisfied/EffectiveSteps）全在这里。
package models

import (
	"regexp"
	"strings"
	"time"
)

// Workflow —— 图纸
// job 类型收敛为 freestyle 一种，Spec 直接是具体结构体而非 interface{}
type Workflow struct {
	Name          string            `bson:"_id"       json:"name"` // 图纸名即主键
	Stages        []*Stage          `bson:"stages"    json:"stages"`
	Source        *SourceSpec       `bson:"source,omitempty" json:"source,omitempty"`                 // 统一代码源（画布最左一级节点，clone 不属于任何 job）
	GlobalContext map[string]string `bson:"global_context,omitempty" json:"global_context,omitempty"` // 图纸级变量初始值（开跑时拷进 task.GlobalContext，M4）
	Webhooks      []*WebhookConfig  `bson:"webhooks,omitempty" json:"webhooks,omitempty"`             // T3.2：git push 触发配置（可多条）
	CreatedAt     time.Time         `bson:"created_at" json:"created_at"`
	UpdatedAt     time.Time         `bson:"updated_at" json:"updated_at"`
}

// SourceSpec —— 统一代码源声明（窄类型，有意不复用 StepSpec：step 的十几个字段对
// 代码源全是噪音）。执行语义在引擎侧：BuildTask 把它合成进任务快照的前置 checkout
// stage（Kind="source"），克隆进任务级共享工作区后下游 job 直接见文件——状态机/
// 重试/取消全免费继承，本类型自身零执行知识。git 步骤类型保留（存量图纸零破坏 +
// 多仓边角），代码源是推荐路径。
type SourceSpec struct {
	Repo   string `bson:"repo"                 json:"repo"`             // https 地址（同 git step）
	Branch string `bson:"branch,omitempty"     json:"branch,omitempty"` // 空 = main（gitCtl 同语义）
	Token  string `bson:"token,omitempty"      json:"token,omitempty"`  // 私有仓库令牌（注入 URL；明文姿态与 git step/host 同档）
	Dir    string `bson:"dir,omitempty"        json:"dir,omitempty"`    // 检出目录（相对 /workspace；空 = URL 尾段派生）
}

// dirUnsafeChars —— 检出目录/文件名消毒白名单（与 step.unsafeDirChars 同规则；
// 原实现包私有于 step，代码源需要在模型层派生目录名故上移成单一真相源）
var dirUnsafeChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// RepoDirName —— URL 尾段当目录名（github.com/o/r.git → r），非法字符压成 '-'
// （step.gitCtl 与 SourceSpec.CheckoutDir 共用；原 step 包私有 repoDirName 上移）
func RepoDirName(repo string) string {
	s := strings.TrimSuffix(repo, ".git")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	s = dirUnsafeChars.ReplaceAllString(s, "-")
	if s == "" {
		s = "repo"
	}
	return s
}

// CheckoutDir —— 代码源的检出目录（相对任务工作区 /workspace）：显式 Dir 优先，缺省从 Repo 派生
func (s *SourceSpec) CheckoutDir() string {
	if s.Dir != "" {
		return s.Dir
	}
	return RepoDirName(s.Repo)
}

// CheckoutStep —— 代码源 → git 步骤规格（BuildTask 合成 checkout stage 的执行体）。
// 纯字段映射零逻辑：clone 脚本渲染 / token 注 URL / branch 缺省 main 全在 step.gitCtl
// 单源复用——渲染层不因代码源多一条分支
func (s *SourceSpec) CheckoutStep() *StepSpec {
	return &StepSpec{Name: "clone", Type: StepGit, Repo: s.Repo, Branch: s.Branch, Token: s.Token, ContextDir: s.Dir}
}

// WebhookConfig —— git push 触发规则（T3.2，两条字段的极简形态）：
// repo 全名或 URL 尾段匹配 + 分支匹配 + HMAC-SHA256 对称密钥（secret 明文同 chart values 档案）。
type WebhookConfig struct {
	Repo   string `bson:"repo"   json:"repo"`                       // owner/repo 或完整 https URL（.git 尾缀忽略）
	Branch string `bson:"branch" json:"branch"`                     // refs/heads/<branch> 的 <branch>
	Secret string `bson:"secret,omitempty" json:"secret,omitempty"` // 空 = 不验签（明确接受）
}

// Stage —— 阶段
// 手动阀门只有一个：ManualExec（手动分步执行，M3 灵魂；审批以 approvers 扩展挂在其上，不设第二个阀门）
type Stage struct {
	Name       string      `bson:"name"                 json:"name"`
	Parallel   bool        `bson:"parallel"             json:"parallel"` // 阶段内 job 并行/串行
	ManualExec *ManualExec `bson:"manual_exec,omitempty" json:"manual_exec,omitempty"`
	Jobs       []*Job      `bson:"jobs"                 json:"jobs"`
}

// ManualExec —— 手动分步执行（T3.4 扩展原生审批）
// Enabled: 停下来等人;  Executed: 人已经放行过;  NotificationSent: 通知去重（占位）
// Approvers: 审批人名单（自由文本——无身份系统，全局信任域）；空名单 = 一次审批即放行
// Policy: any（默认，任一人批即过）| all（全员到齐）
// Approvals: 已收到的审批记录（谁、说了什么、何时）
type ManualExec struct {
	Enabled          bool        `bson:"enabled"           json:"enabled"`
	Executed         bool        `bson:"executed"          json:"executed"`
	NotificationSent bool        `bson:"notification_sent" json:"notification_sent"`
	Approvers        []string    `bson:"approvers,omitempty" json:"approvers,omitempty"`
	Policy           string      `bson:"policy,omitempty"    json:"policy,omitempty"`
	Approvals        []*Approval `bson:"approvals,omitempty" json:"approvals,omitempty"`
}

// 审批策略常量（any/all）
const (
	ApprovalPolicyAny = "any"
	ApprovalPolicyAll = "all"
)

// Approval —— 单条审批记录
type Approval struct {
	Approver string `bson:"approver" json:"approver"`
	Comment  string `bson:"comment,omitempty" json:"comment,omitempty"`
	Time     int64  `bson:"time" json:"time"`
}

// Job —— 任务单元
type Job struct {
	Name       string         `bson:"name" json:"name"`
	JobType    string         `bson:"type" json:"type"`                                   // freestyle（跑步骤）| deploy（patch 部署，T2.2）
	Spec       *FreestyleSpec `bson:"spec" json:"spec"`                                   // freestyle 专用；deploy 时可为 nil
	DeploySpec *DeploySpec    `bson:"deploy_spec,omitempty" json:"deploy_spec,omitempty"` // deploy 专用
}

// JobType 常量（只长两种）
const (
	JobTypeFreestyle = "freestyle"
	JobTypeDeploy    = "deploy" // CD 本体：引擎直接 patch Deployment（不变量3：不起 K8s Job 不挂 sock）
)

// DeploySpec —— 部署作业规格（T2.2）：
// 目标 env（=namespace）+ Deployment 名 + 容器名 + 新镜像（可 {{.jobName.output}} 引用上游）。
type DeploySpec struct {
	Env            string `bson:"env"                 json:"env"` // env 集合里的名字（T2.1）→ 解析出 namespace
	DeploymentName string `bson:"deployment_name"     json:"deployment_name"`
	ContainerName  string `bson:"container_name"      json:"container_name"`
	Image          string `bson:"image"               json:"image"`
	WaitRollout    *bool  `bson:"wait_rollout,omitempty" json:"wait_rollout,omitempty"` // 默认 true；自举发布自己更新自己时设 false 防绞杀
}

// ShouldWaitRollout —— WaitRollout 缺省 true 的取值助手
func (d *DeploySpec) ShouldWaitRollout() bool {
	return d.WaitRollout == nil || *d.WaitRollout
}

// FreestyleSpec —— freestyle 作业规格：镜像 + 步骤列表
// 极简形态：Properties+Steps 两字段就是全部
type FreestyleSpec struct {
	Image          string      `bson:"image,omitempty"           json:"image,omitempty"`           // 空 = 按步骤需要选默认（见 engine.buildJob）
	Scripts        []string    `bson:"scripts,omitempty"         json:"scripts,omitempty"`         // 旧图纸兼容：翻译成隐式 step "main"
	Steps          []*StepSpec `bson:"steps,omitempty"           json:"steps,omitempty"`           // ⭐ 步骤列表（CI/CD 核心）
	Retry          int         `bson:"retry,omitempty"           json:"retry,omitempty"`           // 单 job 重试次数（默认 0=不重试，M4）
	TimeoutMinutes int         `bson:"timeout_minutes,omitempty" json:"timeout_minutes,omitempty"` // 单 job 超时（分钟，默认 10，T1.9）
	ErrorPolicy    string      `bson:"error_policy,omitempty"    json:"error_policy,omitempty"`    // 错误策略（默认 stop，T1.9）
	Hosts          []string    `bson:"hosts,omitempty"           json:"hosts,omitempty"`           // 远程主机引用（models.Host 名；driver 编译期投影私钥进 Pod，shell 里直接 ssh <名>）
}

// 错误策略（T1.9：stop / retry / ignore）。
// 控制/上报分离：策略只改控制流（刹不刹车、重不重试），失败永远如实上报（job/stage 照红）。
const (
	ErrorPolicyStop   = "stop"   // 失败即刹车（默认；未设策略的旧图纸同此 + Retry 预算照用）
	ErrorPolicyRetry  = "retry"  // 失败按 Retry 预算重试，耗尽即刹车
	ErrorPolicyIgnore = "ignore" // 失败记红不刹车：串行继续下一个 job、后续 stage 照跑（task 终态由最差者胜如实给 failed）
)

// StepSpec —— 步骤规格（图纸层，字段按 Type 取用）
type StepSpec struct {
	Name              string  `bson:"name"                 json:"name"`
	Type              string  `bson:"type"                 json:"type"`                                 // 见下方 Step* 常量
	Script            string  `bson:"script,omitempty"     json:"script,omitempty"`                     // shell：多行脚本
	Repo              string  `bson:"repo,omitempty"       json:"repo,omitempty"`                       // git：https 地址
	Branch            string  `bson:"branch,omitempty"     json:"branch,omitempty"`                     // git：默认 main
	Token             string  `bson:"token,omitempty"      json:"token,omitempty"`                      // git：私有仓库访问令牌（注入 URL）
	ContextDir        string  `bson:"context_dir,omitempty" json:"context_dir,omitempty"`               // git 检出目录 / docker_build 上下文（相对当前目录）
	Image             string  `bson:"image,omitempty"      json:"image,omitempty"`                      // docker_build：产物镜像名（registry 引用模式下为相对仓库名）
	Tag               string  `bson:"tag,omitempty"        json:"tag,omitempty"`                        // docker_build：默认 latest
	Dockerfile        string  `bson:"dockerfile,omitempty" json:"dockerfile,omitempty"`                 // docker_build：默认 ./Dockerfile
	DockerfileContent string  `bson:"dockerfile_content,omitempty" json:"dockerfile_content,omitempty"` // docker_build：内联 Dockerfile 正文（非空优先于路径档；b64 烘焙进脚本现场落文件）
	Registry          string  `bson:"registry,omitempty"   json:"registry,omitempty"`                   // docker_build：目标仓库引用（models.Registry 名；空=无引用）
	RegAddr           string  `bson:"reg_addr,omitempty"   json:"reg_addr,omitempty"`                   // ↓ 三件由引擎快照时烘焙（数据直通：
	RegUser           string  `bson:"reg_user,omitempty"   json:"reg_user,omitempty"`                   //   镜像名与凭证随任务快照走，执行侧只管
	RegPass           string  `bson:"reg_pass,omitempty"   json:"reg_pass,omitempty"`                   //   docker login + build/push；明文姿态与 values 同档）
	ReportGlob        string  `bson:"report_glob,omitempty"   json:"report_glob,omitempty"`             // junit_report：XML 通配
	PassRate          float64 `bson:"pass_rate,omitempty"     json:"pass_rate,omitempty"`               // junit_report：门禁 (0,1]，默认 1.0=100%
	ArtifactGlob      string  `bson:"artifact_glob,omitempty" json:"artifact_glob,omitempty"`           // archive：产物路径 glob（相对工作区）
	ArtifactName      string  `bson:"artifact_name,omitempty" json:"artifact_name,omitempty"`           // archive：产物名（默认 <step>.tgz；下载键 = <wf>-<taskID>-<step>）
}

// 步骤类型（5 种）
const (
	StepShell       = "shell"        // 任意 shell 片段
	StepGit         = "git"          // git clone --depth 1
	StepDockerBuild = "docker_build" // 共享 buildkitd 构建池（mTLS TCP，决断 #15 的 buildkit 演进）构建 + 推镜像仓库（registry 引用/默认仓库/内置本地三档，见 StepSpec.Registry）
	StepJunitReport = "junit_report" // 收集 junit XML + 通过率门禁（门禁判定在引擎侧 AfterRun）
	StepArchive     = "archive"      // T2.4：打包产物 → 上传引擎 GridFS → API 下载（多种归档形态合并为一种）
)

// EffectiveSteps —— 引擎只认 steps：新图纸用 spec.Steps；旧图纸只有 Scripts
// 就地翻译成单个隐式 shell 步骤 "main"（api 翻译层和 engine 驱动脚本共用这一个真相源）。
func (s *FreestyleSpec) EffectiveSteps() []*StepSpec {
	if len(s.Steps) > 0 {
		return s.Steps
	}
	if len(s.Scripts) > 0 {
		return []*StepSpec{{Name: "main", Type: StepShell, Script: strings.Join(s.Scripts, "\n")}}
	}
	return nil
}

// NeedsRunner —— 该 job 的步骤是否需要 runner 宿体镜像（git/docker CLI/curl 是
// busybox 没有的工具——archive 上传依赖 curl，busybox wget --post-file 会截断 POST
// body）。"哪种步骤需要什么执行环境"是步骤域的知识，归 models；
// k8s.BuildJob 只消费判定结果选镜像，不再嗅探步骤类型。
func (s *FreestyleSpec) NeedsRunner() bool {
	if len(s.Hosts) > 0 {
		return true // ssh 客户端（openssh-client）是 busybox 没有的工具，主机引用强制走 runner
	}
	for _, st := range s.EffectiveSteps() {
		if st.Type == StepGit || st.Type == StepDockerBuild || st.Type == StepArchive {
			return true
		}
	}
	return false
}

// WantsDockerDaemon —— 该 job 是否需要共享构建 daemon（docker_build 步骤在共享 buildkitd
// 容器里构建，决断 #15）：BuildJob 据此注入 MP_BUILDKIT_ADDR/客户端证书并选 runner 镜像。
// 判定体与旧 WantsDockerSock 一致——变的只是执行物（宿主 sock → dind mTLS → buildkit mTLS）。
func (s *FreestyleSpec) WantsDockerDaemon() bool {
	for _, st := range s.EffectiveSteps() {
		if st.Type == StepDockerBuild {
			return true
		}
	}
	return false
}

// HasRegistryPrefix —— 镜像名首段含 '.'/':' 或就是 localhost 视为已带仓库前缀
// （"mini-demo" → 裸名；"10.0.0.5:5000/foo"、ACR 全名 → 已带）。裸名判定是
// 推送目标解析的分支依据（engine.BuildTask 快照解析与 step 渲染共用这一个真相源，
// 原在 step/dockerbuild.go，2026-11-07 registry 管理上移）。
func HasRegistryPrefix(image string) bool {
	first := image
	if i := strings.Index(image, "/"); i >= 0 {
		first = image[:i]
	}
	return first == "localhost" || strings.ContainsAny(first, ".:")
}

// Satisfied —— T3.4 审批策略判定（纯函数，原 engine.ApprovalSatisfied 随模块化迁入）：
//
//	any（默认）或空名单：凑够 ≥1 条即过
//	all：Approvers 里每个人都得有一条记录（同名去重看 Approvals）
//
// 这里的"名单为空 + any"就是老图纸的原 release 语义——approve 端点对老图纸
// 退化成 release，两套端点一套语义。
func (me *ManualExec) Satisfied() bool {
	if me == nil || len(me.Approvals) == 0 {
		return false
	}
	if me.Policy == ApprovalPolicyAll {
		got := map[string]bool{}
		for _, a := range me.Approvals {
			got[a.Approver] = true
		}
		for _, name := range me.Approvers {
			if !got[name] {
				return false
			}
		}
		return true
	}
	return true // any / 未设策略 / 空名单：有一条就够
}
