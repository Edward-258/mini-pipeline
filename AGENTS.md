# AGENTS.md — mini-pipeline 项目上下文（给下一个会话的你）

> 任何 AI 会话开始于此目录时，先读完本文件再动手。本文以**当前项目状态与实现**为主，历史沿革只保留编号锚点——代码注释与脚本按号引用（`决断 #N`、`§9.N`、`§6 原则N`），**编号永不复用**。

**工作台**：WSL2 Ubuntu-24.04，仓库 `/home/edward/projects/mini-pipeline`（历史以 2026-11-12 单 commit 快照重启，`core.autocrlf=input`）。工具链：go 1.27.1（GOPROXY=goproxy.cn）、helm v3.19.0、kubectl（context `k3s-heyuan`）、jq、Docker Desktop 29.x（本机直构直推）。SSH key `~/.ssh/heyuan81.pem`；Windows 临时目录 `/mnt/c/Temp/opencode`；`deploy/build-*.sh` 是本机构建脚本。Windows 旧副本在 E 盘（仓外存档，勿双向编辑）。

**远端**：`origin` = https://github.com/Edward-258/mini-pipeline.git（public）。推送走仓库 local config 里的 GCM credential.helper，裸 `git push` 即可；gh CLI 已授权。

## 0. 铁律

1. **仓外目录一律只读**（`/mnt/e`、`/mnt/c` 历史目录与 Windows 旧副本）——本仓库是唯一可写真相源，跨机内容以 git 历史为准。
2. **最小可用范围**：单集群、全局信任域，RBAC/身份/多集群/弹性伸缩不在能力面内，代码宁缺毋滥。
3. **集群是临时环境**（master 节点 2026-11-16 到期），别在上面跑任何重要东西。
4. **三节点 /etc/rancher/k3s/registries.yaml 是手工合并的**——改前先 cat 现状再合并，改完滚动重启 k3s（master=`k3s`，worker=`k3s-agent`）。
5. **仓库纪律**：新仓**永不 push tags**（旧史封存于账号下私有仓，地址在 `.git/config` 键 `mini-pipeline.archive-repo`，**只读**——永不推送、永不 fetch 其 ref 进本仓，考古走临时 clone）；commit message 简短低调（如「chore: 收口」），不写长叙事。
6. **全仓零痕迹**：ACR 密码 / mongo 密码 / 私钥不进仓（例外保留：节点 IP、nip.io 域名、ACR 端点与用户名）。
7. **数据面纪律**：registry 记录动数据必走 API（CRUD 即触发 buildkitd reconcile，直改库 = 绕过钩子制造漂移）；图纸改动走 PUT（DELETE 前置会级联删任务档案）。
8. **发版纪律**：执行部署命令前必重读本文件原文（会话摘要滞后一个 rev）；helm v3 不复用上次 --set，所有键显式带全 + 存量集群必带 `mongo.persistence.existingClaim=mongo-data`；镜像 tag 复用前先查占用（ACR 静默覆盖）。

## 1. 项目定位

微型发布工作流引擎（从零实现）：Go + gin + mongo-driver(v1) + client-go 单体服务（module `pipeline`）；MongoDB 三集合 `workflow`（图纸）/ `workflow_task`（施工档案）/ `counter`（发号器）；Job = K8s Job（主控只创建/轮询/收割/清理，不执行脚本）；引擎本体跑在 k3s 里（helm chart），API 经 ingress-nginx 暴露。README.md 是对外能力说明书。

三条不变量：**图纸/实例分离**（run 时整棵定义快照进 task）、**durable 暂停**（落库 + goroutine 退出，跨重启存活）、**Job = K8s Job**。

## 2. 能力全景（当前实现）

### 引擎核心

