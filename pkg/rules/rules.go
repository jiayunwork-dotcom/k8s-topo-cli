package rules

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"

	"github.com/k8s-topo-cli/pkg/discovery"
)

type Rule struct {
	Name      string `yaml:"name"`
	Resource  string `yaml:"resource"`
	Condition string `yaml:"condition"`
	Severity  string `yaml:"severity"`
	Message   string `yaml:"message"`
}

type RulesFile struct {
	Rules []Rule `yaml:"rules"`
}

type Alert struct {
	RuleName  string
	Severity  string
	Resource  string
	Namespace string
	Name      string
	Message   string
}

func LoadRules(filePath string) ([]Rule, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read rules file: %w", err)
	}

	var rf RulesFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return nil, fmt.Errorf("failed to parse rules YAML: %w", err)
	}

	for i, rule := range rf.Rules {
		if rule.Name == "" {
			return nil, fmt.Errorf("rule #%d: name is required", i+1)
		}
		if rule.Resource == "" {
			return nil, fmt.Errorf("rule #%d: resource is required", i+1)
		}
		if rule.Condition == "" {
			return nil, fmt.Errorf("rule #%d: condition is required", i+1)
		}
		if rule.Severity == "" {
			rf.Rules[i].Severity = "info"
		}
		if rule.Message == "" {
			rf.Rules[i].Message = rule.Name
		}
	}

	return rf.Rules, nil
}

func EvaluateRules(rules []Rule, res *discovery.DiscoveredResources) []Alert {
	var alerts []Alert

	for _, rule := range rules {
		switch strings.ToLower(rule.Resource) {
		case "pod":
			alerts = append(alerts, evaluatePods(rule, res)...)
		case "deployment":
			alerts = append(alerts, evaluateDeployments(rule, res)...)
		case "node":
			alerts = append(alerts, evaluateNodes(rule, res)...)
		case "service":
			alerts = append(alerts, evaluateServices(rule, res)...)
		case "ingress":
			alerts = append(alerts, evaluateIngresses(rule, res)...)
		}
	}

	sort.Slice(alerts, func(i, j int) bool {
		order := map[string]int{"critical": 0, "warning": 1, "info": 2}
		oi, _ := order[alerts[i].Severity]
		oj, _ := order[alerts[j].Severity]
		if oi != oj {
			return oi < oj
		}
		return alerts[i].RuleName < alerts[j].RuleName
	})

	return alerts
}

func evaluatePods(rule Rule, res *discovery.DiscoveredResources) []Alert {
	var alerts []Alert
	for _, pod := range res.Pods {
		fields := buildPodFields(pod)
		matched, err := evaluateCondition(rule.Condition, fields)
		if err != nil || !matched {
			continue
		}
		msg := expandTemplate(rule.Message, fields)
		alerts = append(alerts, Alert{
			RuleName:  rule.Name,
			Severity:  rule.Severity,
			Resource:  "Pod",
			Namespace: pod.Namespace,
			Name:      pod.Name,
			Message:   msg,
		})
	}
	return alerts
}

func evaluateDeployments(rule Rule, res *discovery.DiscoveredResources) []Alert {
	var alerts []Alert
	for _, deploy := range res.Deployments {
		fields := buildDeploymentFields(deploy)
		matched, err := evaluateCondition(rule.Condition, fields)
		if err != nil || !matched {
			continue
		}
		msg := expandTemplate(rule.Message, fields)
		alerts = append(alerts, Alert{
			RuleName:  rule.Name,
			Severity:  rule.Severity,
			Resource:  "Deployment",
			Namespace: deploy.Namespace,
			Name:      deploy.Name,
			Message:   msg,
		})
	}
	return alerts
}

func evaluateNodes(rule Rule, res *discovery.DiscoveredResources) []Alert {
	var alerts []Alert
	for _, node := range res.Nodes {
		fields := buildNodeFields(node)
		matched, err := evaluateCondition(rule.Condition, fields)
		if err != nil || !matched {
			continue
		}
		msg := expandTemplate(rule.Message, fields)
		alerts = append(alerts, Alert{
			RuleName:  rule.Name,
			Severity:  rule.Severity,
			Resource:  "Node",
			Namespace: "",
			Name:      node.Name,
			Message:   msg,
		})
	}
	return alerts
}

