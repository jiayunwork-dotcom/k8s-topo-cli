package trace

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/k8s-topo-cli/pkg/discovery"
)

var (
	greenStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	yellowStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	redStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	boldStyle    = lipgloss.NewStyle().Bold(true)
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	cyanStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)

type TraceNode struct {
	Type      string
	Namespace string
	Name      string
	Status    string
	Broken    bool
	Reason    string
}

type TraceResult struct {
	Target     TraceNode
	Chain      []TraceNode
	BrokenLinks []BrokenLink
}

type BrokenLink struct {
	FromType string
	FromName string
	ToType   string
	Reason   string
}

func Trace(resourceRef string, res *discovery.DiscoveredResources) (*TraceResult, error) {
	parts := strings.SplitN(resourceRef, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid resource reference format, expected <type>/<name> (e.g. pod/nginx-abc123)")
	}

	resourceType := strings.ToLower(parts[0])
	resourceName := parts[1]

	switch resourceType {
	case "pod":
		return tracePod(resourceName, res)
	case "service", "svc":
		return traceService(resourceName, res)
	case "deployment", "deploy":
		return traceDeployment(resourceName, res)
	case "ingress", "ing":
		return traceIngress(resourceName, res)
	default:
		return nil, fmt.Errorf("unsupported resource type: %s (supported: pod, service, deployment, ingress)", resourceType)
	}
}

func tracePod(name string, res *discovery.DiscoveredResources) (*TraceResult, error) {
	var targetPod *corev1.Pod
	for _, pod := range res.Pods {
		if pod.Name == name {
			targetPod = pod
			break
		}
	}
	if targetPod == nil {
		return nil, fmt.Errorf("pod '%s' not found", name)
	}

	result := &TraceResult{
		Target: TraceNode{
			Type:      "Pod",
			Namespace: targetPod.Namespace,
			Name:      targetPod.Name,
			Status:    getPodStatus(targetPod),
		},
	}

	var ownerRS *appsv1.ReplicaSet
	for _, ref := range targetPod.OwnerReferences {
		if ref.Kind == "ReplicaSet" {
			for _, rs := range res.ReplicaSets {
				if rs.Name == ref.Name && rs.Namespace == targetPod.Namespace {
					ownerRS = rs
					break
				}
			}
			break
		}
	}

	var ownerDeploy *appsv1.Deployment
	if ownerRS != nil {
		for _, ref := range ownerRS.OwnerReferences {
			if ref.Kind == "Deployment" {
				for _, deploy := range res.Deployments {
					if deploy.Name == ref.Name && deploy.Namespace == ownerRS.Namespace {
						ownerDeploy = deploy
						break
					}
				}
				break
			}
		}
	}

	chain := []TraceNode{}

	if ownerDeploy != nil {
		chain = append(chain, TraceNode{
			Type:      "Deployment",
			Namespace: ownerDeploy.Namespace,
			Name:      ownerDeploy.Name,
			Status:    getDeploymentStatus(ownerDeploy),
		})
	}

	if ownerRS != nil {
		chain = append(chain, TraceNode{
			Type:      "ReplicaSet",
			Namespace: ownerRS.Namespace,
			Name:      ownerRS.Name,
			Status:    getRSStatus(ownerRS),
		})
	}

	chain = append(chain, result.Target)

	for _, svc := range res.Services {
		if svc.Namespace != targetPod.Namespace {
			continue
		}
		if len(svc.Spec.Selector) == 0 {
			continue
		}
		selector := labels.Set(svc.Spec.Selector).AsSelectorPreValidated()
		if selector.Matches(labels.Set(targetPod.Labels)) {
			ingresses := findIngressesForService(svc, res)
			for _, ing := range ingresses {
				chain = append([]TraceNode{{
					Type:      "Ingress",
					Namespace: ing.Namespace,
					Name:      ing.Name,
					Status:    "Active",
				}}, chain...)
			}
			chain = append(chain, TraceNode{
				Type:      "Service",
				Namespace: svc.Namespace,
				Name:      svc.Name,
				Status:    "Active",
			})
		}
	}

	result.Chain = chain

	return result, nil
}

