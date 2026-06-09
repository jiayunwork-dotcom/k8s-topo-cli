package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"

	"github.com/k8s-topo-cli/pkg/discovery"
)

type SnapshotData struct {
	Timestamp   string                   `json:"timestamp"`
	Pods        []PodSnapshot            `json:"pods"`
	Deployments []DeploymentSnapshot     `json:"deployments"`
	Services    []ServiceSnapshot         `json:"services"`
	Ingresses   []IngressSnapshot         `json:"ingresses"`
	Nodes       []NodeSnapshot            `json:"nodes"`
	Counts      discovery.ResourceCounts  `json:"counts"`
}

type PodSnapshot struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Status     string `json:"status"`
	UID        string `json:"uid"`
}

type DeploymentSnapshot struct {
	Name            string `json:"name"`
	Namespace       string `json:"namespace"`
	Replicas        int32  `json:"replicas"`
	ReadyReplicas   int32  `json:"readyReplicas"`
	UID             string `json:"uid"`
}

type ServiceSnapshot struct {
	Name      string          `json:"name"`
	Namespace string          `json:"namespace"`
	Ports     []ServicePortSnapshot `json:"ports"`
	UID       string          `json:"uid"`
}

type ServicePortSnapshot struct {
	Name       string `json:"name"`
	Port       int32  `json:"port"`
	TargetPort string `json:"targetPort"`
	Protocol   string `json:"protocol"`
}

type IngressSnapshot struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	UID       string `json:"uid"`
}

type NodeSnapshot struct {
	Name string `json:"name"`
	UID  string `json:"uid"`
}

type DiffResult struct {
	Added   []DiffEntry
	Removed []DiffEntry
	Changed []DiffEntry
}

type DiffEntry struct {
	Type      string
	Namespace string
	Name      string
	Detail    string
	Before    string
	After     string
}

func snapshotDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".k8s-topo", "snapshots")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func SaveSnapshot(name string, res *discovery.DiscoveredResources) error {
	data := buildSnapshotData(res)

	dir, err := snapshotDir()
	if err != nil {
		return fmt.Errorf("failed to create snapshot directory: %w", err)
	}

	filePath := filepath.Join(dir, name+".json")
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize snapshot: %w", err)
	}

	if err := os.WriteFile(filePath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write snapshot file: %w", err)
	}

	return nil
}

func LoadSnapshot(name string) (*SnapshotData, error) {
	dir, err := snapshotDir()
	if err != nil {
		return nil, err
	}

	filePath := filepath.Join(dir, name+".json")
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("snapshot '%s' not found", name)
		}
		return nil, fmt.Errorf("failed to read snapshot: %w", err)
	}

	var snapshot SnapshotData
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("failed to parse snapshot: %w", err)
	}

	return &snapshot, nil
}

