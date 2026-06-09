package diagnosis

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/k8s-topo-cli/pkg/client"
	"github.com/k8s-topo-cli/pkg/discovery"
)

type DiagnosisResult struct {
	PodDiagnoses    []PodDiagnosis
	WarningEvents   []EventInfo
	ProbeFailures   []ProbeFailure
}

type PodDiagnosis struct {
	Namespace    string
	PodName      string
	Status       string
	ReasonChain  []string
	LogTail      string
	ImageError   string
	PendingReason string
	OOMInfo       string
}

type EventInfo struct {
	Count    int32
	Type     string
	Reason   string
	Message  string
	Kind     string
	Name     string
	Namespace string
	LastSeen  string
}

type ProbeFailure struct {
	Namespace    string
	PodName      string
	Container    string
	ProbeType    string
	ProbeConfig  string
	FailureCount int32
}

func Diagnose(ctx context.Context, c *client.ClusterClient, res *discovery.DiscoveredResources) *DiagnosisResult {
	result := &DiagnosisResult{}

	result.PodDiagnoses = diagnosePods(ctx, c, res)
	result.WarningEvents = aggregateEvents(res)
	result.ProbeFailures = diagnoseProbes(res)

	return result
}

func diagnosePods(ctx context.Context, c *client.ClusterClient, res *discovery.DiscoveredResources) []PodDiagnosis {
	var diagnoses []PodDiagnosis

	for _, pod := range res.Pods {
		status := getPodPhase(pod)
		if status == "Running" || status == "Succeeded" {
			continue
		}

		diag := PodDiagnosis{
			Namespace: pod.Namespace,
			PodName:   pod.Name,
			Status:    status,
		}

		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting != nil {
				reason := cs.State.Waiting.Reason
				switch reason {
				case "CrashLoopBackOff":
					diag.ReasonChain = append(diag.ReasonChain,
						fmt.Sprintf("Container %s in CrashLoopBackOff", cs.Name))
					logs, err := getContainerLogs(ctx, c, pod, cs.Name, 20)
					if err == nil {
						diag.LogTail = logs
					} else {
						diag.LogTail = fmt.Sprintf("Failed to fetch logs: %v", err)
					}
				case "ImagePullBackOff", "ErrImagePull":
					diag.ReasonChain = append(diag.ReasonChain,
						fmt.Sprintf("Container %s: image pull failed", cs.Name))
					image := ""
					if len(pod.Spec.Containers) > 0 {
						for _, ctr := range pod.Spec.Containers {
							if ctr.Name == cs.Name {
								image = ctr.Image
								break
							}
						}
					}
					diag.ImageError = fmt.Sprintf("Image: %s\nError: %s", image, cs.State.Waiting.Message)
				}
			}

			if cs.LastTerminationState.Terminated != nil {
				term := cs.LastTerminationState.Terminated
				if term.Reason == "OOMKilled" {
					diag.ReasonChain = append(diag.ReasonChain,
						fmt.Sprintf("Container %s was OOMKilled", cs.Name))
					memLimit := ""
					for _, ctr := range pod.Spec.Containers {
						if ctr.Name == cs.Name {
							if ctr.Resources.Limits != nil {
								if q, ok := ctr.Resources.Limits[corev1.ResourceMemory]; ok {
									memLimit = q.String()
								}
							}
						}
					}
					diag.OOMInfo = fmt.Sprintf("Container: %s\nMemory Limit: %s\nExit Code: %d\nFinished At: %s",
						cs.Name, memLimit, term.ExitCode, term.FinishedAt.Format("2006-01-02 15:04:05"))
				}
			}
		}

		if pod.Status.Phase == corev1.PodPending {
			diag.ReasonChain = append(diag.ReasonChain, "Pod is Pending")
			diag.PendingReason = analyzePendingPod(pod, res)
		}

		if pod.Status.Phase == corev1.PodFailed {
			diag.ReasonChain = append(diag.ReasonChain, "Pod has Failed")
		}

		diagnoses = append(diagnoses, diag)
	}

	return diagnoses
}