func traceService(name string, res *discovery.DiscoveredResources) (*TraceResult, error) {
	var targetSvc *corev1.Service
	for _, svc := range res.Services {
		if svc.Name == name {
			targetSvc = svc
			break
		}
	}
	if targetSvc == nil {
		return nil, fmt.Errorf("service '%s' not found", name)
	}

	result := &TraceResult{
		Target: TraceNode{
			Type:      "Service",
			Namespace: targetSvc.Namespace,
			Name:      targetSvc.Name,
			Status:    "Active",
		},
	}

	chain := []TraceNode{}

	ingresses := findIngressesForService(targetSvc, res)
	for _, ing := range ingresses {
		chain = append(chain, TraceNode{
			Type:      "Ingress",
			Namespace: ing.Namespace,
			Name:      ing.Name,
			Status:    "Active",
		})
	}

	chain = append(chain, result.Target)

	if len(targetSvc.Spec.Selector) > 0 {
		pods := discovery.GetServicePods(targetSvc, res.Pods)
		if len(pods) == 0 {
			result.BrokenLinks = append(result.BrokenLinks, BrokenLink{
				FromType: "Service", FromName: targetSvc.Name,
				ToType: "Pod", Reason: "selector matches no pods",
			})
		}
		for _, pod := range pods {
			chain = append(chain, TraceNode{
				Type:      "Pod",
				Namespace: pod.Namespace,
				Name:      pod.Name,
				Status:    getPodStatus(pod),
			})
		}
	}

	result.Chain = chain
	return result, nil
}

func traceDeployment(name string, res *discovery.DiscoveredResources) (*TraceResult, error) {
	var targetDeploy *appsv1.Deployment
	for _, deploy := range res.Deployments {
		if deploy.Name == name {
			targetDeploy = deploy
			break
		}
	}
	if targetDeploy == nil {
		return nil, fmt.Errorf("deployment '%s' not found", name)
	}

	result := &TraceResult{
		Target: TraceNode{
			Type:      "Deployment",
			Namespace: targetDeploy.Namespace,
			Name:      targetDeploy.Name,
			Status:    getDeploymentStatus(targetDeploy),
		},
	}

	chain := []TraceNode{result.Target}

	for _, rs := range res.ReplicaSets {
		if rs.Namespace != targetDeploy.Namespace {
			continue
		}
		isOwned := false
		for _, ref := range rs.OwnerReferences {
			if ref.Kind == "Deployment" && ref.Name == targetDeploy.Name {
				isOwned = true
				break
			}
		}
		if !isOwned {
			continue
		}

		chain = append(chain, TraceNode{
			Type:      "ReplicaSet",
			Namespace: rs.Namespace,
			Name:      rs.Name,
			Status:    getRSStatus(rs),
		})

		for _, pod := range res.Pods {
			if pod.Namespace != rs.Namespace {
				continue
			}
			for _, ref := range pod.OwnerReferences {
				if ref.Kind == "ReplicaSet" && ref.Name == rs.Name {
					chain = append(chain, TraceNode{
						Type:      "Pod",
						Namespace: pod.Namespace,
						Name:      pod.Name,
						Status:    getPodStatus(pod),
					})
				}
			}
		}
	}

	result.Chain = chain
	return result, nil
}

func traceIngress(name string, res *discovery.DiscoveredResources) (*TraceResult, error) {
	var targetIng *networkingv1.Ingress
	for _, ing := range res.Ingresses {
		if ing.Name == name {
			targetIng = ing
			break
		}
	}
	if targetIng == nil {
		return nil, fmt.Errorf("ingress '%s' not found", name)
	}

	result := &TraceResult{
		Target: TraceNode{
			Type:      "Ingress",
			Namespace: targetIng.Namespace,
			Name:      targetIng.Name,
			Status:    "Active",
		},
	}

	chain := []TraceNode{result.Target}

	svcs := discovery.GetIngressServices(targetIng, res.Services)
	if len(svcs) == 0 {
		result.BrokenLinks = append(result.BrokenLinks, BrokenLink{
			FromType: "Ingress", FromName: targetIng.Name,
			ToType: "Service", Reason: "backend service not found",
		})
	}

	for _, svc := range svcs {
		chain = append(chain, TraceNode{
			Type:      "Service",
			Namespace: svc.Namespace,
			Name:      svc.Name,
			Status:    "Active",
		})

		if len(svc.Spec.Selector) > 0 {
			pods := discovery.GetServicePods(svc, res.Pods)
			if len(pods) == 0 {
				result.BrokenLinks = append(result.BrokenLinks, BrokenLink{
					FromType: "Service", FromName: svc.Name,
					ToType: "Pod", Reason: "selector matches no pods",
				})
			}
			for _, pod := range pods {
				chain = append(chain, TraceNode{
					Type:      "Pod",
					Namespace: pod.Namespace,
					Name:      pod.Name,
					Status:    getPodStatus(pod),
				})
			}
		}
	}

	result.Chain = chain
	return result, nil
}

