package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

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
