# AGENTS.md — mini-pipeline 项目上下文（给下一个会话的你）

> **工作台（2026-11-02 起迁移）**：WSL2 Ubuntu-24.04，本仓库位于 `/home/edward/projects/mini-pipeline`（2026-11-12 起历史以单 commit 快照重启，`core.autocrlf=input`）。工具链：go 1.27.1（/usr/local/go，GOPROXY=goproxy.cn，2026-11-05 校对）、helm v3.19.0（~/.local/bin，2026-11-05 校对；⚠️ v3 的 upgrade **不复用**上次 --set，部署时所有 --set 必须显式带全）、kubectl（context `k3s-heyuan` 已配通）、jq、gcc、git（身份 Edward-258）、**Docker Desktop 29.x（2026-11-04 起，本机直构直推镜像）**。路径映射：SSH key → `~/.ssh/heyuan81.pem`（/mnt/e 原件 drvfs 权限 ssh 不认，已拷贝 + chmod 600）；Windows 临时目录 → `/mnt/c/Temp/opencode`。`deploy/build-push.sh`/`build-runner.sh`/`build-web.sh` 是本机构建脚本（docker build + push ACR，不再 ssh worker）。Windows 旧副本在 E 盘（仓外存档，勿双向编辑）。

> **远端（2026-11-12 换址）**：`origin` = https://github.com/Edward-258/mini-pipeline.git（**public**——决断 #10 的公开姿态平移到新仓，明文密码暴露风险知情接受）。本仓历史 2026-11-12 以单 commit 快照重启（决断 #19）；完整旧史存档于账号下**私有仓**（地址在 `.git/config` 自定义键 `mini-pipeline.archive-repo`，**只读**：永不推送、永不 fetch 其 ref 进本仓，考古走临时 clone）。推送走 Windows GCM 桥——credential.helper 已写进本仓库 local config（指向 `/mnt/c/Program Files/Git/mingw64/bin/git-credential-manager.exe`），裸 `git push` 即可；gh CLI 2.103.3 已装 `~/.local/bin` 并经 GCM token 授权。

> 任何 AI 会话开始于此目录时，先读完本文件再动手。内容按"必读 → 速查 → 参考"排序。

## 0. 铁律

1. **仓外目录一律只读**：`/mnt/e`、`/mnt/c` 下的历史工作目录与存档只读不改（含 Windows 旧副本）——本仓库是唯一可写真相源，跨机内容以 git 历史为准。
2. 本项目按**最小可用范围**实现，不是产品：RBAC/身份系统/多集群/弹性伸缩不在能力面内，代码宁缺毋滥（审批/IM 通知/webhook/cron 已按最小形态落地，能力全景见 §2）。
3. 集群是临时环境（master 节点 2026-11-16 到期），别在上面跑任何重要东西。
4. **三节点 /etc/rancher/k3s/registries.yaml 是手工合并出来的**（原有 docker.io/quay.io 加速 + 我们加的 localhost:5000 mirror），改它必须先 cat 看现状再合并，改完要滚动重启 k3s（master=`k3s`，worker=`k3s-agent`）。

## 1. 项目定位

微型发布工作流引擎（从零实现）：

- Go + gin + mongo-driver(v1) + client-go，单体服务，module 名 `pipeline`
- MongoDB 三集合：`workflow`（图纸）/ `workflow_task`（施工档案）/ `counter`（发号器）
- Job = K8s Job：主控只创建/轮询/清理，不执行脚本
- **任务级共享工作区（决断 #17，0.7.14）**：/workspace 生命周期从 Job 升格为任务——`mp-ws-<wf>-<taskID>` 一个 PVC（k8s/workspace.go，RWO/local-path/容量 `workspace.size` 默认 2Gi），同任务全 job 挂同一卷，job A 克隆的仓库/go 依赖（GOCACHE/GOMODCACHE 钉 `/workspace/.mp-cache/`）job B 直接见；调度器卷拓扑原生共置零亲和代码；清理四面一兜底 = finish 钩子（`CleanupWorkspace` var 钩子）+ CancelTask 纯数据路径 + Recover 宣判 + 泄漏清扫（label `mp/workspace=true` 扫描 → annotation `mp/task` 反查 → 终态/已删才删）；pause 不清（durable 期间工作区存活）；fail-fast：Unschedulable >2min 提前人话报错（节点暴毙不再沉默 10 分钟超时）
- **统一代码源（决断 #18，0.7.14）**：clone 是流水线声明不属于任何 job——`Workflow.Source`（SourceSpec 窄类型）经 BuildTask 前插合成 checkout stage（`Kind="source"` 渲染标记、单 job "clone"），克隆进共享工作区后下游 job 开箱 `$MP_SOURCE_DIR`；状态机/重试/取消/pause/刹车全免费继承，合成只在任务快照（图纸与编辑器 JSON 保持纯声明）；git 步骤类型保留（存量零破坏 + 多仓边角）
- **引擎本体也跑在 k3s 里**（helm chart 部署），API 经 ingress-nginx 暴露

MVP 已全量落地：变量/重试/取消/队列/CD deploy/archive/自举/cron/webhook/审批/通知/半吊子前端全部活体验收（能力全景见 §2；README.md 是对外能力说明书）。

**三条灵魂（及格线，缺一不算达标）**：
1. 图纸/实例分离，task 内嵌当时的完整定义快照
2. Stage 状态机 + **durable 暂停**（暂停=落库+goroutine 退出，进程重启可恢复）
3. Job = K8s Job

## 2. 已实现能力全景（截至 2026-11-12，集群镜像 0.7.14 / runner 0.3.0 / web 0.1.14；决断 #17 任务级共享工作区 + #18 统一代码源落地）

> 引擎全部能力域均已落地并活体验收：四门（35+20+25+20=100 断言）+ frontend jsdom 冒烟 12 断言在 0.7.10 上全绿（0.7.11 ack 收严 / 0.7.12 构建池 buildkit 化均为内部演进，各自单测 11 包 + 活体 scratch 回归全绿——0.7.12 的活体含双档构建 + 密封链 + mTLS + fnv 分散，见 §8），`go test ./pkg/...` 11 包单测绿。历史任务账本（MVP 四阶段，PR #1–#31）见工作台桌面的计划存档（仓外，文件名不入仓）；踩坑实录全在 §9。以下按能力域描述引擎**现在能做什么**。

### 里程碑时间线（全部落地）

M0 地基 → M1 第一口气（run→Job→轮询→ack→清理，引擎 helm 化进集群）→ M2 状态机（0.2.0）→ M3 durable 暂停（0.3.0）→ M3.5 启动宣判（0.3.1）→ 核心 CI/CD 步骤化（0.4.4，runner 进 ACR）→ M5 密封构建闭环（0.4.6）→ MVP 四阶段：执行基建（变量/重试/取消/队列）→ CD 闭环（env/deploy/archive/自举图纸）→ 触发与协作（cron/webhook/审批/通知）→ 门面（列表分页/前端）→ pkg 模块化分层（PR #31）。各阶段踩的坑与修法全部沉淀在 §9；详细验收标准见桌面计划存档（仓外）。

### 引擎核心

- 三集合 `workflow`（图纸）/ `workflow_task`（施工档案）/ `counter`（发号器）；run = 发号 → 快照落库 → K8s Job 创建/轮询（2s）/收割/清理；ack 全量写（`_id` 自然键 `<wf>-<taskID>`）；**ack 两档（0.7.11）**：中途写 best-effort（失败只记日志，job 轮询 2s 全量重写下一拍自愈）；goroutine 即将消失的致命写（finish 终态 / Enqueue 入队 / pause 闸门）走 `ackStrict`——0.5/1/2/4s 指数退避共 5 次（≈8s 封顶）+ 错误上抛（Enqueue 失败即触发失败 → API 500、内存态回滚 created；ErrNotFound 不重试立即返回），耗尽打 TERMINAL/GATE ACK FAILED 响亮日志、重启 Recover 兜底；`UpdateTask` 带 MatchedCount==0 → ErrNotFound 守卫（文档被删不静默"成功"，release/approve 自动获 404 语义）
- runStages 状态机：stage 内并行/串行 + WorstOf 最差者胜（cancelled(7)>failed(6)>pause(5)>running(4)>passed(3)>created(2)>skipped(1)）+ 失败刹车（串行剩余置 skipped / 并行全员跑完再聚合 / 后续 stage 全 skipped）
- durable 暂停：闸门落库 + goroutine 退出，kill 引擎 Pod 档案仍在；release 端点只翻 `ManualExec.Executed` 位（releaseMu 防双击，与 approve 同锁）
- 启动宣判 Recover（main.go 在 gin 起来之前跑）：created/queued/running 孤儿宣判死亡 + 清泄漏 Job；pause 稳态不碰（有意保留：pause 是无需看护人的稳态）。不变量：running 要么有活着的看护人，要么是终态
- 任务队列：1s ticker 扫 queued（task_id FIFO）→ CAS 抢占（repo.ClaimQueued 条件更新防双派）→ go Run；全局 `MAX_RUNNING_TASKS`（默认3，0=只收尾）+ 同 wf 串行；pause 不占名额；release/approve/cron/webhook 全走正常入队；Recover 扫描集含 queued（重启排空）
- 取消 DELETE /api/tasks/:name/:id：活任务 close(chan) 信号 202 一拍收口 / pause·queued·孤儿 CancelTree 纯数据 200 / 终态 409；cancelEntry closed 标志防二次 close panic
- K8s Job 名 `mp-<wf>-<taskID>-<stage>-<job>`（跨 wf 唯一）

### 变量与重试