- run = 发号 → 快照落库 → K8s Job 创建/轮询（2s）/收割/清理；ack 全量写（`_id` 自然键 `<wf>-<taskID>`），中途写 best-effort 自愈、致命写（终态/入队/闸门）走 ackStrict（指数退避 5 次 + 错误上抛，Recover 兜底）；`UpdateTask` MatchedCount==0 → ErrNotFound 守卫（文档被删不静默"成功"）
- runStages 状态机：stage 内 job 并行/串行 + WorstOf 最差者胜（cancelled > failed > pause > running > passed > created > skipped）+ 失败刹车（串行剩余置 skipped / 后续 stage 全 skipped）
- durable 暂停：闸门落库 + goroutine 退出，release/approve 续跑（releaseMu 防双击，同锁）；pause 不进 Recover 扫描（无需看护人的稳态，跨重启存活）
- 任务队列：1s ticker 扫 queued（task_id FIFO）+ CAS 条件更新防双派 + 全局 `MAX_RUNNING_TASKS`（默认 3，0=只收尾）+ 同 wf 串行；pause 不占名额；cron/webhook/release/approve 全走正常入队
- 取消 `DELETE /api/tasks/:name/:id`：活任务 close(chan) 202 / pause·queued·孤儿纯数据 200 / 终态 409；树级级联
- 单 job 重试（`spec.retry` + 化石现场清理 + env `MP_ATTEMPT`）；clone = 快照→新号→全新重跑（GlobalContext 清零）；retry = 树内手术（failed/cancelled/skipped 残留复活、passed 子树不动、Retry 预算归零）
- 超时 `timeout_minutes`（默认 10，重活显式调大）+ 错误策略 `error_policy` stop/retry/ignore（只改控制流，失败照红）
- 启动宣判 Recover（gin 起来前跑）：created/queued/running 孤儿宣判死亡 + 清泄漏 Job；不变量：running 要么有看护人要么是终态

### 变量

- 图纸任意字段写 `{{.ns.key}}`，渲染 = JSON marshal → 字符串替换（SetEscapeHTML(false) 保脚本字符）→ unmarshal；步骤用 `===MP=== OUTPUT k=v` banner 上报 → `<job>.<output>` 点分键进 GlobalContext → 下游 job（含 deploy 镜像字段）引用渲染

### CD

- env：CRUD + 创建/更新时幂等随建 namespace（EnsureNamespace：Terminating 拒绝复用 + managed-by 标签）；**建不删非对称**——删 env 不动 namespace（ClusterRole `pipeline-engine-ns` 仅 get/create）
- deploy job：不起 K8s Job——引擎 client-go StrategicMergePatch 直改目标容器镜像 + 轮询 rollout（observedGeneration 追上 + 副本齐 + 无不可用）；`wait_rollout=false` 专为自举防绞杀
- archive 步骤：tar → 引擎 GridFS（bucket artifacts，键 `<wf>-<taskID>-<step>`，32MB 上限 413）→ API 流式下载
- registry 管理：CRUD + `is_default` 全局唯一（删默认回落"无默认"稳态不选举）；docker_build 推送目标三档：显式 `registry:` 引用 > 裸名跟默认 > 全名直通（**裸名无默认 = 报错点名**）；**地址 `http://` 前缀 = 明文档**，CRUD/引擎启动触发 `SyncBuildkitRegistries` 全量生成 buildkitd.toml + hash 注解滚动构建池（幂等）；凭证每 attempt 现解析，不落任务档案
- host：CRUD + job 级 `spec.hosts` 名字引用，私钥 base64 投影进 Pod（`/tmp` 0600 + `~/.ssh/config` 条目 + `MP_H_<名>_IP/_PORT/_USER` 环境变量），shell 步骤里直接 `ssh <名>`；引用即强制 runner；名字不存在执行时点名报错
- 自举闭环：`deploy/bootstrap-workflow.json` 克隆自己 → go test 门禁 → 构建推 ACR → deploy 打引擎自身；`bootstrap-ensure.sh` 幂等落库 env self + registry acr

### 触发与协作

