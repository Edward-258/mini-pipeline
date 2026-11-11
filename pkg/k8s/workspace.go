package k8s

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// 任务级共享工作区（决断 #17）：一个任务一个 PVC（mp-ws-<wf>-<taskID>），同任务所有
// job 的 Pod 挂同一块卷当 /workspace——job A 克隆的仓库 / 下载的 go 依赖 job B 直接见
//（旧世界每个 Job 一个 emptyDir，文件随 Pod 生灭）。local-path（RWO +
// WaitForFirstConsumer）+ 调度器原生卷拓扑把同任务 job 共置到卷所在节点，零亲和代码。
// 任务终态（finish / 取消 / Recover 宣判）即删——与 emptyDir 的差别只是生命周期从
// "Job" 升格为"任务"。无 NFS 基建下的更简形态（卷拓扑原生共置替代 NFS 子目录共享）。

const (
	WorkspacePVCLabel   = "mp/workspace" // 泄漏清扫的 list 选择器（label 值域窄，才配当选择器）
	WorkspaceTaskAnno   = "mp/task"      // <wf>/<taskID> 反查引用（annotation 不限字符集——中文 wf 名进不去 label 但进得了 annotation）
	workspaceNamePrefix = "mp-ws-"
)

// WorkspacePVCName —— mp-ws-<wf>-<taskID>（复用 K8s Job 名同款 dns-1123 净化纪律，
// jobNameSanitizer 见 build.go）。名字确定性 ⇒ 各清理点免 label 直接点名删；
// 63 截断的唯一性口径与 JobName 一致（mp-ws-<wf>-<taskID> 前缀在此尺度下够用）。
func WorkspacePVCName(wfName string, taskID int64) string {
	name := fmt.Sprintf("%s%s-%d", workspaceNamePrefix, wfName, taskID)
	name = strings.ToLower(name)
	name = jobNameSanitizer.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// EnsureWorkspacePVC —— 幂等建卷（Get→NotFound 才 Create；并发首拍 AlreadyExists 容错）。
// 首个 job 创建前调用，后续 job 白拿；不指定 storageClass（吃集群默认 local-path），
// WaitForFirstConsumer 下绑定由 Pod 调度触发，无须等 Bound。
func EnsureWorkspacePVC(ctx context.Context, client kubernetes.Interface, ns, wfName string, taskID int64, size string) error {
	name := WorkspacePVCName(wfName, taskID)
	if _, err := client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, name, metav1.GetOptions{}); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	qty, err := resource.ParseQuantity(size)
	if err != nil {
		return fmt.Errorf("parse workspace size %q: %w", size, err)
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   ns,
			Labels:      map[string]string{WorkspacePVCLabel: "true"},
			Annotations: map[string]string{WorkspaceTaskAnno: fmt.Sprintf("%s/%d", wfName, taskID)},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: qty}},
		},
	}
	if _, err := client.CoreV1().PersistentVolumeClaims(ns).Create(ctx, pvc, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// DeleteWorkspacePVC —— 幂等删（NotFound = 已删 = 成功；cancel queued 无卷 / 双重清理都安全）
func DeleteWorkspacePVC(ctx context.Context, client kubernetes.Interface, ns, wfName string, taskID int64) error {
	err := client.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, WorkspacePVCName(wfName, taskID), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// WorkspaceRefOf —— 从 PVC annotation 反查任务引用（Recover 泄漏清扫用）。
// 没标注 / 解析不动 = ok=false——宁可留着不动，不误删活人的工作区。
func WorkspaceRefOf(pvc *corev1.PersistentVolumeClaim) (wfName string, taskID int64, ok bool) {
	ref, ok := pvc.Annotations[WorkspaceTaskAnno]
	if !ok {
		return "", 0, false
	}
	i := strings.LastIndex(ref, "/")
	if i <= 0 {
		return "", 0, false
	}
	id, err := strconv.ParseInt(ref[i+1:], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return ref[:i], id, true
}
