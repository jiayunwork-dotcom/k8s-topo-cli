package display

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"

	"github.com/k8s-topo-cli/pkg/discovery"
	"github.com/k8s-topo-cli/pkg/topology"
)

var (
	greenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	yellowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	redStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	cyanStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	boldStyle   = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	whiteStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
)

func colorizeStatus(status string) string {
	color := topology.GetStatusColor(status)
	switch color {
	case "green":
		return greenStyle.Render(status)
	case "yellow":
		return yellowStyle.Render(status)
	case "red":
		return redStyle.Render(status)
	default:
		return whiteStyle.Render(status)
	}
}

func RenderTree(topo *topology.Topology, res *discovery.DiscoveredResources) string {
	var sb strings.Builder

	sb.WriteString(boldStyle.Render("═══ Kubernetes Cluster Resource Tree ═══") + "\n\n")

	counts := res.Counts
	sb.WriteString(dimStyle.Render(fmt.Sprintf("Resources: %d Namespaces | %d Deployments | %d StatefulSets | %d DaemonSets | %d Pods | %d Services | %d Ingresses",
		counts.Namespaces, counts.Deployments, counts.StatefulSets, counts.DaemonSets, counts.Pods, counts.Services, counts.Ingresses)) + "\n\n")

	for _, root := range topo.Roots {
		renderTreeNode(&sb, root, "", true)
	}

	if len(topo.ServiceAlerts) > 0 {
		sb.WriteString("\n" + yellowStyle.Render("⚠ Service Alerts:") + "\n")
		for _, alert := range topo.ServiceAlerts {
			sb.WriteString(yellowStyle.Render("  ⚡ "+alert) + "\n")
		}
	}

	if len(topo.IngressAlerts) > 0 {
		sb.WriteString("\n" + redStyle.Render("⚠ Ingress Alerts:") + "\n")
		for _, alert := range topo.IngressAlerts {
			sb.WriteString(redStyle.Render("  🔗 "+alert) + "\n")
		}
	}

	return sb.String()
}

func renderTreeNode(sb *strings.Builder, node *topology.TopoNode, prefix string, isLast bool) {
	icon := topology.StatusIcon(node.Status)

	if node.Type == topology.TypeNamespace {
		sb.WriteString(fmt.Sprintf("%s%s %s %s\n", prefix, boldStyle.Render("📁"), icon, boldStyle.Render(node.Name)))
	} else {
		typeIcon := getTypeIcon(node.Type)
		nameStr := node.Name
		if node.Type == topology.TypePod {
			pod := node.Resource.(*corev1.Pod)
			nodeName := pod.Spec.NodeName
			if nodeName != "" {
				nameStr = fmt.Sprintf("%s %s", node.Name, dimStyle.Render("["+nodeName+"]"))
			}
		}
		sb.WriteString(fmt.Sprintf("%s%s %s %s %s\n", prefix, typeIcon, icon, nameStr, colorizeStatus(node.Status)))

		if len(node.Warnings) > 0 {
			for _, w := range node.Warnings {
				sb.WriteString(prefix + "    " + yellowStyle.Render("⚠ "+w) + "\n")
			}
		}
	}

	childPrefix := prefix + "│   "
	if isLast && node.Type != topology.TypeNamespace {
		childPrefix = prefix + "    "
	}

	for i, child := range node.Children {
		isChildLast := i == len(node.Children)-1
		renderTreeNode(sb, child, childPrefix, isChildLast)
	}
}

func getTypeIcon(t topology.ResourceType) string {
	switch t {
	case topology.TypeDeployment:
		return "🚀"
	case topology.TypeStatefulSet:
		return "📦"
	case topology.TypeDaemonSet:
		return "⚙️"
	case topology.TypeReplicaSet:
		return "🔄"
	case topology.TypePod:
		return "🧊"
	case topology.TypeService:
		return "🌐"
	case topology.TypeIngress:
		return "🔀"
	case topology.TypePVC:
		return "💾"
	case topology.TypePV:
		return "💿"
	case topology.TypeConfigMap:
		return "📋"
	case topology.TypeSecret:
		return "🔐"
	case topology.TypeNode:
		return "🖥️"
	default:
		return "📄"
	}
}

