package audit

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/k8s-topo-cli/pkg/discovery"
)

type PolicyLimits struct {
	MaxPods           *int64   `yaml:"maxPods,omitempty" json:"maxPods,omitempty"`
	MaxDeployments    *int64   `yaml:"maxDeployments,omitempty" json:"maxDeployments,omitempty"`
	MaxServices       *int64   `yaml:"maxServices,omitempty" json:"maxServices,omitempty"`
	MaxTotalCPU       *string  `yaml:"maxTotalCPU,omitempty" json:"maxTotalCPU,omitempty"`
	MaxTotalMemory    *string  `yaml:"maxTotalMemory,omitempty" json:"maxTotalMemory,omitempty"`
	MaxContainerCPU   *string  `yaml:"maxContainerCPU,omitempty" json:"maxContainerCPU,omitempty"`
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
	Namespace         string `json:"namespace"`
	PodCount          int64  `json:"podCount"`
	DeploymentCount   int64  `json:"deploymentCount"`
	ServiceCount      int64  `json:"serviceCount"`
	TotalCPURequest   string `json:"totalCPURequest"`
	TotalMemRequest   string `json:"totalMemRequest"`
	MaxContainerCPU   string `json:"maxContainerCPU"`
	MaxContainerMem   string `json:"maxContainerMem"`
	totalCPUQuantity  resource.Quantity
	totalMemQuantity  resource.Quantity
	maxCPUQuantity    resource.Quantity
	maxMemQuantity    resource.Quantity
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
	Namespace   string           `json:"namespace"`
	Compliant   []ComplianceItem `json:"compliant,omitempty"`
	Violations  []Violation      `json:"violations,omitempty"`
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
	Timestamp      string               `json:"timestamp"`
	ClusterName    string               `json:"clusterName"`
	PolicyFile     string               `json:"policyFile"`
	Namespaces     []NamespaceAuditResult `json:"namespaces"`
	GlobalStats    GlobalStats          `json:"globalStats"`
}

type TrendDirection string

const (
	TrendUp      TrendDirection = "increased"
	TrendDown    TrendDirection = "decreased"
	TrendFlat    TrendDirection = "flat"
)

type TrendItem struct {
	Namespace  string           `json:"namespace"`
	Dimension  ViolationDimension `json:"dimension"`
	Direction  TrendDirection   `json:"direction"`
	OldValue   string           `json:"oldValue"`
	NewValue   string           `json:"newValue"`
	Status     string           `json:"status"`
}

type DiffReport struct {
	CurrentTime     string      `json:"currentTime"`
	PreviousTime    string      `json:"previousTime"`
	Trends          []TrendItem `json:"trends"`
	OldCompliance   float64     `json:"oldCompliance"`
	NewCompliance   float64     `json:"newCompliance"`
	ComplianceDelta float64     `json:"complianceDelta"`
}

func LoadPolicies(filePath string) ([]Policy, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read policy file: %w", err)
	}

	var pf PolicyFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("failed to parse policy YAML: %w", err)
	}

	for i, p := range pf.Policies {
		if p.Name == "" {
			return nil, fmt.Errorf("policy #%d: name is required", i+1)
		}
		if p.Scope != "namespace" && p.Scope != "cluster" {
			return nil, fmt.Errorf("policy #%d (%s): scope must be 'namespace' or 'cluster'", i+1, p.Name)
		}
		if p.Action != "warn" && p.Action != "block" && p.Action != "report" {
			return nil, fmt.Errorf("policy #%d (%s): action must be 'warn', 'block', or 'report'", i+1, p.Name)
		}
		if p.Scope == "namespace" && len(p.Targets) == 0 {
			return nil, fmt.Errorf("policy #%d (%s): namespace-scoped policy must specify targets", i+1, p.Name)
		}
	}

	if err := resolveInheritance(pf.Policies); err != nil {
		return nil, err
	}

	return pf.Policies, nil
}