- `TriggerWorkflow` 统一触发入口（cron/webhook/手动共用"发号→快照→入队"唯一构造序列）；cron：手搓 5 段 crontab 匹配器（坏表达式写入时 400）+ 整分钟对齐调度器（truncate+sleep 不漂移）+ `POST /api/crons/:name/fire` 手动点火
- webhook：GitHub/Gitee push 事件，HMAC-SHA256 constant-time 双轨验签（Gitee 兼明文 token）+ repo 归一（剥 .git/scheme/host）+ branch 匹配；无签名仅当全部配置无 secret 放行
- 原生审批：闸门 stage `approvers` + `policy`（any/all），与 release 同锁同闸门位；老图纸空名单 = 一条审批即过
- IM 通知：notify CRUD + 任务终态 fire-and-forget（1 重试，失败只记日志）；飞书/钉钉双报文双加签；纯数据取消不发（无看护人在场）

### 门面

- 任务列表过滤分页（envelope `{items,total,page,size}`，task_id 降序，size ≤ 100）+ 跨库总账本 `GET /api/tasks`（created_at 降序）+ 批量删除（工作流级联任务档案+定时器；任务只收终态）+ workflow/env PUT 端点（全量替换）
- React 18 + TS strict + antd 5（Vite），独立 nginx Pod（ingress 统一入口，nginx 反代 `/api`、`/healthz`）：画布式编辑器（横 stage 纵 job、最左统一代码源列、JobDrawer live-patch、5 种步骤表单、镜像自动档、JSON 双模逃生舱）、任务三级树 + 分步日志抽屉 + cancel/retry/clone/approve/release、环境/镜像仓库/通知/定时 CRUD
- 质量门：`npm run build`（tsc strict 零错）+ jsdom 真执行冒烟 `frontend/e2e/smoke.mjs`（12 断言）

### CI 步骤体系

- 6 种步骤类型：`shell` / `build`（shell 同语法 + 强制 runner = 工具链声明位）/ `git` / `docker_build` / `junit_report`（通过率门禁，默认 100% 翻红）/ `archive`
- driver：引擎把 steps 编译成 POSIX sh（⚠️ alpine 是 ash，无 local/`[[ ]]`/数组），首行 `exec 2>&1` 单流化 + banner 协议（`===MP=== STEP-START/END` + `OUTPUT k=v`）+ EXIT trap 补发末班 banner——Pod stdout 即步骤状态信道，零新依赖零 RBAC
- 日志：活任务透传 Pod 尾部，终态回档案按 banner 切段（64KB 保尾截断）；引擎每 2s 刮日志幂等重放维护三级树
- 镜像三档：`spec.image` > runner（大满贯：alpine + git/docker-cli/buildx + golang 1.27 + openssh-client，自动档通吃）> busybox；引擎按步骤自动选（纯 shell 落 busybox，RunnerImage 空 = 创建前报错）
- docker_build：共享 buildkitd 池执行（决断 #15）+ Dockerfile 双档（仓库路径 / 内联正文 b64 落盘）+ `--provenance=false` + 成功发 `OUTPUT image_full` 供下游引用

### 工作区与代码源

- 任务级共享 PVC `mp-ws-<wf>-<taskID>`（决断 #17）：同任务全 job 挂同一卷（RWO，容量 `workspace.size` 默认 2Gi），GOCACHE/GOMODCACHE 钉 `/workspace/.mp-cache/`；`MP_WORKSPACE_SHARED=false` 整链回落 emptyDir；清理四面一兜底（finish 钩子 / CancelTask 纯数据 / Recover 宣判 / label 泄漏清扫；pause 不清）；Unschedulable >2min fail-fast 人话报错
- 统一代码源（决断 #18）：画布最左 SourceSpec（repo/branch/token/检出目录）→ BuildTask 前插合成 checkout stage（只进任务快照，图纸保持纯声明）；下游 job 开箱 `$MP_SOURCE_DIR`；git 步骤类型保留（存量零破坏 + 多仓边角），编辑器用户面已退役；校验双侧（后端 400 + 前端红标）

## 3. 环境速查

