package dag

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcnkl/rpm/models"
)

func TestNewGraph(t *testing.T) {
	g := NewGraph()
	assert.NotNil(t, g)
	assert.NotNil(t, g.Nodes)
	assert.Empty(t, g.Nodes)
}

func TestGraph_AddTarget(t *testing.T) {
	tests := []struct {
		name           string
		targets        []*models.Target
		expectedCount  int
		expectedNodeID string
	}{
		{
			name: "add single target",
			targets: []*models.Target{
				{Name: "app_build", BundleName: "core"},
			},
			expectedCount:  1,
			expectedNodeID: "core:app_build",
		},
		{
			name: "add multiple targets",
			targets: []*models.Target{
				{Name: "app_build", BundleName: "core"},
				{Name: "app_dev", BundleName: "core"},
				{Name: "server_build", BundleName: "api"},
			},
			expectedCount:  3,
			expectedNodeID: "core:app_build",
		},
		{
			name: "duplicate target ignored",
			targets: []*models.Target{
				{Name: "app_build", BundleName: "core"},
				{Name: "app_build", BundleName: "core"},
			},
			expectedCount:  1,
			expectedNodeID: "core:app_build",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGraph()
			for _, target := range tt.targets {
				g.AddTarget(target)
			}
			assert.Equal(t, tt.expectedCount, len(g.Nodes))
			assert.Contains(t, g.Nodes, tt.expectedNodeID)
		})
	}
}

