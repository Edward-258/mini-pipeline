#!/usr/bin/env bash
# 引擎镜像构建+推送（2026-11-04 起本机 Docker Desktop 直构直推，ssh-worker 链路退役）。
# ⚠️ --provenance=false 是 ACR 硬要求：ACR 拒收 BuildKit 默认的 provenance/attestation 清单
#    （报 unknown manifest class for application/vnd.oci.empty.v1+json）。
# 前提：本机已对推送端点 docker login 过一次（凭证存本机 ~/.docker/config.json，不进仓——
#       决断 #16 零痕迹：仓库端点也是环境绑定，MP_REGISTRY 显式给）。
# Usage: MP_REGISTRY=<registry 公网端点> deploy/build-push.sh <tag>
#   e.g. MP_REGISTRY=registry.example.com deploy/build-push.sh 0.7.13
set -euo pipefail

Tag="${1:?usage: $0 <tag>}"
REG="${MP_REGISTRY:?usage: MP_REGISTRY=<registry 推送端点> $0 <tag>（决断 #16：端点不进仓）}"
NS="${MP_IMAGE_NS:-codepipeline-learning}"
repo_root="$(cd "$(dirname "$0")/.." && pwd)"

docker build --provenance=false -f "$repo_root/docker/engine.Dockerfile" -t "${REG}/${NS}/mini-pipeline:${Tag}" "$repo_root"
docker push "${REG}/${NS}/mini-pipeline:${Tag}"

echo
echo "pushed: ${REG}/${NS}/mini-pipeline:${Tag}"
echo "next:   helm upgrade mini-pipeline deploy/chart/mini-pipeline -n pipeline \\"
echo "          --set image.repo=<registry VPC/内网端点>/codepipeline-learning --set image.tag=${Tag} \\"
echo "          --set runner.image=<同上>/codepipeline-learning/pipeline-runner:<rtag> \\"
echo "          --set web.image=<同上>/codepipeline-learning/mini-pipeline-web:<wtag> \\"
echo "          --set ingress.host=<域名> --set tags.engine=true --wait"
