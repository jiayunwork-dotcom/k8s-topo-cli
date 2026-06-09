package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/k8s-topo-cli/pkg/audit"
	"github.com/k8s-topo-cli/pkg/client"
	"github.com/k8s-topo-cli/pkg/diagnosis"
	"github.com/k8s-topo-cli/pkg/discovery"
	"github.com/k8s-topo-cli/pkg/display"
	"github.com/k8s-topo-cli/pkg/health"
	"github.com/k8s-topo-cli/pkg/metrics"
	"github.com/k8s-topo-cli/pkg/output"
	"github.com/k8s-topo-cli/pkg/rules"
	"github.com/k8s-topo-cli/pkg/snapshot"
	"github.com/k8s-topo-cli/pkg/trace"
	"github.com/k8s-topo-cli/pkg/tui"
	"github.com/k8s-topo-cli/pkg/topology"
)

var (
	kubeconfig     string
	contextName    string
	namespace      string
	topoMode       bool
	tableMode      bool
	interactive    bool
	outputFmt      string
	showDiag       bool
	snapshotSave   string
	snapshotDiff   string
	rulesFile      string
	traceTarget    string
	policyFile     string
	auditOutput    string
	auditDiff      string
	auditEmit      bool
	auditTemplate  string
)

var rootCmd = &cobra.Command{
	Use:   "k8s-topo",
	Short: "Kubernetes cluster resource topology visualization and anomaly diagnosis CLI",
	Long:  "A CLI tool that visualizes Kubernetes cluster resource topology and assists with troubleshooting in the terminal.",
	RunE:  run,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file (default ~/.kube/config)")
	rootCmd.PersistentFlags().StringVar(&contextName, "context", "", "Kubernetes context name")
	rootCmd.PersistentFlags().StringVarP(&namespace, "namespace", "n", "", "Namespace to filter (default: all namespaces)")
	rootCmd.PersistentFlags().BoolVar(&topoMode, "topo", false, "Show topology graph mode (Ingress→Service→Pod)")
	rootCmd.PersistentFlags().BoolVar(&tableMode, "table", false, "Show table mode with all pods")
	rootCmd.PersistentFlags().BoolVarP(&interactive, "interactive", "i", false, "Interactive TUI mode")
	rootCmd.PersistentFlags().StringVarP(&outputFmt, "output", "o", "", "Output format: json, yaml, dot")
	rootCmd.PersistentFlags().BoolVar(&showDiag, "diag", false, "Run anomaly diagnosis")
	rootCmd.PersistentFlags().StringVar(&snapshotSave, "snapshot-save", "", "Save cluster state snapshot with given name")
	rootCmd.PersistentFlags().StringVar(&snapshotDiff, "snapshot-diff", "", "Diff current cluster state against snapshot with given name")
	rootCmd.PersistentFlags().StringVar(&rulesFile, "rules", "", "Path to custom alert rules YAML file")
	rootCmd.PersistentFlags().StringVar(&traceTarget, "trace", "", "Trace upstream dependencies: <type>/<name> (e.g. pod/nginx-abc123)")
	rootCmd.PersistentFlags().StringVar(&policyFile, "policy", "", "Path to quota policy YAML file for resource audit")
	rootCmd.PersistentFlags().StringVar(&auditOutput, "audit-output", "", "Export audit result to JSON file")
	rootCmd.PersistentFlags().StringVar(&auditDiff, "audit-diff", "", "Path to historical audit report JSON for trend comparison")
	rootCmd.PersistentFlags().BoolVar(&auditEmit, "audit-emit", false, "Emit Kubernetes Warning Events for block-level violations")
	rootCmd.PersistentFlags().StringVar(&auditTemplate, "audit-template", "", "Path to Go template file for rendering audit report (mutually exclusive with --audit-output)")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	if traceTarget != "" {
		return handleTrace(ctx)
	}

	fmt.Fprintf(os.Stderr, "Connecting to cluster...\n")

	c, err := client.NewClusterClient(kubeconfig, contextName, namespace)
	if err != nil {
		return fmt.Errorf("cluster connection failed: %w", err)
	}

	version, _ := c.Clientset.ServerVersion()
	fmt.Fprintf(os.Stderr, "Connected to Kubernetes %s\n\n", version.GitVersion)

	fmt.Fprintf(os.Stderr, "Discovering resources...\n")
	d := discovery.NewDiscoverer(c, namespace)
	res, err := d.Discover(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: discovery completed with errors: %v\n", err)
	}

	if snapshotSave != "" || snapshotDiff != "" {
		return handleSnapshot(res)
	}

	if policyFile != "" {
		if auditOutput != "" && auditTemplate != "" {
			return fmt.Errorf("--audit-output and --audit-template are mutually exclusive, please specify only one")
		}
		return handleAudit(ctx, res, c)
	}

	var topoObj *topology.Topology
	if topoMode || outputFmt == "" || interactive {
		fmt.Fprintf(os.Stderr, "Building topology...\n")
		topoObj = topology.BuildTopology(res)
	}

	var metricsResult *metrics.MetricsResult
	var diagResult *diagnosis.DiagnosisResult

	if outputFmt == "" || showDiag || interactive {
		fmt.Fprintf(os.Stderr, "Collecting metrics...\n")
		metricsResult = metrics.CollectMetrics(ctx, c, res)

		if showDiag || outputFmt == "" {
			fmt.Fprintf(os.Stderr, "Running diagnosis...\n")
			diagResult = diagnosis.Diagnose(ctx, c, res)
		}
	}

	if rulesFile != "" {
		fmt.Fprintf(os.Stderr, "Loading custom rules...\n")
		ruleList, err := rules.LoadRules(rulesFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to load rules: %v\n", err)
		} else {
			alerts := rules.EvaluateRules(ruleList, res)
			fmt.Print(rules.RenderAlerts(alerts))
		}
	}

	if outputFmt != "" {
		return handleOutput(topoObj, res, metricsResult, diagResult)
	}

	if interactive {
		return tui.RunInteractive(ctx, c, res, topoObj, metricsResult, diagResult)
	}

	if tableMode {
		fmt.Print(display.RenderTable(res, metricsResult))
		return nil
	}

	if topoMode {
		fmt.Print(display.RenderTopo(topoObj, res))
		return nil
	}

	fmt.Print(display.RenderTree(topoObj, res))
	fmt.Print(display.RenderResourceSummary(res))

	healthResult := health.CalculateHealth(res, metricsResult)
	fmt.Print(health.RenderHealthScore(healthResult))

	if showDiag && diagResult != nil {
		fmt.Print(renderDiagOutput(diagResult))
	}

	if metricsResult != nil {
		fmt.Print(metrics.RenderNodeMetrics(metricsResult))
		fmt.Print(metrics.RenderHotspots(metricsResult))
	}

	return nil
}