| 项 | 值 |
|---|---|
| k3s master | 8.155.137.224（内网 172.29.237.251，control-plane+etcd，**2026-11-16 到期**） |
| k3s worker-1 / worker-2 | 8.155.128.91（172.29.237.250）/ 8.155.128.81（172.29.237.249） |
| kubectl context | `k3s-heyuan` |
| MongoDB | ns `pipeline`，ClusterIP（chart 内置）；本机调试 port-forward svc/mongo 27017:27017，密码从 mongo-auth Secret 取 |
| ACR 个人版 | `crpi-28fqgwft325ylccc.cn-heyuan.personal.cr.aliyuncs.com`（push 走公网）+ `-vpc` 变体（集群拉取免公网）；ns `codepipeline-learning` 三仓：mini-pipeline / pipeline-runner / mini-pipeline-web（全部公开匿名可拉）；build 必须 `--provenance=false`；老共享域名 `registry.cn-heyuan.aliyuncs.com` 不属于本实例 |
| buildkit 构建池 | chart `tags.buildkit`：STS buildkit×2（privileged + overlayfs，moby/buildkit:v0.34.2，双 worker hostname 反亲和）+ headless svc 1234（mTLS，证书 Secret `buildkit-tls-certs`）+ buildkitd.toml CM（引擎全量生成，变化滚动）+ 每日 prune CronJob |
| 自部署 registry | registry:2 + UI 由 k3s-gitops（argocd `registry` app）管辖，本仓零副本；UI 入口 `registry-ui.47.113.177.198.nip.io`；registry-data PVC 带 keep 注解 |
| 引擎入口 | `https://pipeline.47.113.177.198.nip.io`（前端 nginx 反代 `/api`、`/healthz`） |
| 安全组 | `sg-f8z8wcoum4myexrjfr40`；80/443/6443/22/3389 + 同组内网互通（30017 已收） |
| 集群存量 | argocd、ingress-nginx（非我们所有，别动）；keepalived HaVip `172.29.237.252` 挂两 worker 的 dummy0（**勿还原回 eth0**——flannel vxlan 源地址过 VPC 反欺骗会丢包） |
| SSH | `ssh -i ~/.ssh/heyuan81.pem root@<节点IP>` 三台全通 |
| aliyun CLI | 已配 AK/SK（profile default），查询显式 `--RegionId cn-heyuan`（够不到集群本身） |

## 4. 架构决断（代码注释按号引用，编号永不复用）

