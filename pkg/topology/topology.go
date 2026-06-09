package topology

import (
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	"github.com/k8s-topo-cli/pkg/discovery"
)

type ResourceType string

const (
	TypeNamespace   ResourceType = "Namespace"
	TypeDeployment  ResourceType = "Deployment"
	TypeStatefulSet ResourceType = "StatefulSet"
	TypeDaemonSet   ResourceType = "DaemonSet"
	TypeReplicaSet  ResourceType = "ReplicaSet"
	TypePod         ResourceType = "Pod"
	TypeService     ResourceType = "Service"
	TypeIngress     ResourceType = "Ingress"
	TypePVC         ResourceType = "PVC"
	TypePV          ResourceType = "PV"
	TypeConfigMap   ResourceType = "ConfigMap"
	TypeSecret      ResourceType = "Secret"
	TypeNode        ResourceType = "Node"
)

type TopoNode struct {
	UID      types.UID
	Name     string
	Namespace string
	Type     ResourceType
	Status   string
	Children []*TopoNode
	Parent   *TopoNode
	Resource interface{}
	Warnings []string
}

type Topology struct {
	Roots         []*TopoNode
	AllNodes      []*TopoNode
	NamespaceMap  map[string]*TopoNode
	ServiceAlerts []string
	IngressAlerts []string
}