func evaluateServices(rule Rule, res *discovery.DiscoveredResources) []Alert {
	var alerts []Alert
	for _, svc := range res.Services {
		fields := buildServiceFields(svc)
		matched, err := evaluateCondition(rule.Condition, fields)
		if err != nil || !matched {
			continue
		}
		msg := expandTemplate(rule.Message, fields)
		alerts = append(alerts, Alert{
			RuleName:  rule.Name,
			Severity:  rule.Severity,
			Resource:  "Service",
			Namespace: svc.Namespace,
			Name:      svc.Name,
			Message:   msg,
		})
	}
	return alerts
}

func evaluateIngresses(rule Rule, res *discovery.DiscoveredResources) []Alert {
	var alerts []Alert
	for _, ing := range res.Ingresses {
		fields := buildIngressFields(ing)
		matched, err := evaluateCondition(rule.Condition, fields)
		if err != nil || !matched {
			continue
		}
		msg := expandTemplate(rule.Message, fields)
		alerts = append(alerts, Alert{
			RuleName:  rule.Name,
			Severity:  rule.Severity,
			Resource:  "Ingress",
			Namespace: ing.Namespace,
			Name:      ing.Name,
			Message:   msg,
		})
	}
	return alerts
}

func buildPodFields(pod *corev1.Pod) map[string]string {
	status := string(pod.Status.Phase)
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			reason := cs.State.Waiting.Reason
			if reason == "CrashLoopBackOff" || reason == "ImagePullBackOff" || reason == "OOMKilled" {
				status = reason
				break
			}
		}
	}

	fields := map[string]string{
		"name":                pod.Name,
		"namespace":           pod.Namespace,
		"status.phase":        status,
		"status.podIP":        pod.Status.PodIP,
		"spec.nodeName":       pod.Spec.NodeName,
	}

	for k, v := range pod.Labels {
		fields["metadata.labels."+k] = v
	}

	var totalRestarts int32
	for _, cs := range pod.Status.ContainerStatuses {
		totalRestarts += cs.RestartCount
	}
	fields["status.containerRestarts"] = strconv.FormatInt(int64(totalRestarts), 10)

	return fields
}

func buildDeploymentFields(deploy *appsv1.Deployment) map[string]string {
	fields := map[string]string{
		"name":               deploy.Name,
		"namespace":          deploy.Namespace,
		"spec.replicas":      strconv.FormatInt(int64(*deploy.Spec.Replicas), 10),
		"status.replicas":    strconv.FormatInt(int64(deploy.Status.Replicas), 10),
		"status.readyReplicas": strconv.FormatInt(int64(deploy.Status.ReadyReplicas), 10),
		"status.availableReplicas": strconv.FormatInt(int64(deploy.Status.AvailableReplicas), 10),
	}

	for k, v := range deploy.Labels {
		fields["metadata.labels."+k] = v
	}

	return fields
}

func buildNodeFields(node *corev1.Node) map[string]string {
	fields := map[string]string{
		"name": node.Name,
	}

	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			fields["status.conditions.Ready"] = string(cond.Status)
		}
		if cond.Type == corev1.NodeMemoryPressure {
			fields["status.conditions.MemoryPressure"] = string(cond.Status)
		}
		if cond.Type == corev1.NodeDiskPressure {
			fields["status.conditions.DiskPressure"] = string(cond.Status)
		}
	}

	for k, v := range node.Labels {
		fields["metadata.labels."+k] = v
	}

	return fields
}

func buildServiceFields(svc *corev1.Service) map[string]string {
	fields := map[string]string{
		"name":      svc.Name,
		"namespace": svc.Namespace,
		"spec.type": string(svc.Spec.Type),
	}

	for k, v := range svc.Labels {
		fields["metadata.labels."+k] = v
	}

	if len(svc.Spec.Selector) > 0 {
		var selectorParts []string
		for k, v := range svc.Spec.Selector {
			selectorParts = append(selectorParts, k+"="+v)
		}
		fields["spec.selector"] = strings.Join(selectorParts, ",")
	}

	return fields
}

func buildIngressFields(ing *networkingv1.Ingress) map[string]string {
	fields := map[string]string{
		"name":      ing.Name,
		"namespace": ing.Namespace,
	}

	for k, v := range ing.Labels {
		fields["metadata.labels."+k] = v
	}

	return fields
}