1. **引擎跑在集群内**：InClusterConfig + SA `pipeline-engine`（Role 仅 jobs/pods）；保留 kubeconfig fallback 供本机调试（`K8S_CONTEXT`）。
2. **部署形态 = helm chart**：`deploy/chart/mini-pipeline`，`tags.*` 组件开关 + `connections.*` 参数化；helm v3 upgrade 不复用 --set——所有键显式带全。
3. **内置 registry:2 已退役**（2026-11-10，随决断 #16）：docker_build 推送目标全走 registry 管理记录；kubelet 拉取侧 = 集群管理员职责；历史"localhost:5000 mirror 密封链"语义一并退役。
4. **镜像本机构建直推 ACR**：`deploy/build-push.sh`（--provenance=false 内置；不再 ssh worker 远程构建）。
5. **docker_build 路线①**（fat image 挂 docker.sock）——已被 #15 推翻退役，历史锚。
6. **状态获取轮询 2s，不上 watch**。
7. **runner 不主动回调**：Pod 退出码 = job 结果，日志走 API 拉。
8. "第一版不做队列"已过时——任务队列/并发控制已落地（§2 引擎核心）。
9. **挂 docker.sock 的 Pod ≈ 节点 root**，仅限自有临时集群；0.7.8 起构建池常驻容器化 + mTLS。
10. mongo 密码曾明文入仓（当时知情接受）→ **被 #16 推翻**：install 现场生成（mongo-auth Secret，lookup 复用）。
11. registry 管理权沿革（argocd→本 chart→随 #16 退役）；产品面保留 = is_default 全量制 + 删默认不选举回落"无默认"。
12. **镜像分发走 ACR 个人版独立实例**：push 公网端点 / pull VPC 端点（升 runner tag 不用重编引擎，chart values 注入）；Job Pod 拉取凭证走 `MP_PULL_SECRETS` 名单。
13. **env 建不删非对称**：EnsureNamespace 幂等随建，删除 env 不删 namespace（共享 ns 误删 = 连窝端）；api 层经 `Deps.EnsureNamespace` 钩子注入，nil = 纯登记降级。
14. **host 全局作用域 + 凭证不落任务档案**：名字引用每 attempt 现解析投影；不加新步骤类型（shell 自由编排）。
15. **docker_build = 共享 buildkitd 池**：chart 渲染 STS（privileged + overlayfs rootful，emptyDir /var/lib/buildkit）+ headless svc（per-pod DNS）+ mTLS 证书（chart 现场生成只落集群 Secret，lookup 守卫防轮换）+ prune CronJob；引擎 `fnv32a(wf/taskID) % replicas` 零状态 task 亲和（同 task 重试吃热缓存，跨 task 散开）；runner 只当 buildx remote 客户端（auth 经 SolveRequest 传递）。
16. **registry 外部化 + buildkitd 白名单运行时 reconcile + 全仓零痕迹**（2026-11-10）：`SyncBuildkitRegistries` 全量生成 toml + hash 注解滚动（触发面 = 引擎启动 + registry CRUD 钩子，失败只记日志自愈）；`http://` 前缀 = 明文档进白名单，缺省/https = 公信直通零配置；裸名无默认 = 报错点名；mongo 密码现场生成、ACR 凭证出库、镜像源/域名全部 `--set` 绑定（required 守卫）。
17. **任务级共享工作区 PVC**（2026-11-12）：`mp-ws-<wf>-<taskID>`（k8s/workspace.go，名字确定性免 label 点名删），RWO + WaitForFirstConsumer 调度器原生共置零亲和代码；清理四面一兜底（finish 钩子 / 取消 / Recover / 泄漏清扫）；Unschedulable >2min fail-fast。
18. **统一代码源**（2026-11-12）：clone 是流水线声明不属于任何 job——SourceSpec 窄类型 + `StageTask.Kind="source"` 渲染标记 + BuildTask 合成 checkout stage（**只进快照**，图纸与编辑器 JSON 保持纯声明）；状态机/重试/取消/刹车全免费继承；git 步骤类型执行保留、编辑器退役。
19. **仓库换址 + 历史快照重启**（2026-11-12）：origin = GitHub public（公开姿态知情接受）；旧仓私有封存**只读**；**永不 push tags**；本地 reflog expire + gc 后旧对象清空。
20. **依赖注入收口 + 分层纪律成测试**（2026-11-12）：exec 三 resolver 收进 `Ctx` 函数字段、api 两钩子改 `Deps` 构造注入（nil = 降级语义不变）；`pkg/arch` TestLayering 四规则（go list 包图）钉死依赖方向，行为零变化。

## 5. 常用命令