func resolveInheritance(policies []Policy) error {
	pm := make(map[string]*Policy)
	for i := range policies {
		pm[policies[i].Name] = &policies[i]
	}

	visited := make(map[string]bool)
	inStack := make(map[string]bool)

	var resolve func(name string, depth int) error
	resolve = func(name string, depth int) error {
		if depth > 3 {
			return fmt.Errorf("inheritance chain exceeds 3 levels at policy '%s'", name)
		}
		if inStack[name] {
			return fmt.Errorf("circular inheritance detected at policy '%s'", name)
		}
		if visited[name] {
			return nil
		}

		p, ok := pm[name]
		if !ok {
			return fmt.Errorf("policy '%s' not found", name)
		}

		if p.Inherits == "" {
			visited[name] = true
			return nil
		}

		inStack[name] = true
		if err := resolve(p.Inherits, depth+1); err != nil {
			return err
		}
		inStack[name] = false

		parent, ok := pm[p.Inherits]
		if !ok {
			return fmt.Errorf("parent policy '%s' not found for '%s'", p.Inherits, p.Name)
		}

		if p.Limits.MaxPods == nil {
			p.Limits.MaxPods = parent.Limits.MaxPods
		}
		if p.Limits.MaxDeployments == nil {
			p.Limits.MaxDeployments = parent.Limits.MaxDeployments
		}
		if p.Limits.MaxServices == nil {
			p.Limits.MaxServices = parent.Limits.MaxServices
		}
		if p.Limits.MaxTotalCPU == nil {
			p.Limits.MaxTotalCPU = parent.Limits.MaxTotalCPU
		}
		if p.Limits.MaxTotalMemory == nil {
			p.Limits.MaxTotalMemory = parent.Limits.MaxTotalMemory
		}
		if p.Limits.MaxContainerCPU == nil {
			p.Limits.MaxContainerCPU = parent.Limits.MaxContainerCPU
		}
		if p.Limits.MaxContainerMemory == nil {
			p.Limits.MaxContainerMemory = parent.Limits.MaxContainerMemory
		}

		visited[name] = true
		return nil
	}

	for _, p := range policies {
		if err := resolve(p.Name, 1); err != nil {
			return err
		}
	}

	return nil
}

func CollectNamespaceStats(res *discovery.DiscoveredResources) []NamespaceStats {
	nsMap := make(map[string]*NamespaceStats)

	for _, ns := range res.Namespaces {
		nsMap[ns.Name] = &NamespaceStats{
			Namespace:        ns.Name,
			totalCPUQuantity: resource.MustParse("0"),
			totalMemQuantity: resource.MustParse("0"),
			maxCPUQuantity:   resource.MustParse("0"),
			maxMemQuantity:   resource.MustParse("0"),
		}
	}

	for _, pod := range res.Pods {
		stats, ok := nsMap[pod.Namespace]
		if !ok {
			stats = &NamespaceStats{
				Namespace:        pod.Namespace,
				totalCPUQuantity: resource.MustParse("0"),
				totalMemQuantity: resource.MustParse("0"),
				maxCPUQuantity:   resource.MustParse("0"),
				maxMemQuantity:   resource.MustParse("0"),
			}
			nsMap[pod.Namespace] = stats
		}
		stats.PodCount++

		for _, c := range pod.Spec.Containers {
			if req := c.Resources.Requests; req != nil {
				if cpu, ok := req[corev1.ResourceCPU]; ok {
					stats.totalCPUQuantity.Add(cpu)
					if cpu.Cmp(stats.maxCPUQuantity) > 0 {
						stats.maxCPUQuantity = cpu.DeepCopy()
					}
				}
				if mem, ok := req[corev1.ResourceMemory]; ok {
					stats.totalMemQuantity.Add(mem)
					if mem.Cmp(stats.maxMemQuantity) > 0 {
						stats.maxMemQuantity = mem.DeepCopy()
					}
				}
			}
		}
	}

	for _, dep := range res.Deployments {
		if stats, ok := nsMap[dep.Namespace]; ok {
			stats.DeploymentCount++
		}
	}

	for _, svc := range res.Services {
		if stats, ok := nsMap[svc.Namespace]; ok {
			stats.ServiceCount++
		}
	}

	var result []NamespaceStats
	for _, ns := range res.Namespaces {
		stats := nsMap[ns.Name]
		stats.TotalCPURequest = stats.totalCPUQuantity.String()
		stats.TotalMemRequest = stats.totalMemQuantity.String()
		stats.MaxContainerCPU = stats.maxCPUQuantity.String()
		stats.MaxContainerMem = stats.maxMemQuantity.String()
		result = append(result, *stats)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Namespace < result[j].Namespace
	})

	return result
}



