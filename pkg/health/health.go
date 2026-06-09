package health

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	corev1 "k8s.io/api/core/v1"

	"github.com/k8s-topo-cli/pkg/discovery"
	"github.com/k8s-topo-cli/pkg/metrics"
)

var (
	greenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	yellowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	redStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	boldStyle   = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

type Deduction struct {
	Resource  string
	Namespace string
	Name      string
	Reason    string
	Points    int
}

type HealthResult struct {
	Score      int
	MaxScore   int
	Deductions []Deduction
}

func CalculateHealth(res *discovery.DiscoveredResources, metricsResult *metrics.MetricsResult) *HealthResult {
	result := &HealthResult{
		MaxScore: 100,
		Score:    100,
	}

	for _, pod := range res.Pods {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting != nil {
				reason := cs.State.Waiting.Reason
				switch reason {
				case "CrashLoopBackOff":
					result.Deductions = append(result.Deductions, Deduction{
						Resource: "Pod", Namespace: pod.Namespace, Name: pod.Name,
						Reason: "CrashLoopBackOff", Points: 3,
					})
				case "ImagePullBackOff":
					result.Deductions = append(result.Deductions, Deduction{
						Resource: "Pod", Namespace: pod.Namespace, Name: pod.Name,
						Reason: "ImagePullBackOff", Points: 2,
					})
				}
			}
		}

		if pod.Status.Phase == corev1.PodPending {
			age := time.Since(pod.CreationTimestamp.Time)
			if age > 5*time.Minute {
				result.Deductions = append(result.Deductions, Deduction{
					Resource: "Pod", Namespace: pod.Namespace, Name: pod.Name,
					Reason: fmt.Sprintf("Pending > 5min (%s)", formatDuration(age)), Points: 1,
				})
			}
		}

		for _, cs := range pod.Status.ContainerStatuses {
			if cs.RestartCount > 5 {
				result.Deductions = append(result.Deductions, Deduction{
					Resource: "Pod", Namespace: pod.Namespace, Name: pod.Name,
					Reason: fmt.Sprintf("Container %s restarted %d times", cs.Name, cs.RestartCount), Points: 1,
				})
			}
		}
	}

	for _, svc := range res.Services {
		if len(svc.Spec.Selector) == 0 {
			continue
		}
		pods := discovery.GetServicePods(svc, res.Pods)
		hasReady := false
		for _, p := range pods {
			for _, c := range p.Status.Conditions {
				if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
					hasReady = true
					break
				}
			}
			if hasReady {
				break
			}
		}
		if !hasReady {
			result.Deductions = append(result.Deductions, Deduction{
				Resource: "Service", Namespace: svc.Namespace, Name: svc.Name,
				Reason: "No ready backend pods", Points: 2,
			})
		}
	}

	for _, ing := range res.Ingresses {
		svcs := discovery.GetIngressServices(ing, res.Services)
		if len(svcs) == 0 {
			result.Deductions = append(result.Deductions, Deduction{
				Resource: "Ingress", Namespace: ing.Namespace, Name: ing.Name,
				Reason: "Dangling ingress: no backend service found", Points: 2,
			})
		}
	}

	if metricsResult != nil && metricsResult.MetricsAvailable {
		for _, nm := range metricsResult.NodeMetrics {
			if nm.CPUAllocatable > 0 {
				pct := float64(nm.CPUUsage) / float64(nm.CPUAllocatable) * 100
				if pct > 90 {
					result.Deductions = append(result.Deductions, Deduction{
						Resource: "Node", Namespace: "", Name: nm.Name,
						Reason: fmt.Sprintf("CPU utilization %.1f%% (>90%%)", pct), Points: 5,
					})
				}
			}
		}
	}

	totalDeduction := 0
	for _, d := range result.Deductions {
		totalDeduction += d.Points
	}
	result.Score = 100 - totalDeduction
	if result.Score < 0 {
		result.Score = 0
	}

	return result
}

func RenderHealthScore(result *HealthResult) string {
	var sb strings.Builder

	sb.WriteString(boldStyle.Render("═══ Cluster Health Score ═══") + "\n\n")

	scoreStyle := greenStyle
	if result.Score < 60 {
		scoreStyle = redStyle
	} else if result.Score < 80 {
		scoreStyle = yellowStyle
	}

	barWidth := 40
	filled := int(float64(barWidth) * float64(result.Score) / 100)
	if filled > barWidth {
		filled = barWidth
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	sb.WriteString(fmt.Sprintf("  Score: %s/%d\n", scoreStyle.Render(fmt.Sprintf("%d", result.Score)), result.MaxScore))
	sb.WriteString(fmt.Sprintf("  %s %s\n", scoreStyle.Render(bar), scoreStyle.Render(fmt.Sprintf("%d%%", result.Score))))
	sb.WriteString("\n")

	if len(result.Deductions) > 0 {
		sb.WriteString("  Deduction Details:\n")
		for _, d := range result.Deductions {
			ns := ""
			if d.Namespace != "" {
				ns = d.Namespace + "/"
			}
			sb.WriteString(fmt.Sprintf("    -%d  %s%s [%s] %s\n",
				d.Points, ns, d.Name, d.Resource, d.Reason))
		}
		sb.WriteString(fmt.Sprintf("\n  Total deductions: %d points\n", result.MaxScore-result.Score))
	} else {
		sb.WriteString(greenStyle.Render("  ✅ No issues found. Cluster is healthy!\n"))
	}

	return sb.String()
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