```bash
# ===== 部署循环（改代码后的标准姿势） =====
MP_REGISTRY=crpi-28fqgwft325ylccc.cn-heyuan.personal.cr.aliyuncs.com deploy/build-push.sh <tag>
# 升级 release：helm v3 所有 --set 显式带全；存量集群 mongo existingClaim 必带（漏带撞 fail-safe 拒绝）
helm upgrade mini-pipeline deploy/chart/mini-pipeline --kube-context k3s-heyuan -n pipeline \
  --set image.repo=crpi-28fqgwft325ylccc-vpc.cn-heyuan.personal.cr.aliyuncs.com/codepipeline-learning/mini-pipeline --set image.tag=<tag> \
  --set runner.image=crpi-28fqgwft325ylccc-vpc.cn-heyuan.personal.cr.aliyuncs.com/codepipeline-learning/pipeline-runner:<tag> \
  --set web.image=crpi-28fqgwft325ylccc-vpc.cn-heyuan.personal.cr.aliyuncs.com/codepipeline-learning/mini-pipeline-web:<tag> \
  --set ingress.host=pipeline.47.113.177.198.nip.io \
  --set tags.engine=true --set tags.mongo=true --set tags.web=true --set tags.buildkit=true \
  --set mongo.persistence.existingClaim=mongo-data --wait

# ===== 全新集群部署前置（helm 之外）：① 镜像源三件 + ingress.host --set 绑定（required 守卫挡漏配）
#  ② ingress-nginx 控制器自装  ③ 可选节点层 registries.yaml（拉取侧=集群管理员职责）

# ===== runner / web 镜像（低频；tag 须同步 chart values / --set） =====
MP_REGISTRY=crpi-28fqgwft325ylccc.cn-heyuan.personal.cr.aliyuncs.com deploy/build-runner.sh <tag>
MP_REGISTRY=crpi-28fqgwft325ylccc.cn-heyuan.personal.cr.aliyuncs.com deploy/build-web.sh <tag>

# ===== 自举图纸落库（凭证走环境变量，不进仓） =====
MP_BASE=https://pipeline.47.113.177.198.nip.io \
MP_ACR_ADDR=crpi-28fqgwft325ylccc.cn-heyuan.personal.cr.aliyuncs.com \
MP_ACR_USER=nick9188231182 MP_ACR_PASS=<密码> bash deploy/bootstrap-ensure.sh

# ===== 验收 =====
go test ./pkg/... -count=1                                   # 11 包单测（repo 类需 port-forward mongo）
(cd frontend && MP_BASE=https://pipeline.47.113.177.198.nip.io npm run smoke)  # jsdom 冒烟（⚠️ 单一引擎纪律 §9.20）

# ===== git =====
git push                                  # GCM 桥已配，裸敲即可
git config --get mini-pipeline.archive-repo   # 旧史存档仓（只读，考古临时 clone）

# ===== 本机调试（引擎跑本机连集群 mongo；单引擎纪律 §9.20） =====
kubectl --context k3s-heyuan port-forward -n pipeline svc/mongo 27017:27017
export MONGO_URI="mongodb://root:<密码>@localhost:27017" K8S_CONTEXT=k3s-heyuan && go run .
# 密码：kubectl --context k3s-heyuan get secret mongo-auth -n pipeline -o jsonpath='{.data.password}' | base64 -d

# ===== 集群 / mongo 直连 =====
kubectl --context k3s-heyuan get pods -n pipeline
kubectl --context k3s-heyuan get jobs -n pipeline
MPW=$(kubectl --context k3s-heyuan -n pipeline get secret mongo-auth -o jsonpath='{.data.password}' | base64 -d)
kubectl exec -i -n pipeline deploy/mongo -- mongosh -u root -p "$MPW" --quiet --eval 'db = db.getSiblingDB("pipeline"); printjson(db.getCollectionNames())'

# ===== 前端迭代 =====
cd frontend && npm run dev     # vite proxy 指集群 API；构建门 npm run build
```

## 6. 设计原则（编号被代码引用）

1. **暂停的实现**：pause 落库 → goroutine return（不占线程）→ release 置位 → RunStages 跳过已 passed 的 stage 续跑。暂停多久都不丢。
2. **资源与数据分离**：K8s 资源是消耗品（结束/暂停时清理），Mongo 记录是档案（跑完不删）。
3. **状态聚合最差者胜**：cancelled(7) > failed(6) > pause(5) > running(4) > passed(3) > created(2) > skipped(1)——running 排 passed 前（没跑完不许宣布成功），skipped 垫底。
4. **ack 全量写**：每前进一步整棵树 ReplaceOne；前提 `_id` 自然键插入前定好（空 `_id` = ObjectId 拿不回 = ack 静默失效）。
5. **变量渲染**：marshal → 字符串替换 → unmarshal，单点在 `pkg/engine/vars.go`。
6. **启动宣判，不复活**：重启后遗留 running 宣判死亡 + 清泄漏 Job，用户重跑即可；pause 例外（durable 稳态，跨重启存活）。
7. **构建 job 的真身 = freestyle + 步骤列表**：步骤在 Pod 内由驱动脚本顺序执行（失败且非 OnFailure 跳过剩余），状态走日志 banner 信道，"分步分界面"的数据源就是三级状态树。
8. **日志流 = 状态信道**：banner 协议 + 引擎幂等重放；护栏三件套（单流化 / say() 前置换行 / EXIT trap）；顺序铁律 **Run → SaveInfo → Clean**（先收割再销毁）。

