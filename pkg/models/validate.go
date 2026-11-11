package models

import (
	"fmt"
	"regexp"
	"strings"
)

// 图纸校验。领域规则归领域模型：
// api 层只做形状绑定（ShouldBindJSON），这里的错一律 400。

// nameProtocolSafe —— 名字里的单引号会打断 driver 脚本的 mp_run '<name>' 单引号协议
// （step 名直插 POSIX 脚本；stage/job 名统一约束防呆）。空格是允许的——END banner
// 的解析器按"从右往左剥"专门为含空格的名字设计（step/logs.go）。
func nameProtocolSafe(s string) bool {
	return !strings.Contains(s, "'")
}

// imageRefRe —— docker_build 产物镜像名字符集（决断 #16 后 image = 命名空间/仓库名相对路径，
// registry 地址由推送目标解析拼；首段带 . 或 :端口 = 全名直通档，HasRegistryPrefix 同语义）。
// 大写/空格/中文会一路活到 buildkit 才炸 invalid reference format——拦在保存前。
// 含 {{ 的变量模板跳过（renderVars 渲染后才有真值）。
var imageRefRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(:[0-9]+)?(/[a-z0-9][a-z0-9._-]*)*$`)

// imageTagRe —— docker tag 规范：字母/数字/_ 开头，全长 ≤128
var imageTagRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)

// ValidateWorkflow —— 图纸写入（POST/PUT）前的完整性校验：
// 名字/stage/job 结构齐全、job 类型白名单（空 type 视作 freestyle 兼容历史图纸）、
// deploy/freestyle 各自的 spec 必备字段、错误策略白名单、步骤类型白名单。
func ValidateWorkflow(wf *Workflow) error {
	if wf.Name == "" {
		return fmt.Errorf("workflow name is required")
	}
	if len(wf.Stages) == 0 {
		return fmt.Errorf("at least one stage is required")
	}
	// 统一代码源：Repo 是唯一硬前提，branch/dir 留空有派生语义（gitCtl/引擎合成）。
	// token 形状不校验（与 git step 同档——校验层只查形状不查存在性）
	if wf.Source != nil && wf.Source.Repo == "" {
		return fmt.Errorf("source: repo is required when code source is declared")
	}
	for i, st := range wf.Stages {
		if st.Name == "" {
			return fmt.Errorf("stage[%d]: name is required", i)
		}
		if !nameProtocolSafe(st.Name) {
			return fmt.Errorf("stage[%d]: name must not contain single quotes", i)
		}
		if len(st.Jobs) == 0 {
			return fmt.Errorf("stage[%d]: at least one job is required", i)
		}
		for j, jb := range st.Jobs {
			if jb.Name == "" {
				return fmt.Errorf("stage[%d].job[%d]: name is required", i, j)
			}
			if !nameProtocolSafe(jb.Name) {
				return fmt.Errorf("stage[%d].job[%d]: name must not contain single quotes", i, j)
			}
			if jb.Name == "global" {
				// 变量命名空间保留字：收割键是 "<job>.<output>"（job 名不含点是前提），
				// job 叫 global 会把自己的输出顶进 {{.global.*}} 污染全局变量
				return fmt.Errorf("stage[%d].job[%d]: job name \"global\" is reserved (variable namespace)", i, j)
			}
			switch jb.JobType {
			case JobTypeFreestyle: // 兼容历史图纸：空 type 视作 freestyle
			case JobTypeDeploy:
			case "":
				jb.JobType = JobTypeFreestyle
			default:
				return fmt.Errorf("stage[%d].job[%d]: only freestyle|deploy is supported, got %q", i, j, jb.JobType)
			}
			// T2.2：deploy job 必须带 DeploySpec；freestyle 必须带 Spec（历史图纸走默认分支已归位）
			if jb.JobType == JobTypeDeploy {
				if jb.DeploySpec == nil {
					return fmt.Errorf("stage[%d].job[%d]: deploy job requires deploy_spec", i, j)
				}
				if jb.DeploySpec.Env == "" || jb.DeploySpec.DeploymentName == "" || jb.DeploySpec.ContainerName == "" || jb.DeploySpec.Image == "" {
					return fmt.Errorf("stage[%d].job[%d]: deploy_spec requires env/deployment_name/container_name/image", i, j)
				}
				continue
			}
			if jb.Spec == nil {
				return fmt.Errorf("stage[%d].job[%d]: spec is required", i, j)
			}
			// CI/CD 起 image 可空：引擎按步骤需要选默认（git/docker_build→mini-runner，纯 shell→busybox）。
			// steps/scripts 至少其一——引擎只认 steps，scripts 会翻译成隐式 step "main"（EffectiveSteps）
			if len(jb.Spec.Steps) == 0 && len(jb.Spec.Scripts) == 0 {
				return fmt.Errorf("stage[%d].job[%d]: spec.steps or spec.scripts is required", i, j)
			}
			// T1.9：错误策略白名单（拼错的 "ignor" 悄悄变 stop 比报错更危险）
			switch jb.Spec.ErrorPolicy {
			case "", ErrorPolicyStop, ErrorPolicyRetry, ErrorPolicyIgnore:
			default:
				return fmt.Errorf("stage[%d].job[%d]: unknown error_policy %q (want stop|retry|ignore)", i, j, jb.Spec.ErrorPolicy)
			}
			// 2026-11-08：远程主机引用形状校验（存在性在执行时由 resolveHosts 把关——
			// 校验层摸不到 repo，只能查形状；同 registry 引用的分层）
			for _, hn := range jb.Spec.Hosts {
				if !regNameRe.MatchString(hn) {
					return fmt.Errorf("stage[%d].job[%d]: host %q must match [a-z0-9][a-z0-9-]{0,62}", i, j, hn)
				}
			}
			for _, ss := range jb.Spec.Steps {
				if ss.Name == "" {
					return fmt.Errorf("stage[%d].job[%d]: step name is required", i, j)
				}
				if !nameProtocolSafe(ss.Name) {
					return fmt.Errorf("stage[%d].job[%d]: step %q name must not contain single quotes", i, j, ss.Name)
				}
				switch ss.Type {
				case StepShell, StepGit, StepDockerBuild, StepJunitReport, StepArchive:
				default:
					return fmt.Errorf("stage[%d].job[%d]: unknown step type %q", i, j, ss.Type)
				}
				// 2026-11-07：docker_build 的目标仓库引用形状校验（存在性在触发/克隆时由
				// BuildTask 烘焙解析把关——校验层摸不到 repo，只能查形状）
				if ss.Type == StepDockerBuild && ss.Registry != "" && !regNameRe.MatchString(ss.Registry) {
					return fmt.Errorf("stage[%d].job[%d]: step %q registry must match [a-z0-9][a-z0-9-]{0,62}", i, j, ss.Name)
				}
				// 2026-11-11：镜像名/tag 字符集（前端 collectErrors 同规则红标；这里是保存终门）
				if ss.Type == StepDockerBuild {
					if ss.Image != "" && !strings.Contains(ss.Image, "{{") && !imageRefRe.MatchString(ss.Image) {
						return fmt.Errorf("stage[%d].job[%d]: step %q image %q must be lowercase path segments ([a-z0-9._-], / separated) or a {{var}} template", i, j, ss.Name, ss.Image)
					}
					if ss.Tag != "" && !strings.Contains(ss.Tag, "{{") && !imageTagRe.MatchString(ss.Tag) {
						return fmt.Errorf("stage[%d].job[%d]: step %q tag %q must match [A-Za-z0-9_][A-Za-z0-9._-]{0,127} or be a {{var}} template", i, j, ss.Name, ss.Tag)
					}
				}
			}
		}
	}
	return nil
}