func TestGraph_Resolve(t *testing.T) {
	tests := []struct {
		name        string
		targets     []*models.Target
		bundles     map[string]*models.Bundle
		expectError bool
		errorMsg    string
	}{
		{
			name: "resolve local dependency",
			targets: []*models.Target{
				{Name: "app_build", BundleName: "core", Deps: []string{":lib_build"}},
				{Name: "lib_build", BundleName: "core"},
			},
			bundles: map[string]*models.Bundle{
				"core": {Name: "core"},
			},
			expectError: false,
		},
		{
			name: "resolve cross-bundle dependency",
			targets: []*models.Target{
				{Name: "app_build", BundleName: "api", Deps: []string{"core:lib_build"}},
				{Name: "lib_build", BundleName: "core"},
			},
			bundles: map[string]*models.Bundle{
				"core": {Name: "core"},
				"api":  {Name: "api"},
			},
			expectError: false,
		},
		{
			name: "missing dependency",
			targets: []*models.Target{
				{Name: "app_build", BundleName: "core", Deps: []string{":nonexistent"}},
			},
			bundles: map[string]*models.Bundle{
				"core": {Name: "core"},
			},
			expectError: true,
			errorMsg:    "dependency not found",
		},
		{
			name: "invalid reference format",
			targets: []*models.Target{
				{Name: "app_build", BundleName: "core", Deps: []string{"invalid"}},
			},
			bundles: map[string]*models.Bundle{
				"core": {Name: "core"},
			},
			expectError: true,
			errorMsg:    "invalid target reference",
		},
		{
			name: "no dependencies",
			targets: []*models.Target{
				{Name: "app_build", BundleName: "core"},
			},
			bundles: map[string]*models.Bundle{
				"core": {Name: "core"},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGraph()
			for _, target := range tt.targets {
				g.AddTarget(target)
			}

			err := g.Resolve(tt.bundles)
			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestGraph_TopologicalSort(t *testing.T) {
	tests := []struct {
		name        string
		setup       func() *Graph
		expectError bool
		errorMsg    string
		validate    func(t *testing.T, sorted []*Node)
	}{
		{
			name: "simple linear dependency",
			setup: func() *Graph {
				g := NewGraph()
				g.AddTarget(&models.Target{Name: "app_build", BundleName: "core", Deps: []string{":lib_build"}})
				g.AddTarget(&models.Target{Name: "lib_build", BundleName: "core"})
				g.Resolve(map[string]*models.Bundle{"core": {Name: "core"}})
				return g
			},
			expectError: false,
			validate: func(t *testing.T, sorted []*Node) {
				assert.Len(t, sorted, 2)
				libIdx := -1
				appIdx := -1
				for i, n := range sorted {
					if n.ID == "core:lib_build" {
						libIdx = i
					}
					if n.ID == "core:app_build" {
						appIdx = i
					}
				}
				assert.True(t, libIdx < appIdx, "lib_build should come before app_build")
			},
		},
		{
			name: "cycle detection",
			setup: func() *Graph {
				g := NewGraph()
				t1 := &models.Target{Name: "a", BundleName: "core", Deps: []string{":b"}}
				t2 := &models.Target{Name: "b", BundleName: "core", Deps: []string{":a"}}
				g.AddTarget(t1)
				g.AddTarget(t2)

				nodeA := g.Nodes["core:a"]
				nodeB := g.Nodes["core:b"]
				nodeA.Deps = []*Node{nodeB}
				nodeB.Deps = []*Node{nodeA}
				return g
			},
			expectError: true,
			errorMsg:    "dependency cycle detected",
		},
		{
			name: "no dependencies",
			setup: func() *Graph {
				g := NewGraph()
				g.AddTarget(&models.Target{Name: "a", BundleName: "core"})
				g.AddTarget(&models.Target{Name: "b", BundleName: "core"})
				return g
			},
			expectError: false,
			validate: func(t *testing.T, sorted []*Node) {
				assert.Len(t, sorted, 2)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := tt.setup()
			sorted, err := g.TopologicalSort()
			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, sorted)
				}
			}
		})
	}
}

func TestGraph_Ancestors(t *testing.T) {
	tests := []struct {
		name              string
		setup             func() *Graph
		targetID          string
		expectedAncestors []string
	}{
		{
			name: "single ancestor",
			setup: func() *Graph {
				g := NewGraph()
				g.AddTarget(&models.Target{Name: "app", BundleName: "core", Deps: []string{":lib"}})
				g.AddTarget(&models.Target{Name: "lib", BundleName: "core"})
				g.Resolve(map[string]*models.Bundle{"core": {Name: "core"}})
				return g
			},
			targetID:          "core:app",
			expectedAncestors: []string{"core:lib"},
		},
		{
			name: "multiple ancestors",
			setup: func() *Graph {
				g := NewGraph()
				g.AddTarget(&models.Target{Name: "app", BundleName: "core", Deps: []string{":lib", ":util"}})
				g.AddTarget(&models.Target{Name: "lib", BundleName: "core", Deps: []string{":base"}})
				g.AddTarget(&models.Target{Name: "util", BundleName: "core"})
				g.AddTarget(&models.Target{Name: "base", BundleName: "core"})
				g.Resolve(map[string]*models.Bundle{"core": {Name: "core"}})
				return g
			},
			targetID:          "core:app",
			expectedAncestors: []string{"core:lib", "core:util", "core:base"},
		},
		{
			name: "no ancestors",
			setup: func() *Graph {
				g := NewGraph()
				g.AddTarget(&models.Target{Name: "standalone", BundleName: "core"})
				return g
			},
			targetID:          "core:standalone",
			expectedAncestors: []string{},
		},
		{
			name: "nonexistent target",
			setup: func() *Graph {
				g := NewGraph()
				g.AddTarget(&models.Target{Name: "app", BundleName: "core"})
				return g
			},
			targetID:          "core:nonexistent",
			expectedAncestors: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := tt.setup()
			ancestors := g.Ancestors(tt.targetID)
			ancestorIDs := make([]string, len(ancestors))
			for i, a := range ancestors {
				ancestorIDs[i] = a.ID
			}
			assert.ElementsMatch(t, tt.expectedAncestors, ancestorIDs)
		})
	}
}

