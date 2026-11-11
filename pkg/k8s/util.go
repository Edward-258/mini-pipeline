package k8s

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// PodExitInfo —— 挖出 Pod 的退出码/原因（决断 #3"Pod 退出码 = job 结果"的落点）
func PodExitInfo(client kubernetes.Interface, ns, jobName string) string {
	pods, err := client.CoreV1().Pods(ns).List(context.TODO(), metav1.ListOptions{LabelSelector: "job-name=" + jobName})
	if err != nil || len(pods.Items) == 0 {
		return "unknown (no pod found)"
	}
	for _, cs := range pods.Items[0].Status.ContainerStatuses {
		if term := cs.State.Terminated; term != nil {
			return fmt.Sprintf("code %d: %s", term.ExitCode, strings.TrimSpace(term.Message))
		}
		if w := cs.State.Waiting; w != nil {
			return fmt.Sprintf("%s: %s", w.Reason, w.Message)
		}
	}
	return "unknown"
}

// PodUnschedulable —— job 的 Pod 是否卡在 Pending/Unschedulable（节点暴毙 / 卷绑不上 /
// 资源不足）。返回 PodScheduled=False condition 的 LastTransitionTime（无状态判定时机，
// 轮询循环无须自记首见时间戳）与调度器原始 reason；Pod 没起 / 条件不在场 = 未阻塞。
// 消费方在 exec 轮询循环（fail-fast，决断 #17）——沉默 10 分钟超时换成早期人话报错。
func PodUnschedulable(client kubernetes.Interface, ns, jobName string) (since time.Time, reason string, blocked bool) {
	pods, err := client.CoreV1().Pods(ns).List(context.TODO(), metav1.ListOptions{LabelSelector: "job-name=" + jobName})
	if err != nil || len(pods.Items) == 0 {
		return time.Time{}, "", false
	}
	for _, c := range pods.Items[0].Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && c.Reason == corev1.PodReasonUnschedulable {
			return c.LastTransitionTime.Time, strings.TrimSpace(c.Message), true
		}
	}
	return time.Time{}, "", false
}

// DeleteOpts —— 显式 Background 级联：默认删 Job 会留下孤儿 Pod（k8s 的坑，日志里警告过）
func DeleteOpts() metav1.DeleteOptions {
	return metav1.DeleteOptions{PropagationPolicy: func() *metav1.DeletionPropagation {
		p := metav1.DeletePropagationBackground
		return &p
	}()}
}
