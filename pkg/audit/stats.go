package audit

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/k8s-topo-cli/pkg/discovery"
)

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
			for _, s := range stats {
				if matchesAnyTarget(s.Namespace, policy.Targets) {
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
	checkCountLimit(policy, s, nsResult, DimMaxPods, float64(s.PodCount), policy.Limits.MaxPods)
	checkCountLimit(policy, s, nsResult, DimMaxDeployments, float64(s.DeploymentCount), policy.Limits.MaxDeployments)
	checkCountLimit(policy, s, nsResult, DimMaxServices, float64(s.ServiceCount), policy.Limits.MaxServices)

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

func checkCountLimit(policy Policy, s NamespaceStats, nsResult *NamespaceAuditResult, dim ViolationDimension, current float64, limit *int64) {
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

func roundTwo(v float64) float64 {
	return math.Round(v*100) / 100
}

func matchTarget(target, namespace string) bool {
	if len(target) >= 2 && strings.HasPrefix(target, "/") && strings.HasSuffix(target, "/") {
		pattern := target[1 : len(target)-1]
		re, err := regexp.Compile(pattern)
		if err != nil {
			return false
		}
		return re.MatchString(namespace)
	}
	if strings.HasSuffix(target, "*") {
		prefix := target[:len(target)-1]
		return strings.HasPrefix(namespace, prefix)
	}
	return target == namespace
}

func matchesAnyTarget(namespace string, targets []string) bool {
	for _, t := range targets {
		if matchTarget(t, namespace) {
			return true
		}
	}
	return false
}