func DiffSnapshot(current *SnapshotData, saved *SnapshotData) *DiffResult {
	result := &DiffResult{}

	currentPodMap := make(map[string]PodSnapshot)
	savedPodMap := make(map[string]PodSnapshot)
	for _, p := range current.Pods {
		currentPodMap[p.Namespace+"/"+p.Name] = p
	}
	for _, p := range saved.Pods {
		savedPodMap[p.Namespace+"/"+p.Name] = p
	}

	for key, cp := range currentPodMap {
		if sp, ok := savedPodMap[key]; !ok {
			result.Added = append(result.Added, DiffEntry{
				Type: "Pod", Namespace: cp.Namespace, Name: cp.Name,
				Detail: "Pod added",
			})
		} else {
			if cp.Status != sp.Status {
				result.Changed = append(result.Changed, DiffEntry{
					Type: "Pod", Namespace: cp.Namespace, Name: cp.Name,
					Detail: "Pod status changed",
					Before: sp.Status, After: cp.Status,
				})
			}
		}
	}
	for key, sp := range savedPodMap {
		if _, ok := currentPodMap[key]; !ok {
			result.Removed = append(result.Removed, DiffEntry{
				Type: "Pod", Namespace: sp.Namespace, Name: sp.Name,
				Detail: "Pod removed",
			})
		}
	}

	currentDeployMap := make(map[string]DeploymentSnapshot)
	savedDeployMap := make(map[string]DeploymentSnapshot)
	for _, d := range current.Deployments {
		currentDeployMap[d.Namespace+"/"+d.Name] = d
	}
	for _, d := range saved.Deployments {
		savedDeployMap[d.Namespace+"/"+d.Name] = d
	}

	for key, cd := range currentDeployMap {
		if sd, ok := savedDeployMap[key]; !ok {
			result.Added = append(result.Added, DiffEntry{
				Type: "Deployment", Namespace: cd.Namespace, Name: cd.Name,
				Detail: "Deployment added",
			})
		} else {
			if cd.Replicas != sd.Replicas {
				result.Changed = append(result.Changed, DiffEntry{
					Type: "Deployment", Namespace: cd.Namespace, Name: cd.Name,
					Detail: "Replica count changed",
					Before: fmt.Sprintf("%d", sd.Replicas), After: fmt.Sprintf("%d", cd.Replicas),
				})
			}
			if cd.ReadyReplicas != sd.ReadyReplicas {
				result.Changed = append(result.Changed, DiffEntry{
					Type: "Deployment", Namespace: cd.Namespace, Name: cd.Name,
					Detail: "Ready replicas changed",
					Before: fmt.Sprintf("%d", sd.ReadyReplicas), After: fmt.Sprintf("%d", cd.ReadyReplicas),
				})
			}
		}
	}
	for key, sd := range savedDeployMap {
		if _, ok := currentDeployMap[key]; !ok {
			result.Removed = append(result.Removed, DiffEntry{
				Type: "Deployment", Namespace: sd.Namespace, Name: sd.Name,
				Detail: "Deployment removed",
			})
		}
	}

	currentSvcMap := make(map[string]ServiceSnapshot)
	savedSvcMap := make(map[string]ServiceSnapshot)
	for _, s := range current.Services {
		currentSvcMap[s.Namespace+"/"+s.Name] = s
	}
	for _, s := range saved.Services {
		savedSvcMap[s.Namespace+"/"+s.Name] = s
	}

	for key, cs := range currentSvcMap {
		if ss, ok := savedSvcMap[key]; !ok {
			result.Added = append(result.Added, DiffEntry{
				Type: "Service", Namespace: cs.Namespace, Name: cs.Name,
				Detail: "Service added",
			})
		} else {
			diffServicePorts(&cs, &ss, result)
		}
	}
	for key, ss := range savedSvcMap {
		if _, ok := currentSvcMap[key]; !ok {
			result.Removed = append(result.Removed, DiffEntry{
				Type: "Service", Namespace: ss.Namespace, Name: ss.Name,
				Detail: "Service removed",
			})
		}
	}

	currentIngMap := make(map[string]IngressSnapshot)
	savedIngMap := make(map[string]IngressSnapshot)
	for _, i := range current.Ingresses {
		currentIngMap[i.Namespace+"/"+i.Name] = i
	}
	for _, i := range saved.Ingresses {
		savedIngMap[i.Namespace+"/"+i.Name] = i
	}
	for key, ci := range currentIngMap {
		if _, ok := savedIngMap[key]; !ok {
			result.Added = append(result.Added, DiffEntry{
				Type: "Ingress", Namespace: ci.Namespace, Name: ci.Name,
				Detail: "Ingress added",
			})
		}
	}
	for key, si := range savedIngMap {
		if _, ok := currentIngMap[key]; !ok {
			result.Removed = append(result.Removed, DiffEntry{
				Type: "Ingress", Namespace: si.Namespace, Name: si.Name,
				Detail: "Ingress removed",
			})
		}
	}

	return result
}

func diffServicePorts(current, saved *ServiceSnapshot, result *DiffResult) {
	currentPorts := make(map[string]ServicePortSnapshot)
	savedPorts := make(map[string]ServicePortSnapshot)
	for _, p := range current.Ports {
		key := fmt.Sprintf("%s/%d/%s", p.Name, p.Port, p.Protocol)
		currentPorts[key] = p
	}
	for _, p := range saved.Ports {
		key := fmt.Sprintf("%s/%d/%s", p.Name, p.Port, p.Protocol)
		savedPorts[key] = p
	}
	for key, cp := range currentPorts {
		if _, ok := savedPorts[key]; !ok {
			result.Changed = append(result.Changed, DiffEntry{
				Type: "Service", Namespace: current.Namespace, Name: current.Name,
				Detail: "Port added",
				After: fmt.Sprintf("%s:%d→%s (%s)", cp.Name, cp.Port, cp.TargetPort, cp.Protocol),
			})
		}
	}
	for key, sp := range savedPorts {
		if _, ok := currentPorts[key]; !ok {
			result.Changed = append(result.Changed, DiffEntry{
				Type: "Service", Namespace: current.Namespace, Name: current.Name,
				Detail: "Port removed",
				Before: fmt.Sprintf("%s:%d→%s (%s)", sp.Name, sp.Port, sp.TargetPort, sp.Protocol),
			})
		}
	}
}

