#!/usr/bin/env bash
# 自举图纸落库（T2.5）：建 env self（引擎自身 namespace，deploy job 的自举靶标）+
# 【可选】registry acr 记录 + 建 bootstrap 工作流。⭐ 只落库，不触发执行——执行是
# 【需人工】动作（见尾部清单）。
# 凭证零痕迹（决断 #16）：registry 记录的地址/凭证全走环境变量，本脚本与仓库里
# 不含任何真实端点或密码——凭证活在 mongo，不进 git。
# Usage:
#   MP_BASE=http://localhost:8080 \
#   MP_ACR_ADDR=<registry 推送端点> MP_ACR_USER=<用户名> MP_ACR_PASS=<密码> \
#     bash deploy/bootstrap-ensure.sh
#   （MP_ACR_ADDR 不给 = 只建 env self + 图纸；docker_build 的 registry: acr 引用会
#    在执行时点名报错，先去 API/前端登记或补环境变量重跑）
set -euo pipefail

BASE="${MP_BASE:-http://localhost:8080}"
DIR="$(cd "$(dirname "$0")" && pwd)"

# 1) env self：deploy job 解析 "self" → 引擎所在 namespace（Role 也在这个 ns，权限边界一致）
code=$(curl -sk -o /tmp/env-resp.json -w '%{http_code}' -X POST "$BASE/api/envs" \
  -H 'Content-Type: application/json' \
  -d '{"name":"self","namespace":"pipeline","desc":"引擎自身所在 namespace（自举部署靶标）"}')
[ "$code" = "200" ] || [ "$code" = "409" ] || { echo "❌ ensure env self failed ($code): $(cat /tmp/env-resp.json)"; exit 1; }
echo "✅ env self 就绪 ($code)"

# 1.5) registry acr（可选）：docker_build 的推送目标。地址/凭证全部来自环境变量
# （jq -n 安全构 JSON——手拼必数错括号，§9.7）；MP_ACR_USER 留空 = 免认证仓库（内网裸仓）
if [ -n "${MP_ACR_ADDR:-}" ]; then
  payload=$(jq -n --arg addr "$MP_ACR_ADDR" --arg user "${MP_ACR_USER:-}" --arg pass "${MP_ACR_PASS:-}" \
    '{name:"acr",address:$addr,username:$user,password:$pass,desc:"bootstrap 自举镜像推送目标（凭证经环境变量录入，活库不进仓）"}')
  code=$(curl -sk -o /tmp/reg-resp.json -w '%{http_code}' -X POST "$BASE/api/registries" \
    -H 'Content-Type: application/json' -d "$payload")
  [ "$code" = "200" ] || [ "$code" = "409" ] || { echo "❌ ensure registry acr failed ($code): $(cat /tmp/reg-resp.json)"; exit 1; }
  echo "✅ registry acr 就绪 ($code)"
else
  echo "ℹ️  MP_ACR_ADDR 未设置——跳过 registry acr 登记（bootstrap 的 registry: acr 引用执行时会点名报错）"
fi

# 2) bootstrap 工作流（先删后建，图纸改了重跑本脚本即生效）
curl -sk -X DELETE "$BASE/api/workflows/bootstrap" >/dev/null || true
code=$(curl -sk -o /tmp/wf-resp.json -w '%{http_code}' -X POST "$BASE/api/workflows" \
  -H 'Content-Type: application/json' -d @"$DIR/bootstrap-workflow.json")
[ "$code" = "200" ] || { echo "❌ ensure workflow bootstrap failed ($code): $(cat /tmp/wf-resp.json)"; exit 1; }
echo "✅ bootstrap 图纸已落库"

echo ""
echo "执行前置清单（全部【需人工】）:"
echo "  ① registry acr 已登记（MP_ACR_ADDR 或 API/前端）——构建推送凭证活库不进仓（决断 #16）"
echo "  ② chart Role 扩展已活体生效（role.yaml 的 helm upgrade，含 apps 组 patch + configmaps）"
echo "  ③ 之后：curl -sk -X POST $BASE/api/workflows/bootstrap/run"
echo "  ⚠️ selfdeploy 的 wait_rollout=false 是自陷保护：patch 即成功，不等 rollout（防新 Pod 绞杀正在跑的自己）"
echo "  ℹ️ deploy 靶标镜像 = {{.build-ci.image_full}}（上游 docker_build 的 OUTPUT 宣告，决断 #16 起不再硬编码域名）"
