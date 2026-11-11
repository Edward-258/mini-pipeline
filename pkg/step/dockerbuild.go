package step

import (
	"encoding/base64"
	"fmt"
	"strings"

	"pipeline/pkg/models"
)

// docker_build 步骤：打共享 buildkitd 构建池（mTLS TCP 1234，决断 #15 的 buildkit 演进）
// 构建 + 推镜像仓库。脚本本体零地址耦合——buildx 的 remote driver 读 Pod env
// （MP_BUILDKIT_ADDR/MP_BUILDKIT_CERTS，build.go 注入）握手远端 buildkitd，构建/推送全在
// 那边执行（--push 让 buildkitd 直推仓库，产物不回 Pod）。
// 推送目标三档（解析在 engine/exec/registry.go，烘焙成 RegAddr/RegUser/RegPass 副本）：
//   RegAddr != "" → 仓库引用档：全名 NormalizeRegistryAddr(RegAddr)/Image:tag，有凭证先 docker login
//   全名带前缀    → 直通匿名推（HTTPS 公信端点零配置；明文端点须已登记 → 引擎 reconcile 白名单）
//   裸名          → 决断 #16 起报错（内置 registry 退役，无默认记录无家可归——exec 层已挡，
//                    这里防御兜底）。$MP_NODE_REGISTRY/$MP_NODE_IP 随内置仓库退役。
// 成功构建后发 `===MP=== OUTPUT image_full=<全名>` banner（引擎收割成 <job>.image_full
// 进 GlobalContext）——下游 deploy job 的镜像字段可引 {{.<job>.image_full}} 做构建后即部署
// （bootstrap 自举图纸靠它去硬编码域名）。
// Dockerfile 两档（0.7.10 起）：内联正文（DockerfileContent，前端终端编辑面/本地导入）
//   优先于仓库内路径档——内容是数据不是脚本（单引号/换行/$ 什么都可能有），b64 烘焙进
//   脚本、构建前现场落文件，绕开 shell 引号协议；文件名固定（同 job 多个内联构建步骤
//   顺序执行，写完即建互不干扰）。

// inlineDockerfile —— 内联内容的现场落盘名（构建上下文内隐藏文件，-f 直引）
const inlineDockerfile = ".mp-inline-Dockerfile"

// buildxBuilder —— remote builder 实例名（Pod 一次性，rm+create 幂等重建即可，无共享状态）
const buildxBuilder = "mp"

// buildxHelper —— 构建片段内联的 shell 助手（POSIX sh 函数；同 job 多个构建步骤重复定义
// 同体，无害）：幂等重建 remote builder 再由调用方 && 进 buildx build。
// 证书自适应：MP_BUILDKIT_CERTS 非空 = chart 池（mTLS，driver-opt 三证书）；空 = 外部
// buildkitd 直连档（BUILDKIT_ADDR，自管安全，buildx 裸连）。
// ⚠️ ACR provenance 防线在这里：--provenance=false（§9.9）——ACR 拒收 BuildKit 默认生成
// 的 provenance attestation 清单，dind 时代的 DOCKER_BUILDKIT=0 随 classic builder 退役。
const buildxHelper = `mp_bk() {
  docker buildx rm ` + buildxBuilder + ` >/dev/null 2>&1
  if [ -n "$MP_BUILDKIT_CERTS" ]; then
    docker buildx create --name ` + buildxBuilder + ` --driver remote --driver-opt "cacert=$MP_BUILDKIT_CERTS/ca.pem,cert=$MP_BUILDKIT_CERTS/cert.pem,key=$MP_BUILDKIT_CERTS/key.pem" "$MP_BUILDKIT_ADDR" >/dev/null
  else
    docker buildx create --name ` + buildxBuilder + ` --driver remote "$MP_BUILDKIT_ADDR" >/dev/null
  fi
}
`

