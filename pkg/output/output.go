package output

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"

	"github.com/k8s-topo-cli/pkg/diagnosis"
	"github.com/k8s-topo-cli/pkg/discovery"
	"github.com/k8s-topo-cli/pkg/metrics"
	"github.com/k8s-topo-cli/pkg/topology"
)

type ClusterReport struct {
	APIVersion    string            `json:"apiVersion"`
	Kind          string            `json:"kind"`
	ResourceCounts discovery.ResourceCounts `json:"resourceCounts"`
	Namespaces    []NamespaceReport `json:"namespaces,omitempty"`
	Alerts        AlertsReport      `json:"alerts,omitempty"`
}

type NamespaceReport struct {
	Name         string           `json:"name"`
	Deployments  []ResourceReport `json:"deployments,omitempty"`
	StatefulSets []ResourceReport `json:"statefulSets,omitempty"`
	DaemonSets   []ResourceReport `json:"daemonSets,omitempty"`
	Services     []ServiceReport  `json:"services,omitempty"`
	Ingresses    []IngressReport  `json:"ingresses,omitempty"`
	Pods         []PodReport      `json:"pods,omitempty"`
	PVCs         []PVCReport      `json:"pvcs,omitempty"`
}

type ResourceReport struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type ServiceReport struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Status       string   `json:"status"`
	Backends     []string `json:"backends,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

type IngressReport struct {
	Name     string   `json:"name"`
	Backends []string `json:"backends,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

type PodReport struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Node       string `json:"node,omitempty"`
	Restarts   int32  `json:"restarts"`
	CPU        string `json:"cpu,omitempty"`
	Memory     string `json:"memory,omitempty"`
	OOMKilled  bool   `json:"oomKilled,omitempty"`
}

type PVCReport struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	PV     string `json:"pv,omitempty"`
}

type AlertsReport struct {
	ServiceAlerts  []string `json:"serviceAlerts,omitempty"`
	IngressAlerts  []string `json:"ingressAlerts,omitempty"`
	WarningEvents  []EventReport `json:"warningEvents,omitempty"`
}

