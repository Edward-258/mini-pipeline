// Package config —— 环境配置装配（leaf 包：env 变量 → Config 结构，被所有层消费）。
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	MongoURI     string // MongoDB 连接串（MONGO_URI 整串 env 优先；否则 MONGO_HOST+MONGO_ROOT_PASSWORD 组拼——chart 把密码从 mongo-auth Secret 注入，仓库零静态密钥）
	HTTPAddr     string // API 监听地址
	K8SNamespace string // K8s Job 所在 namespace（M1+）
	K8SContext   string // 仅本机调试用：kubeconfig 的 context 名；引擎在集群内跑时留空走 InClusterConfig
	RunnerImage  string // 步骤执行器宿体镜像（git/docker_build 步骤的默认容器；chart values runner.image 注入，决断 #16 起必填——Job 创建时守卫报错）

	MaxRunningTasks int    // T1.6 全局并发上限（0 = 停止派发只收尾，维护用）
	EngineURL       string // Pod 内访问引擎的地址（T2.4 archive 上传用；本机调试时 Pod 打不到宿主，属已知边界）

	BuildkitAddr     string   // 共享 buildkitd 构建池地址（决断 #15 的 buildkit 演进，docker_build 的执行引擎）；空 = 按 buildkit-<i>.buildkit 稳定 DNS 拼（fnv task 亲和）
	BuildkitReplicas int      // buildkit StatefulSet 副本数（chart values buildkit.replicas 注入；fnv 亲和的模数）
	BKMirror         string   // buildkitd.toml mirror 段（MP_BK_MIRROR；SyncBuildkitRegistries 全量生成 toml 时消费，决断 #16）
	PullSecrets      []string // Job Pod 拉私有镜像的 imagePullSecrets 名单（MP_PULL_SECRETS 逗号分隔；空 = 全公开镜像匿名拉，决断 #16 起凭证不进仓）

	WorkspaceShared bool   // 决断 #17：任务级共享工作区 PVC（同任务全 job 挂同一卷，job 间文件/依赖缓存直通）；false = 回落每 Job emptyDir 旧世界
	WorkspaceSize   string // 工作区 PVC 容量声明（local-path 薄配置额不预占；峰值 ≈ 并发任务数 × 此值，节点盘扛不住就调小）
}

func Load() *Config {
	cfg := &Config{
		// 决断 #16 零痕迹：MONGO_URI 整串 env 优先（外部 mongo / 本机调试 port-forward 用）；
		// 缺省按 MONGO_HOST+MONGO_ROOT_PASSWORD 组拼——集群内 chart 把密码从 mongo-auth
		// Secret（install 现场生成）经 env 注入，仓库里没有任何连接串默认值。
		// 本机调试姿势：port-forward 后设 MONGO_URI 整串（密码 kubectl get secret mongo-auth 取）。
		HTTPAddr:     getenv("HTTP_ADDR", ":8080"),
		K8SNamespace: getenv("K8S_NAMESPACE", "pipeline"),
		K8SContext:   getenv("K8S_CONTEXT", ""),
		// 决断 #16 零痕迹：runner 镜像 = 部署期 chart values --set 注入（必填，Job 创建时守卫）；
		// 本机调试时手动 export RUNNER_IMAGE=busybox 之类覆盖。
		RunnerImage: getenv("RUNNER_IMAGE", ""),

		MaxRunningTasks: getenvInt("MAX_RUNNING_TASKS", 3),
		// T2.4：archive 步骤从 Pod 里 curl 引擎。默认 = 本 namespace 的引擎 svc
		// （chart 部署形态的服务名 <release-name>.<ns>.svc）；本机 go run 调试时 Pod 打
		// 不到宿主引擎，archive 上传的活体验证需引擎在集群内跑（或 MP_ENGINE_URL 指到
		// 可达地址），API 层回环由单测/验收脚本覆盖。
		EngineURL: getenv("MP_ENGINE_URL", "http://mini-pipeline."+getenv("K8S_NAMESPACE", "pipeline")+".svc.cluster.local:8080"),

		// 决断 #15（buildkit 演进，2026-11-09 替换 dind）：docker_build 的构建/推送全在共享
		// buildkitd 容器里执行，Job Pod 经 buildx remote driver 打过来。默认按 buildkit-<i>.buildkit
		// headless svc 的 per-pod 稳定 DNS 拼（引擎侧 fnv task 亲和，非 Service 层均衡——
		// fnv 零状态哈希，无外部副本索引）；BUILDKIT_ADDR 显式覆盖 = 直连外部
		// buildkitd（自管安全，不注客户端证书）。
		BuildkitAddr:     getenv("BUILDKIT_ADDR", ""),
		BuildkitReplicas: getenvInt("BUILDKIT_REPLICAS", 1),
		BKMirror:         getenv("MP_BK_MIRROR", ""),
		PullSecrets:      splitCSV(getenv("MP_PULL_SECRETS", "")),

		WorkspaceShared: getenvBool("MP_WORKSPACE_SHARED", true),
		WorkspaceSize:   getenv("MP_WORKSPACE_SIZE", "2Gi"),
	}
	if uri := getenv("MONGO_URI", ""); uri != "" {
		cfg.MongoURI = uri
	} else {
		cfg.MongoURI = fmt.Sprintf("mongodb://root:%s@%s",
			os.Getenv("MONGO_ROOT_PASSWORD"), getenv("MONGO_HOST", "mongo.pipeline.svc.cluster.local:27017"))
	}
	return cfg
}

// splitCSV —— 逗号分隔名单解析（MP_PULL_SECRETS）；空串/空白段剔除。
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getenvInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

func getenvBool(k string, def bool) bool {
	if v := os.Getenv(k); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
