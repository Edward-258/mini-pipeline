# mini-pipeline

从零实现的微型 CI/CD 发布工作流引擎：**图纸/实例分离 · durable 暂停 · Job = K8s Job**。
单体 Go 服务跑在 k3s 里（helm 一键部署），React 前端独立 Pod，统一入口经 ingress-nginx。

> 最小可用范围的实现：单集群、全局信任域，代码宁缺毋滥。

## 界面预览

| 画布式流水线编辑器 | 任务详情与分步日志 |
|:---:|:---:|
| （截图待补） | （截图待补） |

<!-- 截图占位：图片就位后替换上行两个单元格（建议放 docs/images/ 下引用） -->

## 能力一览

- **工作流**：stage → job → step 三级状态树；并行/串行编排，最差者胜聚合 + 失败刹车；超时/重试/错误策略；取消、克隆、整任务重试
- **durable 暂停**：闸门 stage 落库即暂停，引擎重启档案不丢；原生审批（any/all）或 release 放行续跑
- **统一代码源**：克隆是流水线的声明、不属于任何 job；任务级共享工作区 PVC——同任务全部 job 见同一份代码与缓存
- **CI 步骤**：shell / 编译构建（类型即工具链声明，强制 runner 宿体）/ 镜像构建（共享 buildkitd 构建池，mTLS + fnv 任务亲和吃层缓存，产出 `image_full` 供下游引用）/ junit 测试报告（通过率门禁）/ 产物归档（GridFS 下载）
- **CD 部署**：deploy job 直改 Deployment 镜像并轮询 rollout；环境（env）、镜像仓库（registry）、远程主机（host/ssh）三类资源管理，凭证不落任务档案
- **触发与协作**：手动 / cron / webhook（GitHub·Gitee HMAC 验签）；任务终态 IM 通知（飞书/钉钉双加签）
- **自举闭环**：内置图纸「用流水线发布流水线自身」——集群现跑的引擎就是它自己构建、推送、发布的

## 技术栈

| 层 | 选型 |
|---|---|
| 引擎 | Go · gin · mongo-driver · client-go（单体，依赖单向向下分层） |
| 执行 | K8s Job + POSIX sh driver——Pod stdout 即步骤状态信道（banner 协议，零新依赖零 RBAC） |
| 构建 | 共享 buildkitd 池（StatefulSet + headless DNS + 每日 prune），runner 只当 buildx mTLS 客户端 |
| 存储 | MongoDB：图纸 / 任务档案 / GridFS 产物 |
| 前端 | React 18 · TypeScript strict · antd 5 · Vite，独立 nginx Pod 反代 /api |
| 交付 | helm chart：引擎 / mongo / buildkit / 前端一键全家桶 |

## 快速开始

```bash
helm install mini-pipeline deploy/chart/mini-pipeline -n pipeline --create-namespace \
  --set image.repo=<registry>/<ns> \
  --set runner.image=<registry>/<ns>/pipeline-runner:<tag> \
  --set web.image=<registry>/<ns>/mini-pipeline-web:<tag> \
  --set ingress.host=<域名>

curl -k https://<域名>/healthz     # 引擎入口
open https://<域名>                # 浏览器直接开
```

镜像源与域名全部部署期 `--set` 绑定——仓库零痕迹，不含任何端点与凭证。完整前置清单见 `values.yaml` 注释块与 [AGENTS.md](AGENTS.md)。

## 设计取舍

三条灵魂（及格线，缺一不算达标）：**图纸/实例分离**（task 内嵌完整定义快照）、**durable 暂停**（落库 + goroutine 退出）、**Job = K8s Job**（主控只创建/轮询/收割/清理）。更多设计与工程实录见 [AGENTS.md](AGENTS.md)。