type EventReport struct {
	Count     int32  `json:"count"`
	Reason    string `json:"reason"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Message   string `json:"message"`
}

func BuildReport(topo *topology.Topology, res *discovery.DiscoveredResources, metricsResult *metrics.MetricsResult, diagResult *diagnosis.DiagnosisResult) *ClusterReport {
	report := &ClusterReport{
		APIVersion:    "k8s-topo-cli/v1",
		Kind:          "ClusterReport",
		ResourceCounts: res.Counts,
	}

	for _, root := range topo.Roots {
		nsReport := NamespaceReport{
			Name: root.Name,
		}

		for _, child := range root.Children {
			switch child.Type {
			case topology.TypeDeployment:
				nsReport.Deployments = append(nsReport.Deployments, ResourceReport{
					Name:   child.Name,
					Status: child.Status,
				})
			case topology.TypeStatefulSet:
				nsReport.StatefulSets = append(nsReport.StatefulSets, ResourceReport{
					Name:   child.Name,
					Status: child.Status,
				})
			case topology.TypeDaemonSet:
				nsReport.DaemonSets = append(nsReport.DaemonSets, ResourceReport{
					Name:   child.Name,
					Status: child.Status,
				})
			case topology.TypeService:
				svcReport := ServiceReport{
					Name:     child.Name,
					Status:   child.Status,
					Warnings: child.Warnings,
				}
				if svc, ok := child.Resource.(*corev1.Service); ok {
					svcReport.Type = string(svc.Spec.Type)
				}
				nsReport.Services = append(nsReport.Services, svcReport)
			case topology.TypeIngress:
				ingReport := IngressReport{
					Name:     child.Name,
					Warnings: child.Warnings,
				}
				nsReport.Ingresses = append(nsReport.Ingresses, ingReport)
			case topology.TypePod:
				podReport := PodReport{
					Name:   child.Name,
					Status: child.Status,
				}
				if pod, ok := child.Resource.(*corev1.Pod); ok {
					podReport.Node = pod.Spec.NodeName
					for _, cs := range pod.Status.ContainerStatuses {
						podReport.Restarts += cs.RestartCount
					}
					if metricsResult != nil {
						cpu, mem := metrics.GetPodMetricsPercent(metricsResult, pod.Namespace, pod.Name)
						podReport.CPU = cpu
						podReport.Memory = mem
					}
				}
				nsReport.Pods = append(nsReport.Pods, podReport)
			case topology.TypePVC:
				pvcReport := PVCReport{
					Name:   child.Name,
					Status: child.Status,
				}
				for _, pvChild := range child.Children {
					if pvChild.Type == topology.TypePV {
						pvcReport.PV = pvChild.Name
					}
				}
				nsReport.PVCs = append(nsReport.PVCs, pvcReport)
			}
		}

		report.Namespaces = append(report.Namespaces, nsReport)
	}

	report.Alerts.ServiceAlerts = topo.ServiceAlerts
	report.Alerts.IngressAlerts = topo.IngressAlerts

	if diagResult != nil {
		for _, e := range diagResult.WarningEvents {
			report.Alerts.WarningEvents = append(report.Alerts.WarningEvents, EventReport{
				Count:     e.Count,
				Reason:    e.Reason,
				Kind:      e.Kind,
				Name:      e.Name,
				Namespace: e.Namespace,
				Message:   e.Message,
			})
		}
	}

	return report
}

func ToJSON(report *ClusterReport) (string, error) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func ToYAML(report *ClusterReport) (string, error) {
	data, err := yaml.Marshal(report)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func ToDOT(topo *topology.Topology) string {
	var sb strings.Builder
	sb.WriteString("digraph k8s_topology {\n")
	sb.WriteString("  rankdir=LR;\n")
	sb.WriteString("  node [shape=box, style=filled, fontname=\"Arial\"];\n")
	sb.WriteString("  edge [color=\"#666666\"];\n\n")

	for _, root := range topo.Roots {
		nsID := dotID("ns", root.Name)
		sb.WriteString(fmt.Sprintf("  %s [label=\"%s\", fillcolor=\"#E8F5E9\", color=\"#4CAF50\"];\n", nsID, root.Name))

		for _, child := range root.Children {
			childID := dotID(dotKind(child.Type), child.Name)
			color := dotColor(child.Status)
			sb.WriteString(fmt.Sprintf("  %s [label=\"%s\\n%s\", fillcolor=\"%s\"];\n",
				childID, child.Name, child.Type, color))
			sb.WriteString(fmt.Sprintf("  %s -> %s;\n", nsID, childID))

			for _, grandchild := range child.Children {
				gcID := dotID(dotKind(grandchild.Type), grandchild.Name)
				gcColor := dotColor(grandchild.Status)
				sb.WriteString(fmt.Sprintf("  %s [label=\"%s\\n%s\", fillcolor=\"%s\"];\n",
					gcID, grandchild.Name, grandchild.Type, gcColor))
				sb.WriteString(fmt.Sprintf("  %s -> %s;\n", childID, gcID))
			}
		}

		sb.WriteString("\n")
	}

	sb.WriteString("}\n")
	return sb.String()
}

func dotID(prefix, name string) string {
	clean := strings.ReplaceAll(name, "-", "_")
	clean = strings.ReplaceAll(clean, ".", "_")
	return prefix + "_" + clean
}

func dotKind(t topology.ResourceType) string {
	return strings.ToLower(string(t))
}

func dotColor(status string) string {
	switch {
	case status == "Running" || status == "Healthy" || status == "Active" || status == "Bound":
		return "#C8E6C9"
	case status == "Pending" || status == "Degraded":
		return "#FFF9C4"
	case status == "Failed" || status == "CrashLoopBackOff" || status == "ImagePullBackOff" || status == "OOMKilled":
		return "#FFCDD2"
	default:
		return "#F5F5F5"
	}
}