func analyzePendingPod(pod *corev1.Pod, res *discovery.DiscoveredResources) string {
	var reasons []string

	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse {
			reasons = append(reasons, "Not scheduled: "+cond.Message)
		}
		if cond.Type == corev1.PodReasonUnschedulable {
			reasons = append(reasons, "Unschedulable: "+cond.Message)
		}
	}

	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			reasons = append(reasons, fmt.Sprintf("Container %s waiting: %s - %s",
				cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message))
		}
	}

	if len(pod.Spec.NodeSelector) > 0 {
		reasons = append(reasons, fmt.Sprintf("Has node selector constraints: %v", pod.Spec.NodeSelector))
	}

	if pod.Spec.Affinity != nil {
		if pod.Spec.Affinity.NodeAffinity != nil {
			reasons = append(reasons, "Has node affinity constraints")
		}
		if pod.Spec.Affinity.PodAffinity != nil {
			reasons = append(reasons, "Has pod affinity constraints")
		}
		if pod.Spec.Affinity.PodAntiAffinity != nil {
			reasons = append(reasons, "Has pod anti-affinity constraints")
		}
	}

	unschedulableTaints := false
	if pod.Spec.Tolerations == nil && len(res.Nodes) > 0 {
		for _, node := range res.Nodes {
			for _, taint := range node.Spec.Taints {
				if taint.Effect == corev1.TaintEffectNoSchedule || taint.Effect == corev1.TaintEffectNoExecute {
					hasToleration := false
					for _, tol := range pod.Spec.Tolerations {
						if tol.Key == taint.Key && (tol.Operator == corev1.TolerationOpExists ||
							(tol.Operator == corev1.TolerationOpEqual && tol.Value == taint.Value)) {
							hasToleration = true
							break
						}
					}
					if !hasToleration {
						unschedulableTaints = true
						reasons = append(reasons, fmt.Sprintf("Cannot tolerate taint %s=%s on node %s",
							taint.Key, taint.Value, node.Name))
					}
				}
			}
		}
	}

	if !unschedulableTaints && len(reasons) == 0 {
		resourceInsufficient := checkResourceInsufficiency(pod, res)
		if resourceInsufficient != "" {
			reasons = append(reasons, resourceInsufficient)
		}
	}

	if len(reasons) == 0 {
		return "Unknown reason for pending state"
	}

	return strings.Join(reasons, "\n")
}

func checkResourceInsufficiency(pod *corev1.Pod, res *discovery.DiscoveredResources) string {
	var podCPU, podMem int64
	for _, c := range pod.Spec.Containers {
		if c.Resources.Requests != nil {
			if q, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
				podCPU += q.MilliValue()
			}
			if q, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
				podMem += q.Value()
			}
		}
	}

	for _, node := range res.Nodes {
		cpuQ := node.Status.Allocatable[corev1.ResourceCPU]
		memQ := node.Status.Allocatable[corev1.ResourceMemory]
		allocatableCPU := (&cpuQ).MilliValue()
		allocatableMem := (&memQ).Value()

		requestedCPU := int64(0)
		requestedMem := int64(0)
		for _, p := range res.Pods {
			if p.Spec.NodeName == node.Name && p.UID != pod.UID {
				for _, c := range p.Spec.Containers {
					if c.Resources.Requests != nil {
						if q, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
							requestedCPU += q.MilliValue()
						}
						if q, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
							requestedMem += q.Value()
						}
					}
				}
			}
		}

		if allocatableCPU-requestedCPU >= podCPU && allocatableMem-requestedMem >= podMem {
			return ""
		}
	}

	return "Insufficient resources: no node has enough CPU/Memory to schedule this pod"
}

func aggregateEvents(res *discovery.DiscoveredResources) []EventInfo {
	var events []EventInfo
	for _, e := range res.Events {
		if e.Type != "Warning" {
			continue
		}
		events = append(events, EventInfo{
			Count:     e.Count,
			Type:      e.Type,
			Reason:    e.Reason,
			Message:   e.Message,
			Kind:      e.InvolvedObject.Kind,
			Name:      e.InvolvedObject.Name,
			Namespace: e.InvolvedObject.Namespace,
			LastSeen:  e.LastTimestamp.Format("2006-01-02 15:04:05"),
		})
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].Count > events[j].Count
	})

	return events
}

func diagnoseProbes(res *discovery.DiscoveredResources) []ProbeFailure {
	var failures []ProbeFailure

	for _, pod := range res.Pods {
		for _, cs := range pod.Status.ContainerStatuses {
			if !cs.Ready {
				for _, container := range pod.Spec.Containers {
					if container.Name != cs.Name {
						continue
					}
					if container.ReadinessProbe != nil {
						failures = append(failures, ProbeFailure{
							Namespace:    pod.Namespace,
							PodName:      pod.Name,
							Container:    cs.Name,
							ProbeType:    "Readiness",
							ProbeConfig:  formatProbe(container.ReadinessProbe),
							FailureCount: cs.RestartCount,
						})
					}
				}
			}

			if cs.RestartCount > 0 {
				for _, container := range pod.Spec.Containers {
					if container.Name != cs.Name {
						continue
					}
					if container.LivenessProbe != nil {
						already := false
						for _, f := range failures {
							if f.PodName == pod.Name && f.Container == cs.Name && f.ProbeType == "Liveness" {
								already = true
								break
							}
						}
						if !already {
							failures = append(failures, ProbeFailure{
								Namespace:    pod.Namespace,
								PodName:      pod.Name,
								Container:    cs.Name,
								ProbeType:    "Liveness",
								ProbeConfig:  formatProbe(container.LivenessProbe),
								FailureCount: cs.RestartCount,
							})
						}
					}
				}
			}
		}
	}

	return failures
}

