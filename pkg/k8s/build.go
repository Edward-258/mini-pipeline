package k8s

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"pipeline/pkg/config"
	"pipeline/pkg/models"
)

// K8s 资源名约束：dns-1123（小写字母数字与 '-'，≤63 字符）
var jobNameSanitizer = regexp.MustCompile(`[^a-z0-9-]+`)

// JobName —— 命名规则 mp-<wf>-<taskID>-<stage>-<job>（wf 名进名字，全局唯一保幂等删不误伤）。
// ⚠️ workflow 名必须进名字（T1.6 活体验收抓的老雷）：只有 taskID 时，两个不同 workflow
// 的同号任务 + 同名 stage/job = 同一个 K8s Job 名，幂等 Delete 会删掉别人家的 Job、
// Clean 会掀掉别人正在轮询的执行物。wf 名在任务档案里唯一（图纸名即主键），进名字后全局唯一。
func JobName(wfName string, taskID int64, stage, job string) string {
	name := fmt.Sprintf("mp-%s-%d-%s-%s", wfName, taskID, stage, job)
	name = strings.ToLower(name)
	name = jobNameSanitizer.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if len(name) > 63 {
		name = name[:63] // 截断只发生在超长名时；前缀 mp-<wf>-<taskID> 唯一性在此尺度下够用
	}
	return name
}

