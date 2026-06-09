package audit

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

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