func RenderTopo(topo *topology.Topology, res *discovery.DiscoveredResources) string {
	var sb strings.Builder

	sb.WriteString(boldStyle.Render("═══ Kubernetes Service Topology ═══") + "\n\n")

	nsIngresses := make(map[string][]*networkingv1.Ingress)
	for _, ing := range res.Ingresses {
		nsIngresses[ing.Namespace] = append(nsIngresses[ing.Namespace], ing)
	}

	for _, ns := range topo.Roots {
		sb.WriteString(boldStyle.Render(fmt.Sprintf("📁 Namespace: %s", ns.Name)) + "\n")

		ingresses := nsIngresses[ns.Name]
		for _, ing := range ingresses {
			sb.WriteString(fmt.Sprintf("  %s %s\n", cyanStyle.Render("Ingress:"), ing.Name))
			for _, rule := range ing.Spec.Rules {
				if rule.HTTP == nil {
					continue
				}
				for _, path := range rule.HTTP.Paths {
					svcName := path.Backend.Service.Name
					sb.WriteString(fmt.Sprintf("    %s\n", yellowStyle.Render(fmt.Sprintf("%s ──→ [%s]", ing.Name, svcName))))

					svc := findService(res.Services, svcName, ns.Name)
					if svc != nil {
						pods := discovery.GetServicePods(svc, res.Pods)
						for _, pod := range pods {
							status := getSimplePodStatus(pod)
							icon := topology.StatusIcon(status)
							sb.WriteString(fmt.Sprintf("      %s\n", fmt.Sprintf("    [%s] ──→ %s %s %s", svcName, icon, pod.Name, colorizeStatus(status))))
						}
						if len(pods) == 0 && len(svc.Spec.Selector) > 0 {
							sb.WriteString(yellowStyle.Render("      [" + svcName + "] ──→ (no ready pods)\n"))
						}
					} else {
						sb.WriteString(redStyle.Render("      [" + svcName + "] ──→ ⚠ SERVICE NOT FOUND\n"))
					}
				}
			}
		}

		for _, svc := range res.Services {
			if svc.Namespace != ns.Name {
				continue
			}
			found := false
			for _, ing := range ingresses {
				for _, rule := range ing.Spec.Rules {
					if rule.HTTP == nil {
						continue
					}
					for _, path := range rule.HTTP.Paths {
						if path.Backend.Service.Name == svc.Name {
							found = true
							break
						}
					}
				}
			}
			if found {
				continue
			}

			pods := discovery.GetServicePods(svc, res.Pods)
			sb.WriteString(fmt.Sprintf("  %s %s\n", greenStyle.Render("Service:"), svc.Name))
			if len(svc.Spec.Selector) == 0 {
				sb.WriteString(dimStyle.Render("    (no selector - external/endpoints)\n"))
				continue
			}
			for _, pod := range pods {
				status := getSimplePodStatus(pod)
				icon := topology.StatusIcon(status)
				sb.WriteString(fmt.Sprintf("    %s\n", fmt.Sprintf("[%s] ──→ %s %s %s", svc.Name, icon, pod.Name, colorizeStatus(status))))
			}
			if len(pods) == 0 {
				sb.WriteString(yellowStyle.Render("    [" + svc.Name + "] ──→ (no ready pods)\n"))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func findService(services []*corev1.Service, name, namespace string) *corev1.Service {
	for _, s := range services {
		if s.Name == name && s.Namespace == namespace {
			return s
		}
	}
	return nil
}

func getSimplePodStatus(pod *corev1.Pod) string {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			reason := cs.State.Waiting.Reason
			if reason == "CrashLoopBackOff" || reason == "ImagePullBackOff" || reason == "OOMKilled" {
				return reason
			}
		}
	}
	return string(pod.Status.Phase)
}

func RenderTable(res *discovery.DiscoveredResources) string {
	var sb strings.Builder

	sb.WriteString(boldStyle.Render("═══ Kubernetes Pod Table ═══") + "\n\n")

	header := fmt.Sprintf("%-20s %-20s %-45s %-20s %-10s %-10s %-20s %-8s %-8s",
		"NAMESPACE", "OWNER", "POD NAME", "STATUS", "RESTARTS", "AGE", "NODE", "CPU%", "MEM%")
	sb.WriteString(cyanStyle.Render(header) + "\n")
	sb.WriteString(strings.Repeat("─", 170) + "\n")

	sort.Slice(res.Pods, func(i, j int) bool {
		if res.Pods[i].Namespace != res.Pods[j].Namespace {
			return res.Pods[i].Namespace < res.Pods[j].Namespace
		}
		return res.Pods[i].Name < res.Pods[j].Name
	})

	for _, pod := range res.Pods {
		owner := getPodOwner(pod)
		status := topology.GetStatusColor(getSimplePodStatus(pod))
		restarts := getTotalRestarts(pod)
		age := formatAge(pod.CreationTimestamp.Time)

		var statusStr string
		switch status {
		case "green":
			statusStr = greenStyle.Render(getSimplePodStatus(pod))
		case "yellow":
			statusStr = yellowStyle.Render(getSimplePodStatus(pod))
		case "red":
			statusStr = redStyle.Render(getSimplePodStatus(pod))
		default:
			statusStr = whiteStyle.Render(getSimplePodStatus(pod))
		}

		restartStr := fmt.Sprintf("%d", restarts)
		if restarts > 5 {
			restartStr = redStyle.Render(restartStr + "⚠")
		}

		row := fmt.Sprintf("%-20s %-20s %-45s %-20s %-10s %-10s %-20s %-8s %-8s",
			pod.Namespace,
			truncate(owner, 20),
			truncate(pod.Name, 45),
			statusStr,
			restartStr,
			age,
			truncate(pod.Spec.NodeName, 20),
			"N/A",
			"N/A")
		sb.WriteString(row + "\n")
	}

	return sb.String()
}

func getPodOwner(pod *corev1.Pod) string {
	for _, ref := range pod.OwnerReferences {
		return ref.Kind + "/" + ref.Name
	}
	return "-"
}

func getTotalRestarts(pod *corev1.Pod) int32 {
	var total int32
	for _, cs := range pod.Status.ContainerStatuses {
		total += cs.RestartCount
	}
	return total
}

func formatAge(t time.Time) string {
	d := time.Since(t)
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

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func RenderResourceSummary(res *discovery.DiscoveredResources) string {
	var sb strings.Builder
	sb.WriteString(boldStyle.Render("═══ Resource Summary ═══") + "\n\n")
	sb.WriteString(fmt.Sprintf("  Namespaces:    %d\n", res.Counts.Namespaces))
	sb.WriteString(fmt.Sprintf("  Deployments:   %d\n", res.Counts.Deployments))
	sb.WriteString(fmt.Sprintf("  StatefulSets:  %d\n", res.Counts.StatefulSets))
	sb.WriteString(fmt.Sprintf("  DaemonSets:    %d\n", res.Counts.DaemonSets))
	sb.WriteString(fmt.Sprintf("  ReplicaSets:   %d\n", res.Counts.ReplicaSets))
	sb.WriteString(fmt.Sprintf("  Pods:          %d\n", res.Counts.Pods))
	sb.WriteString(fmt.Sprintf("  Services:      %d\n", res.Counts.Services))
	sb.WriteString(fmt.Sprintf("  Ingresses:     %d\n", res.Counts.Ingresses))
	sb.WriteString(fmt.Sprintf("  PVCs:          %d\n", res.Counts.PVCs))
	sb.WriteString(fmt.Sprintf("  PVs:           %d\n", res.Counts.PVs))
	sb.WriteString(fmt.Sprintf("  ConfigMaps:    %d\n", res.Counts.ConfigMaps))
	sb.WriteString(fmt.Sprintf("  Secrets:       %d\n", res.Counts.Secrets))
	return sb.String()
}

func PrintOutput(content string) {
	fmt.Fprint(os.Stdout, content)
}