## 7. 模块地图（写对应模块前先读现有实现与单测——不变量钉在测试里）

| 主题 | 路径 |
|---|---|
| 领域模型 / 状态机 / 校验 | `pkg/models/`（status.go WorstOf · workflow.go StepSpec · validate.go 保存门） |
| 驱动脚本 + banner 协议 | `pkg/step/driver.go` · `pkg/step/logs.go`（回归钉在 driver_roundtrip_test.go / logs_test.go） |
| K8s 执行物 / Job 形状 | `pkg/k8s/build.go`（BuildJob 镜像三档）· `pkg/engine/exec/`（Run→SaveInfo→Clean） |
| 变量渲染 | `pkg/engine/vars.go` |
| chart 形态 | `deploy/chart/mini-pipeline/`（tags.* + required 守卫） |
| 分层纪律 | `pkg/arch/`（TestLayering 四规则） |

代码结构：依赖全部单向向下无环——`config/`(leaf) → `models/`(leaf 领域模型+纯逻辑 ValidateWorkflow/CronMatch/NeedsRunner) → `repo/`(mongo+GridFS，集合句柄包内私有) → `step/`(driver/hosts/StepCtl 一Ctl一文件/logs) → `k8s/`(client/build/buildkit/workspace/log/util) → `engine/`(run/stages/jobs/finish/task/trigger/ops/gate/queue/recover/vars + 子包 cancel/exec/notify/cron) → `webhook/`(签名/匹配纯逻辑) → `api/`(纯 HTTP 形状翻译，外部副作用经 Deps 注入) → `main.go`(仅装配)。

## 8. 当前部署态势（2026-11-13）

- helm release **rev56**：engine **0.7.16**（编译构建 build 步骤类型——shell 同语法 + NeedsRunner 强制 runner）+ web **0.1.16** + runner **0.3.0** + chart **0.7.15**；upgrade 命令 = §5
- registry 集合稳态：`acr`（裸域名 HTTPS 档）+ `local`（`http://` svc DNS 明文档，**is_default**——裸名图纸推自部署 registry）
- **待办（设计定稿，未开工）**：buildkit 缓存改造——replicas=2 不动、emptyDir → volumeClaimTemplates（每副本独立 PVC 抗滚动清零）、fnv 亲和输入 `<wf>/<taskID>` → `<wf>`（同 wf 恒同台吃热缓存，跨 wf 散开保产能）；⚠️ 实施雷：STS volumeClaimTemplates 不可变，需先 `kubectl delete sts buildkit -n pipeline --cascade=orphan` 再 helm upgrade
- 版本线（近几代）：rev55 = 0.7.15 + 0.1.15（bootstrap 陷阱修复 + 编辑器 git 步骤退役）；rev54 = 0.7.14（共享工作区 + 统一代码源）；rev53/52 = web 镜像名拆分 / 前端性能三连修；rev51 = 事故 rev（values 照抄旧值）已过
- 遗留清理项：ACR 测试镜像 bk-plain-probe / bk-live-probe / scratch-bk-img（验证残留，可忽略）；bootstrap 图纸 toolchain 步已补 docker-cli-buildx

## 9. 案例存档 / 已知坑

> 编号是全仓引用锚（代码与脚本按号引用），已清档的不复位，空号 = 已清。每条只留仍生效的结论。

