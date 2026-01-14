package dags

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcnkl/rpm/dag"
	"github.com/vcnkl/rpm/models"
)

func TestNewStore(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{
			name: "creates store with path",
			path: "/tmp/dag.json",
		},
		{
			name: "empty path",
			path: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(tt.path)
			assert.NotNil(t, store)
			assert.Equal(t, tt.path, store.path)
		})
	}
}

func TestStore_Save(t *testing.T) {
	tests := []struct {
		name        string
		setupGraph  func() *dag.Graph
		expectError bool
	}{
		{
			name: "save empty graph",
			setupGraph: func() *dag.Graph {
				return dag.NewGraph()
			},
			expectError: false,
		},
		{
			name: "save graph with single node",
			setupGraph: func() *dag.Graph {
				g := dag.NewGraph()
				g.AddTarget(&models.Target{Name: "app_build", BundleName: "core"})
				return g
			},
			expectError: false,
		},
		{
			name: "save graph with dependencies",
			setupGraph: func() *dag.Graph {
				g := dag.NewGraph()
				g.AddTarget(&models.Target{Name: "app_build", BundleName: "core", Deps: []string{":lib_build"}})
				g.AddTarget(&models.Target{Name: "lib_build", BundleName: "core"})
				g.Resolve(map[string]*models.Bundle{"core": {Name: "core"}})
				return g
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			path := filepath.Join(tmpDir, "subdir", "dag.json")

			store := NewStore(path)
			graph := tt.setupGraph()

			err := store.Save(graph)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)

				_, err := os.Stat(path)
				assert.NoError(t, err, "file should exist")
			}
		})
	}
}

func TestStore_Load(t *testing.T) {
	tests := []struct {
		name          string
		content       string
		expectError   bool
		expectedNodes int
		expectedEdges int
	}{
		{
			name:          "load valid JSON",
			content:       `{"nodes":[{"id":"core:app_build","bundle":"core","name":"app_build"}],"edges":[]}`,
			expectError:   false,
			expectedNodes: 1,
			expectedEdges: 0,
		},
		{
			name:          "load JSON with edges",
			content:       `{"nodes":[{"id":"core:app_build","bundle":"core","name":"app_build"},{"id":"core:lib_build","bundle":"core","name":"lib_build"}],"edges":[{"from":"core:app_build","to":"core:lib_build"}]}`,
			expectError:   false,
			expectedNodes: 2,
			expectedEdges: 1,
		},
		{
			name:          "load empty JSON",
			content:       `{"nodes":[],"edges":[]}`,
			expectError:   false,
			expectedNodes: 0,
			expectedEdges: 0,
		},
		{
			name:        "load invalid JSON",
			content:     `{invalid}`,
			expectError: true,
		},
		{
			name:          "file does not exist",
			content:       "",
			expectError:   false,
			expectedNodes: 0,
			expectedEdges: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			path := filepath.Join(tmpDir, "dag.json")

			if tt.content != "" {
				err := os.WriteFile(path, []byte(tt.content), 0644)
				require.NoError(t, err)
			} else if tt.name != "file does not exist" {
				err := os.WriteFile(path, []byte(tt.content), 0644)
				require.NoError(t, err)
			}

			store := NewStore(path)
			result, err := store.Load()

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedNodes, len(result.Nodes))
				assert.Equal(t, tt.expectedEdges, len(result.Edges))
			}
		})
	}
}

func TestStore_Serialize(t *testing.T) {
	tests := []struct {
		name          string
		setupGraph    func() *dag.Graph
		expectedNodes []Node
		expectedEdges []Edge
	}{
		{
			name: "serialize empty graph",
			setupGraph: func() *dag.Graph {
				return dag.NewGraph()
			},
			expectedNodes: []Node{},
			expectedEdges: []Edge{},
		},
		{
			name: "serialize single node",
			setupGraph: func() *dag.Graph {
				g := dag.NewGraph()
				g.AddTarget(&models.Target{Name: "app_build", BundleName: "core"})
				return g
			},
			expectedNodes: []Node{
				{ID: "core:app_build", Bundle: "core", Name: "app_build"},
			},
			expectedEdges: []Edge{},
		},
		{
			name: "serialize with dependencies",
			setupGraph: func() *dag.Graph {
				g := dag.NewGraph()
				g.AddTarget(&models.Target{Name: "app_build", BundleName: "core", Deps: []string{":lib_build"}})
				g.AddTarget(&models.Target{Name: "lib_build", BundleName: "core"})
				g.Resolve(map[string]*models.Bundle{"core": {Name: "core"}})
				return g
			},
			expectedNodes: []Node{
				{ID: "core:app_build", Bundle: "core", Name: "app_build"},
				{ID: "core:lib_build", Bundle: "core", Name: "lib_build"},
			},
			expectedEdges: []Edge{
				{From: "core:app_build", To: "core:lib_build"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore("")
			graph := tt.setupGraph()

			result := store.serialize(graph)

			assert.ElementsMatch(t, tt.expectedNodes, result.Nodes)
			assert.ElementsMatch(t, tt.expectedEdges, result.Edges)
		})
	}
}

func TestStore_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "dag.json")

	g := dag.NewGraph()
	g.AddTarget(&models.Target{Name: "app_build", BundleName: "core", Deps: []string{":lib_build"}})
	g.AddTarget(&models.Target{Name: "lib_build", BundleName: "core"})
	g.Resolve(map[string]*models.Bundle{"core": {Name: "core"}})

	store := NewStore(path)
	err := store.Save(g)
	require.NoError(t, err)

	loaded, err := store.Load()
	require.NoError(t, err)

	assert.Len(t, loaded.Nodes, 2)
	assert.Len(t, loaded.Edges, 1)

	nodeIDs := make([]string, len(loaded.Nodes))
	for i, n := range loaded.Nodes {
		nodeIDs[i] = n.ID
	}
	assert.ElementsMatch(t, []string{"core:app_build", "core:lib_build"}, nodeIDs)

	assert.Equal(t, "core:app_build", loaded.Edges[0].From)
	assert.Equal(t, "core:lib_build", loaded.Edges[0].To)
}