func handleSnapshot(res *discovery.DiscoveredResources) error {
	if snapshotSave != "" {
		if err := snapshot.SaveSnapshot(snapshotSave, res); err != nil {
			return fmt.Errorf("failed to save snapshot: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Snapshot '%s' saved successfully.\n", snapshotSave)
		return nil
	}

	if snapshotDiff != "" {
		saved, err := snapshot.LoadSnapshot(snapshotDiff)
		if err != nil {
			return fmt.Errorf("failed to load snapshot: %w", err)
		}
		current := snapshot.BuildCurrentSnapshot(res)
		diffResult := snapshot.DiffSnapshot(current, saved)
		fmt.Print(snapshot.RenderDiff(diffResult, snapshotDiff))
		return nil
	}

	return fmt.Errorf("no snapshot operation specified, use --snapshot-save <name> or --snapshot-diff <name>")
}

func handleTrace(ctx context.Context) error {
	fmt.Fprintf(os.Stderr, "Connecting to cluster...\n")

	c, err := client.NewClusterClient(kubeconfig, contextName, namespace)
	if err != nil {
		return fmt.Errorf("cluster connection failed: %w", err)
	}

	version, _ := c.Clientset.ServerVersion()
	fmt.Fprintf(os.Stderr, "Connected to Kubernetes %s\n\n", version.GitVersion)

	fmt.Fprintf(os.Stderr, "Discovering resources...\n")
	d := discovery.NewDiscoverer(c, namespace)
	res, err := d.Discover(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: discovery completed with errors: %v\n", err)
	}

	result, err := trace.Trace(traceTarget, res)
	if err != nil {
		return err
	}

	fmt.Print(trace.RenderTrace(result))
	return nil
}

func handleOutput(topo *topology.Topology, res *discovery.DiscoveredResources, m *metrics.MetricsResult, d *diagnosis.DiagnosisResult) error {
	switch strings.ToLower(outputFmt) {
	case "json":
		report := output.BuildReport(topo, res, m, d)
		data, err := output.ToJSON(report)
		if err != nil {
			return err
		}
		fmt.Println(data)
	case "yaml":
		report := output.BuildReport(topo, res, m, d)
		data, err := output.ToYAML(report)
		if err != nil {
			return err
		}
		fmt.Println(data)
	case "dot":
		fmt.Println(output.ToDOT(topo))
	default:
		return fmt.Errorf("unsupported output format: %s (supported: json, yaml, dot)", outputFmt)
	}
	return nil
}

func renderDiagOutput(d *diagnosis.DiagnosisResult) string {
	var sb strings.Builder

	sb.WriteString("\n═══ Anomaly Diagnosis ═══\n\n")

	if len(d.PodDiagnoses) > 0 {
		sb.WriteString("❌ Pod Anomalies:\n")
		for _, pd := range d.PodDiagnoses {
			sb.WriteString(fmt.Sprintf("  %s/%s [%s]\n", pd.Namespace, pd.PodName, pd.Status))
			for _, r := range pd.ReasonChain {
				sb.WriteString(fmt.Sprintf("    → %s\n", r))
			}
			if pd.LogTail != "" {
				sb.WriteString("    Last logs:\n")
				lines := strings.Split(pd.LogTail, "\n")
				for _, l := range lines {
					if l != "" {
						sb.WriteString(fmt.Sprintf("      %s\n", l))
					}
				}
			}
			if pd.ImageError != "" {
				sb.WriteString(fmt.Sprintf("    Image error: %s\n", pd.ImageError))
			}
			if pd.PendingReason != "" {
				sb.WriteString(fmt.Sprintf("    Pending reason: %s\n", pd.PendingReason))
			}
			if pd.OOMInfo != "" {
				sb.WriteString(fmt.Sprintf("    OOM info: %s\n", pd.OOMInfo))
			}
		}
	}

	if len(d.WarningEvents) > 0 {
		sb.WriteString("\n⚠ Top Warning Events:\n")
		limit := 10
		if len(d.WarningEvents) < limit {
			limit = len(d.WarningEvents)
		}
		for i := 0; i < limit; i++ {
			e := d.WarningEvents[i]
			sb.WriteString(fmt.Sprintf("  %d. [%dx] %s/%s %s - %s\n", i+1, e.Count, e.Namespace, e.Kind, e.Name, e.Reason))
			sb.WriteString(fmt.Sprintf("     %s\n", e.Message))
		}
	}

	if len(d.ProbeFailures) > 0 {
		sb.WriteString("\n⚠ Probe Failures:\n")
		for _, pf := range d.ProbeFailures {
			sb.WriteString(fmt.Sprintf("  %s/%s %s [%s] failures: %d\n", pf.Namespace, pf.PodName, pf.Container, pf.ProbeType, pf.FailureCount))
			sb.WriteString(fmt.Sprintf("    Config: %s\n", pf.ProbeConfig))
		}
	}

	return sb.String()
}

func handleAudit(ctx context.Context, res *discovery.DiscoveredResources, c *client.ClusterClient) error {
	fmt.Fprintf(os.Stderr, "Loading quota policies...\n")
	policies, err := audit.LoadPolicies(policyFile)
	if err != nil {
		return fmt.Errorf("failed to load policies: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Loaded %d policy(ies) from %s\n", len(policies), policyFile)

	fmt.Fprintf(os.Stderr, "Scanning namespace resources...\n")
	stats := audit.CollectNamespaceStats(res)

	fmt.Fprintf(os.Stderr, "Evaluating policies against resource usage...\n")
	nsResults := audit.EvaluatePolicies(policies, stats)

	clusterName := c.ClusterName
	if clusterName == "" {
		clusterName = "unknown"
	}

	report := audit.BuildAuditReport(policies, nsResults, policyFile, clusterName)

	if auditEmit {
		fmt.Fprintf(os.Stderr, "Emitting violation events to Kubernetes...\n")
		if err := audit.EmitViolationEvents(ctx, c.Clientset, nsResults); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to emit some events: %v\n", err)
		}
	}

	if auditTemplate != "" {
		if err := audit.RenderAuditReportWithTemplate(report, auditTemplate); err != nil {
			os.Exit(1)
		}
		return nil
	}

	if auditOutput != "" {
		if err := audit.ExportAuditReport(report, auditOutput); err != nil {
			return fmt.Errorf("failed to export audit report: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Audit report exported to %s\n", auditOutput)
	}

	fmt.Print(audit.RenderAuditSummary(report))

	if auditDiff != "" {
		fmt.Fprintf(os.Stderr, "Loading historical audit report...\n")
		oldReport, err := audit.LoadAuditReport(auditDiff)
		if err != nil {
			return fmt.Errorf("failed to load historical audit report: %w", err)
		}

		diffResult := audit.DiffAuditReports(report, oldReport)
		fmt.Print(audit.RenderDiffReport(diffResult))
	}

	if topoMode {
		fmt.Fprintf(os.Stderr, "Building topology with audit annotations...\n")
		topo := topology.BuildTopology(res)
		audit.AnnotateTopologyWithViolations(topo, nsResults)
		fmt.Print(display.RenderTree(topo, res))
	}

	return nil
}