// BuildJob —— 把 JobTask 翻译成 K8s Job
//
//	驱动脚本直接拼进 sh -c（步骤执行器本体 = 引擎代码生成的脚本，见 driver.go）
//	backoffLimit=0 + RestartPolicy=Never：Pod 退出码 = job 结果，不重试（决断 #3 语义根基）
//	镜像三档：显式指定 > git/docker_build 在场用 pipeline-runner > 纯 shell 用 busybox。
//	runner 在 ACR 是私有镜像（与引擎镜像同实例，决断 #12 同链路），Job Pod 必须自带 pull secret——
//	引擎 Deployment 挂的 imagePullSecrets 不会继承给运行时创建的 Job
func BuildJob(namespace string, t *models.WorkflowTask, jt *models.JobTask, script string) *batchv1.Job {
	spec := jt.Spec
	cfg := config.Load() // 单次装配（RunnerImage/EngineURL 都从这里出）
	image := spec.Image
	if image == "" {
		if spec.NeedsRunner() { // 步骤域的判定（models），这里只消费选镜像
			image = cfg.RunnerImage // env RUNNER_IMAGE / chart values runner.image
		} else {
			image = "busybox"
		}
	}

	// 决断 #17：共享工作区档 = 任务级 PVC（mp-ws-<wf>-<taskID>，同任务全 job 挂同一卷，
	// job A 克隆的仓库/go 依赖 job B 直接见）；false = 旧世界每 Job emptyDir（回滚保险）
	var workspaceVol corev1.Volume
	if cfg.WorkspaceShared {
		workspaceVol = corev1.Volume{Name: "workspace", VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: WorkspacePVCName(t.WorkflowName, t.TaskID)},
		}}
	} else {
		workspaceVol = corev1.Volume{Name: "workspace", VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{}, // 步骤间的工作区（git 检出 → build 上下文 → 报告）
		}}
	}
	volumes := []corev1.Volume{workspaceVol}
	mounts := []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}}
	// 决断 #15（buildkit 演进，2026-11-09 替换 dind）：docker_build 的构建/推送在共享
	// buildkitd 容器（chart buildkit StatefulSet）里执行，本 Pod 经 buildx remote driver 打
	// 过去。fat 路线（决断 #5：挂宿主 docker.sock）与 dind 路线先后退役——旧世界每个 build
	// Pod ≈ 节点 root、dind 常驻 dockerd+containerd 双进程太重，buildkitd 单进程专职构建，
	// 特权收敛在常驻构建池容器 + mTLS 门禁（buildkitd args 见 templates/buildkit.yaml）。
	envs := []corev1.EnvVar{
		{Name: "MP_ATTEMPT", Value: strconv.Itoa(jt.Retry + 1)}, // T1.4：第几次尝试（重试脚本可确定性分支）
		{Name: "MP_ENGINE_URL", Value: cfg.EngineURL},           // T2.4：archive 上传目标
		{Name: "MP_TASK_WF", Value: t.WorkflowName},             // T2.4：artifact 上传 URL 组装
		{Name: "MP_TASK_ID", Value: strconv.FormatInt(t.TaskID, 10)},
	}
	if cfg.WorkspaceShared {
		// go 工具链缓存随任务卷走（用户痛点：job A 的 go.mod 依赖 job B 重下一遍）——
		// GOPATH 本体仍在容器层（/go，Pod 灭即焚），只把可共享的 cache 目录钉到卷上；
		// .mp-cache 隐藏目录不碍 git 步骤的 rm -rf 检出
		envs = append(envs,
			corev1.EnvVar{Name: "GOCACHE", Value: "/workspace/.mp-cache/go-build"},
			corev1.EnvVar{Name: "GOMODCACHE", Value: "/workspace/.mp-cache/go-mod"},
		)
	}
	if src := t.SourceSpec(); src != nil {
		// 统一代码源（决断 #18）：下游 job 免猜检出目录名（clone job cd 进去后 shell
		// 位置不继承给别的 Pod，$MP_SOURCE_DIR 是跨 job 的目录契约）
		envs = append(envs, corev1.EnvVar{Name: "MP_SOURCE_DIR", Value: src.CheckoutDir()})
	}
	// （MP_NODE_IP/MP_NODE_REGISTRY 随内置 registry:2 退役——决断 #16：推送目标全走
	// registry 管理记录，裸名无默认在 exec 解析层报错，不再有"节点 IP 兜底"）
	if spec.WantsDockerDaemon() {
		// 零状态确定性 task 亲和：同 task 的构建与 retry 永落同一台 buildkitd（层缓存命中），
		// 不同 task 哈希散开（fnv 零状态，无外部副本索引）。BUILDKIT_ADDR 显式覆盖 = 直用外部 buildkitd 且不注客户端证书（自管安全）；
		// 驱动脚本（step/dockerbuild.go）按 MP_BUILDKIT_CERTS 是否非空自适应带不带证书。
		if cfg.BuildkitAddr != "" {
			envs = append(envs, corev1.EnvVar{Name: "MP_BUILDKIT_ADDR", Value: cfg.BuildkitAddr})
		} else {
			envs = append(envs,
				corev1.EnvVar{Name: "MP_BUILDKIT_ADDR", Value: fmt.Sprintf("tcp://buildkit-%d.buildkit:1234", buildkitIndex(t.WorkflowName, t.TaskID, cfg.BuildkitReplicas))},
				corev1.EnvVar{Name: "MP_BUILDKIT_CERTS", Value: bkClientCertPath},
			)
			// 客户端证书三层注入不变：chart 现场生成、只落集群 Secret（templates/buildkit.yaml）；
			// items 改名成 ca.pem/cert.pem/key.pem。buildx remote driver 经 driver-opt 显式引用
			// （docker CLI 的 DOCKER_CERT_PATH 约定对 buildx 不生效），消费方在 step/dockerbuild.go。
			volumes = append(volumes, corev1.Volume{Name: "buildkit-client-tls", VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: "buildkit-tls-certs",
					Items: []corev1.KeyToPath{
						{Key: "ca.crt", Path: "ca.pem"},
						{Key: "client.crt", Path: "cert.pem"},
						{Key: "client.key", Path: "key.pem"},
					},
				},
			}})
			mounts = append(mounts, corev1.VolumeMount{Name: "buildkit-client-tls", MountPath: bkClientCertPath, ReadOnly: true})
		}
	}

	// 决断 #16：拉取凭证名单（MP_PULL_SECRETS）注入 Job Pod——引擎 Deployment 挂的
	// imagePullSecrets 不会继承给运行时创建的 Job（决断 #12 老结论，名单化后语义不变）
	var pullSecrets []corev1.LocalObjectReference
	for _, name := range cfg.PullSecrets {
		pullSecrets = append(pullSecrets, corev1.LocalObjectReference{Name: name})
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jt.K8sJobName,
			Namespace: namespace,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: ptr.To(int32(0)),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:    corev1.RestartPolicyNever,
					ImagePullSecrets: pullSecrets, // 决断 #16 零痕迹：名单由 chart values imagePullSecrets 注入；默认空 = 全公开镜像匿名拉（原 acr-credential 硬编码随凭证出库退役）
					Containers: []corev1.Container{{
						Name:         "runner",
						Image:        image,
						WorkingDir:   "/workspace",
						Command:      []string{"/bin/sh", "-c"},
						Args:         []string{script},
						Env:          envs,
						VolumeMounts: mounts,
					}},
					Volumes: volumes,
				},
			},
		},
	}
}

// bkClientCertPath —— runner Pod 内 buildx 客户端证书目录（Secret 卷 items 改名出
// ca.pem/cert.pem/key.pem；dockerbuild.go 的 driver-opt 引用同一路径，消费靠 MP_BUILDKIT_CERTS）
const bkClientCertPath = "/etc/mp/bk-client"

// buildkitIndex —— buildkit 副本分配：fnv32a("<wf>/<taskID>") % 副本数（决断 #15）。同 task
// 恒同 index（缓存亲和，retry 吃热层缓存），跨 task 近均匀（fnv 雪崩）；副本扩缩会重排（低频运维，接受）。
func buildkitIndex(wfName string, taskID int64, replicas int) int {
	if replicas < 1 {
		replicas = 1
	}
	h := fnv.New32a()
	fmt.Fprintf(h, "%s/%d", wfName, taskID)
	return int(h.Sum32() % uint32(replicas))
}