- renderVars：JSON marshal → `{{.ns.key}}` 替换（SetEscapeHTML(false) 保脚本字符）→ unmarshal；driver `===MP=== OUTPUT k=v` banner 收割 → GlobalContext（`<job>.<output>` 点分键）→ 下游 job Run 时渲染（锁内，防并行收割竞态）
- 单 job 重试：spec.Retry + resetForRetry（化石现场清理）+ Pod env `MP_ATTEMPT`（重试剧本可确定性分支）
- 超时与错误策略：spec.TimeoutMinutes（默认 10，重活显式调大）+ spec.ErrorPolicy（stop/retry/ignore，图纸白名单校验）；**控制/上报分离**（策略只改控制流，失败照红）；task 尾态 WorstOf 如实收口
- clone（快照→新号→入队，creator=clone:<src>，GlobalContext 全新）/ retry（终态 409 守卫；resetForRetryRun 树内手术：failed/cancelled/**skipped 刹车残留**复活、passed 子树不动、Retry 预算归零）

### CD 闭环

- env 集合：name/namespace/desc，deploy job 的靶标映射（POST/GET/DELETE /api/envs，409/400/404 全路径）；**env 创建随建 namespace**（2026-11-07，PR #53）：EnsureNamespace 幂等确保（Terminating 拒绝复用 + managed-by 标签 + 并发 AlreadyExists 容错），ClusterRole `pipeline-engine-ns` 仅 get/create，**建不删非对称**（§4 决断 #13）；api 层经 `EnsureNamespaceHook` 钩子装配（main.go），nil 钩子 = 纯登记降级（单测模式）
- deploy job（CD 本体）：JobTypeDeploy + DeploySpec（env/dep名/容器名/镜像/wait_rollout 默认 true）；**不变量3：不起 K8s Job 不挂 sock，引擎 client-go StrategicMergePatch 直 patch**（只动目标容器）；rollout 轮询 = observedGeneration 追上 + updated 齐 + unavailable=0；wait_rollout=false = 自举防绞杀；DeploySpec 进变量渲染管线（image 可引上游 output）
- chart Role 已含 apps 组 deployments/statefulsets/daemonsets patch/update（活体生效，引擎 SA 可 patch 自身 Deployment）
- archive step：tar czf → curl 上传引擎 GridFS（bucket artifacts，键 `<wf>-<taskID>-<step>` 重跑覆盖，32MB 上限 413）→ GET 流式下载；Pod env 注入 MP_ENGINE_URL/MP_TASK_WF/MP_TASK_ID（curl -f 让上传失败翻红）
- **镜像仓库管理**（2026-11-07；2026-11-10 决断 #16 起升级为运行时 reconcile）：`registry` 集合（name/address/username/password/is_default/desc）+ CRUD 四端点；**Address 支持可选 scheme 前缀作传输档声明**（`http://`=明文档、`https://`/裸名=默认 HTTPS 公信直通，入库保留原样、消费点经 models.NormalizeRegistryAddr 剥——2026-11-10 翻转了旧"入库剥 scheme"语义，scheme 现在是数据不是噪音）；**推送目标三档解析**（`pkg/engine/exec/registry.go`，driver 编译前每 attempt 一次——凭证不落任务档案，retry/clone 拿仓库记录的当前值；与 deploy 的 envResolver 同一钩子模式）：显式 `registry:` 引用 > 裸名跟随 `is_default`（全局唯一默认，建/改互斥翻转；**删默认回落"无默认"稳态不选举补位**——强行选举会让裸名目标静默漂到任意历史记录，bootstrap-5 gate 活体抓的，PR #50）> 全名直通（匿名直推 HTTPS 端点）；**裸名无默认 = 报错点名**（决断 #16：内置 registry:2 退役后裸名无家可归，不再回落 $MP_NODE_REGISTRY——语义回归"推送目标必须有人站"）。CRUD 成功即触发 `BuildkitSyncHook`（api 层 var 钩子，main.go 装配真身，nil 降级）→ `k8s.SyncBuildkitRegistries` 全量生成 buildkitd.toml 写回 CM + hash 注解滚动构建池（极简形态，best-effort 失败只记日志自愈）。烘焙链渲染在 `pkg/step/dockerbuild.go`：全名 `NormalizeRegistryAddr(addr)/image:tag` + 凭证走 `--password-stdin` + **DOCKER_CONFIG=/tmp/.docker 子 shell**（/root/.docker hostPath 只读，login 写不进去——活体抓的）；**构建成功发 `===MP=== OUTPUT image_full=<全名>`**（下游 deploy 可引 `{{.<job>.image_full}}`，bootstrap 图纸靠它去硬编码域名）。明文姿态：凭证活库不进仓（bootstrap-ensure.sh 走 MP_ACR_ADDR/MP_ACR_USER/MP_ACR_PASS 环境变量录入）。**内联 Dockerfile 档（0.7.10）**：`dockerfile_content` 非空优先于路径档——正文 base64 烘焙进脚本、构建前现场落 `.mp-inline-Dockerfile` 再 `-f` 引用（内容是数据不是脚本，单引号/换行/`$` 全绕开 shell 引号协议；空内容 = 纯路径档，存量图纸零感知）
- **远程主机管理**（2026-11-08，PR #55）：`host` 集合（name/user_name/ip/port 默认22/private_key/desc）+ CRUD 四端点 `/api/hosts`；图纸 job 级 `spec.hosts []string` **名字引用**（job 级名字引用形态，不加新步骤类型）——**私钥不落任务档案**：exec.resolveHosts（var 钩子，同 registryResolver）每 attempt 现解析，driver 编译期 `pkg/step/hosts.go` 把私钥 base64 内嵌投影进 Pod（`/tmp/mp_host_<n>_PK` 0600 + `~/.ssh/config` 条目 accept-new + 环境变量 `MP_H_<名大写>_IP/_PORT/_USER`），用户 shell 步骤直接 `ssh <名>`；setup 失败 `|| exit $?` 掀翻驱动脚本不带病进步骤。**NeedsRunner 对 hosts>0 强制走 runner**（0.2.1 起 +openssh-client）。ip/user 走字符白名单（config 条目经 heredoc 展开，注入面封死）；名字不存在 → job 失败点名（同 registry 显式引用缺失）。范围外：探活、项目授权、VM agent、Windows、产物回传。明文姿态与 registry.password 同档；作用域全局共享 = 决断 #14
- 自举闭环（活体已通，bootstrap-3 passed；2026-11-07 起推镜像走 registry 引用 `acr`，bootstrap-4 复验；决断 #16 后凭证经 MP_ACR_ADDR/MP_ACR_USER/MP_ACR_PASS 环境变量录入、deploy 靶标镜像改引 `{{.build-ci.image_full}}` 上游产出）：deploy/bootstrap-workflow.json（checkout→go test 门禁→docker_build 推 registry acr tag=bootstrap→OUTPUT image_full）+ deploy job 打自身（wait_rollout=false）+ bootstrap-ensure.sh 落库（env self = pipeline ns + 可选 registry acr）——"用流水线发布流水线自身"
- deploy 单测手法：fake clientset + reactor 模拟 rollout 控制器追上（fake 无控制器行为）；pollInterval/rolloutWaitTimeout 做成 var 供测试缩短节拍

### 触发与协作

- cron：手搓 5 段 crontab 匹配器（*/n/范围/列表/周日双写，坏表达式写入时 400）+ 整分钟对齐调度器（truncate+sleep 不漂移）；POST /api/crons/:name/fire 手动点火（验收免等钟点）
- webhook：POST /api/webhook/:workflow；HMAC-SHA256 验签先行（GitHub X-Hub-Signature-256 / Gitee 双轨含明文 token；constant-time；多配置任一通过）；无签名仅当全部配置无 secret 放行（显式裸奔模式）；repo 归一（剥 .git/scheme/host）+ branch 匹配；creator=webhook:<repo>@<branch>
- **TriggerWorkflow 统一触发入口**：cron/webhook/手动共用唯一"发号→快照→入队"构造器（RunWorkflow 重构其上）
- 原生审批：ManualExec 扩 approvers/policy(any/all)/approvals；POST /api/tasks/:name/:id/approve（与 release 同锁同闸门位，同名重批覆盖 comment）；审批记录属于运行不入图纸快照（快照漏拷 approvers 曾让 all 静默变 any，已修）；老图纸空名单 = 一条审批即过 ≡ release
- 通知：notify 集合 CRUD + 任务终态 fire-and-forget 钩子（1 重试，失败只记日志）；飞书/钉钉双报文 + 双平台加签（两家 HMAC 约定不同，注释钉死）；**不对称文档化**：CancelTree 纯数据取消不发通知（无看护人在场）

### 门面