func findIngressesForService(svc *corev1.Service, res *discovery.DiscoveredResources) []*networkingv1.Ingress {
	var result []*networkingv1.Ingress
	for _, ing := range res.Ingresses {
		if ing.Namespace != svc.Namespace {
			continue
		}
		svcs := discovery.GetIngressServices(ing, res.Services)
		for _, s := range svcs {
			if s.Name == svc.Name {
				result = append(result, ing)
				break
			}
		}
	}
	return result
}

func getPodStatus(pod *corev1.Pod) string {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			reason := cs.State.Waiting.Reason
			if reason == "CrashLoopBackOff" || reason == "ImagePullBackOff" ||
				reason == "ErrImagePull" || reason == "OOMKilled" {
				return reason
			}
		}
	}
	return string(pod.Status.Phase)
}

func getDeploymentStatus(d *appsv1.Deployment) string {
	if d.Status.Replicas == d.Status.ReadyReplicas && d.Status.Replicas == d.Status.AvailableReplicas {
		return "Healthy"
	}
	if d.Status.ReadyReplicas == 0 && d.Status.Replicas > 0 {
		return "Unavailable"
	}
	return "Degraded"
}

func getRSStatus(rs *appsv1.ReplicaSet) string {
	if rs.Status.ReadyReplicas == rs.Status.Replicas {
		return "Healthy"
	}
	return "Degraded"
}

func RenderTrace(result *TraceResult) string {
	var sb strings.Builder

	sb.WriteString(boldStyle.Render("═══ Resource Reverse Trace ═══") + "\n\n")

	ns := result.Target.Namespace
	if ns != "" {
		ns = ns + "/"
	}
	sb.WriteString(fmt.Sprintf("Target: %s%s [%s]\n\n", ns, result.Target.Name, result.Target.Type))

	brokenSet := make(map[string]bool)
	for _, bl := range result.BrokenLinks {
		brokenSet[bl.FromType+"/"+bl.FromName] = true
	}

	for i, node := range result.Chain {
		prefix := ""
		if i > 0 {
			prefix = strings.Repeat("  ", i) + "↑ "
		}

		nodeNs := node.Namespace
		if nodeNs != "" {
			nodeNs = nodeNs + "/"
		}

		statusStr := colorizeStatus(node.Status)
		line := fmt.Sprintf("%s%s%s [%s] %s", prefix, nodeNs, node.Name, node.Type, statusStr)

		if node.Broken {
			line = redStyle.Render(line)
		}

		sb.WriteString(line + "\n")

		if brokenSet[node.Type+"/"+node.Name] {
			for _, bl := range result.BrokenLinks {
				if bl.FromType == node.Type && bl.FromName == node.Name {
					sb.WriteString(redStyle.Render(fmt.Sprintf("%s  ⚠ BROKEN LINK: %s → %s (%s)\n",
						strings.Repeat("  ", i), bl.FromType, bl.ToType, bl.Reason)))
				}
			}
		}
	}

	if len(result.BrokenLinks) > 0 {
		sb.WriteString("\n" + redStyle.Render("⚠ Broken Dependencies:") + "\n")
		for _, bl := range result.BrokenLinks {
			sb.WriteString(redStyle.Render(fmt.Sprintf("  %s → %s: %s\n", bl.FromType, bl.ToType, bl.Reason)))
		}
	}

	return sb.String()
}

func colorizeStatus(status string) string {
	switch {
	case status == "Running" || status == "Healthy" || status == "Active" || status == "Succeeded":
		return greenStyle.Render(status)
	case status == "Pending" || status == "Degraded":
		return yellowStyle.Render(status)
	case status == "Failed" || status == "Unavailable" || status == "CrashLoopBackOff" ||
		status == "ImagePullBackOff" || status == "OOMKilled" || status == "ErrImagePull":
		return redStyle.Render(status)
	default:
		return dimStyle.Render(status)
	}
}
