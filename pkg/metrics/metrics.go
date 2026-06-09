package metrics

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"

	"github.com/k8s-topo-cli/pkg/client"
	"github.com/k8s-topo-cli/pkg/discovery"
)

var (
	greenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	yellowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	redStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	boldStyle   = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	cyanStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)

type NodeMetrics struct {
	Name        string
	CPURequest  int64
	CPULimit    int64
	CPUUsage    int64
	CPUAllocatable int64
	MemRequest  int64
	MemLimit    int64
	MemUsage    int64
	MemAllocatable int64
}

type PodMetricsInfo struct {
	Namespace string
	Name      string
	CPUUsage  int64
	MemUsage  int64
}

type MetricsResult struct {
	NodeMetrics   []NodeMetrics
	PodMetrics    []PodMetricsInfo
	TopCPU        []PodMetricsInfo
	TopMem        []PodMetricsInfo
	MetricsAvailable bool
}

func CollectMetrics(ctx context.Context, c *client.ClusterClient, res *discovery.DiscoveredResources) *MetricsResult {
	result := &MetricsResult{}

	result.MetricsAvailable = c.IsMetricsAvailable()

	podMetricsMap := make(map[string]*metricsv1beta1.PodMetrics)
	if result.MetricsAvailable {
		podMetricsList, err := c.MetricsClient.MetricsV1beta1().PodMetricses("").List(ctx, metav1.ListOptions{})
		if err == nil {
			for i := range podMetricsList.Items {
				key := podMetricsList.Items[i].Namespace + "/" + podMetricsList.Items[i].Name
				podMetricsMap[key] = &podMetricsList.Items[i]
			}
		}
	}

	nodeRequestCPU := make(map[string]int64)
	nodeRequestMem := make(map[string]int64)
	nodeLimitCPU := make(map[string]int64)
	nodeLimitMem := make(map[string]int64)

	for _, pod := range res.Pods {
		nodeName := pod.Spec.NodeName
		if nodeName == "" {
			continue
		}
		for _, container := range pod.Spec.Containers {
			if container.Resources.Requests != nil {
				if q, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
					nodeRequestCPU[nodeName] += q.MilliValue()
				}
				if q, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
					nodeRequestMem[nodeName] += q.Value()
				}
			}
			if container.Resources.Limits != nil {
				if q, ok := container.Resources.Limits[corev1.ResourceCPU]; ok {
					nodeLimitCPU[nodeName] += q.MilliValue()
				}
				if q, ok := container.Resources.Limits[corev1.ResourceMemory]; ok {
					nodeLimitMem[nodeName] += q.Value()
				}
			}
		}
	}

	nodeUsageCPU := make(map[string]int64)
	nodeUsageMem := make(map[string]int64)

	for _, pod := range res.Pods {
		key := pod.Namespace + "/" + pod.Name
		pm, ok := podMetricsMap[key]
		if !ok {
			continue
		}
		nodeName := pod.Spec.NodeName
		for _, cm := range pm.Containers {
			nodeUsageCPU[nodeName] += cm.Usage.Cpu().MilliValue()
			nodeUsageMem[nodeName] += cm.Usage.Memory().Value()
		}
	}

	for _, node := range res.Nodes {
		cpuAlloc := node.Status.Allocatable[corev1.ResourceCPU]
		memAlloc := node.Status.Allocatable[corev1.ResourceMemory]
		nm := NodeMetrics{
			Name:           node.Name,
			CPURequest:     nodeRequestCPU[node.Name],
			CPULimit:       nodeLimitCPU[node.Name],
			CPUUsage:       nodeUsageCPU[node.Name],
			CPUAllocatable: (&cpuAlloc).MilliValue(),
			MemRequest:     nodeRequestMem[node.Name],
			MemLimit:       nodeLimitMem[node.Name],
			MemUsage:       nodeUsageMem[node.Name],
			MemAllocatable: (&memAlloc).Value(),
		}
		result.NodeMetrics = append(result.NodeMetrics, nm)
	}

	for _, pod := range res.Pods {
		key := pod.Namespace + "/" + pod.Name
		pm, ok := podMetricsMap[key]
		if !ok {
			continue
		}
		var cpuTotal, memTotal int64
		for _, cm := range pm.Containers {
			cpuTotal += cm.Usage.Cpu().MilliValue()
			memTotal += cm.Usage.Memory().Value()
		}
		result.PodMetrics = append(result.PodMetrics, PodMetricsInfo{
			Namespace: pod.Namespace,
			Name:      pod.Name,
			CPUUsage:  cpuTotal,
			MemUsage:  memTotal,
		})
	}

	sort.Slice(result.PodMetrics, func(i, j int) bool {
		return result.PodMetrics[i].CPUUsage > result.PodMetrics[j].CPUUsage
	})
	n := 5
	if len(result.PodMetrics) < n {
		n = len(result.PodMetrics)
	}
	result.TopCPU = make([]PodMetricsInfo, n)
	copy(result.TopCPU, result.PodMetrics[:n])

	sort.Slice(result.PodMetrics, func(i, j int) bool {
		return result.PodMetrics[i].MemUsage > result.PodMetrics[j].MemUsage
	})
	n = 5
	if len(result.PodMetrics) < n {
		n = len(result.PodMetrics)
	}
	result.TopMem = make([]PodMetricsInfo, n)
	copy(result.TopMem, result.PodMetrics[:n])

	return result
}