- 任务列表过滤分页：GET /api/tasks/:name?status=&creator=&from=&to=&page=&size= → **envelope {items,total,page,size}**（裸数组形状已废，消费方注意）；task_id 降序 + size 上限 100 + CountDocuments 总数。**0.7.9 跨库总账本**：GET /api/tasks（无 :name，?workflow= 可选过滤，缺省 = 全部工作流，created_at 降序——task_id 是 per-wf 计数器，跨库排序拿它会把 A 库 1 号排到 B 库 100 号前）
- **批量删除（0.7.9，全库第一个批量端点）**：POST /api/workflows/batch-delete `{names}` 与 POST /api/tasks/batch-delete `{ids}` → 恒 200 `{deleted, failed}` 部分成功如实上报。工作流删除（单删/批量同走 engine.DeleteWorkflowFull）**级联清理任务档案 + 定时器**（webhook 内嵌图纸文档随删自灭；孤儿 cron 否则每分钟空转报错永不停止）；任务档案删除**只收终态**（非终态拒绝点名——running 有看护 goroutine ack 回写会复活，pause 是 durable 稳态不拆闸门），双保险 = 内存终态判定 + DeleteTask 条件删除（防读到删窗口的 retry 竞态）；不存在 = 幂等成功。GridFS 产物不追（保留策略范畴）
- workflow/env **PUT 端点已补**（PR #33）：PUT /api/workflows/:name、PUT /api/envs/:name——全量替换、路径名为准、name 不一致 400、created_at 从旧档带过来；前端"保存=删除重建"时代结束
- **React 前端**（2026-11-06 重构，PR #34–#36）：`frontend/`（Vite+TS strict+antd5，暗色 teal 侧边栏自成一体）；页面 = 工作流列表/**画布式四层编辑器**（2026-11-07 画布化：横=stage 串行时间轴、竖=job 堆叠——并行竖堆/串行 ↓ 链；job 压成紧凑条框+详情右侧 JobDrawer（live-patch 无缓冲）；列间分界线 ⊕ 插 stage、条框悬停 ⊕ 插 job；**最左统一代码源列**（2026-11-12 决断 #18：SourceColumn 常驻，卡片=repo 尾段+⑂分支 chip，空态=＋添加虚线卡，抽屉编辑仓库/分支/令牌/检出目录；clone 不再属于任何 job）；列底审批闸门条（any/all+审批人）；collectErrors 逐节点实时红标；job freestyle/deploy 二选一：**镜像自动档**（2026-11-07 用户拍板删自定义镜像选择——引擎按步骤选 runner/busybox，`pkg/k8s/build.go` 镜像三档；旧图纸显式镜像只留 Tag+清除入口，真要定制走 JSON 逃生舱）+retry/timeout/error_policy、deploy 靶标四项+env 下拉；5 种 step 表单**竖排字段**（label 上、控件全宽下——560px 抽屉横排五连挤被用户否决）；shell 脚本与内联 Dockerfile 用**终端风编辑面**（TermEditor：mac 三圆点标题栏+深色等宽+teal 光标，纯观感件值协议同 TextArea）；docker_build 的 Dockerfile **双档**（仓库内路径 / 内联编写 + 导入本地 Dockerfile，内联=`dockerfile_content` 协议字段，清空回落路径档）；JSON 双模逃生舱）/任务列表过滤分页（0.7.9 起**默认全部工作流总账本**+切单库）/任务详情三级树（分步日志抽屉+cancel/retry/clone/approve/release）；**0.7.9 批量删除面**：两列表页 antd rowSelection checkbox（表头全选框 = "全部勾上再删"；任务页非终态行勾选框置灰 getCheckboxProps + preserveSelectedRowKeys 跨页累积）+ 批量删除按钮（Modal.useModal 主题化确认弹窗——首个，批量/全选删除必配确定界面）；api 层 typed client 对齐 27 端点、usePolling（setTimeout 链防重叠、终态停）
- 前端部署形态：**独立 nginx Pod**（多阶段 Dockerfile node build→nginx；/api、/healthz 反代引擎 service 经 envsubst 模板+resolv.conf 注入 resolver），ingress 单条规则指前端 Pod（chart `tags.web`）；引擎 API-only（go:embed 已退役，main.go 无前端）
- 前端质量门：`npm run build`（tsc strict 零错）+ `frontend/e2e/smoke.mjs` **jsdom 真执行冒烟**（12 断言：React 真挂载/五导航/真引擎数据/批量删除面（勾选框+按钮）/菜单真切换路由/任务页总账本（全部工作流）/**编辑器终端风真事件链**（新建页→job 条开抽屉→添加步骤 Dropdown（click 触发）→镜像构建竖排字段→内联编写切档→Dockerfile/shell 双终端面）/API 可达；手法=vite 单 chunk 无 import 语句→new Function eval+window 全量桥接）——"字符串在位"≠"脚本活着"（曾配套的 bundle ESM 语法门随四门存档于 tag acceptance-suite）

### CI 步骤实现要点（留档）

- driver 生成 `pkg/step/driver.go`：⚠️ POSIX sh——alpine 是 ash，无 local/[[ ]]/数组；**首行 `exec 2>&1` 单流化是切分确定性的地基**
- banner 解析/切分/收口 `pkg/step/logs.go`：ApplyStepBanners 幂等重放且**行中 banner 也认** / SliceStepLogs 按 START 切段（未知步骤进黑洞）/ FinalizeSteps：created→skipped、悬空 running→failed、AfterRun 门禁、64KB 保尾部截断
- StepCtl `pkg/step/stepctl.go`：git clone --depth 1 + token 注入 URL；docker_build **裸名 = PreRun 报错**（决断 #16：内置本地档退役，`$MP_NODE_REGISTRY` 前缀注入随之绝迹——曾有的"前缀段双引号在单引号外"shell 语义坑随其入档历史）；junit 只认 testsuite 开标签属性、跳过 testsuites 包装层防双算
- JobCtl `pkg/engine/exec/k8s.go`：Run 轮询循环内顺手刮日志、SaveInfo **稳定探测**（连续等长兜尾部）；镜像三档 spec.image > runner（RunnerImage 空 = 创建前守卫报错，决断 #16 必填化）> busybox；**docker_build 执行物 = 共享 buildkitd**（决断 #15 的 buildkit 演进：Job Pod 注 MP_BUILDKIT_ADDR/MP_BUILDKIT_CERTS + 客户端证书卷，驱动脚本 mp_bk 助手幂等建 remote builder 后 `docker buildx build --push`；fnv task 亲和直连 per-pod DNS；`--provenance=false` 防 ACR 拒收 §9.9——classic builder 随 dind 退役）；Dockerfile 双档（仓库内路径 / 内联正文 b64 落盘，见 §2 CD 段）；**driver 编译前 registry 推送目标解析**（exec/registry.go，三档+裸名报错见 §2 CD 段）；roundtrip 测试 `pkg/step/driver_roundtrip_test.go` 真 /bin/sh 跑→分离捕获断言单流不变量→banner 反解→finalize 全链

### 代码结构（2026-11-06 模块化重构后，依赖全部单向向下无环——方向纪律由 TestLayering 钉死（决断 #20）；模块名 `pipeline`）

`pkg/config/`(leaf 环境配置) → `pkg/models/`(leaf 领域模型：结构体＋Status/WorstOf＋纯领域逻辑 ValidateWorkflow/CronMatch/Satisfied/EffectiveSteps/NeedsRunner·WantsDockerDaemon) → `pkg/repo/`(mongo 持久层＋GridFS artifacts，集合句柄全包内私有) →
`pkg/step/`(脚本执行层：driver.go POSIX 驱动生成 / hosts.go 主机 ssh 预热段（base64 私钥投影 + ~/.ssh/config 条目）/ StepCtl 接口+工厂，shell/git/dockerbuild/junit/archive 一Ctl一文件全私有 / logs.go banner 解析·切分·收口——只 import models) →
`pkg/k8s/`(K8s 访问层：client.go 双模式 client / build.go JobName+BuildJob（MP_PULL_SECRETS 名单注入）/ buildkit.go SyncBuildkitRegistries——buildkitd.toml 运行时生成+CM/STS reconcile（决断 #16）/ log.go PodLog+TailJobLog / util.go PodExitInfo+DeleteOpts) →
`pkg/engine/`(编排核心：run.go Run 入口+taskCtx+ack / stages.go stage 状态机+闸门刹车谓词 / jobs.go job 状态机+变量渲染编排+输出收割 / finish.go 终态收口+通知 / task.go BuildTask 快照 / trigger.go 统一触发+spawn 唯一构造序列 / ops.go 任务运维操作面 CloneTask·RetryTask·CancelTask·TaskLog+sentinel 错误 / gate.go 闸门审批域独立锁面 ReleaseGate·RecordApproval（gateMu）/ queue.go 派发器 / recover.go 启动宣判 / vars.go 变量渲染) 下挂四个子包：
　`engine/cancel/`(取消注册表 Register/Request/Signalled/Tree/ErrCancelled)、`engine/exec/`(JobCtl 接口+Ctx 视图：k8s.go K8sJobCtl+重试+轮询、deploy.go DeployJobCtl+rollout、registry.go 推送目标三档解析、hosts.go 远程主机解析——env/host/registry 解析依赖经 Ctx 函数字段注入 FindEnv/FindHost/FindRegistry/FindDefaultRegistry，engine 装配真身、单测传 stub，不 import repo（决断 #20）；jobTimeout/pollInterval 在此；整树写回经 Ctx.Ack 由 engine 注入)、`engine/notify/`(IM 通知 NotifyOnFinish)、`engine/cron/`(定时调度 cron.Start(FireFunc)——触发动作回调注入，不 import engine) →
`pkg/webhook/`(git 签名/载荷/匹配纯逻辑层) → `pkg/api/`(gin handler 层：纯 HTTP 形状翻译——taskFromPath 样板单点、32 个 handler 全包内私有、业务全在 engine/models/repo；workflow/task 运维+approve+artifact+env/registry/host/notify/cron CRUD+webhook 编排；外部副作用经 NewRouter(deps Deps) 构造注入——EnsureNamespace/BuildkitSync，nil=降级，决断 #20) → `main.go`(根，仅装配：config→repo.Init→engine.Recover→go exec 派发器→go cron.Start(engine.TriggerWorkflow)→gin；api.Deps/exec.Ctx 解析依赖真身都在这一处装配)。
另有 `pkg/arch/`(分层方向断言测试 TestLayering——纯测试包零生产代码，go list 包图四规则，决断 #20) `docker/`(engine/runner/web 三镜像 Dockerfile) `deploy/chart/mini-pipeline/`(helm) `deploy/build-push.sh`/`build-runner.sh`/`build-web.sh`(本机构建) `deploy/bootstrap-*`(自举) `frontend/`(React 前端，e2e/smoke.mjs jsdom 冒烟)；**七门验收脚本＋fix 脚本已退役**（2026-11-06，存档 tag acceptance-suite，见验收资产节）。

### 验收资产（活体七门已退役，2026-11-06 存档于 tag acceptance-suite）

- **退役清单**：四门 test-mvp1~4（100 断言：变量/重试/取消/队列/clone/超时错误策略、env/deploy/archive、cron/webhook/审批/通知/release、列表分页/前端语法门/日志）+ 老三门 test-m3（durable 暂停含 kill Pod）/ test-m3.5（启动宣判）/ test-cicd（核心 CI/CD）+ fix-daemon-insecure.sh——引擎 0.7.0 封版、集群 11-16 到期，用户拍板清理；终版全绿基线固化在 git tag `acceptance-suite`（指向 d4c23a6；tag 随旧史留在存档仓，见决断 #19）
- 恢复任意一门：从存档仓**临时 clone** 取脚本——`git clone $(git config --get mini-pipeline.archive-repo) /tmp/mp-archive && git -C /tmp/mp-archive checkout acceptance-suite -- deploy/` 后照原命令打集群 ingress（⚠️ 单一引擎纪律 §9.20 仍适用；不 fetch 进本仓，决断 #19）
- 现存活动验收：`go test ./pkg/... -count=1`（11 包单测，repo 类需 port-forward mongo）+ `cd frontend && MP_BASE=… npm run smoke`（jsdom 真执行 7 断言）
- §9 案例存档中的脚本名为历史叙事，保留原状

## 3. 环境速查（全部实测可用）

| 项 | 值 |
|---|---|
| k3s master | 8.155.137.224（内网 172.29.237.251，control-plane+etcd，**2026-11-16 到期**） |
| k3s worker-1 | 8.155.128.91（内网 172.29.237.250） |
| k3s worker-2 | 8.155.128.81（内网 172.29.237.249）✅ 网络已修复（2026-11-01） |
| kubectl context | `k3s-heyuan` |
| MongoDB | ns `pipeline`，**ClusterIP**（chart 内置），本机调试走 `kubectl --context k3s-heyuan port-forward -n pipeline svc/mongo 27017:27017` |
| registry:2 | **已从 chart 退役**（决断 #16，2026-11-10：`templates/registry.yaml` 删除，chart 不再内置产物仓库）。现存集群里的七件套资源随 upgrade 被 helm prune（PVC 有 `helm.sh/resource-policy: keep` 护体不删）；要产物仓库就登记外部 registry，或自部署——本集群实例 = k3s-gitops（argocd registry app，私有仓）管辖的 registry:2 + UI 六件，**本仓零副本**（2026-11-10 当日从手工 apply 态移交回 argocd，UI ingress 复活，见 §8 移交注记）。历史（归 chart 管时期）：七件套 2026-11-07 从 ArgoCD 收回，反向接管实录 §9.23；域名端点对 Pod 内 docker 不可推的教训 §9.23 仍然有效 |
| **ACR 个人版** | 引擎 + runner + 前端镜像分发仓库：独立实例 `crpi-28fqgwft325ylccc.cn-heyuan.personal.cr.aliyuncs.com`（公网推送）+ `-vpc` 后缀变体（集群 VPC 拉取，免公网流量）。命名空间 `codepipeline-learning` 下三仓：`mini-pipeline`（引擎）、`pipeline-runner`（步骤执行器，chart values `runner.image`）、`mini-pipeline-web`（React 前端+nginx，chart values `web.image`）。**2026-11-10 三仓全部转公开**（匿名 token 实测可拉）→ ACR 用户名/密码已随决断 #16 出库（不再进 chart values / bootstrap 脚本；推送凭证 = 本机 docker login 状态 + bootstrap 时 MP_ACR_* 环境变量）。build 必须 `--provenance=false`（详见 §9.9）；老共享域名 `registry.cn-heyuan.aliyuncs.com` 不属于本实例（Forbidden Host），别用 |
| **buildkit（共享 buildkitd 构建池）** | chart `tags.buildkit` 渲染：StatefulSet buildkit×2（privileged + overlayfs snapshotter，moby/buildkit:v0.34.2，hostname 反亲和两 worker 各一）+ headless svc 1234（mTLS grpc，证书 Secret `buildkit-tls-certs`）+ buildkitd.toml ConfigMap（**全量内容由引擎 SyncBuildkitRegistries 运行时生成**——daocloud mirror 段（MP_BK_MIRROR）+ registry 集合 http:// 声明记录的白名单段，CM 变化 patch STS hash 注解滚动；决断 #16）+ 每日 prune CronJob（buildctl 逐副本）；docker_build 全在这两台跑（引擎 fnv task 亲和，决断 #15 演进；2026-11-09 替换 dind——dind 常驻 dockerd+containerd 双进程太重） |
| 引擎入口 | `https://pipeline.47.113.177.198.nip.io`（复用集群现成 nip.io+ingress-nginx 链路，SG 80/443 已放行） |
| 安全组 | `sg-f8z8wcoum4myexrjfr40`；**30017 已收掉**，现有 80/443/6443/22/3389 + 同组内网互通 |
| 集群存量 | argocd（ns argocd）、ingress-nginx（DaemonSet hostNetwork 双 worker）——都不是我们的，别动。另：keepalived 的 HaVip `172.29.237.252` 挂在两台 worker 的 **dummy0** 上（勿还原回 eth0——flannel 会选它当 vxlan 源地址，VPC 反欺骗校验丢包，跨节点网络全灭；2026-11-01 修过一整轮） |
| SSH | `ssh -i ~/.ssh/heyuan81.pem root@<节点IP>`，三台全通（集群操作直接 ssh，pem 是 /mnt/e 原件的拷贝） |
| aliyun CLI | 已配 AK/SK（profile default，查询时显式 `--RegionId cn-heyuan`）。仅云 API 查询用（如安全组），够不到集群本身 |

## 4. 已定稿的架构决断（不要再讨论）

1. **引擎跑在集群内**（2026-11-01 拍板，推翻更早的"本机跑"方案）：InClusterConfig + SA `pipeline-engine`（Role 仅 jobs/pods 权限）；代码保留 kubeconfig fallback 供本机调试（`K8S_CONTEXT=k3s-heyuan`）。
2. **部署形态 = helm chart**：`deploy/chart/mini-pipeline`，`tags.{engine,mongo,registry}` 组件开关 + `connections.mongo.uri` 参数化。⚠️ 本机 helm v3.19.0：upgrade **不复用**上次 --set 值——部署时所有 --set 必须显式带全。
3. **registry:2 提前建**（原 M5 组件）→ **2026-11-10 随决断 #16 整条退役**：内置产物仓库不再由 chart 提供，docker_build 推送目标全走 registry 管理记录（自部署形态 2026-11-10 移交 k3s-gitops 由 argocd 管，本仓零副本，见 §8）；"$MP_NODE_REGISTRY 兜底 + kubelet localhost:5000 mirror 密封链路"语义一并退役（kubelet 拉取侧 = 集群管理员职责）。历史形态（留档）：镜像名统一 `localhost:5000/mini-pipeline:<tag>`，push 节点推 `127.0.0.1:30500`，pull 三节点 registries.yaml mirror 到节点内网 IP:30500（不能只配 127.0.0.1——worker-2 回环 NodePort 不通，见 §9）；registry 存储剥离 host 前缀使两边镜像名天然一致。（资源 2026-11-02 曾移交 ArgoCD、2026-11-07 收回本 chart，见决断 #11；2026-11-10 chart 模板删除后七件套随 upgrade prune，PVC keep 注解保命）
4. **镜像在本机构建直推 ACR**（2026-11-04 起本机 Docker Desktop 可用后改版）：`deploy/build-push.sh <tag>` = 本机 `docker build`（多阶段 golang:1.26-alpine，GOPROXY=goproxy.cn，.dockerignore 瘦上下文）→ push ACR 公网端点（`--provenance=false` 硬要求，见 §9.9）。历史：2026-11-03~04 曾走"scp worker-1 远程构建"链路（本机无 docker），ssh 僵尸会话坑频发，本机 engine 就位后退役；worker 上构建一去不复返。
5. **镜像构建走路线①**（M5 生效）：fat image（docker CLI）+ 挂宿主 `/var/run/docker.sock`；k3s 是 containerd，两个镜像仓库互不相认，registry:2 是必选项。（**2026-11-08 起挂 sock 半截被决断 #15 推翻退役；2026-11-09 决断 #15 再演进一步：构建物从共享 dind 换成共享 buildkitd 池**，runner 带 docker CLI + buildx 插件当客户端，registry:2 必选项语义不变）
6. 状态获取用**轮询**（每 2s 查 K8s Job），不上 watch。
7. runner 不主动回调：**Pod 退出码 = job 结果**，日志用 API 拉。
8. 第一版不做队列/并发控制。
9. 挂 docker.sock 的 Pod ≈ 节点 root，仅限自有临时集群，fat image 永不推公网。（0.7.8 起构建池常驻容器化 + mTLS 门禁——决断 #15；0.7.12 起 dind 再换 buildkitd（特权面同 privileged，但单进程比 dockerd+containerd 双进程轻得多），从"N 个短命 Pod"缩成"常驻单副本集合"的收敛不变）
10. Mongo 密码明文写在 chart values 和 config/config.go——当时知情接受（2026-11-05 起仓库定 **public**，用户知情接受明文密码暴露风险并拍板维持公开；轮换 mongo/ACR 密码为可选卫生动作，集群 11-16 到期风险窗口有限）；ACR 密码 `acr.password` 同档处理。→ **2026-11-10 被决断 #16 推翻**：mongo 密码 install 现场生成（mongo-auth Secret，lookup 复用），仓库零静态密钥；ACR 用户名/密码全部出库（三仓转公开，匿名可拉）。存量集群升级先手建 Secret 注入现密码（一次性迁移）。
11. **registry:2 管理权：ArgoCD → 本 chart（2026-11-07 用户拍板收回，推翻 2026-11-02 的移交）→ 2026-11-10 随决断 #16 chart 退役**：七件套曾由 `templates/registry.yaml` 渲染（PVC keep 注解护体）——模板已删除，upgrade 即 prune（PVC 幸存）。前史：2026-11-02 曾移交 k3s-gitops `registry` app；反向接管实录与级联事变见 §9.23。**2026-11-07 产品面保留并演进**：引擎 registry 管理（CRUD + is_default 全量制——用户在"显式引用制 vs 全量制"两分岔中拍板全量制：裸名 docker_build 跟随默认仓库，设默认会静默改变所有裸名图纸的推送目标）；2026-11-10 起 CRUD 即触发 buildkitd 白名单 reconcile（见 #16），裸名无默认 = 报错点名（原"无默认=回落内置本地"稳态随内置仓库退役）。
12. **引擎镜像分发走 ACR 个人版独立实例**（2026-11-03）：push = 公网端点（构建侧，节点或本地），pull = VPC 端点（`-vpc` 后缀，集群内网免公网流量）。**2026-11-04 扩展：runner 镜像同链路跟进**（`codepipeline-learning/pipeline-runner`，chart values `runner.image` → env `RUNNER_IMAGE` 注入引擎，升 tag 不用重编引擎）。⚠️ 引擎运行时创建的 Job Pod 必须自带镜像拉取凭证（Deployment 挂的 secret 不会继承，buildJob 注入）——**2026-11-10 决断 #16 起 voucher 出库**：三仓转公开匿名拉，硬编码 acr-credential 改为通用 `MP_PULL_SECRETS` 名单（chart values `imagePullSecrets` 注入，部署者私仓时自配）。ACR 的坑见 §9.9。
13. **env 建不删非对称 + namespace 随建权限最小化**（2026-11-07）：创建/更新 env 时引擎现场幂等建 namespace（`k8s.EnsureNamespace`：Terminating 拒绝复用 + `app.kubernetes.io/managed-by: mini-pipeline` 标签 + 并发 AlreadyExists 容错）——不变量"落库的 env 其 ns 必然已存在"；DELETE env 纯数据，**namespace 原地不动**——env 级联删 namespace 的接口不做（label 匹配删除需要 delete 权限 + 共享 ns 灭顶风险：有人登记 env→pipeline 引擎自家居所，删 env 就把引擎连窝端）。ClusterRole `pipeline-engine-ns`（clusterrole.yaml，tags.engine 门控）verbs 只有 get/create，永无 delete/list。api 层不 import K8s：`EnsureNamespaceHook` var 钩子由 main.go 装配真身，nil = 纯登记降级（单测路由零改动）。
14. **远程主机（host 集合）全局作用域 + 凭证不落档案**（2026-11-08）：host 集合与 workflow/env/registry 同形态——全局共享无项目隔离（无 RBAC 前提下的一贯口径）；图纸 `spec.hosts` 只存**名字引用**，私钥每 attempt 现解析（exec.resolveHosts 钩子，同 registry 三档时序）经 driver 编译投影进 Pod——任务档案/任务 API 永不含私钥，retry/clone 拿记录当前值。不加新步骤类型（job 级名字引用形态，用户 shell 里自由 ssh）；范围外：探活/项目授权/VM agent/Windows/产物回传。私钥明文存库与 registry.password 同档（知情接受）。
15. **docker_build 走共享构建 daemon（mTLS TCP）——2026-11-09 由 dind 演进为 buildkitd 池**（先推翻决断 #5 的挂 sock 半截，再替换 dind：常驻 dockerd+containerd 双进程太重，buildkitd 单进程专职构建）：chart `tags.buildkit` 渲染 StatefulSet（privileged + overlayfs snapshotter＝官方 rootful 姿势，`moby/buildkit:v0.34.2`，emptyDir /var/lib/buildkit，hostname 反亲和）+ headless svc（只为 per-pod 稳定 DNS `buildkit-<i>.buildkit`，无 VIP 不做转发）+ buildkitd.toml ConfigMap（**2026-11-10 决断 #16 起全量内容由引擎运行时生成**——模板只留 mirror 兜底段，原"registry:2 三节点 `http=true` 逐 host 白名单"随其退役）+ prune CronJob（每日 buildctl 逐副本打）。runner Pod 带 buildx 插件（0.3.0 起，v0.38.2）当客户端：BuildJob 对 wantsDocker job 注 `MP_BUILDKIT_ADDR=tcp://buildkit-<i>.buildkit:1234`（**fnv32a(wf/taskID)%replicas 零状态 task 亲和——同 task 构建与 retry 永落同一台吃层缓存，跨 task 哈希散开**）+ `MP_BUILDKIT_CERTS=/etc/mp/bk-client`（证书三层注入不变：chart sprig genCA/genSignedCert 现场生成、只落集群 Secret `buildkit-tls-certs`、lookup 守卫防 upgrade 轮换；items 改名 ca.pem/cert.pem/key.pem）；驱动脚本 mp_bk 助手幂等建 remote builder（driver-opt 三证书；MP_BUILDKIT_CERTS 空 = BUILDKIT_ADDR 外部直连档裸连）→ `docker buildx build --builder mp --provenance=false --push`（**ACR provenance 防线从 DOCKER_BUILDKIT=0 移交 buildx flag §9.9；auth 传递已活体实锤——login 写的 config.json 被 buildx 收进 SolveRequest 传 buildkitd，日志 [auth] token 行为证**）。dind 时代其余语义原样平移：/root/.docker hostPath 不存在（全名直推私有仓库必须走 registry 引用档）、明文口不存在。范围外：每 job 独占 sidecar、buildkit缓存持久化 PVC（emptyDir 语义对齐 dind 时代）、rootless buildkit、buildx 多架构、自建 buildkit 镜像。
16. **registry 外部化 + buildkitd 白名单运行时 reconcile + 全仓零痕迹**（2026-11-10 用户拍板，本 PR 三合一）：① **内置 registry:2 七件套彻底退役**（templates/registry.yaml 删除、`tags.registry`/`registry.*`/`acr.*` values 段删除，自部署清单当日先落 `deploy/registry-external.yaml`、随即移交 k3s-gitops 由 argocd 管（本仓零副本，见 §8））——起因：buildkitd.toml 的三节点 IP 白名单是环境绑定数据（[registry] 只认 host 不认 CIDR），换集群要现场改 values；设计基线：registry 是运行时数据、白名单随触发面 reconcile（进构建守护进程的 args+certs.d 是一条路），我们收敛为两刀：`k8s.SyncBuildkitRegistries`（`k8s/buildkit.go`）全量生成 buildkitd.toml（mirror 段 MP_BK_MIRROR + 白名单段）写回 CM、内容变化 patch STS pod-template 注解 `mp/buildkitd-toml-hash` 滚动（buildkitd 只在启动时读 toml）；触发面 = 引擎启动（main.go Recover 后一次）+ registry CRUD（`api.BuildkitSyncHook` var 钩子，EnsureNamespaceHook 同款，best-effort 失败只记日志自愈；helm upgrade 重置 CM → 引擎重启写回 = 自愈闭环）。**scheme 显式分派**：记录地址带 `http://` 前缀 = 明文档进白名单；缺省/`https://` = buildkitd 默认 HTTPS 公信直通零配置（ACR 天然此档，连白名单都不进）；自签 CA 后续演进。② **推送目标语义翻转**：裸名无默认 = 报错点名（内置本地档退役后无家可归，决断 #3/#11 的"回落内置"语义随其退役）；docker_build 成功发 `OUTPUT image_full` banner（下游 deploy `{{.job.image_full}}` 引用）。③ **全仓零痕迹**（AGENTS.md 例外保留）：ACR 用户名/密码出库（三仓转公开匿名可拉，token 实测）、imagePullSecrets 整链退役改通用 MP_PULL_SECRETS 名单、镜像源三件+ingress.host 全部 `--set` 绑定（required 守卫）、mongo 密码 install 现场生成（mongo-auth Secret，推翻 #10 的明文档）、build-*.sh 走 MP_REGISTRY env、bootstrap 凭证走 MP_ACR_* env。Role 加 configmaps get/list/patch/update（CM reconcile 刚需）。
17. **任务级共享工作区 PVC（2026-11-12 用户拍板）**：/workspace 生命周期从 Job 升格为任务——痛点 = 每 Job 一个 emptyDir（build.go 旧世界），job A 克隆的仓库/go.mod 依赖到 job B 全断流。`mp-ws-<wf>-<taskID>` 一个 PVC（`k8s/workspace.go`：名字确定性免 label 点名删、Ensure 幂等 Get→NotFound 才 Create、RWO、storageClass 缺省吃 local-path、容量 values `workspace.size` 默认 2Gi；label `mp/workspace=true` + annotation `mp/task=<wf>/<taskID>`——中文 wf 名进不了 label 进得了 annotation），同任务全 job 挂同一卷；**调度器卷拓扑原生共置**（RWO + WaitForFirstConsumer，零亲和代码——无 NFS 基建下的更简形态）。GOCACHE/GOMODCACHE 钉 `/workspace/.mp-cache/`（`MP_WORKSPACE_SHARED=false` 整链回落 emptyDir 旧世界=回滚保险）。**清理四面一兜底**：finish 钩子（`engine.CleanupWorkspace` var 钩子，main.go 装配、nil=降级保单测）+ CancelTask 纯数据路径（无看护人，finish 不经过）+ Recover 宣判任务 + 泄漏清扫（sweepWorkspacePVCs：label 扫描 → annotation 反查 → 已终态/已删才删——覆盖删卷失败搁浅/宣判/档案已删三股；pause 不清，durable 暂停工作区存活）；**fail-fast（用户补充条）**：轮询循环查 PodScheduled=False/Unschedulable 持续 >2min（condition LastTransitionTime 无状态判定，helper `k8s.PodUnschedulable`）→ 提前人话报错点名「retry 任务即可在新节点重建工作区」——节点暴毙的沉默 10 分钟超时换成 2 分钟内可读、指向动作的报错（超时兜底→finish 删搁浅 PVC→retry 重建的链路本就闭环，此条只换报错姿态）。Role 加 pvc create/get/list/delete。
18. **统一代码源（2026-11-12 用户拍板）**：clone 是流水线的声明、不属于任何 job——画布最左一级「代码源」节点（repo/branch/token/检出目录）。模型层窄类型 `SourceSpec`（`Workflow.Source`，有意不复用 StepSpec：step 十几个字段对代码源全是噪音）+ `StageTask.Kind`（"source" 渲染标记，不靠名字嗅探）；执行层 `SourceSpec.CheckoutStep()` 纯字段映射 → git 步骤（clone 渲染/token 注 URL/branch 缺省 main 全在 gitCtl 单源，`RepoDirName` 上移 models 单一真相源）；引擎层 `BuildTask` 前插合成 checkout stage（Name=checkout、单 job "clone"）——状态机/重试/取消/pause/Recover/失败刹车全免费继承（clone 失败→后续全 skipped 天然正确），**合成只在任务快照**：workflow 文档与编辑器 JSON 保持纯声明（快照=可执行真相，灵魂 #1）。下游 job 开箱 `$MP_SOURCE_DIR`（BuildJob 注入，跨 job 的目录契约）。前端 SourceColumn 常驻画布最左（卡片=repo 尾段+⑂分支 chip，抽屉编辑，TaskDetail kind=source 渲染「代码源」）。git 步骤类型保留（存量图纸/档案零破坏 + 多仓边角仍可用），代码源是推荐路径。校验双侧：ValidateWorkflow Repo 必填 400 + 前端 collectErrors 红标。
19. **仓库换址 + 历史快照重启（2026-11-12 用户拍板）**：origin 切至 `https://github.com/Edward-258/mini-pipeline.git`（public）——本地以当日 master 工作树做 **orphan 单 commit 快照重启**（树指纹前后一致 = 内容零差异、历史零继承）。旧仓（私有）原样封存 = 完整历史存档，地址记 `.git/config` 自定义键 `mini-pipeline.archive-repo`（**不入仓**，避免公开侧互相指向），**只读**：永不推送、永不 fetch 其 ref 进本仓（考古走临时 clone）。**新仓禁令：永不 push tags**（旧 tag 指向旧史链，一旦可达 = 整条旧史公开）。本地 reflog expire + gc 后旧对象清空，本机 .git 只含快照史。决断 #10 的公开姿态平移到新仓（「禁止翻转可见性」随旧仓封存失效）。
20. **依赖注入收口 + 分层纪律成测试（2026-11-12 用户拍板，纯内部演进）**：三件事一刀切，**行为零变化、零部署成本**（协议/chart/镜像语义全不动，集群在跑的版本不受影响）。① **exec 三个 resolver 的包级 var 钩子收进 Ctx**：deploy `envResolver` / hosts `hostResolver` / registry `registryResolver`+`defaultRegistryFinder` 退役，改 `exec.Ctx` 函数字段 `FindEnv`/`FindHost`/`FindRegistry`/`FindDefaultRegistry`（exec.go，签名即文档）——engine 构造 Ctx 处注入 repo 真身（jobs.go，与 Mu/Ack 同点装配），单测构造 stub 不再 save/restore 全局；exec 包 import repo 随之清零（每 attempt 解析时序与「凭证不落档案」语义原样）。② **api 两个包级 Hook 改构造注入**：`EnsureNamespaceHook`/`BuildkitSyncHook` 退役，改 `api.Deps{EnsureNamespace, BuildkitSync}` 经 `NewRouter(deps)` 显式传入（router.go，路由处闭包包装消费方；nil = 纯登记降级语义不变）——装配关系收拢在 main.go 构造调用处一目了然，测试不再改全局再恢复（`engine.CleanupWorkspace` 仍 var 钩子形态，装配点同在 main）。③ **分层方向断言 TestLayering**（新包 `pkg/arch`，纯测试包零生产代码，doc.go 带实测包图分层快照）：四条会咬人的规则全走 `go list` 包图查询、测试自身不 import 任何被检包——models/repo/step 闭包不引 engine/api、exec 闭包不引 engine 编排本体与 api（兄弟子包 cancel 合法）、api 直接 import（含测试文件）不引 k8s/step（钩子边界：K8s 访问经钩子注入，api 只做 HTTP 形状翻译）、step 闭包不引 repo——踩线即红灯点名违规包，不再靠 review 自觉；口径按现状校准（engine 编排本体已不 import step，故无该断言）。

## 5. 常用命令

```bash
# ===== 部署循环（改代码后的标准姿势；本机 Docker Desktop 直构直推；决断 #16 零痕迹——端点走 env/--set 不进仓） =====
# 1) 构建镜像（--provenance=false 是 ACR 硬要求，脚本已内置；本机 docker login 状态即凭证，首次需 login 一次）
MP_REGISTRY=crpi-28fqgwft325ylccc.cn-heyuan.personal.cr.aliyuncs.com deploy/build-push.sh 0.7.14
# 2) 升级 release（helm v3 不复用上次 --set，所有 --set 显式带全；镜像源三件+ingress.host 必填——required 守卫挡漏配）
#    ⚠️ mongo existingClaim 是存量集群专属纪律：mongo-data 是 M0 手建 PVC 非 helm 所有，漏带 = helm 试图新建
#    同名 PVC 撞 "resource exists" fail-safe 拒绝（升级原子中止，不会静默换卷）；registry-data 已随七件套退役不再需要
#    ⚠️ 决断 #16 存量迁移一次性动作：升级前 kubectl create secret generic mongo-auth --from-literal=password=<现密码>
#    （否则 upgrade 会现场生成新密码，与现存 mongo 的 root 密码不一致 → 引擎连不上）
helm upgrade mini-pipeline deploy/chart/mini-pipeline --kube-context k3s-heyuan -n pipeline \
  --set image.repo=crpi-28fqgwft325ylccc-vpc.cn-heyuan.personal.cr.aliyuncs.com/codepipeline-learning/mini-pipeline --set image.tag=0.7.14 \
  --set runner.image=crpi-28fqgwft325ylccc-vpc.cn-heyuan.personal.cr.aliyuncs.com/codepipeline-learning/pipeline-runner:0.3.0 \
  --set web.image=crpi-28fqgwft325ylccc-vpc.cn-heyuan.personal.cr.aliyuncs.com/codepipeline-learning/mini-pipeline-web:0.1.14 \
  --set ingress.host=pipeline.47.113.177.198.nip.io \
  --set tags.engine=true --set tags.mongo=true --set tags.web=true --set tags.buildkit=true \
  --set mongo.persistence.existingClaim=mongo-data --wait

# ===== 全新集群部署前置（helm 之外的环境事务，2026-11-10 决断 #16 盘点；chart 本体 helm install 即自举） =====
#  1. 镜像源绑定（必填）——image.repo/runner.image/web.image 三件 --set；构建侧 MP_REGISTRY=<推送端点>
#  2. ingress-nginx 控制器——chart 只渲染 Ingress 资源，不装控制器；ingress.host --set
#  3. 可选：节点层 /etc/rancher/k3s/registries.yaml——kubelet 拉 docker.io 加速 / 拉自部署 registry 产物加 endpoint（拉取侧 = 集群管理员职责）
#  （mongo PVC/密码/buildkit TLS 由 chart 自建；docker_build 产物仓库不内置——自起一个或登记外部 registry；本集群的自部署 registry = k3s-gitops/argocd 管辖，不在本仓）

# ===== runner 镜像（低频；runner 变更时跑，tag 须同步 chart values runner.image） =====
MP_REGISTRY=crpi-28fqgwft325ylccc.cn-heyuan.personal.cr.aliyuncs.com deploy/build-runner.sh 0.3.0

# ===== web 前端镜像（低频；前端变更时跑，tag 经 helm upgrade --set web.image=<registry>/<ns>/mini-pipeline-web:<tag> 绑定——values 默认空） =====
MP_REGISTRY=crpi-28fqgwft325ylccc.cn-heyuan.personal.cr.aliyuncs.com deploy/build-web.sh 0.1.14

# ===== 自举图纸落库（凭证活库不进仓，决断 #16） =====
MP_BASE=https://pipeline.47.113.177.198.nip.io \
MP_ACR_ADDR=crpi-28fqgwft325ylccc.cn-heyuan.personal.cr.aliyuncs.com \
MP_ACR_USER=nick9188231182 MP_ACR_PASS=<密码> \
  bash deploy/bootstrap-ensure.sh

# ===== 验收（活体七门已退役存档 tag acceptance-suite，恢复见 §2 验收资产；现存两件） =====
go test ./pkg/... -count=1                                                             # 11 包单测（repo 类需 port-forward mongo）
(cd frontend && MP_BASE=https://pipeline.47.113.177.198.nip.io npm run smoke)           # jsdom 冒烟 12 断言（⚠️ 单一引擎纪律 §9.20）

# ===== git 远端（GitHub，public；旧史存档仓只读，见决断 #19） =====
git push   # GCM 凭证桥已配在仓库 local config，裸敲即可（首次会弹 Windows 登录窗）
git config --get mini-pipeline.archive-repo   # 旧史存档仓地址（考古：临时 clone 它，永不 fetch 进本仓、永不 push tags）

# ===== 本机调试（引擎跑本机，连集群 mongo/创建集群 Job） =====
kubectl --context k3s-heyuan port-forward -n pipeline svc/mongo 27017:27017   # 窗口A
# 密码从 mongo-auth Secret 取（决断 #16 起 install 现场生成，不在仓库里）：
#   kubectl --context k3s-heyuan get secret mongo-auth -n pipeline -o jsonpath='{.data.password}' | base64 -d
export MONGO_URI="mongodb://root:<密码>@localhost:27017" K8S_CONTEXT=k3s-heyuan && go run .   # 窗口B
# （Windows/PS5.1 时代的引号坑全档在 §9；bash 下引号可靠，JSON 直接 -d 单引号串）

# ===== 集群 =====
kubectl --context k3s-heyuan get pods -n pipeline
kubectl --context k3s-heyuan get jobs -n pipeline        # 验收：mp-N-… 出现又消失

# ===== 直连集群里的 Mongo 看数据 =====
kubectl exec -i -n pipeline deploy/mongo -- mongosh -u root -p mini-pipeline-2026 --quiet <<'EOF'
db = db.getSiblingDB("pipeline")
printjson(db.getCollectionNames())
EOF

go test ./pkg/repo/ -v -run TestNextID   # 需先 port-forward mongo
```

## 6. 写代码时遵守的设计原则

1. **暂停的实现**：`stage.Status = pause` 写 Mongo → goroutine 直接 return（不 sleep 不占线程）→ 人调 release API 标记 `ManualExec.Executed = true` → 任务重跑 → `RunStages` 跳过已 passed 的 stage → 闸门放行。暂停多久都不丢。
2. **资源与数据分离**：K8s 资源是消耗品（结束/暂停时清理），Mongo 记录是档案（跑完不删，只受管理员保留策略约束）。
3. **状态聚合最差者胜**：cancelled(7)>failed(6)>pause(5)>running(4)>passed(3)>created(2)>skipped(1)——running 排在 passed 前（没跑完不许宣布成功），skipped 垫底（被刹车跳过的 job 不改变聚合结果）。
4. **ack() 全量写**：任务每前进一步把整棵树 ReplaceOne 写回，不做增量 patch。前提：`_id` 是插入前定好的自然键字符串（`<wf>-<taskID>`）——空 `_id` 插入会让 Mongo 生成 ObjectId 而结构体拿不回，ack 从此静默失效（M1 踩过）。
5. **变量渲染**：JSON marshal → 字符串 `{{.key}}` 替换 → unmarshal（M4 定型的"粗暴但有效"方案）。
6. **启动宣判，不复活**（M3.5）：进程重启后遗留的 running 档案宣判 cancelled（revoker 语义=system）、泄漏 Job 清掉，用户重跑即可——adopt 半跑任务树是不确定性的脏活。pause 例外：它是无需看护人的稳态，跨重启存活（有意为之的 durable 稳态）。
7. **构建 job 的真身是 freestyle + 步骤列表**（2026-11-03 定型）：步骤（git → shell 编译 → docker_build → …）在 **Pod 内**由引擎生成的驱动脚本顺序执行（失败且非 OnFailure 则跳过剩余步骤），步骤状态走日志 banner 信道（引擎幂等重放切分三级树，`pkg/step/logs.go`）。"分节点分界面"的数据源就是 workflow_task 里 stages→jobs→steps 三级状态树，前端无特殊机制。
8. **日志流=状态信道**（核心 CI/CD 落地，2026-11-04）：引擎把 steps 编译成 POSIX sh 驱动脚本（每步函数化 mp_step_N + mp_run 包装 + banner `===MP=== STEP-START/END <name> passed|failed exit=N`），Pod stdout 就是步骤状态信道（零新依赖零 RBAC）。引擎每 2s 刮 Pod 日志**幂等重放** banner 维护三级树；终态 SaveInfo 全量收割→切分→收口（AfterRun 门禁）→再 Clean 删 Job（**顺序铁律 Run→SaveInfo→Clean，先收割再销毁**）。护栏三件套（首行单流化 / say() 前置换行 / EXIT trap 补发末班 banner）由 `driver_roundtrip_test.go` 与 `logs_test.go` 回归钉死。

## 7. 模块地图（写对应模块前先读本仓实现与单测）

| 主题 | 本仓路径 |
|---|---|
| 领域模型 / 状态机 / 校验 | `pkg/models/`（status.go WorstOf 偏序 · workflow.go StepSpec · validate.go 保存门） |
| 驱动脚本 + banner 协议 | `pkg/step/driver.go` · `pkg/step/logs.go`（不变量钉在 driver_roundtrip_test.go / logs_test.go） |
| K8s 执行物 / Job 形状 | `pkg/k8s/build.go`（BuildJob 镜像三档 + 卷装配）· `pkg/engine/exec/`（Run→SaveInfo→Clean 三件套） |
| 变量渲染 | `pkg/engine/vars.go`（marshal → 替换 → unmarshal 单点） |
| chart 形态 | `deploy/chart/mini-pipeline/`（tags.* 组件开关 + connections.* 参数化 + required 守卫） |
| 分层纪律 / 依赖方向 | `pkg/arch/`（TestLayering 四规则 + doc.go 包图分层快照，决断 #20） |

写模块前先读对应包的现有实现与单测——不变量都钉在测试里。

## 8. 当前部署与验证态势（快照，2026-11-10）

- 集群 release：helm `mini-pipeline` **rev54**（rev54 = engine 0.7.14 + web 0.1.14 任务级共享工作区 + 统一代码源（PR #64：mp-ws-<wf>-<taskID> PVC 卷生命周期升格 + GOCACHE/GOMODCACHE 钉卷 + Unschedulable fail-fast + 清理四面一兜底 + 画布最左代码源列/checkout 合成 stage；活体验收：对照组旧世界 writer 必挂 / ws-a#3 全链 passed（pause 期间节点上裸 PVC 承载全部现场实锤 + reader 跨 job 读到 hello.txt + GOPROXY=off 离线构建 OK）/ 取消 202 → PVC Terminating / 杀引擎 Pod → Recover 日志 `reclaimed workspace pvc` / cordon 双 worker → 122s 人话报错；ws-a 旧档被 0.7.13 建时静默丢 source 字段——PUT 补声明后重跑即正，老图纸迁移 = 重新保存一次）；rev53 = web 0.1.13 docker_build 镜像名拆分（PR #63：编辑抽屉「左命名空间 + 右镜像名」，协议仍单字段 ns/name 首个 / 切分；镜像名/tag 字符集校验双侧收口——前端 collectErrors 红标 + 后端 ValidateWorkflow 400（正则兼容 HasRegistryPrefix 全名直通档）；目标仓库空选项过时文案「内置集群 registry」→「跟随默认仓库」；探针 5/5 + 现网抽屉解析回显实锤）；rev52 = web 0.1.12 前端性能三连修 + React Compiler（PR #62：JobDrawer key 去 name/usePolling deps 即时拉取/created_at 索引/编译器自动记忆化/更新式契约/errorElement，探针 8/8 实证）；rev51 = **事故 rev，已过**：§5 命令块从会话摘要照抄旧值 image.repo 缺 `/mini-pipeline` 触发全量重渲 + web 0.1.11 tag 撞车（§9.29）；rev50 = web 0.1.11——registry 表单文案对齐决断 #16 + registry 数据收口见下；rev49 = mongo-auth Secret 缺 helm 归属元数据被 fail-safe 拒绝 adopt——手建 Secret 补 `app.kubernetes.io/managed-by=Helm` label + `meta.helm.sh/release-*` 注解后重跑；同轮实录：image.repo 语义修正——模板直接 `repo:tag` 拼接，repo 必须含镜像名全路径，第一次 upgrade 镜像引用拼成 `codepipeline-learning:0.7.13` 拉不到；mongo 首次 spec 滚动暴露 RWO 死锁——RollingUpdate 双 Pod 同节点抢 WiredTiger 锁 exit 100，chart 补 `strategy: Recreate` + 手删老 Pod 解锁），chart **0.7.15** + 引擎 **0.7.14**（任务级共享工作区 + 统一代码源，决断 #17/#18；上代 chart 0.7.14/引擎 0.7.13 = registry 外部化 + buildkitd 白名单运行时 reconcile + 全仓零痕迹，决断 #16；上代 rev47/0.7.12 = 构建池 buildkit 化（#60 关闭未合并——本 PR 一并吸收），rev45 = ack 收严 #59）+ **web 0.1.14** + runner **0.3.0**；registry:2 七件套已 prune（registry-data PVC keep 注解幸存），**自部署 registry** 当日由 deploy/registry-external.yaml 起家（清单同日移交 k3s-gitops 由 argocd 管，见下条移交注记；原地认领旧卷）；upgrade 命令 = §5（镜像源三件+ingress.host --set 显式带全，mongo existingClaim 带回，registry-data 不再需要）
- **registry 数据收口（2026-11-10 rev50 当日）**：① `local` 记录地址曾被验证收尾时**直改库**写成裸名 `registry.pipeline.svc:5000`（绕过 BuildkitSyncHook——引擎日志 10h 内仅前端 15s 轮询 GET、零 API 写入实锤），而 CM 白名单还留着上次同步的 host：**滞后幸存态**——下一次 CRUD/引擎重启就会把它抖出白名单，明文推送即坏；已走 API PUT 补回 `http://` 前缀。② `local-ingress`（worker-1 IP:30500，11-06 建）从未真被删过（created_at 为证，用户记忆中的"已删除"不存在）——决断 #16 要退役的 IP 绑定反模式在数据层的活化石，已 API 删除；白名单随之收敛为仅 `[registry."registry.pipeline.svc:5000"]`，STS hash 变化 buildkit 双台滚动（CRUD→白名单→滚动的**删除向**活体补实）。③ 现稳态：registry 集合 = acr（裸域名 HTTPS 档）+ local（`http://` svc DNS 明文档，**is_default**——裸名图纸推自部署 registry）。④ 教训：**registry 记录是同步链的真相源，直改库 = 绕过钩子制造隐性漂移**——动数据必走 API，让 reconcile 看得见。⑤ 前端 RegistryList 四处过时文案对齐 #16（http:// 明文声明提示、裸名无默认报错点名）——随 web 0.1.11 上线
- **决断 #16 registry 外部化活体验证（2026-11-10 全绿，本 PR 当日）**：① **reconcile 自愈闭环**——引擎启动首拍把 CM 从模板兜底段覆写为全量生成体 + STS hash 注解 `mp/buildkitd-toml-hash` 触发 buildkit 双台滚动（pod creationTimestamp 实证）；registry CRUD（POST local + PUT local-ingress）一拍同步白名单（`[registry."registry.pipeline.svc:5000"]` + `[registry."172.29.237.250:30500"]` 双 http=true 按序在位）并再滚动；同内容复 PUT hash 不变不滚动（幂等实锤）。② **scheme 显式分派**——库里 acr（裸域名）/local-ingress（node IP→改 `http://` 前缀）双形态：HTTPS 档不进白名单零配置直推，http:// 档进白名单。③ **推送双档实锤**——bk-plain（裸名跟默认→buildkitd 推 `registry.pipeline.svc:5000/scratch-plain:t1` + `===MP=== OUTPUT image_full=` 宣告 + stage-2 经 localhost:5000 mirror 密封链拉回跑出 `plain-ok`）；bk-acr（显式引用→日志 `[auth] …pull,push token for crpi-…` 推 ACR 成功）。④ **负路径**——bk-bare-err（裸名无默认）failed 错误点名 `bare image name "scratch-bare" but no default registry registered`。⑤ **零痕迹门**——全仓 grep crpi/nick/密码/nip.io/节点 IP（AGENTS.md 例外）零命中；helm template 默认态被 required 守卫拒绝、--set 态 19 对象干净渲染。⑥ **11 包真跑全绿**（mongo 门控不再 skip：api 8s/repo 5s，registries_test 补 scheme 保留 + hook 恰 3 次断言）。⑦ **自部署 registry 闭环**——registry-external.yaml apply 起家（清单随后迁 k3s-gitops，见下条）+ registry-data 旧卷原地接续（catalog 历史仓库原样在），buildkit Pod 内 svc DNS 明文 wget `/v2/_catalog` 实锤
- **registry 管理权移交 k3s-gitops（2026-11-10 当日，本 PR 后续 commit）**：`deploy/registry-external.yaml` 出库（本仓零副本），registry:2 五件 + UI ingress 六件改由 k3s-gitops（argocd `registry` app，私有仓）管辖——复活 11-02 旧 app 形态（git 历史 69b9dba/3ab1eed，当年清单原文从历史找回照抄样式，目录布局 apps/registry.yaml + registry/ kustomization 同 net-check）；**认领零滚动**（服务端 dry-run 四件 unchanged 在先，registry Pod 13h 未动——README 惯例预期的 tracking 标签滚动都没触发）；PVC `helm.sh/resource-policy: keep` 注解随清单进 gitops（谁接手都不许删数据卷，§9.23 教训平移）；UI ingress 复活（#16 prune 后 UI 只能 port-forward，registry-ui.47.113.177.198.nip.io 直访恢复：页面 200 + /v2 反代 catalog 12 仓）。**附带抓个真雷：nginx 的 resolver 指令不吃 resolv.conf search 域**——UI 反代目标写短名 `registry` 必 NXDOMAIN（/v2/ 反代 502，而 Pod 内 wget 短名一直正常——雷被 ingress 缺席捂了 13h），修正为 FQDN `registry.pipeline.svc.cluster.local`（回归 11-02 原版写法；gitops 提交 3105818 + bd8e97e，registry-ui 因此滚了一次，registry 本体 Pod 全程未动）。Engine 零感知：svc DNS / mongo 记录 / buildkitd 白名单全没动。回滚姿态 = gitops 删 app 清单（无 finalizer，2026-11-08 新约定）→ 业务资源原地回手工模式
- **遗留清理项**：ACR 测试镜像 `codepipeline-learning/bk-plain-probe:t1`；**Playwright 自动化选择器备忘（PR #63 当日实证）**：antd AutoComplete 的 placeholder 是 `<span class="ant-select-selection-placeholder">` 非 input 属性、真实类名 `ant-select-auto-complete` 非 `ant-auto-complete`——getByPlaceholder 定不到它，用 `.ant-select-auto-complete input`（本次 scratch 残留，同 bk-live-probe 档可忽略）；Docker Desktop WSL 集成挂载 `/mnt/wsl/docker-desktop` 断链时 docker 命令全灭——启动 Docker Desktop.exe 后 20s 自愈（本机怪癖备忘）；**bootstrap 自举在 PR 合并前必然失败**（图纸 checkout master = 0.7.11 代码，其 docker_build 脚本指向已退役的 dind 世界——2026-11-10 实测 gate/checkout passed、image 步 exit=125 `unknown flag: --name`，属"旧代码打新集群"的预期负回归；合并后 master 自举即恢复，engine 未被 patch 无恙）。**2026-11-10 凭证/自举复查补钉**：合并后自举其实还差一颗钉——构建 job 的 `spec.image=golang:1.27-alpine`（非 runner 宿体）toolchain 步只 `apk add docker-cli`，而 Alpine 的 buildx 是独立包 `docker-cli-buildx`——buildkit 时代 docker_build 脚本的 `mp_bk`（`docker buildx create`）当场 `unknown command: docker buildx`（本机同款镜像实证；0.7.12 时代的 bk-* scratch 回归全走 runner 宿体没暴露这雷）；toolchain 步已补 `docker-cli-buildx`（本 PR），活库图纸经 **PUT** 同步（PUT 全量替换不动任务档案——ensure 脚本的 DELETE 前置会级联删 bootstrap 历史账本，改图纸别走那条）
- **buildkit 构建池活体验证（2026-11-09 全绿，dind→buildkit 迁移当日）**：mTLS 双向实锤（服务端 `[::]:1234` TLS 监听在；无证书裸连被拒 `error reading server preface: EOF`；prune CronJob 手动触发 buildctl 带证书双副本都通）；**内置档密封链闭环**（内联 Dockerfile busybox 经 daocloud mirror 构建 → `--push` 推 `$MP_NODE_REGISTRY`/scratch-bk-img:t1 → kubelet 经 localhost:5000 mirror 拉回跑出构建期烘焙的 `buildkit-live-ok`；⚠️ 决断 #16 起 $MP_NODE_REGISTRY 兜底退役，密封链的拉取侧 mirror 语义仍在但推送侧全走 registry 记录）；**引用档 ACR + auth 传递实锤**（registry 记录凭证 → login 子 shell → 日志 `[auth] …pull,push token for crpi-…` 证明 buildx 把 CLI config.json 凭证收进 SolveRequest 传 buildkitd → push 成功；manifest 复查 `content-type: application/vnd.oci.image.manifest.v1+json` 单 manifest 非 attestation index——`--provenance=false` 防线成立，digest 与推送一致）；**fnv 分散**（scratch-bk/1→buildkit-1、scratch-bk-acr/1→buildkit-0，buildctl du 两台各 6.84MB 一台一发）；runner 0.3.0 镜像内 `docker buildx version` v0.38.2 实测。遗留：ACR 测试镜像 `codepipeline-learning/bk-live-probe:t1`（验证期残留可忽略）
- **buildkit 构建池活体验证（2026-11-09 全绿，dind→buildkit 迁移当日）**：mTLS 双向实锤（服务端 `[::]:1234` TLS 监听在；无证书裸连被拒 `error reading server preface: EOF`；prune CronJob 手动触发 buildctl 带证书双副本都通）；**内置档密封链闭环**（内联 Dockerfile busybox 经 daocloud mirror 构建 → `--push` 推 `$MP_NODE_REGISTRY`/scratch-bk-img:t1 → kubelet 经 localhost:5000 mirror 拉回跑出构建期烘焙的 `buildkit-live-ok`）；**引用档 ACR + auth 传递实锤**（registry 记录凭证 → login 子 shell → 日志 `[auth] …pull,push token for crpi-…` 证明 buildx 把 CLI config.json 凭证收进 SolveRequest 传 buildkitd → push 成功；manifest 复查 `content-type: application/vnd.oci.image.manifest.v1+json` 单 manifest 非 attestation index——`--provenance=false` 防线成立，digest 与推送一致）；**fnv 分散**（scratch-bk/1→buildkit-1、scratch-bk-acr/1→buildkit-0，buildctl du 两台各 6.84MB 一台一发）；runner 0.3.0 镜像内 `docker buildx version` v0.38.2 实测。遗留：ACR 测试镜像 `codepipeline-learning/bk-live-probe:t1` 与 registry:2 `scratch-bk-img`（验证期残留同 dind-test 档，可忽略）
- **ack 收严活体验证（2026-11-09 全绿）**：rev45 滚动后 healthz ok + scratch 图纸（shell 步骤）run→queued→派发→running→passed 全链无回归（banner 协议 + 步骤输出完好）；负路径以单测注入为准（ackStrict 5 次尝试/退避节拍 0.5-4s/ErrNotFound 不重试、Enqueue 失败回滚 created、UpdateTask 删后 ErrNotFound——mongo 门控）——集群真断 Mongo 会拖死全家，破坏面不成比例不做；顺手活体证据：网页侧 run+cancel（202 一拍收口）在 0.7.11 亦正常。附带修了存量测试隔离缺陷：engine/repo 两包 batch_test 同 creator="batchtest" 并行打同一 mongo 互相串台（creator 改名隔离）
- **批量删除活体验证（2026-11-09 全绿）**：跨库总账本实锤（GET /api/tasks total 451、created_at 降序 bootstrap#7 居首）；任务批量删 scratch 实锤（passed 删 / running 拒绝报「cancel it before deleting」/ 重复与不存在幂等，GET 404 vs 200 现场核对）；工作流批量删级联实锤（图纸 404 + 档案 total 0 + cron 零残留）；取消→删档→单删级联清理路径亦走通，batchdel scratch 全清零。单测 11 包全绿 + repo/engine 批量集成测试 5 项（mongo 门控），smoke 7 断言 PASS（新增两页勾选框/批量删除/总账本断言）
- **内联 Dockerfile 活体验证（2026-11-09 全绿）**：scratch 流水线单 docker_build 步骤纯 `dockerfile_content`（正文故意含单引号 `it's ok` + 多行）→ 任务 passed，Step 3/4 RUN echo 原样输出——b64 落盘 roundtrip 实锤，镜像推 `172.29.237.249:30500/scratch-inline-df:t1` 后 scratch 级联清零；单测钉住内容 roundtrip/内联赢路径/引用档 materialize 先行/空内容纯路径四语义；smoke 12 断言 PASS（编辑器真事件链：新建页→抽屉→加步骤→竖排字段→内联终端面→shell 终端面）；真 Chrome 截图双绿（headless-shell 容器缺 CJK 字体显示方块，用户浏览器正常）
- **chart 自举活体验证（2026-11-09 全绿）**：helm template 双态实锤——默认渲染出 mongo-data/registry-data 两 PVC（全新集群自建）、--set 带回存量名则 PVC 不渲染且 claimName 照旧；rev41 no-op 升级 7 Pod 全员无重启、PVC 仅存量两个、healthz + workflows 数据无恙。全新集群三件 helm 之外前置（registries.yaml/ingress-nginx/环境绑定键）清单在 values.yaml 顶部注释块与 §5
- **dind 活体验证（2026-11-08 全绿；0.7.12 起 dind 被 buildkit 池替换，此段为历史记录）**：dockerd 27.5.1 双台 Running（hostname 反亲和各落一 worker）；`docker info` 实锤 Registry Mirrors=daocloud + insecure 172.29.237.0/24；双 workflow 并发构建各落一台 dind（fnv 分散实锤）、busybox 基镜像两台均经 mirror 拉取；裸名档推 registry:2（digest 828db035…）+ ACR 引用档推 ACR（digest c80c51f3…，classic 无 provenance 报错——§9.9 不复发）；kubelet 经 localhost:5000 mirror 拉起 dind 产物跑通（dind-use passed，密封链路闭环）；mTLS 负面实锤（无证书裸连被 daemon 拒 `Client sent an HTTP request to an HTTPS server`）；任务档案密值 0 命中（仅 login 固定警告文案）；job Pod hostPath 卷 0。entrypoint 陷阱见 §9.25
- **registry 能力活体验证（2026-11-07 全绿）**：引用链路（registry-ref-demo → catalog 见 registry-demo）、默认语义（is_default 置位后裸名 default-demo 实推 172.29.237.250:30500；取消后 bare-demo 回落本节点 hostIP:30500）、自举双跑（bootstrap-4 引用推 ACR digest eee6a91b…；bootstrap-6 含 #50 gate 修复全链 passed——Pod 内自行 login，节点手工 login 不再是前置）。单测：`go test ./pkg/...` 全绿（+exec 解析表测/step 转义用例/api CRUD 回环含共享库快照恢复）。⚠️ 当时稳态 = 无默认记录（acr/local-ingress 均 null）——已被决断 #16 后的现稳态取代：local 为默认、裸名推自部署 registry（见上文 rev50 registry 数据收口）
- **远程主机活体验证（2026-11-08 全绿，PR #55）**：真节点 worker-1（172.29.237.250，root + 现有 pem）录成 `test-node` → ssh-live 图纸双步 passed（`ssh test-node hostname` 实回 k3s-worker-1，accept-new 自动收 ED25519 主机键；`MP_H_TEST_NODE_IP/PORT/USER` 环境变量在位）→ ssh-ghost 图纸 failed 且错误点名 `resolve host "no-such-host": document not found` → **GET 任务档案 grep 密钥材料 0 命中**（workflow_args 只存 `["test-node"]` 名字引用，凭证不落档案活体坐实）。验证残留已清（两图纸 + host 记录皆删）
- 引擎入口 `https://pipeline.47.113.177.198.nip.io`：ingress 单条规则指 **前端 Pod**，nginx 反代 `/api`、`/healthz` 到引擎 service（统一入口）；健康门 `curl -k …/healthz` 不变；引擎自身无前端路由（API-only）
- **单引擎纪律**：集群引擎自带派发器，本机 `go run` 引擎与它共用 mongo 时会抢同一条队列（§9.20）——本机引擎只作调试；活体验收已退役（§2 验收资产，存档 tag acceptance-suite），若恢复一律打集群 ingress
- 改动后的标准循环 = build-push（引擎）/ build-web.sh（前端）→ helm upgrade（--set 显式带全）→ go test + smoke（命令在 §5）；bootstrap 自举图纸可整链发布引擎自身（tag=bootstrap）
- 前端迭代循环：`cd frontend && npm run dev`（vite proxy 指集群 API）→ `npm run build` → `deploy/build-web.sh <tag>` → helm upgrade --set web.image… 或改 values 默认 tag

## 9. 案例存档 / 已知坑

> 编号是全仓引用锚（代码注释与 chart values 按号引用），已解决的坑已清理（2026-11-08 用户拍板删档）——**删后编号不复位**，空号 = 已清档；现存条目都是仍在生效的环境怪癖或纪律。

2. **keepalived 底稿未进仓**：`/etc/keepalived/keepalived.conf` 注释里写"仓库底稿:system/keepalived/"，但 k3s-gitops 仓库（github.com/Edward-258/k3s-gitops，私有）里没有该目录——节点层配置是单点，dummy0 改动目前只活在那两台机器上。建议补进 gitops 仓库。
3. Windows PowerShell 5.1 四坑已入档：中文路径的 .ps1 必须无中文或参数传入（编码）；curl JSON 必须 `-d @file`（吞引号）；远程 bash 里的 `sed '$d'` 要写成 `\$d`；**SSH 远程命令内层引号必被剥——传配置文件/脚本一律 base64**（`echo <b64> | base64 -d > file` 或 `| bash`，本项目已踩三次）。WSL 侧再添两坑（2026-11-03）：ssh 会话**偶发僵死**（scp 已落地但远端命令链卡在起跑线，节点上无任何进程，新开 ssh 一切正常）——杀掉重跑即好，构建脚本卡住先 `ps aux | grep ssh` 排查；想顺手清僵尸时 `pkill -f 'sshd.*notty'` 会把**自己这条**无终端会话一起杀掉（自杀），别用。
6. `flannel-backend` 是 k3s **server 专属**参数，agent 配了会起不来（flag not defined）；worker 的 backend 由 master 下发。
7. **手拼 JSON 必数错括号**——复杂 body 一律 python json.dumps 或落盘后 `jq -e .` 预检（PS5.1 与 bash heredoc 都真踩过，ensure_wf 预检由此而来）；批量文本操作后必 `git diff` 自检（`Set-Content -Encoding ASCII` 曾把中文全毁成 `?`）。
9. **ACR 个人版推送三坑**（2026-11-03，引擎镜像迁 ACR 实录）：① 新版个人版是"独立实例"形态，专属域名带 `crpi-` 前缀（本实例 `crpi-28fqgwft325ylccc.cn-heyuan.personal.cr.aliyuncs.com`）；老共享域名 `registry.cn-heyuan.aliyuncs.com` 对它一律 **Forbidden Host**（端点活着但不是它的家，别被 401 挑战骗了）。② ACR 拒收 BuildKit 默认生成的 provenance/attestation 附加清单，报 `unknown manifest class for application/vnd.oci.empty.v1+json`；`docker build --provenance=false` 即解，层已推上去不受影响（重推全命中 Layer already exists）。③ 认证是 Bearer token 流：`curl -u` 直怼 `/v2/` 永远 401（不代表凭证错，我为此冤枉猜过三种用户名格式）；正经测法 = 先去 `dockerauth-cn-heyuan.aliyuncs.com/auth?service=…&scope=…` 换 token，且返回字段叫 `access_token` 不是 `token`（jq 取 .token 会拿到 null → 报 Forbidden Host 的迷惑性错误）。仓库无需预建，push 自动创建。aliyun CLI 的 `cr` 元数据 API（老 ROA 接口）本版本调不通，registry HTTP API 完全够用。④ **helm chart 存不进免费版**（2026-11-03 实测）：`helm push` OCI 形式报 `unknown manifest class for application/vnd.cncf.helm.config.v1+json`——manifest 白名单只认镜像类，chart 托管是 EE 能力。chart 真相源留在 git 仓库即可，镜像走 ACR / chart 走 git 本就是标准分工。
13. **本机 Docker Desktop 拉不动 docker.io**（2026-11-04）：WSL 侧 docker pull 报 `auth.docker.io/token ... EOF`——TLS 握手即被掐（SNI 阻断），非瞬时抖动。解法（不动 Docker Desktop 全局配置）：经 mirror 拉基镜像再 retag，`docker pull docker.m.daocloud.io/library/golang:1.26-alpine && docker tag ... golang:1.26-alpine`，构建走本地缓存不再碰 docker.io；worker 节点的 daemon.json 自带 mirror 列表不受影响。
15. **jq 查含点字面键要用 `["a.b"]`**（2026-11-05，test-mvp1.sh 两连红真凶）：GlobalContext 收割键是 `<job>.<output>` 字面含点（如 `build.MSG`），`jq '.global_context.build.MSG'` 把它当两层路径 → null。正确写法 `.global_context["build.MSG"]`。引擎数据没问题（下游渲染已证），断言先红时先怀疑查询语法。
20. **双引擎抢同一个队列**：本机 `go run` 引擎与集群引擎共用同一个 mongo——两边派发器在同一条 queued 队列上赛跑（本机排的队被集群引擎抢走跑、断言时序全乱）。**本机引擎只作调试，一切活体验收打集群 ingress**；确需本机独跑先 `kubectl scale deploy/mini-pipeline --replicas=0`。
22. **npm 断网残局**：CDN 大量 ECONNRESET 时 `npm install --maxsockets=3 --fetch-retries=5` 可通；失败 install 会留**空壳 @types 目录**（目录在而文件空，tsc 报 TS2688）——`rm -rf node_modules/@types/*` 后重装，npm 不会自愈。（浏览器走查的降级正解 = jsdom 真执行，已沉淀为 `frontend/e2e/smoke.mjs`，手法见 §2 前端质量门）
23. **argocd→helm 反向接管 registry：finalizer 复活链 + 级联 prune 事变 + ingress 域名对 Pod 内 docker 不可推**（2026-11-07 深夜，registry 管理权收回实录，三个教训都带血）：
   - **事变链**：摘 registry app 级联 finalizer（首次 patch 被 argocd controller 5s 内加回 → scale sts/argocd-application-controller=0 冻结窗口内摘成）→ git rm + push k3s-gitops → **root app selfHeal 用 repo-server 的陈旧 git 缓存把 Application 复活**（带 finalizer 原样重建）→ controller 恢复后下一拍重新 fetch 发现 manifest 已删 → **prune 级联开火：六件无状态对象全灭**（deploy/svc/ingress ×2 + Pod）。**PVC 幸存**——`helm.sh/resource-policy: keep` 注解 argocd 级联也认。复活根因：删 git 清单、删 Application、controller reconcile 三者不原子；陈旧缓存的 selfHeal 窗口就是雷。修法：git 已删后复活链自然断，helm upgrade 渲染七件套原地重建，catalog 四仓旧镜像原样在（mini-demo/mini-pipeline/mini-runner/mp-daemon-probe），registry Pod 仍落 worker-1（local-path PV 节点绑定）。教训：**反向接管 argocd 管的负载，正确序 = 摘 finalizer（冻结窗口内）→ 删 Application 对象 → 再动 git**；顺序反了就是这次的 prune 扫射。PVC keep 注解是最后一道保命符，谁接手都不许省。
   - **registry ingress 域名端点对 Pod 内 docker 不可推**：Pod 内 docker 对 `registry.<EIP>.nip.io` 先试 https（443 端口假证书 ingress.local）→ x509 报错**不触发** HTTP 回退（docker 只对"连接层失败"回退，对"TLS 层失败"不回退）——build 成功 push 失败。而 NodePort `172.29.237.250:30500` 是纯 HTTP 监听 → 连接层非 TLS → 自动回退 HTTP 成功。教训：**给 registry 选推送端点，选"明文端口"不选"HTTP-only ingress"**；docker 的 insecure fallback 语义是 per-connection 不是 per-hostname。
   - **/root/.docker hostPath 是只读挂载，Pod 内 docker login 写不进去**：login 要持久化 config.json，只读卷上必炸。修法：烘焙链 `DOCKER_CONFIG=/tmp/.docker` 子 shell 包裹（凭证只活在 Pod 临时目录，不写回节点——本来也不该写回去）。教训：给"挂载进来的凭证目录"做写操作前，先查挂载的读写形态。