func EvaluatePolicies(policies []Policy, stats []NamespaceStats) []NamespaceAuditResult {
	nsResultMap := make(map[string]*NamespaceAuditResult)
	for _, s := range stats {
		nsResultMap[s.Namespace] = &NamespaceAuditResult{
			Namespace:  s.Namespace,
			Compliant:  []ComplianceItem{},
			Violations: []Violation{},
		}
	}

	for _, policy := range policies {
		var targetStats []NamespaceStats
		if policy.Scope == "cluster" {
			targetStats = stats
		} else {
			targetSet := make(map[string]bool)
			for _, t := range policy.Targets {
				targetSet[t] = true
			}
			for _, s := range stats {
				if targetSet[s.Namespace] {
					targetStats = append(targetStats, s)
				}
			}
		}

		for _, s := range targetStats {
			nsResult, ok := nsResultMap[s.Namespace]
			if !ok {
				continue
			}

			evaluateLimits(policy, s, nsResult)
		}
	}

	var results []NamespaceAuditResult
	for _, s := range stats {
		if r, ok := nsResultMap[s.Namespace]; ok {
			deduplicateViolations(r)
			sortViolations(r)
			results = append(results, *r)
		}
	}

	return results
}

func evaluateLimits(policy Policy, s NamespaceStats, nsResult *NamespaceAuditResult) {
	checkLimit(policy, s, nsResult, DimMaxPods, float64(s.PodCount), policy.Limits.MaxPods)
	checkLimit(policy, s, nsResult, DimMaxDeployments, float64(s.DeploymentCount), policy.Limits.MaxDeployments)
	checkLimit(policy, s, nsResult, DimMaxServices, float64(s.ServiceCount), policy.Limits.MaxServices)

	if policy.Limits.MaxTotalCPU != nil {
		limitQty := resource.MustParse(*policy.Limits.MaxTotalCPU)
		if s.totalCPUQuantity.Cmp(limitQty) > 0 {
			pct := float64(s.totalCPUQuantity.MilliValue())/float64(limitQty.MilliValue())*100 - 100
			addViolation(nsResult, Violation{
				Namespace:    s.Namespace,
				Dimension:    DimMaxTotalCPU,
				CurrentValue: s.TotalCPURequest,
				PolicyLimit:  *policy.Limits.MaxTotalCPU,
				OverPercent:  roundTwo(pct),
				Action:       policy.Action,
				PolicyName:   policy.Name,
			})
		} else {
			addCompliance(nsResult, ComplianceItem{
				Dimension:    DimMaxTotalCPU,
				CurrentValue: s.TotalCPURequest,
				PolicyLimit:  *policy.Limits.MaxTotalCPU,
				PolicyName:   policy.Name,
			})
		}
	}

	if policy.Limits.MaxTotalMemory != nil {
		limitQty := resource.MustParse(*policy.Limits.MaxTotalMemory)
		if s.totalMemQuantity.Cmp(limitQty) > 0 {
			pct := float64(s.totalMemQuantity.Value())/float64(limitQty.Value())*100 - 100
			addViolation(nsResult, Violation{
				Namespace:    s.Namespace,
				Dimension:    DimMaxTotalMemory,
				CurrentValue: s.TotalMemRequest,
				PolicyLimit:  *policy.Limits.MaxTotalMemory,
				OverPercent:  roundTwo(pct),
				Action:       policy.Action,
				PolicyName:   policy.Name,
			})
		} else {
			addCompliance(nsResult, ComplianceItem{
				Dimension:    DimMaxTotalMemory,
				CurrentValue: s.TotalMemRequest,
				PolicyLimit:  *policy.Limits.MaxTotalMemory,
				PolicyName:   policy.Name,
			})
		}
	}

	if policy.Limits.MaxContainerCPU != nil {
		limitQty := resource.MustParse(*policy.Limits.MaxContainerCPU)
		if s.maxCPUQuantity.Cmp(limitQty) > 0 {
			pct := float64(s.maxCPUQuantity.MilliValue())/float64(limitQty.MilliValue())*100 - 100
			addViolation(nsResult, Violation{
				Namespace:    s.Namespace,
				Dimension:    DimMaxContainerCPU,
				CurrentValue: s.MaxContainerCPU,
				PolicyLimit:  *policy.Limits.MaxContainerCPU,
				OverPercent:  roundTwo(pct),
				Action:       policy.Action,
				PolicyName:   policy.Name,
			})
		} else {
			addCompliance(nsResult, ComplianceItem{
				Dimension:    DimMaxContainerCPU,
				CurrentValue: s.MaxContainerCPU,
				PolicyLimit:  *policy.Limits.MaxContainerCPU,
				PolicyName:   policy.Name,
			})
		}
	}

	if policy.Limits.MaxContainerMemory != nil {
		limitQty := resource.MustParse(*policy.Limits.MaxContainerMemory)
		if s.maxMemQuantity.Cmp(limitQty) > 0 {
			pct := float64(s.maxMemQuantity.Value())/float64(limitQty.Value())*100 - 100
			addViolation(nsResult, Violation{
				Namespace:    s.Namespace,
				Dimension:    DimMaxContainerMemory,
				CurrentValue: s.MaxContainerMem,
				PolicyLimit:  *policy.Limits.MaxContainerMemory,
				OverPercent:  roundTwo(pct),
				Action:       policy.Action,
				PolicyName:   policy.Name,
			})
		} else {
			addCompliance(nsResult, ComplianceItem{
				Dimension:    DimMaxContainerMemory,
				CurrentValue: s.MaxContainerMem,
				PolicyLimit:  *policy.Limits.MaxContainerMemory,
				PolicyName:   policy.Name,
			})
		}
	}
}