func formatProbe(probe *corev1.Probe) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("delay=%ds,timeout=%ds,period=%ds,success=%d,failure=%d",
		probe.InitialDelaySeconds, probe.TimeoutSeconds,
		probe.PeriodSeconds, probe.SuccessThreshold, probe.FailureThreshold))
	if probe.HTTPGet != nil {
		sb.WriteString(fmt.Sprintf(" [HTTP GET %s:%d%s]", probe.HTTPGet.Host, probe.HTTPGet.Port.IntValue(), probe.HTTPGet.Path))
	}
	if probe.TCPSocket != nil {
		sb.WriteString(fmt.Sprintf(" [TCP %s:%d]", probe.TCPSocket.Host, probe.TCPSocket.Port.IntValue()))
	}
	if probe.Exec != nil {
		sb.WriteString(fmt.Sprintf(" [Exec: %s]", strings.Join(probe.Exec.Command, " ")))
	}
	return sb.String()
}

func getContainerLogs(ctx context.Context, c *client.ClusterClient, pod *corev1.Pod, container string, lines int64) (string, error) {
	opts := &corev1.PodLogOptions{
		Container: container,
		TailLines: &lines,
	}
	req := c.Clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, opts)
	readCloser, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer readCloser.Close()

	buf := make([]byte, 8192)
	n, err := readCloser.Read(buf)
	if err != nil && n == 0 {
		return "", err
	}
	return string(buf[:n]), nil
}

func getPodPhase(pod *corev1.Pod) string {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			reason := cs.State.Waiting.Reason
			if reason == "CrashLoopBackOff" || reason == "ImagePullBackOff" ||
				reason == "ErrImagePull" || reason == "OOMKilled" {
				return reason
			}
		}
		if cs.LastTerminationState.Terminated != nil && cs.LastTerminationState.Terminated.Reason == "OOMKilled" {
			return "OOMKilled"
		}
	}
	return string(pod.Status.Phase)
}

func GetPodDetail(ctx context.Context, c *client.ClusterClient, namespace, name string) (string, error) {
	pod, err := c.Clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Pod: %s/%s\n", pod.Namespace, pod.Name))
	sb.WriteString(fmt.Sprintf("Status: %s\n", pod.Status.Phase))
	sb.WriteString(fmt.Sprintf("Node: %s\n", pod.Spec.NodeName))
	sb.WriteString(fmt.Sprintf("IP: %s\n", pod.Status.PodIP))
	sb.WriteString(fmt.Sprintf("Labels:\n"))
	for k, v := range pod.Labels {
		sb.WriteString(fmt.Sprintf("  %s: %s\n", k, v))
	}
	sb.WriteString(fmt.Sprintf("Containers:\n"))
	for _, c := range pod.Spec.Containers {
		sb.WriteString(fmt.Sprintf("  - Name: %s\n", c.Name))
		sb.WriteString(fmt.Sprintf("    Image: %s\n", c.Image))
		if c.Resources.Requests != nil {
			sb.WriteString(fmt.Sprintf("    Requests: %v\n", c.Resources.Requests))
		}
		if c.Resources.Limits != nil {
			sb.WriteString(fmt.Sprintf("    Limits: %v\n", c.Resources.Limits))
		}
	}
	sb.WriteString(fmt.Sprintf("Volumes:\n"))
	for _, vol := range pod.Spec.Volumes {
		if vol.ConfigMap != nil {
			sb.WriteString(fmt.Sprintf("  - ConfigMap: %s\n", vol.ConfigMap.Name))
		}
		if vol.Secret != nil {
			sb.WriteString(fmt.Sprintf("  - Secret: %s\n", vol.Secret.SecretName))
		}
		if vol.PersistentVolumeClaim != nil {
			sb.WriteString(fmt.Sprintf("  - PVC: %s\n", vol.PersistentVolumeClaim.ClaimName))
		}
	}

	return sb.String(), nil
}

func GetPodEvents(ctx context.Context, c *client.ClusterClient, namespace, name string) (string, error) {
	events, err := c.Clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.kind=Pod", name),
	})
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	for _, e := range events.Items {
		sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", e.LastTimestamp.Format("15:04:05"), e.Reason, e.Message))
	}
	if sb.Len() == 0 {
		sb.WriteString("No events found.\n")
	}
	return sb.String(), nil
}

func GetPodLogs(ctx context.Context, c *client.ClusterClient, namespace, name, container string, lines int64) (string, error) {
	if container == "" {
		container = ""
	}
	opts := &corev1.PodLogOptions{
		Container: container,
		TailLines: &lines,
	}
	req := c.Clientset.CoreV1().Pods(namespace).GetLogs(name, opts)
	readCloser, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer readCloser.Close()

	buf := make([]byte, 32768)
	n, err := readCloser.Read(buf)
	if err != nil && n == 0 {
		return "", err
	}
	return string(buf[:n]), nil
}
