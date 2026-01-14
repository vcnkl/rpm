package dag

import (
	"fmt"
	"strings"

	"github.com/vcnkl/rpm/models"
)

type Node struct {
	ID         string
	Target     *models.Target
	Deps       []*Node
	Dependents []*Node
}

type Graph struct {
	Nodes map[string]*Node
}

func NewGraph() *Graph {
	return &Graph{
		Nodes: make(map[string]*Node),
	}
}

func (g *Graph) AddTarget(t *models.Target) {
	id := t.ID()
	if _, exists := g.Nodes[id]; !exists {
		g.Nodes[id] = &Node{
			ID:         id,
			Target:     t,
			Deps:       make([]*Node, 0),
			Dependents: make([]*Node, 0),
		}
	}
}

func (g *Graph) Resolve(bundles map[string]*models.Bundle) error {
	for _, node := range g.Nodes {
		for _, depRef := range node.Target.Deps {
			depID, err := g.resolveRef(depRef, node.Target.BundleName, bundles)
			if err != nil {
				return fmt.Errorf("failed to resolve dependency %s for target %s: %w", depRef, node.ID, err)
			}

			depNode, ok := g.Nodes[depID]
			if !ok {
				return fmt.Errorf("dependency not found: %s (referenced by %s)", depID, node.ID)
			}

			node.Deps = append(node.Deps, depNode)
			depNode.Dependents = append(depNode.Dependents, node)
		}
	}

	return nil
}

func (g *Graph) resolveRef(ref, currentBundle string, bundles map[string]*models.Bundle) (string, error) {
	if strings.HasPrefix(ref, ":") {
		return currentBundle + ref, nil
	}

	parts := strings.Split(ref, ":")
	if len(parts) == 2 {
		return ref, nil
	}

	return "", fmt.Errorf("invalid target reference: %s", ref)
}

func (g *Graph) TopologicalSort() ([]*Node, error) {
	var sorted []*Node
	visited := make(map[string]bool)
	inStack := make(map[string]bool)

	var visit func(node *Node, path []string) error
	visit = func(node *Node, path []string) error {
		if inStack[node.ID] {
			cycle := append(path, node.ID)
			return fmt.Errorf("dependency cycle detected: %s", strings.Join(cycle, " -> "))
		}

		if visited[node.ID] {
			return nil
		}

		inStack[node.ID] = true
		path = append(path, node.ID)

		for _, dep := range node.Deps {
			if err := visit(dep, path); err != nil {
				return err
			}
		}

		inStack[node.ID] = false
		visited[node.ID] = true
		sorted = append(sorted, node)

		return nil
	}

	for _, node := range g.Nodes {
		if err := visit(node, nil); err != nil {
			return nil, err
		}
	}

	return sorted, nil
}

func (g *Graph) Ancestors(id string) []*Node {
	var ancestors []*Node
	visited := make(map[string]bool)

	var collect func(node *Node)
	collect = func(node *Node) {
		for _, dep := range node.Deps {
			if !visited[dep.ID] {
				visited[dep.ID] = true
				ancestors = append(ancestors, dep)
				collect(dep)
			}
		}
	}

	if node, ok := g.Nodes[id]; ok {
		collect(node)
	}

	return ancestors
}

func (g *Graph) Descendants(id string) []*Node {
	var descendants []*Node
	visited := make(map[string]bool)

	var collect func(node *Node)
	collect = func(node *Node) {
		for _, dep := range node.Dependents {
			if !visited[dep.ID] {
				visited[dep.ID] = true
				descendants = append(descendants, dep)
				collect(dep)
			}
		}
	}

	if node, ok := g.Nodes[id]; ok {
		collect(node)
	}

	return descendants
}

func (g *Graph) SubgraphFor(ids []string) *Graph {
	subgraph := NewGraph()
	visited := make(map[string]bool)

	var addWithDeps func(node *Node)
	addWithDeps = func(node *Node) {
		if visited[node.ID] {
			return
		}
		visited[node.ID] = true

		subgraph.Nodes[node.ID] = &Node{
			ID:         node.ID,
			Target:     node.Target,
			Deps:       make([]*Node, 0),
			Dependents: make([]*Node, 0),
		}

		for _, dep := range node.Deps {
			addWithDeps(dep)
		}
	}

	for _, id := range ids {
		if node, ok := g.Nodes[id]; ok {
			addWithDeps(node)
		}
	}

	for _, node := range subgraph.Nodes {
		origNode := g.Nodes[node.ID]
		for _, dep := range origNode.Deps {
			if subNode, ok := subgraph.Nodes[dep.ID]; ok {
				node.Deps = append(node.Deps, subNode)
				subNode.Dependents = append(subNode.Dependents, node)
			}
		}
	}

	return subgraph
}
