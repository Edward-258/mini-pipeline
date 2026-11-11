#!/usr/bin/env bash
# 构建 pipeline-runner 并推入镜像仓库（本机 Docker 直构直推，runner 变更时重跑）。
# 推送走公网端点，集群 kubelet 拉取走内网/VPC 端点（同仓同名，chart values runner.image）。
# ⚠️ --provenance=false 是 ACR 硬要求（同 build-push.sh：ACR 拒收 BuildKit provenance 清单）。
# 前提：本机已对推送端点 docker login 过一次（凭证存 ~/.docker/config.json，不进仓）。
# Usage: MP_REGISTRY=<registry 公网端点> deploy/build-runner.sh [tag]
#        # 默认 0.3.0，须与 chart values runner.image 的 tag 一致
set -euo pipefail

TAG="${1:-0.3.0}"
REG="${MP_REGISTRY:?usage: MP_REGISTRY=<registry 推送端点> $0 [tag]（决断 #16：端点不进仓）}"
NS="${MP_IMAGE_NS:-codepipeline-learning}"
IMAGE="${REG}/${NS}/pipeline-runner:${TAG}"
repo_root="$(cd "$(dirname "$0")/.." && pwd)"

# docker.io 直拉被掐（SNI 阻断），本地没有的基镜像经 daocloud mirror 拉 + retag（§9.13）
pull_via_mirror() {
  local img="$1"
  if ! docker image inspect "$img" >/dev/null 2>&1; then
    echo "local ${img} missing → pulling via mirror (§9.13)"
    docker pull "docker.m.daocloud.io/library/${img}"
    docker tag "docker.m.daocloud.io/library/${img}" "$img"
  fi
}
pull_via_mirror golang:1.27-alpine   # 0.2.0 工具链搬运源
pull_via_mirror alpine:3.20

docker build --provenance=false -f "$repo_root/docker/runner.Dockerfile" -t "${IMAGE}" "$repo_root"
docker push "${IMAGE}"
echo "pushed: ${IMAGE}（kubelet 拉取走内网端点，仓库路径不变；push 自动建仓）"
