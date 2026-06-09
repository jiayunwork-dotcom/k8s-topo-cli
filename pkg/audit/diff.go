package audit

import (
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/api/resource"
)

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