func RenderDiff(diff *DiffResult, snapshotName string) string {
	var sb strings.Builder

	sb.WriteString("═══ Snapshot Diff Report ═══\n")
	sb.WriteString(fmt.Sprintf("Comparing current cluster state vs snapshot: %s\n\n", snapshotName))

	if len(diff.Added) == 0 && len(diff.Removed) == 0 && len(diff.Changed) == 0 {
		sb.WriteString("No differences found. Cluster state matches snapshot.\n")
		return sb.String()
	}

	if len(diff.Added) > 0 {
		sb.WriteString("🟢 Added Resources:\n")
		for _, entry := range diff.Added {
			sb.WriteString(fmt.Sprintf("  + %s %s/%s — %s\n", entry.Type, entry.Namespace, entry.Name, entry.Detail))
		}
		sb.WriteString("\n")
	}

	if len(diff.Removed) > 0 {
		sb.WriteString("🔴 Removed Resources:\n")
		for _, entry := range diff.Removed {
			sb.WriteString(fmt.Sprintf("  - %s %s/%s — %s\n", entry.Type, entry.Namespace, entry.Name, entry.Detail))
		}
		sb.WriteString("\n")
	}

	if len(diff.Changed) > 0 {
		sb.WriteString("🟡 Changed Resources:\n")
		for _, entry := range diff.Changed {
			if entry.Before != "" && entry.After != "" {
				sb.WriteString(fmt.Sprintf("  ~ %s %s/%s — %s: %s → %s\n",
					entry.Type, entry.Namespace, entry.Name, entry.Detail, entry.Before, entry.After))
			} else if entry.After != "" {
				sb.WriteString(fmt.Sprintf("  ~ %s %s/%s — %s: %s\n",
					entry.Type, entry.Namespace, entry.Name, entry.Detail, entry.After))
			} else if entry.Before != "" {
				sb.WriteString(fmt.Sprintf("  ~ %s %s/%s — %s: was %s\n",
					entry.Type, entry.Namespace, entry.Name, entry.Detail, entry.Before))
			} else {
				sb.WriteString(fmt.Sprintf("  ~ %s %s/%s — %s\n",
					entry.Type, entry.Namespace, entry.Name, entry.Detail))
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString(fmt.Sprintf("Summary: %d added, %d removed, %d changed\n",
		len(diff.Added), len(diff.Removed), len(diff.Changed)))

	return sb.String()
}

func buildSnapshotData(res *discovery.DiscoveredResources) *SnapshotData {
	data := &SnapshotData{
		Timestamp: time.Now().Format(time.RFC3339),
		Counts:    res.Counts,
	}

	for _, pod := range res.Pods {
		status := getPodStatusForSnapshot(pod)
		data.Pods = append(data.Pods, PodSnapshot{
			Name:      pod.Name,
			Namespace: pod.Namespace,
			Status:    status,
			UID:       string(pod.UID),
		})
	}

	for _, deploy := range res.Deployments {
		data.Deployments = append(data.Deployments, DeploymentSnapshot{
			Name:          deploy.Name,
			Namespace:     deploy.Namespace,
			Replicas:      deploy.Status.Replicas,
			ReadyReplicas: deploy.Status.ReadyReplicas,
			UID:           string(deploy.UID),
		})
	}

	for _, svc := range res.Services {
		var ports []ServicePortSnapshot
		for _, p := range svc.Spec.Ports {
			ports = append(ports, ServicePortSnapshot{
				Name:       p.Name,
				Port:       p.Port,
				TargetPort: p.TargetPort.String(),
				Protocol:   string(p.Protocol),
			})
		}
		data.Services = append(data.Services, ServiceSnapshot{
			Name:      svc.Name,
			Namespace: svc.Namespace,
			Ports:     ports,
			UID:       string(svc.UID),
		})
	}

	for _, ing := range res.Ingresses {
		data.Ingresses = append(data.Ingresses, IngressSnapshot{
			Name:      ing.Name,
			Namespace: ing.Namespace,
			UID:       string(ing.UID),
		})
	}

	for _, node := range res.Nodes {
		data.Nodes = append(data.Nodes, NodeSnapshot{
			Name: node.Name,
			UID:  string(node.UID),
		})
	}

	return data
}

func BuildCurrentSnapshot(res *discovery.DiscoveredResources) *SnapshotData {
	return buildSnapshotData(res)
}

func getPodStatusForSnapshot(pod *corev1.Pod) string {
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
	return string(pod.Status.Phase)
}

func ListSnapshots() ([]string, error) {
	dir, err := snapshotDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, strings.TrimSuffix(entry.Name(), ".json"))
		}
	}
	return names, nil
}

func ConvertToSnapshot(res *discovery.DiscoveredResources) *SnapshotData {
	return buildSnapshotData(res)
}

func init() {
	_ = appsv1.Deployment{}
	_ = corev1.Pod{}
	_ = networkingv1.Ingress{}
}
