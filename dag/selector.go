package dag

import (
	"path/filepath"
	"strings"

	"github.com/vcnkl/rpm/models"
)

type Selector struct {
	graph    *Graph
	repoRoot string
}

func NewSelector(graph *Graph, repoRoot string) *Selector {
	return &Selector{
		graph:    graph,
		repoRoot: repoRoot,
	}
}

func (s *Selector) SelectBySuffix(suffix string) []*Node {
	var selected []*Node
	for _, node := range s.graph.Nodes {
		if strings.HasSuffix(node.Target.Name, suffix) {
			selected = append(selected, node)
		}
	}
	return selected
}

func (s *Selector) SelectByIDs(ids []string) ([]*Node, error) {
	var selected []*Node
	for _, id := range ids {
		node, ok := s.graph.Nodes[id]
		if !ok {
			return nil, &TargetNotFoundError{ID: id}
		}
		selected = append(selected, node)
	}
	return selected, nil
}

func (s *Selector) SelectByBundleWithSuffix(bundleName, suffix string) []*Node {
	var selected []*Node
	prefix := bundleName + ":"
	for _, node := range s.graph.Nodes {
		if strings.HasPrefix(node.ID, prefix) && strings.HasSuffix(node.Target.Name, suffix) {
			selected = append(selected, node)
		}
	}
	return selected
}

func (s *Selector) ResolveTargetRefs(refs []string, suffix string) []string {
	var resolved []string
	for _, ref := range refs {
		if strings.Contains(ref, ":") {
			parts := strings.Split(ref, ":")
			targetName := parts[1]
			if !strings.HasSuffix(targetName, suffix) {
				candidate := parts[0] + ":" + targetName + suffix
				if _, ok := s.graph.Nodes[candidate]; ok {
					resolved = append(resolved, candidate)
					continue
				}
			}
			resolved = append(resolved, ref)
		} else {
			nodes := s.SelectByBundleWithSuffix(ref, suffix)
			if len(nodes) > 0 {
				for _, node := range nodes {
					resolved = append(resolved, node.ID)
				}
			} else {
				resolved = append(resolved, ref)
			}
		}
	}
	return resolved
}

func (s *Selector) SelectAffected(changedFiles []string) []*Node {
	affected := make(map[string]*Node)

	for _, node := range s.graph.Nodes {
		if s.isAffected(node.Target, changedFiles) {
			affected[node.ID] = node
			for _, desc := range s.graph.Descendants(node.ID) {
				affected[desc.ID] = desc
			}
		}
	}

	var result []*Node
	for _, node := range affected {
		result = append(result, node)
	}
	return result
}

func (s *Selector) isAffected(target *models.Target, changedFiles []string) bool {
	bundlePath := filepath.Join(s.repoRoot, target.BundlePath)

	for _, changed := range changedFiles {
		for _, pattern := range target.In {
			fullPattern := pattern
			if strings.HasPrefix(pattern, "./") {
				fullPattern = filepath.Join(bundlePath, pattern[2:])
			} else if !filepath.IsAbs(pattern) && !strings.HasPrefix(pattern, "//") {
				fullPattern = filepath.Join(bundlePath, pattern)
			} else if strings.HasPrefix(pattern, "//") {
				fullPattern = filepath.Join(s.repoRoot, pattern[2:])
			}

			matched, err := filepath.Match(fullPattern, changed)
			if err == nil && matched {
				return true
			}

			if strings.Contains(fullPattern, "**") {
				if simpleGlobMatch(fullPattern, changed) {
					return true
				}
			}
		}
	}

	return false
}

func simpleGlobMatch(pattern, path string) bool {
	parts := strings.Split(pattern, "**")
	if len(parts) != 2 {
		return false
	}

	prefix := parts[0]
	suffix := parts[1]

	if !strings.HasPrefix(path, prefix) {
		return false
	}

	if suffix == "" || suffix == "/" {
		return true
	}

	suffixPattern := strings.TrimPrefix(suffix, "/")

	if suffixPattern != "" {
		matched, _ := filepath.Match(suffixPattern, filepath.Base(path))
		if matched {
			return true
		}
	}

	return false
}

type TargetNotFoundError struct {
	ID string
}

func (e *TargetNotFoundError) Error() string {
	return "dependency not found: " + e.ID
}
