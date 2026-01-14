package dags

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vcnkl/rpm/dag"
)

type Node struct {
	ID     string `json:"id"`
	Bundle string `json:"bundle"`
	Name   string `json:"name"`
}

type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type SerializedGraph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

type Store struct {
	path string
}

func NewStore(path string) *Store {
	return &Store{
		path: path,
	}
}

func (s *Store) Save(graph *dag.Graph) error {
	serialized := s.serialize(graph)

	data, err := json.MarshalIndent(serialized, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize dag: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	tmpPath := s.path + ".tmp"
	if err = os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write dag file %s: %w", tmpPath, err)
	}

	if err = os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("failed to rename dag file: %w", err)
	}

	return nil
}

func (s *Store) Load() (*SerializedGraph, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return &SerializedGraph{
			Nodes: []Node{},
			Edges: []Edge{},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read dag file %s: %w", s.path, err)
	}

	var graph SerializedGraph
	if err = json.Unmarshal(data, &graph); err != nil {
		return nil, fmt.Errorf("failed to parse dag file %s: %w", s.path, err)
	}

	return &graph, nil
}

func (s *Store) serialize(graph *dag.Graph) *SerializedGraph {
	result := &SerializedGraph{
		Nodes: make([]Node, 0, len(graph.Nodes)),
		Edges: make([]Edge, 0),
	}

	for _, node := range graph.Nodes {
		result.Nodes = append(result.Nodes, Node{
			ID:     node.ID,
			Bundle: node.Target.BundleName,
			Name:   node.Target.Name,
		})

		for _, dep := range node.Deps {
			result.Edges = append(result.Edges, Edge{
				From: node.ID,
				To:   dep.ID,
			})
		}
	}

	return result
}