24. **CRUD 回环测试对共享库的"库里只有我"假设必炸**（2026-11-07 深夜，bootstrap-5 gate 首红真凶）：TestRegistriesCRUD 本地跑全绿（当时库空），bootstrap Pod 里跑就挂——同一个集群 mongo，Pod 跑的时候库里已有 acr/local-ingress 两条生产记录，DeleteRegistry 的"选举首个剩余补位"选中了 `_id` 排序最前的 `acr` 而不是测试断言的 A。双修（PR #50）：① **语义层**——删默认不再选举，回落"无默认"稳态（设计理由见 repo/registry.go 注释）；② **测试层**——共享库上的回环测试必须快照/恢复别人的状态，永不断言"全库只有我的数据"（把选举断言改成验证自身记录的终态）。教训：**集成测试连的库是活的**——本地绿不代表 CI/Pod 里绿，断言对象永远收敛到"自己创建的数据自身"，环境假设全部显式化（快照/恢复）。
25. **官方 dind 镜像 entrypoint 的默认参数注入陷阱**（2026-11-08，dind 首部署 CrashLoopBackOff 抓的）：`dockerd-entrypoint.sh` 判定「无参数或首参以 `-` 开头」就**抢先 prepend 自己的 dockerd 参数**——镜像内置 `DOCKER_TLS_CERTDIR=/certs` 使其走自签证书分支（`mkdir /certs/ca`，撞死在我们的只读证书卷上）；把 env 置空则改走明文分支抢注 `--host=tcp://0.0.0.0:2375`（违背"明文 2375 不存在"）。另一种姿势是摘掉 env 让它在可写根文件系统空转（自签证书被后续 args 覆盖）。**我们的解法更直接：`command: ["dockerd-entrypoint.sh", "dockerd"]` 显式以 `dockerd` 为首参**——默认注入整段跳过，entrypoint 的 iptables 探测与 docker-init 僵尸回收照常保留，dockerd 参数完全自持。附带发现：helm upgrade --wait 超时中断后 sts 模板已更新但老 Pod 未滚动（Pod spec 不可变，CrashLoop 的还是旧命令）——`kubectl delete pod` 强制重建即好。教训：给"带智能 entrypoint 的官方镜像"传参前，先 cat 它的 entrypoint 看谁拥有 dockerd 参数的所有权。
26. **`go test ./pkg/...` 的包级并行让两包集成测试在同一 live mongo 上互相串台**（2026-11-09，ack 收严 PR 全量回归首红真凶）：repo/batch_test.go 按 `creator="batchtest"` 断言跨库列表"恰 4 条"，而 engine/batch_test.go 的种子也用 `creator="batchtest"` 打同一个集群 mongo——`go test ./pkg/...` 各包是**并行进程**，engine 的 2-3 条种子恰好在 repo 数数窗口内在场 → total=6 假红。之前多轮全绿纯属时序运气（engine 侧测试先跑完）。修法：engine 侧 creator 改 `batchtest-engine` 隔离（其断言全按显式 ID 点名，不依赖 creator 值）。教训：**跨包共享同一个活库的集成测试，隔离键（creator/工作流名）必须包级唯一**；"恰 N 条"型断言对并行兄弟包的种子永远开放。
27. **蓝图步骤字段是扁平的——嵌套 `spec.script` 被 json 静默吞，下游症状是"exit code 2 零输出"**（2026-11-09，活体验证时的乌龙排查实录）：手写 scratch 蓝图把脚本写成 `{"name":"step-1","type":"shell","spec":{"script":"echo ..."}}`，而 Step 模型的合法形状是**扁平** `{"name","type","script"}`——json.Unmarshal 对未知字段静默忽略，入库步骤空壳。下游连锁：driver 生成 `mp_step_1() { }` **空函数体在 ash 里是语法错**，`sh -c` 整串预解析失败 → **连首行 `exec 2>&1` 都没执行** → 零输出 + exit 2 + 步骤被 finalize 置 skipped（无 banner 可切分）。诊断路径：别看任务档案猜，直接抓活 Job 的 `command/args` 看 driver 原文——空函数体一眼定罪。教训：① 手写蓝图 JSON 前先 `jq` 一个仓库正规图纸对照形状（bootstrap-workflow.json）；② "exit 2 + 零输出"的候选清单里加上"空步骤函数体"——它跳过了单流化 prelude，是唯一连 stderr 都看不见的失败形态。

