package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	corev1 "k8s.io/api/core/v1"

	"github.com/k8s-topo-cli/pkg/client"
	"github.com/k8s-topo-cli/pkg/diagnosis"
	"github.com/k8s-topo-cli/pkg/discovery"
	"github.com/k8s-topo-cli/pkg/metrics"
	"github.com/k8s-topo-cli/pkg/topology"
)

var (
	titleStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).MarginBottom(1)
	selectedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	normalStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	dimStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	warningStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	successStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	statusBarStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Background(lipgloss.Color("0"))
	helpStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)

type listItem struct {
	name      string
	namespace string
	kind      string
	status    string
	node      string
	uid       string
	resource  interface{}
}

type detailView struct {
	title   string
	content string
}

type viewSnapshot struct {
	viewName string
	filtered []listItem
	cursor   int
	offset   int
}

type model struct {
	client      *client.ClusterClient
	resources   *discovery.DiscoveredResources
	topo        *topology.Topology
	metrics     *metrics.MetricsResult
	diagResult  *diagnosis.DiagnosisResult
	ctx         context.Context

	items       []listItem
	filtered    []listItem
	cursor      int
	offset      int
	viewStack   []viewSnapshot
	currentView string
	detail      detailView
	searchInput textinput.Model
	searching   bool
	ready       bool
	err         error
	width       int
	height      int
}

func NewModel(ctx context.Context, c *client.ClusterClient, res *discovery.DiscoveredResources, topo *topology.Topology, m *metrics.MetricsResult, d *diagnosis.DiagnosisResult) model {
	ti := textinput.New()
	ti.Placeholder = "Search..."
	ti.CharLimit = 50

	mdl := model{
		client:      c,
		resources:   res,
		topo:        topo,
		metrics:     m,
		diagResult:  d,
		ctx:         ctx,
		searchInput: ti,
		currentView: "main",
	}

	mdl.buildMainList()
	mdl.filtered = mdl.items
	mdl.ready = true
	return mdl
}

func (m *model) buildMainList() {
	m.items = []listItem{}

	for _, ns := range m.topo.Roots {
		m.items = append(m.items, listItem{
			name:     ns.Name,
			kind:     "Namespace",
			status:   ns.Status,
			resource: ns.Resource,
		})

		for _, child := range ns.Children {
			m.items = append(m.items, listItem{
				name:      child.Name,
				namespace: child.Namespace,
				kind:      string(child.Type),
				status:    child.Status,
				resource:  child.Resource,
			})
		}
	}

	sort.Slice(m.items, func(i, j int) bool {
		if m.items[i].kind != m.items[j].kind {
			return m.items[i].kind < m.items[j].kind
		}
		return m.items[i].name < m.items[j].name
	})
}

func (m *model) filterItems(query string) {
	if query == "" {
		m.filtered = m.items
		return
	}
	q := strings.ToLower(query)
	var filtered []listItem
	for _, item := range m.items {
		if strings.Contains(strings.ToLower(item.name), q) ||
			strings.Contains(strings.ToLower(item.namespace), q) ||
			strings.Contains(strings.ToLower(item.kind), q) ||
			strings.Contains(strings.ToLower(item.status), q) {
			filtered = append(filtered, item)
		}
	}
	m.filtered = filtered
	m.cursor = 0
	m.offset = 0
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.searching {
			return m.handleSearchInput(msg)
		}

		switch msg.String() {
		case "q", "ctrl+c":
			if m.currentView == "main" && len(m.viewStack) == 0 {
				return m, tea.Quit
			}
			m.goBack()
			return m, nil

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				if m.cursor < m.offset {
					m.offset = m.cursor
				}
			}
			return m, nil

		case "down", "j":
			if m.cursor < len(m.filtered)-1 {
				m.cursor++
				visibleHeight := m.height - 6
				if visibleHeight < 1 {
					visibleHeight = 20
				}
				if m.cursor >= m.offset+visibleHeight {
					m.offset = m.cursor - visibleHeight + 1
				}
			}
			return m, nil

		case "enter":
			m.handleEnter()
			return m, nil

		case "esc", "backspace":
			if m.currentView == "main" && len(m.viewStack) == 0 {
				return m, tea.Quit
			}
			m.goBack()
			return m, nil

		case "/":
			m.searching = true
			m.searchInput.Focus()
			return m, textinput.Blink

		case "d":
			m.showDiagnosis()
			return m, nil

		case "m":
			m.showMetricsView()
			return m, nil

		case "e":
			m.showEventsView()
			return m, nil
		}
	}

	return m, nil
}

func (m *model) handleSearchInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.searching = false
		m.searchInput.Blur()
		m.filterItems("")
		return m, nil
	case "enter":
		m.searching = false
		m.searchInput.Blur()
		return m, nil
	}

	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	m.filterItems(m.searchInput.Value())
	return m, cmd
}

func (m *model) pushView(newView string) {
	snapshot := viewSnapshot{
		viewName: m.currentView,
		filtered: make([]listItem, len(m.filtered)),
		cursor:   m.cursor,
		offset:   m.offset,
	}
	copy(snapshot.filtered, m.filtered)
	m.viewStack = append(m.viewStack, snapshot)
	m.currentView = newView
}

