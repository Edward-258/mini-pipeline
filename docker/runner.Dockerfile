# pipeline-runner 0.3 —— 单一全能 runner（2026-11-06 用户拍板：自动档通吃，免"现场选镜像"）。
# 镜像哲学：一个自带完整工具链（go/docker cli/buildx/openssh）的全能 runner，而不是
# 按步骤拼一组系统镜像——0.1.x 的"刻意瘦"在 bootstrap 上撞了墙（build job 被迫
# 显式指定 golang:1.27-alpine），从此转向自动档通吃。
# 工具链从官方 golang 镜像整目录搬运（版本与 bootstrap 工作流对齐），GOPROXY 烘焙进镜像。
# 分发：ACR codepipeline-learning/pipeline-runner（deploy/build-runner.sh 本机直推公网
# 端点，集群 kubelet 经 VPC 端点拉取）。
# 0.2.1：+openssh-client——spec.hosts 远程主机的 ssh 客户端（引擎投影私钥进 Pod，
# 用户 shell 步骤直接 `ssh <名>`；NeedsRunner 对 hosts>0 强制走 runner）。
# 0.3.0：+buildx CLI 插件（docker/buildx v0.38.2）——构建池迁 buildkit（决断 #15 演进）：
# `docker buildx build --push` 经 remote driver 打共享 buildkitd（mTLS），classic
# docker build/docker login 保留（login 写的 config.json 正是 buildx 收 auth 的来源）。
FROM golang:1.27-alpine AS toolchain

FROM alpine:3.20
RUN sed -i 's#dl-cdn.alpinelinux.org#mirrors.aliyun.com#g' /etc/apk/repositories \
    && apk add --no-cache git docker-cli curl ca-certificates make jq openssh-client
# buildx 插件进 docker CLI 约定路径（/usr/local/lib/docker/cli-plugins，docker buildx 即可用）。
# 二进制来自 GitHub release 直链（与 gh CLI 同链路）；curl -f 让 404/截断当场翻红而不是
# 静默装进一个坏插件。版本与集群 buildkitd（chart values buildkit.image）无须严格配对。
ARG BUILDX_VERSION=v0.38.2
RUN mkdir -p /usr/local/lib/docker/cli-plugins \
    && curl -fsSL -o /usr/local/lib/docker/cli-plugins/docker-buildx \
       "https://github.com/docker/buildx/releases/download/${BUILDX_VERSION}/buildx-${BUILDX_VERSION}.linux-amd64" \
    && chmod +x /usr/local/lib/docker/cli-plugins/docker-buildx \
    && docker buildx version
COPY --from=toolchain /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:/go/bin:$PATH \
    GOPATH=/go \
    GOPROXY=https://goproxy.cn,direct
WORKDIR /workspace