// outputImageFull —— 构建成功后的产出宣告 banner（&& 链尾：失败不宣告）。
// 收割键 = <job>.image_full（点分），下游 job 变量渲染消费。
func outputImageFull(full string) string {
	return " && echo " + shQuote("===MP=== OUTPUT image_full="+full)
}

type dockerBuildCtl struct{ spec *models.StepSpec }

func (c *dockerBuildCtl) PreRun() (string, error) {
	if c.spec.Image == "" {
		return "", fmt.Errorf("docker_build step needs image")
	}
	tag := c.spec.Tag
	if tag == "" {
		tag = "latest"
	}
	df := c.spec.Dockerfile
	if df == "" {
		df = "Dockerfile"
	}
	materialize := ""
	if c.spec.DockerfileContent != "" {
		df = inlineDockerfile
		materialize = fmt.Sprintf("printf '%%s' '%s' | base64 -d > '%s' && ",
			base64.StdEncoding.EncodeToString([]byte(c.spec.DockerfileContent)), df)
	}
	ctx := c.spec.ContextDir
	if ctx == "" {
		ctx = "."
	}

	if c.spec.RegAddr != "" {
		// 仓库引用档：镜像名 = addr/相对名:tag——引用模式下 image 就是仓库相对路径
		// （全名在任务侧烘焙拼好，执行侧只管 build/push）。
		// addr 经 NormalizeRegistryAddr 剥传输档 scheme（决断 #16：http:// 前缀是声明不是
		// 地址的一部分，docker 引用不收 scheme）
		addr := models.NormalizeRegistryAddr(c.spec.RegAddr)
		full := fmt.Sprintf("%s/%s:%s", addr, c.spec.Image, tag)
		cmd := fmt.Sprintf("mp_bk && docker buildx build --builder %s --provenance=false --push -f %s -t %s %s%s",
			buildxBuilder, shQuote(df), shQuote(full), shQuote(ctx), outputImageFull(full))
		if c.spec.RegUser != "" {
			// 凭证走 stdin（-p 会进进程列表）+ DOCKER_CONFIG 指到 /tmp：凭证只活在 Pod 本地
			// 临时目录（不碰镜像默认 /root/.docker）。login 后的 config.json 正是 buildx 收 auth
			// 的来源（buildx 与 docker CLI 同读 DOCKER_CONFIG，remote driver 把 CLI 侧凭证随
			// SolveRequest 传给 buildkitd——所以 create/build 必须圈在同一个子 shell 里）。
			// 子 shell 包裹，export 不外泄到同 shell 的后续步骤。值经 shQuote 转义（凭证里
			// 什么字符都可能有）
			cmd = fmt.Sprintf("( export DOCKER_CONFIG=/tmp/.docker && printf '%%s' %s | docker login -u %s --password-stdin %s && %s )",
				shQuote(c.spec.RegPass), shQuote(c.spec.RegUser), shQuote(addr), cmd)
		}
		return buildxHelper + materialize + cmd, nil
	}

	// 全名直通档（决断 #16：内置本地档退役——裸名在 exec 解析层已报错，这里防御兜底）
	if !models.HasRegistryPrefix(c.spec.Image) {
		return "", fmt.Errorf("docker_build step %q: bare image name %q needs a registered default registry (no built-in registry since decision #16)", c.spec.Name, c.spec.Image)
	}
	full := c.spec.Image + ":" + tag
	return buildxHelper + materialize + fmt.Sprintf("mp_bk && docker buildx build --builder %s --provenance=false --push -f %s -t %s %s%s",
		buildxBuilder, shQuote(df), shQuote(full), shQuote(ctx), outputImageFull(full)), nil
}
func (c *dockerBuildCtl) AfterRun(*models.StepTask) error { return nil }

// shQuote —— POSIX 单引号安全包装（值内的 ' 写成 '\”）：凭证字段（password 里什么字符都可能有）
// 不转义会掀翻驱动脚本。仓库地址/用户名同待遇。
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
