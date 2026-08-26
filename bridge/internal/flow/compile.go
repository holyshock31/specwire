package flow

import (
	"fmt"

	"specwire/bridge/internal/domain"
)

func Compile(graph domain.FlowGraph, catalog Catalog) (map[string]any, error) {
	validation := catalog.Validate(graph, true)
	if !validation.Valid {
		return nil, fmt.Errorf("flow graph is not publishable: %s", validation.Diagnostics[0].Message)
	}
	indegree := map[domain.ID]int{}
	adjacency := map[domain.ID][]domain.ID{}
	for _, node := range graph.Nodes {
		indegree[node.ID] = 0
	}
	for _, edge := range graph.Edges {
		adjacency[edge.FromNodeID] = append(adjacency[edge.FromNodeID], edge.ToNodeID)
		indegree[edge.ToNodeID]++
	}
	queue := []domain.ID{}
	for id, degree := range indegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	order := make([]domain.ID, 0, len(graph.Nodes))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		order = append(order, id)
		for _, next := range adjacency[id] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	nodes := make(map[domain.ID]domain.FlowNode, len(graph.Nodes))
	for _, node := range graph.Nodes {
		nodes[node.ID] = node
	}
	ordered := make([]map[string]any, 0, len(order))
	behaviors := []string{}
	for _, id := range order {
		node := nodes[id]
		compiled := map[string]any{"id": node.ID, "kind": node.Kind, "name": node.Name}
		if node.Connector != nil {
			ref := node.Connector.BehaviorKey + "@" + node.Connector.BehaviorVersion
			compiled["behavior_ref"] = ref
			behaviors = append(behaviors, ref)
		}
		if node.Generic != nil {
			compiled["generic_type"] = node.Generic.Type
		}
		ordered = append(ordered, compiled)
	}
	return map[string]any{"plan_version": "1", "node_order": ordered, "behavior_refs": uniqueStrings(behaviors), "edge_count": len(graph.Edges)}, nil
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, value := range values {
		if _, ok := seen[value]; !ok {
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	return out
}