func checkLimit(policy Policy, s NamespaceStats, nsResult *NamespaceAuditResult, dim ViolationDimension, current float64, limit *int64) {
	if limit == nil {
		return
	}
	limitVal := float64(*limit)
	if current > limitVal {
		pct := current/limitVal*100 - 100
		addViolation(nsResult, Violation{
			Namespace:    s.Namespace,
			Dimension:    dim,
			CurrentValue: fmt.Sprintf("%d", int64(current)),
			PolicyLimit:  fmt.Sprintf("%d", *limit),
			OverPercent:  roundTwo(pct),
			Action:       policy.Action,
			PolicyName:   policy.Name,
		})
	} else {
		addCompliance(nsResult, ComplianceItem{
			Dimension:    dim,
			CurrentValue: fmt.Sprintf("%d", int64(current)),
			PolicyLimit:  fmt.Sprintf("%d", *limit),
			PolicyName:   policy.Name,
		})
	}
}

func addViolation(nsResult *NamespaceAuditResult, v Violation) {
	for i, existing := range nsResult.Violations {
		if existing.Namespace == v.Namespace && existing.Dimension == v.Dimension {
			if actionSeverity(v.Action) > actionSeverity(existing.Action) {
				nsResult.Violations[i] = v
			}
			return
		}
	}
	nsResult.Violations = append(nsResult.Violations, v)
}

func addCompliance(nsResult *NamespaceAuditResult, c ComplianceItem) {
	for _, existing := range nsResult.Compliant {
		if existing.Dimension == c.Dimension && existing.PolicyName == c.PolicyName {
			return
		}
	}
	nsResult.Compliant = append(nsResult.Compliant, c)
}

func deduplicateViolations(nsResult *NamespaceAuditResult) {
	seen := make(map[ViolationDimension]bool)
	var deduped []Violation
	for _, v := range nsResult.Violations {
		if !seen[v.Dimension] {
			seen[v.Dimension] = true
			deduped = append(deduped, v)
		}
	}
	nsResult.Violations = deduped
}

