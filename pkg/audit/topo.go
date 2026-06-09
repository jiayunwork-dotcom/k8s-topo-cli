package audit

import (
	"github.com/k8s-topo-cli/pkg/topology"
)

func AnnotateTopologyWithViolations(topo *topology.Topology, nsResults []NamespaceAuditResult) {
	nsActionMap := make(map[string]string)
	nsViolMap := make(map[string][]Violation)

	for _, nr := range nsResults {
		if len(nr.Violations) > 0 {
			highestAction := ""
			for _, v := range nr.Violations {
				if actionSeverity(v.Action) > actionSeverity(highestAction) {
					highestAction = v.Action
				}
				nsViolMap[nr.Namespace] = append(nsViolMap[nr.Namespace], v)
			}
			nsActionMap[nr.Namespace] = highestAction
		}
	}

	for _, root := range topo.Roots {
		if root.Type != topology.TypeNamespace {
			continue
		}
		action, hasViol := nsActionMap[root.Name]
		if !hasViol {
			continue
		}
		root.ViolationAnnotations = append(root.ViolationAnnotations, topology.ViolationAnnotation{
			Action: action,
		})

		violations := nsViolMap[root.Name]
		violDimMap := make(map[string]Violation)
		for _, v := range violations {
			violDimMap[string(v.Dimension)] = v
		}

		annotateDescendants(root, violDimMap)
	}
}

func annotateDescendants(node *topology.TopoNode, violDimMap map[string]Violation) {
	for _, child := range node.Children {
		switch child.Type {
		case topology.TypePod:
			if v, ok := violDimMap[string(DimMaxPods)]; ok {
				child.ViolationAnnotations = append(child.ViolationAnnotations, topology.ViolationAnnotation{
					Action:      v.Action,
					Dimension:   "pods",
					Current:     v.CurrentValue,
					Limit:       v.PolicyLimit,
					OverPercent: v.OverPercent,
				})
			}
			if v, ok := violDimMap[string(DimMaxContainerCPU)]; ok {
				child.ViolationAnnotations = append(child.ViolationAnnotations, topology.ViolationAnnotation{
					Action:      v.Action,
					Dimension:   "containerCPU",
					Current:     v.CurrentValue,
					Limit:       v.PolicyLimit,
					OverPercent: v.OverPercent,
				})
			}
			if v, ok := violDimMap[string(DimMaxContainerMemory)]; ok {
				child.ViolationAnnotations = append(child.ViolationAnnotations, topology.ViolationAnnotation{
					Action:      v.Action,
					Dimension:   "containerMem",
					Current:     v.CurrentValue,
					Limit:       v.PolicyLimit,
					OverPercent: v.OverPercent,
				})
			}
		case topology.TypeDeployment:
			if v, ok := violDimMap[string(DimMaxDeployments)]; ok {
				child.ViolationAnnotations = append(child.ViolationAnnotations, topology.ViolationAnnotation{
					Action:      v.Action,
					Dimension:   "deployments",
					Current:     v.CurrentValue,
					Limit:       v.PolicyLimit,
					OverPercent: v.OverPercent,
				})
			}
		case topology.TypeService:
			if v, ok := violDimMap[string(DimMaxServices)]; ok {
				child.ViolationAnnotations = append(child.ViolationAnnotations, topology.ViolationAnnotation{
					Action:      v.Action,
					Dimension:   "services",
					Current:     v.CurrentValue,
					Limit:       v.PolicyLimit,
					OverPercent: v.OverPercent,
				})
			}
		}
		annotateDescendants(child, violDimMap)
	}
}
