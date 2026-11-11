#!/usr/bin/env bash
# 构建 mini-pipeline-web（React 前端 + nginx）并推入镜像仓库（web 变更时重跑）。
# 与引擎/runner 同形态：推送走公网端点，集群 kubelet 拉取走内网/VPC 端点（chart values web.image）。
# ⚠️ --provenance=false 是 ACR 硬要求（§9.9）。
# 前提：本机已对推送端点 docker login 过一次；基镜像经 mirror 拉（§9.13）。
# Usage: MP_REGISTRY=<registry 公网端点> deploy/build-web.sh [tag]
#        # 默认 0.1.0，须与 chart values web.image 的 tag 一致
set -euo pipefail

TAG="${1:-0.1.0}"
REG="${MP_REGISTRY:?usage: MP_REGISTRY=<registry 推送端点> $0 [tag]（决断 #16：端点不进仓）}"
NS="${MP_IMAGE_NS:-codepipeline-learning}"
IMAGE="${REG}/${NS}/mini-pipeline-web:${TAG}"
repo_root="$(cd "$(dirname "$0")/.." && pwd)"

# docker.io 直拉被掐（SNI 阻断），本地没有的基镜像经 daocloud mirror 拉 + retag
pull_via_mirror() {
  local img="$1"
  if ! docker image inspect "$img" >/dev/null 2>&1; then
    echo "local ${img} missing → pulling via mirror (§9.13)"
    docker pull "docker.m.daocloud.io/library/${img}"
    docker tag "docker.m.daocloud.io/library/${img}" "$img"
  fi
}
pull_via_mirror node:20-alpine
pull_via_mirror nginx:alpine

# 上下文 = 仓库根（与 engine.Dockerfile、runner.Dockerfile 同形态；重货由根 .dockerignore 排除）
docker build --provenance=false -f "$repo_root/docker/web.Dockerfile" -t "${IMAGE}" "$repo_root"
docker push "${IMAGE}"
echo "pushed: ${IMAGE}（kubelet 拉取走内网端点；push 自动建仓 §9.9）"