2. **keepalived 底稿未进 gitops**——节点层配置单点活在两台机器的 dummy0 上（勿还原回 eth0，见 §3）。
3. **Windows PS5.1 四坑**（中文 .ps1 编码 / curl JSON `-d @file` / 远程 `sed '$d'` 转义 / SSH 内层引号必被剥——传配置一律 base64）；**WSL ssh 偶发僵尸**（scp 已落地但命令卡起跑线，杀掉重跑即好）；`pkill -f 'sshd.*notty'` 会把自己这条无终端会话一起杀掉。
6. `flannel-backend` 是 k3s **server 专属**参数，agent 配了起不来（flag not defined）。
7. **手拼 JSON 必数错括号**——一律 python json.dumps 或 `jq -n` / 落盘 `jq -e .` 预检；批量文本操作后必 `git diff` 自检。
9. **ACR 个人版三坑**：老共享域名对独立实例一律 Forbidden Host（端点活着但不是它的家）；ACR 拒收 BuildKit provenance/attestation 附加清单——`--provenance=false` 即解；认证是 Bearer token 流（`www-authenticate` 挑战里的 service 参数不是仓库域名；返回字段叫 `access_token` 非 `token`）；helm chart 存不进免费版（EE 能力）。
13. **本机 Docker Desktop 拉不动 docker.io**（SNI 阻断，TLS 握手即掐）——经 mirror 拉基镜像再 retag，构建走本地缓存。
15. **jq 查含点字面键**要用 `["a.b"]`——GlobalContext 收割键 `<job>.<output>` 是字面含点，`.a.b` 会当两层路径拿 null。
20. **双引擎抢同一队列**：本机 `go run` 引擎与集群引擎共用 mongo 抢 queued 队列，断言时序全乱——本机引擎只作调试，一切活体验收打集群 ingress；确需独跑先 `kubectl scale deploy/mini-pipeline --replicas=0`。
22. **npm 断网残局**：失败 install 留**空壳 @types 目录**（目录在文件空，tsc 报 TS2688）——`rm -rf node_modules/@types/*` 后重装。
23. **argocd 反向接管三教训**：正确序 = 摘 finalizer（冻结窗口内）→ 删 Application → 再动 git，反了就是 prune 扫射（陈旧 git 缓存的 selfHeal 窗口是雷）；PVC `helm.sh/resource-policy: keep` 是保命符；ingress 域名端点对 Pod 内 docker 不可推（TLS 层失败不回退 HTTP，选明文端口）；`/root/.docker` hostPath 只读——login 走 `DOCKER_CONFIG=/tmp/.docker` 子 shell。
24. **共享库回环测试"库里只有我"必炸**——集成测试连的库是活的，快照/恢复别人的状态，断言收敛到自己创建的数据。
25. **官方镜像智能 entrypoint 抢参数注入**——显式以子命令为首参（如 `command: ["dockerd-entrypoint.sh", "dockerd"]`）；helm upgrade --wait 超时中断后 STS 模板已更新但老 Pod 未滚动，`kubectl delete pod` 强制重建。
26. **go test 包级并行 + 共享活库互相串台**——跨包集成测试的隔离键（creator/工作流名）必须包级唯一；"恰 N 条"型断言对并行兄弟包的种子永远开放。
27. **蓝图步骤字段是扁平的**——嵌套 `spec.script` 被 json 静默吞 → driver 空函数体在 ash 是语法错 → exit 2 零输出（连首行 exec 2>&1 都没跑，唯一看不见 stderr 的失败形态）；手写蓝图先 `jq` 对照 bootstrap-workflow.json。
28. **helm --wait 超时标 failed ≠ 部署失败**——资源已铺上，原命令重跑即收敛；带大镜像新组件的 upgrade 预留 `--timeout`。
29. **版本 tag 复用 = 静默覆盖**（发版前先对一眼上代 `helm get values --revision <n>`）；**照抄会话摘要旧命令 = 模板漂移**——部署命令执行前必重读本文件原文。