func RenderNodeMetrics(result *MetricsResult) string {
	var sb strings.Builder

	sb.WriteString(boldStyle.Render("═══ Node Resource Utilization ═══") + "\n\n")

	if !result.MetricsAvailable {
		sb.WriteString(yellowStyle.Render("⚠ Metrics API not available. Showing request/limit data only.\n\n"))
	}

	for _, nm := range result.NodeMetrics {
		sb.WriteString(boldStyle.Render(fmt.Sprintf("🖥️  Node: %s", nm.Name)) + "\n")

		if nm.CPUAllocatable > 0 {
			sb.WriteString(fmt.Sprintf("  CPU:\n"))
			sb.WriteString(fmt.Sprintf("    Request: %s\n", progressBar(nm.CPURequest, nm.CPUAllocatable, "m")))
			sb.WriteString(fmt.Sprintf("    Limit:   %s\n", progressBar(nm.CPULimit, nm.CPUAllocatable, "m")))
			if result.MetricsAvailable {
				sb.WriteString(fmt.Sprintf("    Usage:   %s\n", progressBar(nm.CPUUsage, nm.CPUAllocatable, "m")))
			}
		}

		if nm.MemAllocatable > 0 {
			sb.WriteString(fmt.Sprintf("  Memory:\n"))
			sb.WriteString(fmt.Sprintf("    Request: %s\n", progressBarMem(nm.MemRequest, nm.MemAllocatable)))
			sb.WriteString(fmt.Sprintf("    Limit:   %s\n", progressBarMem(nm.MemLimit, nm.MemAllocatable)))
			if result.MetricsAvailable {
				sb.WriteString(fmt.Sprintf("    Usage:   %s\n", progressBarMem(nm.MemUsage, nm.MemAllocatable)))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func progressBar(current, total int64, unit string) string {
	if total == 0 {
		return dimStyle.Render("N/A")
	}
	pct := float64(current) / float64(total) * 100
	barWidth := 30
	filled := int(float64(barWidth) * pct / 100)
	if filled > barWidth {
		filled = barWidth
	}

	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	var style lipgloss.Style
	switch {
	case pct >= 90:
		style = redStyle
	case pct >= 80:
		style = yellowStyle
	default:
		style = greenStyle
	}

	label := fmt.Sprintf("%d%s/%d%s (%.1f%%)", current, unit, total, unit, pct)
	return style.Render(bar) + " " + label
}

func progressBarMem(current, total int64) string {
	if total == 0 {
		return dimStyle.Render("N/A")
	}
	pct := float64(current) / float64(total) * 100
	barWidth := 30
	filled := int(float64(barWidth) * pct / 100)
	if filled > barWidth {
		filled = barWidth
	}

	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	var style lipgloss.Style
	switch {
	case pct >= 90:
		style = redStyle
	case pct >= 80:
		style = yellowStyle
	default:
		style = greenStyle
	}

	label := fmt.Sprintf("%s/%s (%.1f%%)", formatMemory(current), formatMemory(total), pct)
	return style.Render(bar) + " " + label
}

func formatMemory(bytes int64) string {
	const (
		Ki = 1024
		Mi = 1024 * Ki
		Gi = 1024 * Mi
	)
	switch {
	case bytes >= Gi:
		return fmt.Sprintf("%.1fGi", float64(bytes)/float64(Gi))
	case bytes >= Mi:
		return fmt.Sprintf("%.1fMi", float64(bytes)/float64(Mi))
	case bytes >= Ki:
		return fmt.Sprintf("%.1fKi", float64(bytes)/float64(Ki))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

func RenderHotspots(result *MetricsResult) string {
	var sb strings.Builder

	sb.WriteString(boldStyle.Render("═══ Resource Hotspots (Top 5) ═══") + "\n\n")

	sb.WriteString(cyanStyle.Render("🔥 Top CPU Consumers:") + "\n")
	for i, pod := range result.TopCPU {
		sb.WriteString(fmt.Sprintf("  %d. %s/%s - %dm CPU\n", i+1, pod.Namespace, pod.Name, pod.CPUUsage))
	}

	sb.WriteString("\n" + cyanStyle.Render("🔥 Top Memory Consumers:") + "\n")
	for i, pod := range result.TopMem {
		sb.WriteString(fmt.Sprintf("  %d. %s/%s - %s\n", i+1, pod.Namespace, pod.Name, formatMemory(pod.MemUsage)))
	}

	return sb.String()
}

func GetPodMetricsPercent(result *MetricsResult, namespace, name string) (cpuPct, memPct string) {
	for _, pm := range result.PodMetrics {
		if pm.Namespace == namespace && pm.Name == name {
			cpuPct = fmt.Sprintf("%dm", pm.CPUUsage)
			memPct = formatMemory(pm.MemUsage)
			return
		}
	}
	return "N/A", "N/A"
}