func evaluateCondition(condition string, fields map[string]string) (bool, error) {
	conditions := []string{condition}
	ops := []string{"==", "!=", ">", ">=", "<", "<="}

	for _, op := range ops {
		if strings.Contains(condition, op) {
			parts := strings.SplitN(condition, op, 2)
			if len(parts) != 2 {
				continue
			}
			fieldPath := strings.TrimSpace(parts[0])
			expectedVal := strings.TrimSpace(parts[1])
			expectedVal = strings.Trim(expectedVal, "\"")
			expectedVal = strings.Trim(expectedVal, "'")

			actualVal, ok := fields[fieldPath]
			if !ok {
				actualVal = resolveNestedField(fieldPath, fields)
			}

			switch op {
			case "==":
				return actualVal == expectedVal, nil
			case "!=":
				return actualVal != expectedVal, nil
			case ">":
				return compareNumbers(actualVal, expectedVal, ">")
			case ">=":
				return compareNumbers(actualVal, expectedVal, ">=")
			case "<":
				return compareNumbers(actualVal, expectedVal, "<")
			case "<=":
				return compareNumbers(actualVal, expectedVal, "<=")
			}
		}
	}

	_ = conditions
	return false, fmt.Errorf("unable to parse condition: %s", condition)
}

func resolveNestedField(path string, fields map[string]string) string {
	if val, ok := fields[path]; ok {
		return val
	}
	parts := strings.Split(path, ".")
	for i := len(parts); i > 1; i-- {
		prefix := strings.Join(parts[:i-1], ".")
		suffix := strings.Join(parts[i-1:], ".")
		if val, ok := fields[prefix]; ok {
			return tryExtractSubField(val, suffix)
		}
	}
	return ""
}

func tryExtractSubField(value, subField string) string {
	var obj interface{}
	if err := parseSimpleValue(value, &obj); err != nil {
		return ""
	}
	return extractFromObj(obj, subField)
}

func parseSimpleValue(value string, target interface{}) error {
	return nil
}

func extractFromObj(obj interface{}, path string) string {
	v := reflect.ValueOf(obj)
	parts := strings.Split(path, ".")
	for _, part := range parts {
		if v.Kind() == reflect.Map {
			key := reflect.ValueOf(part)
			if v.MapIndex(key).IsValid() {
				v = v.MapIndex(key)
			} else {
				return ""
			}
		} else {
			return ""
		}
	}
	return fmt.Sprintf("%v", v.Interface())
}

func compareNumbers(actual, expected, op string) (bool, error) {
	a, err1 := strconv.ParseFloat(actual, 64)
	b, err2 := strconv.ParseFloat(expected, 64)
	if err1 != nil || err2 != nil {
		if op == ">" {
			return actual > expected, nil
		}
		if op == ">=" {
			return actual >= expected, nil
		}
		if op == "<" {
			return actual < expected, nil
		}
		if op == "<=" {
			return actual <= expected, nil
		}
		return false, nil
	}
	switch op {
	case ">":
		return a > b, nil
	case ">=":
		return a >= b, nil
	case "<":
		return a < b, nil
	case "<=":
		return a <= b, nil
	}
	return false, nil
}

func expandTemplate(tmpl string, fields map[string]string) string {
	result := tmpl
	for key, val := range fields {
		placeholder := "${" + key + "}"
		result = strings.ReplaceAll(result, placeholder, val)
	}
	return result
}

func RenderAlerts(alerts []Alert) string {
	if len(alerts) == 0 {
		return "═══ Custom Rule Alerts ═══\n\n  No alerts triggered.\n"
	}

	var sb strings.Builder
	sb.WriteString("═══ Custom Rule Alerts ═══\n\n")

	severityIcons := map[string]string{
		"critical": "🔴",
		"warning":  "🟡",
		"info":     "🔵",
	}

	for _, alert := range alerts {
		icon := severityIcons[alert.Severity]
		if icon == "" {
			icon = "⚪"
		}
		ns := alert.Namespace
		if ns != "" {
			ns = ns + "/"
		}
		sb.WriteString(fmt.Sprintf("  %s [%s] %s%s/%s — %s\n",
			icon, alert.Severity, ns, alert.Resource, alert.Name, alert.Message))
	}

	sb.WriteString(fmt.Sprintf("\nTotal: %d alert(s)\n", len(alerts)))
	return sb.String()
}