func BuildTopology(res *discovery.DiscoveredResources) *Topology {
	topo := &Topology{
		NamespaceMap:  make(map[string]*TopoNode),
		ServiceAlerts: []string{},
		IngressAlerts: []string{},
	}

	podMap := make(map[types.UID]*corev1.Pod)
	for _, p := range res.Pods {
		podMap[p.UID] = p
	}

	rsMap := make(map[types.UID]*appsv1.ReplicaSet)
	for _, rs := range res.ReplicaSets {
		rsMap[rs.UID] = rs
	}

	deployMap := make(map[types.UID]*appsv1.Deployment)
	for _, d := range res.Deployments {
		deployMap[d.UID] = d
	}

	stsMap := make(map[types.UID]*appsv1.StatefulSet)
	for _, s := range res.StatefulSets {
		stsMap[s.UID] = s
	}

	dsMap := make(map[types.UID]*appsv1.DaemonSet)
	for _, d := range res.DaemonSets {
		dsMap[d.UID] = d
	}

	svcMap := make(map[types.UID]*corev1.Service)
	for _, s := range res.Services {
		svcMap[s.UID] = s
	}

	for _, ns := range res.Namespaces {
		node := &TopoNode{
			UID:      ns.UID,
			Name:     ns.Name,
			Type:     TypeNamespace,
			Status:   string(ns.Status.Phase),
			Resource: ns,
		}
		topo.Roots = append(topo.Roots, node)
		topo.NamespaceMap[ns.Name] = node
		topo.AllNodes = append(topo.AllNodes, node)
	}

	for _, deploy := range res.Deployments {
		parent := topo.NamespaceMap[deploy.Namespace]
		if parent == nil {
			continue
		}
		node := &TopoNode{
			UID:       deploy.UID,
			Name:      deploy.Name,
			Namespace: deploy.Namespace,
			Type:      TypeDeployment,
			Status:    getDeploymentStatus(deploy),
			Resource:  deploy,
		}
		parent.Children = append(parent.Children, node)
		node.Parent = parent
		topo.AllNodes = append(topo.AllNodes, node)
	}

	for _, sts := range res.StatefulSets {
		parent := topo.NamespaceMap[sts.Namespace]
		if parent == nil {
			continue
		}
		node := &TopoNode{
			UID:       sts.UID,
			Name:      sts.Name,
			Namespace: sts.Namespace,
			Type:      TypeStatefulSet,
			Status:    getStatefulSetStatus(sts),
			Resource:  sts,
		}
		parent.Children = append(parent.Children, node)
		node.Parent = parent
		topo.AllNodes = append(topo.AllNodes, node)
	}

	for _, ds := range res.DaemonSets {
		parent := topo.NamespaceMap[ds.Namespace]
		if parent == nil {
			continue
		}
		node := &TopoNode{
			UID:       ds.UID,
			Name:      ds.Name,
			Namespace: ds.Namespace,
			Type:      TypeDaemonSet,
			Status:    getDaemonSetStatus(ds),
			Resource:  ds,
		}
		parent.Children = append(parent.Children, node)
		node.Parent = parent
		topo.AllNodes = append(topo.AllNodes, node)
	}

	for _, rs := range res.ReplicaSets {
		var parentNode *TopoNode
		for _, ref := range rs.OwnerReferences {
			if ref.Kind == "Deployment" {
				for _, n := range topo.AllNodes {
					if n.UID == ref.UID {
						parentNode = n
						break
					}
				}
				break
			}
		}
		if parentNode == nil {
			parent := topo.NamespaceMap[rs.Namespace]
			if parent != nil {
				node := &TopoNode{
					UID:       rs.UID,
					Name:      rs.Name,
					Namespace: rs.Namespace,
					Type:      TypeReplicaSet,
					Status:    getReplicaSetStatus(rs),
					Resource:  rs,
				}
				parent.Children = append(parent.Children, node)
				node.Parent = parent
				topo.AllNodes = append(topo.AllNodes, node)
			}
		}
	}

	for _, pod := range res.Pods {
		var parentNode *TopoNode

		for _, ref := range pod.OwnerReferences {
			for _, n := range topo.AllNodes {
				if n.UID == ref.UID {
					parentNode = n
					break
				}
			}
			if parentNode != nil {
				break
			}
		}

		if parentNode == nil {
			for _, n := range topo.AllNodes {
				if n.Type == TypeDeployment && n.Namespace == pod.Namespace {
					deploy := n.Resource.(*appsv1.Deployment)
					if deploy.Spec.Selector != nil {
						sel, err := metav1.LabelSelectorAsSelector(deploy.Spec.Selector)
						if err == nil && sel.Matches(labels.Set(pod.Labels)) {
							parentNode = n
							break
						}
					}
				}
			}
		}

		if parentNode == nil {
			for _, n := range topo.AllNodes {
				if n.Type == TypeStatefulSet && n.Namespace == pod.Namespace {
					sts := n.Resource.(*appsv1.StatefulSet)
					if sts.Spec.Selector != nil {
						sel, err := metav1.LabelSelectorAsSelector(sts.Spec.Selector)
						if err == nil && sel.Matches(labels.Set(pod.Labels)) {
							parentNode = n
							break
						}
					}
				}
			}
		}

		if parentNode == nil {
			parent := topo.NamespaceMap[pod.Namespace]
			if parent != nil {
				parentNode = parent
			}
		}

		if parentNode != nil {
			node := &TopoNode{
				UID:       pod.UID,
				Name:      pod.Name,
				Namespace: pod.Namespace,
				Type:      TypePod,
				Status:    getPodStatus(pod),
				Resource:  pod,
				Warnings:  getPodWarnings(pod),
			}
			parentNode.Children = append(parentNode.Children, node)
			node.Parent = parentNode
			topo.AllNodes = append(topo.AllNodes, node)
		}
	}

	for _, svc := range res.Services {
		parent := topo.NamespaceMap[svc.Namespace]
		if parent == nil {
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
		status := "Active"
		warnings := []string{}
		if !hasReady && len(svc.Spec.Selector) > 0 {
			status = "NoBackend"
			warnings = append(warnings, "no ready endpoints")
			topo.ServiceAlerts = append(topo.ServiceAlerts,
				fmt.Sprintf("Service %s/%s has no ready endpoints", svc.Namespace, svc.Name))
		}

		node := &TopoNode{
			UID:       svc.UID,
			Name:      svc.Name,
			Namespace: svc.Namespace,
			Type:      TypeService,
			Status:    status,
			Resource:  svc,
			Warnings:  warnings,
		}
		parent.Children = append(parent.Children, node)
		node.Parent = parent
		topo.AllNodes = append(topo.AllNodes, node)
	}

	for _, ing := range res.Ingresses {
		parent := topo.NamespaceMap[ing.Namespace]
		if parent == nil {
			continue
		}
		warnings := []string{}
		svcs := discovery.GetIngressServices(ing, res.Services)
		if len(svcs) == 0 {
			warnings = append(warnings, "dangling ingress: backend service not found")
			topo.IngressAlerts = append(topo.IngressAlerts,
				fmt.Sprintf("Ingress %s/%s points to non-existent service", ing.Namespace, ing.Name))
		}

		node := &TopoNode{
			UID:       ing.UID,
			Name:      ing.Name,
			Namespace: ing.Namespace,
			Type:      TypeIngress,
			Status:    "Active",
			Resource:  ing,
			Warnings:  warnings,
		}
		parent.Children = append(parent.Children, node)
		node.Parent = parent
		topo.AllNodes = append(topo.AllNodes, node)
	}

	for _, pvc := range res.PVCs {
		parent := topo.NamespaceMap[pvc.Namespace]
		if parent == nil {
			continue
		}
		pv := discovery.GetPVForPVC(pvc, res.PVs)
		status := string(pvc.Status.Phase)
		warnings := []string{}
		if pv == nil && pvc.Spec.VolumeName == "" {
			warnings = append(warnings, "no bound PV")
		}

		node := &TopoNode{
			UID:       pvc.UID,
			Name:      pvc.Name,
			Namespace: pvc.Namespace,
			Type:      TypePVC,
			Status:    status,
			Resource:  pvc,
			Warnings:  warnings,
		}
		parent.Children = append(parent.Children, node)
		node.Parent = parent
		topo.AllNodes = append(topo.AllNodes, node)

		if pv != nil {
			pvNode := &TopoNode{
				UID:      pv.UID,
				Name:     pv.Name,
				Type:     TypePV,
				Status:   string(pv.Status.Phase),
				Resource: pv,
			}
			node.Children = append(node.Children, pvNode)
			pvNode.Parent = node
			topo.AllNodes = append(topo.AllNodes, pvNode)
		}
	}

	for _, root := range topo.Roots {
		sortChildren(root)
	}

	return topo
}

func sortChildren(node *TopoNode) {
	sort.SliceStable(node.Children, func(i, j int) bool {
		typeOrder := map[ResourceType]int{
			TypeDeployment:  0,
			TypeStatefulSet: 1,
			TypeDaemonSet:   2,
			TypeReplicaSet:  3,
			TypeService:     4,
			TypeIngress:     5,
			TypePVC:         6,
			TypePod:         7,
		}
		oi, _ := typeOrder[node.Children[i].Type]
		oj, _ := typeOrder[node.Children[j].Type]
		if oi != oj {
			return oi < oj
		}
		return node.Children[i].Name < node.Children[j].Name
	})
	for _, child := range node.Children {
		sortChildren(child)
	}
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
		if cs.LastTerminationState.Terminated != nil {
			if cs.LastTerminationState.Terminated.Reason == "OOMKilled" {
				return "OOMKilled"
			}
		}
	}

	switch pod.Status.Phase {
	case corev1.PodRunning:
		return "Running"
	case corev1.PodPending:
		return "Pending"
	case corev1.PodFailed:
		return "Failed"
	case corev1.PodSucceeded:
		return "Succeeded"
	default:
		return string(pod.Status.Phase)
	}
}