func (m *model) handleEnter() {
	if m.cursor >= len(m.filtered) {
		return
	}

	item := m.filtered[m.cursor]

	if item.kind == "Pod" {
		pod, ok := item.resource.(*corev1.Pod)
		if !ok {
			return
		}

		detail := fmt.Sprintf("Pod: %s/%s\n", pod.Namespace, pod.Name)
		detail += fmt.Sprintf("Status: %s\n", pod.Status.Phase)
		detail += fmt.Sprintf("Node: %s\n", pod.Spec.NodeName)
		detail += fmt.Sprintf("IP: %s\n", pod.Status.PodIP)
		detail += "\nContainers:\n"
		for _, c := range pod.Spec.Containers {
			detail += fmt.Sprintf("  - %s (%s)\n", c.Name, c.Image)
		}
		detail += "\nVolumes:\n"
		for _, v := range pod.Spec.Volumes {
			if v.ConfigMap != nil {
				detail += fmt.Sprintf("  ConfigMap: %s\n", v.ConfigMap.Name)
			}
			if v.Secret != nil {
				detail += fmt.Sprintf("  Secret: %s\n", v.Secret.SecretName)
			}
			if v.PersistentVolumeClaim != nil {
				detail += fmt.Sprintf("  PVC: %s\n", v.PersistentVolumeClaim.ClaimName)
			}
		}

		events, err := diagnosis.GetPodEvents(m.ctx, m.client, pod.Namespace, pod.Name)
		if err == nil {
			detail += "\nRecent Events:\n" + events
		}

		logs := ""
		if len(pod.Spec.Containers) > 0 {
			l, err := diagnosis.GetPodLogs(m.ctx, m.client, pod.Namespace, pod.Name, pod.Spec.Containers[0].Name, 30)
			if err == nil {
				logs = l
			}
		}
		if logs != "" {
			detail += "\nRecent Logs:\n" + logs
		}

		m.pushView("detail")
		m.detail = detailView{
			title:   fmt.Sprintf("Pod: %s/%s", pod.Namespace, pod.Name),
			content: detail,
		}
		m.cursor = 0
		m.offset = 0

	} else if item.kind == "Namespace" {
		m.pushView("namespace")
		nsName := item.name
		var nsItems []listItem
		for _, it := range m.items {
			if it.namespace == nsName || (it.kind == "Namespace" && it.name == nsName) {
				nsItems = append(nsItems, it)
			}
		}
		m.filtered = nsItems
		m.cursor = 0
		m.offset = 0
	}
}

func (m *model) showDiagnosis() {
	if m.diagResult == nil {
		return
	}
	m.pushView("diagnosis")
	m.cursor = 0
	m.offset = 0
}

func (m *model) showMetricsView() {
	if m.metrics == nil {
		return
	}
	m.pushView("metrics")
	m.cursor = 0
	m.offset = 0
}

func (m *model) showEventsView() {
	m.pushView("events")
	m.cursor = 0
	m.offset = 0
}

func (m *model) goBack() {
	if len(m.viewStack) == 0 {
		m.currentView = "main"
		m.filtered = m.items
		m.cursor = 0
		m.offset = 0
		return
	}

	snapshot := m.viewStack[len(m.viewStack)-1]
	m.viewStack = m.viewStack[:len(m.viewStack)-1]

	m.currentView = snapshot.viewName
	m.filtered = snapshot.filtered
	m.cursor = snapshot.cursor
	m.offset = snapshot.offset
}

func (m model) View() string {
	if !m.ready {
		return "Loading..."
	}

	switch m.currentView {
	case "detail":
		return m.renderDetail()
	case "diagnosis":
		return m.renderDiagnosis()
	case "metrics":
		return m.renderMetrics()
	case "events":
		return m.renderEvents()
	default:
		return m.renderList()
	}
}

func (m model) renderList() string {
	var sb strings.Builder

	title := "k8s-topo-cli Interactive Mode"
	if m.searching {
		title += "  |  Search: " + m.searchInput.View()
	}
	sb.WriteString(titleStyle.Render(title) + "\n")

	visibleHeight := m.height - 6
	if visibleHeight < 1 {
		visibleHeight = 20
	}

	end := m.offset + visibleHeight
	if end > len(m.filtered) {
		end = len(m.filtered)
	}

	for i := m.offset; i < end; i++ {
		item := m.filtered[i]
		cursor := "  "
		style := normalStyle

		if i == m.cursor {
			cursor = "▸ "
			style = selectedStyle
		}

		icon := topology.StatusIcon(item.status)
		ns := ""
		if item.namespace != "" {
			ns = dimStyle.Render(item.namespace + "/")
		}

		line := fmt.Sprintf("%s%s %s %s%s [%s]", cursor, icon, item.kind, ns, style.Render(item.name), item.status)
		sb.WriteString(line + "\n")
	}

	sb.WriteString("\n" + statusBarStyle.Render(fmt.Sprintf(" %d/%d items ", m.cursor+1, len(m.filtered))))
	sb.WriteString(helpStyle.Render("  ↑↓ navigate | Enter drill-in | / search | d diag | m metrics | e events | Esc back | q quit"))

	return sb.String()
}

