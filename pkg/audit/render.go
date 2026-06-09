package audit

import (
	"fmt"
	"os"
	"strings"
)

const (
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiBold   = "\033[1m"
	ansiReset  = "\033[0m"
)

func shouldUseColor() bool {
	term := os.Getenv("TERM")
	if term == "dumb" {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return true
}

func colorize(text, color string) string {
	if !shouldUseColor() {
		return text
	}
	return color + text + ansiReset
}

func bold(text string) string {
	if !shouldUseColor() {
		return text
	}
	return ansiBold + text + ansiReset
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
			sb.WriteString(fmt.Sprintf("  ✓ %s — %s\n", nr.Namespace, colorize("compliant", ansiGreen)))
			continue
		}
		sb.WriteString(fmt.Sprintf("  ✗ %s — %s\n", nr.Namespace, colorize(fmt.Sprintf("%d violation(s)", len(nr.Violations)), ansiRed)))
		for _, v := range nr.Violations {
			icon := actionIcon(v.Action)
			actionLabel := v.Action
			switch v.Action {
			case "block":
				actionLabel = colorize(v.Action, ansiRed)
			case "warn":
				actionLabel = colorize(v.Action, ansiYellow)
			}
			sb.WriteString(fmt.Sprintf("    %s [%s] %s: current=%s limit=%s over=%.1f%% (policy: %s)\n",
				icon, actionLabel, v.Dimension, v.CurrentValue, v.PolicyLimit, v.OverPercent, v.PolicyName))
		}
	}

	sb.WriteString(fmt.Sprintf("\n─── Global Summary ───\n"))
	sb.WriteString(fmt.Sprintf("  Total namespaces: %d\n", report.GlobalStats.TotalNamespaces))
	sb.WriteString(fmt.Sprintf("  Compliant: %d\n", report.GlobalStats.CompliantCount))
	sb.WriteString(fmt.Sprintf("  Violating: %d\n", report.GlobalStats.ViolationCount))
	sb.WriteString(fmt.Sprintf("  Actions: block=%s warn=%s report=%d\n",
		colorize(fmt.Sprintf("%d", report.GlobalStats.BlockCount), ansiRed),
		colorize(fmt.Sprintf("%d", report.GlobalStats.WarnCount), ansiYellow),
		report.GlobalStats.ReportCount))

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
			var statusLabel string
			switch t.Status {
			case "new":
				statusLabel = colorize("NEW", ansiRed)
			case "fixed":
				statusLabel = colorize("FIXED", ansiGreen)
			case "ongoing":
				statusLabel = colorize("ONGOING", ansiYellow)
			default:
				statusLabel = t.Status
			}

			sb.WriteString(fmt.Sprintf("  [%s] %s/%s: %s (%s → %s) direction=%s\n",
				statusLabel, t.Namespace, t.Dimension, t.Status, t.OldValue, t.NewValue, t.Direction))
		}
	}

	sb.WriteString(fmt.Sprintf("\n─── Compliance Rate ───\n"))
	sb.WriteString(fmt.Sprintf("  Previous: %.1f%%\n", diff.OldCompliance))
	sb.WriteString(fmt.Sprintf("  Current:  %.1f%%\n", diff.NewCompliance))

	deltaStr := fmt.Sprintf("%+.1f%%", diff.ComplianceDelta)
	if diff.ComplianceDelta > 0 {
		sb.WriteString(fmt.Sprintf("  Change:   %s (%s)\n", colorize(deltaStr, ansiGreen), "improved"))
	} else if diff.ComplianceDelta < 0 {
		sb.WriteString(fmt.Sprintf("  Change:   %s (%s)\n", colorize(deltaStr, ansiRed), "degraded"))
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