func sortViolations(nsResult *NamespaceAuditResult) {
	sort.Slice(nsResult.Violations, func(i, j int) bool {
		si := actionSeverity(nsResult.Violations[i].Action)
		sj := actionSeverity(nsResult.Violations[j].Action)
		if si != sj {
			return si > sj
		}
		return nsResult.Violations[i].Dimension < nsResult.Violations[j].Dimension
	})
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

func roundTwo(v float64) float64 {
	return math.Round(v*100) / 100
}

func BuildAuditReport(policies []Policy, nsResults []NamespaceAuditResult, policyFile string, clusterName string) *AuditReport {
	gs := GlobalStats{}
	gs.TotalNamespaces = len(nsResults)

	for _, nr := range nsResults {
		if len(nr.Violations) > 0 {
			gs.ViolationCount++
		} else {
			gs.CompliantCount++
		}
		for _, v := range nr.Violations {
			switch v.Action {
			case "warn":
				gs.WarnCount++
			case "block":
				gs.BlockCount++
			case "report":
				gs.ReportCount++
			}
		}
	}

	return &AuditReport{
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		ClusterName: clusterName,
		PolicyFile:  policyFile,
		Namespaces:  nsResults,
		GlobalStats: gs,
	}
}

func ExportAuditReport(report *AuditReport, outputPath string) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal audit report: %w", err)
	}
	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write audit report: %w", err)
	}
	return nil
}

func LoadAuditReport(filePath string) (*AuditReport, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read audit report: %w", err)
	}
	var report AuditReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("failed to parse audit report: %w", err)
	}
	return &report, nil
}

func DiffAuditReports(current *AuditReport, old *AuditReport) *DiffReport {
	diff := &DiffReport{
		CurrentTime:  current.Timestamp,
		PreviousTime: old.Timestamp,
		Trends:       []TrendItem{},
	}

	currentViolMap := buildViolationMap(current)
	oldViolMap := buildViolationMap(old)

	for key, cv := range currentViolMap {
		if ov, exists := oldViolMap[key]; exists {
			trend := compareValues(cv.CurrentValue, ov.CurrentValue)
			diff.Trends = append(diff.Trends, TrendItem{
				Namespace: cv.Namespace,
				Dimension: cv.Dimension,
				Direction: trend,
				OldValue:  ov.CurrentValue,
				NewValue:  cv.CurrentValue,
				Status:    "ongoing",
			})
		} else {
			diff.Trends = append(diff.Trends, TrendItem{
				Namespace: cv.Namespace,
				Dimension: cv.Dimension,
				Direction: TrendUp,
				OldValue:  "0",
				NewValue:  cv.CurrentValue,
				Status:    "new",
			})
		}
	}

	for key, ov := range oldViolMap {
		if _, exists := currentViolMap[key]; !exists {
			diff.Trends = append(diff.Trends, TrendItem{
				Namespace: ov.Namespace,
				Dimension: ov.Dimension,
				Direction: TrendDown,
				OldValue:  ov.CurrentValue,
				NewValue:  "0",
				Status:    "fixed",
			})
		}
	}

	sort.Slice(diff.Trends, func(i, j int) bool {
		if diff.Trends[i].Namespace != diff.Trends[j].Namespace {
			return diff.Trends[i].Namespace < diff.Trends[j].Namespace
		}
		return diff.Trends[i].Dimension < diff.Trends[j].Dimension
	})

	oldTotal := old.GlobalStats.TotalNamespaces
	newTotal := current.GlobalStats.TotalNamespaces
	if oldTotal > 0 {
		diff.OldCompliance = roundTwo(float64(old.GlobalStats.CompliantCount) / float64(oldTotal) * 100)
	}
	if newTotal > 0 {
		diff.NewCompliance = roundTwo(float64(current.GlobalStats.CompliantCount) / float64(newTotal) * 100)
	}
	diff.ComplianceDelta = roundTwo(diff.NewCompliance - diff.OldCompliance)

	return diff
}

func buildViolationMap(report *AuditReport) map[string]Violation {
	m := make(map[string]Violation)
	for _, nr := range report.Namespaces {
		for _, v := range nr.Violations {
			key := nr.Namespace + "/" + string(v.Dimension)
			m[key] = v
		}
	}
	return m
}

func compareValues(current, old string) TrendDirection {
	cv := parseValueToFloat(current)
	ov := parseValueToFloat(old)
	if cv > ov*1.05 {
		return TrendUp
	}
	if cv < ov*0.95 {
		return TrendDown
	}
	return TrendFlat
}