func getPodWarnings(pod *corev1.Pod) []string {
	var warnings []string
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.RestartCount > 5 {
			warnings = append(warnings, fmt.Sprintf("container %s: frequent restarts (%d)", cs.Name, cs.RestartCount))
		}
		if cs.State.Waiting != nil {
			reason := cs.State.Waiting.Reason
			if reason == "CrashLoopBackOff" || reason == "ImagePullBackOff" ||
				reason == "ErrImagePull" || reason == "OOMKilled" {
				warnings = append(warnings, fmt.Sprintf("container %s: %s - %s", cs.Name, reason, cs.State.Waiting.Message))
			}
		}
		if cs.LastTerminationState.Terminated != nil {
			if cs.LastTerminationState.Terminated.Reason == "OOMKilled" {
				warnings = append(warnings, fmt.Sprintf("container %s: OOMKilled", cs.Name))
			}
		}
	}
	return warnings
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

func getStatefulSetStatus(s *appsv1.StatefulSet) string {
	if s.Status.ReadyReplicas == s.Status.Replicas {
		return "Healthy"
	}
	return "Degraded"
}

func getDaemonSetStatus(d *appsv1.DaemonSet) string {
	if d.Status.DesiredNumberScheduled == d.Status.NumberReady {
		return "Healthy"
	}
	return "Degraded"
}

func getReplicaSetStatus(rs *appsv1.ReplicaSet) string {
	if rs.Status.ReadyReplicas == rs.Status.Replicas {
		return "Healthy"
	}
	return "Degraded"
}

func StatusIcon(status string) string {
	switch {
	case status == "Running" || status == "Healthy" || status == "Active" || status == "Bound" || status == "Succeeded":
		return "🟢"
	case status == "Pending" || status == "Degraded" || status == "NoBackend":
		return "🟡"
	case status == "Failed" || status == "Unavailable" || status == "CrashLoopBackOff" ||
		status == "ImagePullBackOff" || status == "OOMKilled" || status == "ErrImagePull":
		return "❌"
	default:
		if strings.HasPrefix(status, "CrashLoop") || strings.HasPrefix(status, "ImagePull") ||
			strings.HasPrefix(status, "OOM") || strings.HasPrefix(status, "Err") {
			return "❌"
		}
		return "⚪"
	}
}

func GetStatusColor(status string) string {
	switch {
	case status == "Running" || status == "Healthy" || status == "Active" || status == "Bound" || status == "Succeeded":
		return "green"
	case status == "Pending" || status == "Degraded" || status == "NoBackend":
		return "yellow"
	case status == "Failed" || status == "Unavailable" || status == "CrashLoopBackOff" ||
		status == "ImagePullBackOff" || status == "OOMKilled" || status == "ErrImagePull":
		return "red"
	default:
		if strings.HasPrefix(status, "CrashLoop") || strings.HasPrefix(status, "ImagePull") ||
			strings.HasPrefix(status, "OOM") || strings.HasPrefix(status, "Err") {
			return "red"
		}
		return "white"
	}
}