28. **helm --wait 超时标 failed ≠ 部署失败——资源已铺上，原命令重跑即收敛**（2026-11-09，buildkit 池首部署实录）：worker-2 经节点 mirror 首拉 `moby/buildkit:v0.34.2`（108MB）花了 3m45s，`--wait` 默认 5m 预算被吃爆 → release 标 `failed`，但 StatefulSet/Pod/Secret 已全部创建、buildkit-0 甚至已 Ready——只是"等待收敛"这一步超时。修法：等慢节点 Pod Ready 后**原命令重跑一遍**（无变更纯等，秒级）→ 新 rev `deployed`。教训：给带大镜像新组件的 upgrade 预留 `--timeout`（或先在慢节点手动拉镜像），failed rev 不用回滚。附带两个小坑：① ACR 查 manifest 的 token 舞步——`www-authenticate` 挑战里的 `service` 参数是 `registry.aliyuncs.com:cn-heyuan:26842` **不是仓库域名**（拿域名当 service 换的 token 一律 invalid_token）；匿名 token 只对公开仓放行，ACR push 自动建仓默认私有，查私有仓必须 `-u` 带凭证换 token。② `kubectl run --overrides` 里嵌 `$(...)`/`$var` 的 JSON 会被 shell 转义掀翻（Invalid JSON Patch）——复杂探针直接落 manifest 文件 `kubectl apply -f` 更稳。
29. **版本 tag 复用 = 静默覆盖 + 会话摘要照抄旧命令 = 模板漂移重渲**（2026-11-11，web 0.1.11 部署实录三连）：① `deploy/build-web.sh 0.1.11` 对**已存在**的 ACR tag 静默覆盖（registry tag 只留新 digest，旧内容只剩活 Pod 的 imageID 引用）——发版前先 `helm get values --revision <上代> | grep web` 对一眼已用版本，撞了就跳号；② helm upgrade 引擎 values 无意漂移（image.repo 少 `/mini-pipeline`，required 守卫只查非空不查路径形状）→ deployment 模板变更 → 新 Pod ImagePullBackOff，但**旧 Pod 原地健在**——引擎服务零中断，修正 values 重跑即回原 RS（模板还原 = 控制器复用旧 RS，无回滚动作）；③ 根因是照抄了会话摘要里的陈旧 §5 命令——**部署命令执行前必重读 AGENTS.md 原文**，摘要会滞后一个 rev。