func parseValueToFloat(s string) float64 {
	var f float64
	fmt.Sscanf(s, "%f", &f)
	if f == 0 {
		if qty, err := resource.ParseQuantity(s); err == nil {
			return float64(qty.MilliValue())
		}
	}
	return f
}

func RenderAuditSummary(report *AuditReport) string {
	var sb strings.Builder

	sb.WriteString("\n═══ Resource Quota Audit ═══\n\n")

	if len(report.Namespaces) == 0 {
		sb.WriteString("  No namespaces found.\n")
		return sb.String()
	}

	for _, nr := range report.Namespaces {
		if len(nr.Violations) == 0 {
			sb.WriteString(fmt.Sprintf("  ✓ %s — compliant\n", nr.Namespace))
			continue
		}
		sb.WriteString(fmt.Sprintf("  ✗ %s — %d violation(s)\n", nr.Namespace, len(nr.Violations)))
		for _, v := range nr.Violations {
			icon := actionIcon(v.Action)
			sb.WriteString(fmt.Sprintf("    %s [%s] %s: current=%s limit=%s over=%.1f%% (policy: %s)\n",
				icon, v.Action, v.Dimension, v.CurrentValue, v.PolicyLimit, v.OverPercent, v.PolicyName))
		}
	}

	sb.WriteString(fmt.Sprintf("\n─── Global Summary ───\n"))
	sb.WriteString(fmt.Sprintf("  Total namespaces: %d\n", report.GlobalStats.TotalNamespaces))
	sb.WriteString(fmt.Sprintf("  Compliant: %d\n", report.GlobalStats.CompliantCount))
	sb.WriteString(fmt.Sprintf("  Violating: %d\n", report.GlobalStats.ViolationCount))
	sb.WriteString(fmt.Sprintf("  Actions: block=%d warn=%d report=%d\n",
		report.GlobalStats.BlockCount, report.GlobalStats.WarnCount, report.GlobalStats.ReportCount))

	return sb.String()
}

func RenderDiffReport(diff *DiffReport) string {
	var sb strings.Builder

	sb.WriteString("\n═══ Audit Trend Comparison ═══\n\n")
	sb.WriteString(fmt.Sprintf("  Previous: %s\n", diff.PreviousTime))
	sb.WriteString(fmt.Sprintf("  Current:  %s\n\n", diff.CurrentTime))

	if len(diff.Trends) == 0 {
		sb.WriteString("  No changes detected.\n")
	} else {
		for _, t := range diff.Trends {
			var colorTag string
			switch t.Status {
			case "new":
				colorTag = "RED"
			case "fixed":
				colorTag = "GREEN"
			case "ongoing":
				colorTag = "YELLOW"
			default:
				colorTag = ""
			}

			sb.WriteString(fmt.Sprintf("  [%s] %s/%s: %s (%s → %s) direction=%s\n",
				colorTag, t.Namespace, t.Dimension, t.Status, t.OldValue, t.NewValue, t.Direction))
		}
	}

	sb.WriteString(fmt.Sprintf("\n─── Compliance Rate ───\n"))
	sb.WriteString(fmt.Sprintf("  Previous: %.1f%%\n", diff.OldCompliance))
	sb.WriteString(fmt.Sprintf("  Current:  %.1f%%\n", diff.NewCompliance))

	deltaStr := fmt.Sprintf("%+.1f%%", diff.ComplianceDelta)
	if diff.ComplianceDelta > 0 {
		sb.WriteString(fmt.Sprintf("  Change:   %s (improved)\n", deltaStr))
	} else if diff.ComplianceDelta < 0 {
		sb.WriteString(fmt.Sprintf("  Change:   %s (degraded)\n", deltaStr))
	} else {
		sb.WriteString(fmt.Sprintf("  Change:   %s (no change)\n", deltaStr))
	}

	return sb.String()
}

func actionIcon(action string) string {
	switch action {
	case "block":
		return "🚫"
	case "warn":
		return "⚠️"
	case "report":
		return "📋"
	default:
		return "•"
	}
}