func (m model) renderDetail() string {
	var sb strings.Builder
	sb.WriteString(titleStyle.Render("📋 "+m.detail.title) + "\n")
	sb.WriteString(m.detail.content)
	sb.WriteString("\n" + helpStyle.Render("Esc/Backspace to go back | q to quit"))
	return sb.String()
}

func (m model) renderDiagnosis() string {
	var sb strings.Builder
	sb.WriteString(titleStyle.Render("🔍 Anomaly Diagnosis") + "\n\n")

	if m.diagResult == nil {
		sb.WriteString("No diagnosis data available.\n")
	} else {
		sb.WriteString(errorStyle.Render("❌ Pod Anomalies:") + "\n")
		for _, d := range m.diagResult.PodDiagnoses {
			sb.WriteString(fmt.Sprintf("\n  Pod: %s/%s - %s\n", d.Namespace, d.PodName, errorStyle.Render(d.Status)))
			for _, r := range d.ReasonChain {
				sb.WriteString(warningStyle.Render("    → "+r) + "\n")
			}
			if d.LogTail != "" {
				sb.WriteString(dimStyle.Render("    Logs (last lines):\n"))
				for _, line := range strings.Split(d.LogTail, "\n") {
					if line != "" {
						sb.WriteString(dimStyle.Render("      "+line) + "\n")
					}
				}
			}
			if d.ImageError != "" {
				sb.WriteString(warningStyle.Render("    Image Error:\n"))
				for _, line := range strings.Split(d.ImageError, "\n") {
					sb.WriteString(warningStyle.Render("      "+line) + "\n")
				}
			}
			if d.PendingReason != "" {
				sb.WriteString(warningStyle.Render("    Pending Reason:\n"))
				for _, line := range strings.Split(d.PendingReason, "\n") {
					sb.WriteString(warningStyle.Render("      "+line) + "\n")
				}
			}
			if d.OOMInfo != "" {
				sb.WriteString(errorStyle.Render("    OOM Info:\n"))
				for _, line := range strings.Split(d.OOMInfo, "\n") {
					sb.WriteString(errorStyle.Render("      "+line) + "\n")
				}
			}
		}

		if len(m.diagResult.ProbeFailures) > 0 {
			sb.WriteString("\n" + warningStyle.Render("⚠ Probe Failures:") + "\n")
			for _, pf := range m.diagResult.ProbeFailures {
				sb.WriteString(fmt.Sprintf("  %s/%s %s [%s] - failures: %d\n",
					pf.Namespace, pf.PodName, pf.Container, pf.ProbeType, pf.FailureCount))
				sb.WriteString(dimStyle.Render("    Config: "+pf.ProbeConfig+"\n"))
			}
		}
	}

	sb.WriteString("\n" + helpStyle.Render("Esc/Backspace to go back | q to quit"))
	return sb.String()
}

func (m model) renderMetrics() string {
	var sb strings.Builder
	sb.WriteString(titleStyle.Render("📊 Resource Metrics") + "\n\n")

	if m.metrics == nil {
		sb.WriteString("No metrics data available.\n")
	} else {
		sb.WriteString(metrics.RenderNodeMetrics(m.metrics))
		sb.WriteString(metrics.RenderHotspots(m.metrics))
	}

	sb.WriteString(helpStyle.Render("Esc/Backspace to go back | q to quit"))
	return sb.String()
}

func (m model) renderEvents() string {
	var sb strings.Builder
	sb.WriteString(titleStyle.Render("⚠ Warning Events (Top N)") + "\n\n")

	if m.diagResult == nil {
		sb.WriteString("No event data available.\n")
	} else {
		events := m.diagResult.WarningEvents
		if len(events) == 0 {
			sb.WriteString(successStyle.Render("No warning events found! 🎉\n"))
		} else {
			limit := 20
			if len(events) < limit {
				limit = len(events)
			}
			for i := 0; i < limit; i++ {
				e := events[i]
				sb.WriteString(fmt.Sprintf("  %d. [%dx] %s/%s %s - %s\n",
					i+1, e.Count, e.Namespace, e.Kind, e.Name, e.Reason))
				sb.WriteString(dimStyle.Render("     "+e.Message+"\n"))
				sb.WriteString(dimStyle.Render("     Last seen: "+e.LastSeen+"\n\n"))
			}
		}
	}

	sb.WriteString(helpStyle.Render("Esc/Backspace to go back | q to quit"))
	return sb.String()
}

func RunInteractive(ctx context.Context, c *client.ClusterClient, res *discovery.DiscoveredResources, topo *topology.Topology, m *metrics.MetricsResult, d *diagnosis.DiagnosisResult) error {
	mdl := NewModel(ctx, c, res, topo, m, d)
	p := tea.NewProgram(mdl, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