func TestGraph_Descendants(t *testing.T) {
	tests := []struct {
		name                string
		setup               func() *Graph
		targetID            string
		expectedDescendants []string
	}{
		{
			name: "single descendant",
			setup: func() *Graph {
				g := NewGraph()
				g.AddTarget(&models.Target{Name: "app", BundleName: "core", Deps: []string{":lib"}})
				g.AddTarget(&models.Target{Name: "lib", BundleName: "core"})
				g.Resolve(map[string]*models.Bundle{"core": {Name: "core"}})
				return g
			},
			targetID:            "core:lib",
			expectedDescendants: []string{"core:app"},
		},
		{
			name: "multiple descendants",
			setup: func() *Graph {
				g := NewGraph()
				g.AddTarget(&models.Target{Name: "app1", BundleName: "core", Deps: []string{":lib"}})
				g.AddTarget(&models.Target{Name: "app2", BundleName: "core", Deps: []string{":lib"}})
				g.AddTarget(&models.Target{Name: "lib", BundleName: "core"})
				g.Resolve(map[string]*models.Bundle{"core": {Name: "core"}})
				return g
			},
			targetID:            "core:lib",
			expectedDescendants: []string{"core:app1", "core:app2"},
		},
		{
			name: "no descendants",
			setup: func() *Graph {
				g := NewGraph()
				g.AddTarget(&models.Target{Name: "leaf", BundleName: "core"})
				return g
			},
			targetID:            "core:leaf",
			expectedDescendants: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := tt.setup()
			descendants := g.Descendants(tt.targetID)
			descendantIDs := make([]string, len(descendants))
			for i, d := range descendants {
				descendantIDs[i] = d.ID
			}
			assert.ElementsMatch(t, tt.expectedDescendants, descendantIDs)
		})
	}
}

func TestGraph_SubgraphFor(t *testing.T) {
	tests := []struct {
		name          string
		setup         func() *Graph
		ids           []string
		expectedNodes []string
	}{
		{
			name: "single target with dependencies",
			setup: func() *Graph {
				g := NewGraph()
				g.AddTarget(&models.Target{Name: "app", BundleName: "core", Deps: []string{":lib"}})
				g.AddTarget(&models.Target{Name: "lib", BundleName: "core", Deps: []string{":base"}})
				g.AddTarget(&models.Target{Name: "base", BundleName: "core"})
				g.AddTarget(&models.Target{Name: "other", BundleName: "core"})
				g.Resolve(map[string]*models.Bundle{"core": {Name: "core"}})
				return g
			},
			ids:           []string{"core:app"},
			expectedNodes: []string{"core:app", "core:lib", "core:base"},
		},
		{
			name: "multiple targets",
			setup: func() *Graph {
				g := NewGraph()
				g.AddTarget(&models.Target{Name: "a", BundleName: "core", Deps: []string{":shared"}})
				g.AddTarget(&models.Target{Name: "b", BundleName: "core", Deps: []string{":shared"}})
				g.AddTarget(&models.Target{Name: "shared", BundleName: "core"})
				g.Resolve(map[string]*models.Bundle{"core": {Name: "core"}})
				return g
			},
			ids:           []string{"core:a", "core:b"},
			expectedNodes: []string{"core:a", "core:b", "core:shared"},
		},
		{
			name: "nonexistent id ignored",
			setup: func() *Graph {
				g := NewGraph()
				g.AddTarget(&models.Target{Name: "app", BundleName: "core"})
				return g
			},
			ids:           []string{"core:nonexistent"},
			expectedNodes: []string{},
		},
		{
			name: "empty ids",
			setup: func() *Graph {
				g := NewGraph()
				g.AddTarget(&models.Target{Name: "app", BundleName: "core"})
				return g
			},
			ids:           []string{},
			expectedNodes: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := tt.setup()
			subgraph := g.SubgraphFor(tt.ids)
			nodeIDs := make([]string, 0, len(subgraph.Nodes))
			for id := range subgraph.Nodes {
				nodeIDs = append(nodeIDs, id)
			}
			assert.ElementsMatch(t, tt.expectedNodes, nodeIDs)
		})
	}
}
