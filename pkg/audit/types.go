package audit

import (
	"k8s.io/apimachinery/pkg/api/resource"
)

type PolicyLimits struct {
	MaxPods            *int64  `yaml:"maxPods,omitempty" json:"maxPods,omitempty"`
	MaxDeployments     *int64  `yaml:"maxDeployments,omitempty" json:"maxDeployments,omitempty"`
	MaxServices        *int64  `yaml:"maxServices,omitempty" json:"maxServices,omitempty"`
	MaxTotalCPU        *string `yaml:"maxTotalCPU,omitempty" json:"maxTotalCPU,omitempty"`
	MaxTotalMemory     *string `yaml:"maxTotalMemory,omitempty" json:"maxTotalMemory,omitempty"`
	MaxContainerCPU    *string `yaml:"maxContainerCPU,omitempty" json:"maxContainerCPU,omitempty"`
	MaxContainerMemory *string `yaml:"maxContainerMemory,omitempty" json:"maxContainerMemory,omitempty"`
}

type Policy struct {
	Name     string       `yaml:"name" json:"name"`
	Scope    string       `yaml:"scope" json:"scope"`
	Targets  []string     `yaml:"targets,omitempty" json:"targets,omitempty"`
	Limits   PolicyLimits `yaml:"limits" json:"limits"`
	Action   string       `yaml:"action" json:"action"`
	Inherits string       `yaml:"inherits,omitempty" json:"inherits,omitempty"`
}

type PolicyFile struct {
	Policies []Policy `yaml:"policies"`
}

type NamespaceStats struct {
	Namespace        string `json:"namespace"`
	PodCount         int64  `json:"podCount"`
	DeploymentCount  int64  `json:"deploymentCount"`
	ServiceCount     int64  `json:"serviceCount"`
	TotalCPURequest  string `json:"totalCPURequest"`
	TotalMemRequest  string `json:"totalMemRequest"`
	MaxContainerCPU  string `json:"maxContainerCPU"`
	MaxContainerMem  string `json:"maxContainerMem"`
	totalCPUQuantity resource.Quantity
	totalMemQuantity resource.Quantity
	maxCPUQuantity   resource.Quantity
	maxMemQuantity   resource.Quantity
}

type ViolationDimension string

const (
	DimMaxPods            ViolationDimension = "maxPods"
	DimMaxDeployments     ViolationDimension = "maxDeployments"
	DimMaxServices        ViolationDimension = "maxServices"
	DimMaxTotalCPU        ViolationDimension = "maxTotalCPU"
	DimMaxTotalMemory     ViolationDimension = "maxTotalMemory"
	DimMaxContainerCPU    ViolationDimension = "maxContainerCPU"
	DimMaxContainerMemory ViolationDimension = "maxContainerMemory"
)

type Violation struct {
	Namespace    string             `json:"namespace"`
	Dimension    ViolationDimension `json:"dimension"`
	CurrentValue string             `json:"currentValue"`
	PolicyLimit  string             `json:"policyLimit"`
	OverPercent  float64            `json:"overPercent"`
	Action       string             `json:"action"`
	PolicyName   string             `json:"policyName"`
}

type ComplianceItem struct {
	Dimension    ViolationDimension `json:"dimension"`
	CurrentValue string             `json:"currentValue"`
	PolicyLimit  string             `json:"policyLimit"`
	PolicyName   string             `json:"policyName"`
}

type NamespaceAuditResult struct {
	Namespace  string           `json:"namespace"`
	Compliant  []ComplianceItem `json:"compliant,omitempty"`
	Violations []Violation      `json:"violations,omitempty"`
}

type GlobalStats struct {
	TotalNamespaces int `json:"totalNamespaces"`
	CompliantCount  int `json:"compliantCount"`
	ViolationCount  int `json:"violationCount"`
	WarnCount       int `json:"warnCount"`
	BlockCount      int `json:"blockCount"`
	ReportCount     int `json:"reportCount"`
}

type AuditReport struct {
	Timestamp   string                 `json:"timestamp"`
	ClusterName string                `json:"clusterName"`
	PolicyFile  string                 `json:"policyFile"`
	Namespaces  []NamespaceAuditResult `json:"namespaces"`
	GlobalStats GlobalStats            `json:"globalStats"`
}

type TrendDirection string

const (
	TrendUp   TrendDirection = "increased"
	TrendDown TrendDirection = "decreased"
	TrendFlat TrendDirection = "flat"
)

type TrendItem struct {
	Namespace string             `json:"namespace"`
	Dimension ViolationDimension `json:"dimension"`
	Direction TrendDirection     `json:"direction"`
	OldValue  string             `json:"oldValue"`
	NewValue  string             `json:"newValue"`
	Status    string             `json:"status"`
}

type DiffReport struct {
	CurrentTime     string      `json:"currentTime"`
	PreviousTime    string      `json:"previousTime"`
	Trends          []TrendItem `json:"trends"`
	OldCompliance   float64     `json:"oldCompliance"`
	NewCompliance   float64     `json:"newCompliance"`
	ComplianceDelta float64     `json:"complianceDelta"`
}

func actionSeverity(action string) int {
	switch action {
	case "block":
		return 3
	case "warn":
		return 2
	case "report":
		return 1
	default:
		return 0
	}
}
