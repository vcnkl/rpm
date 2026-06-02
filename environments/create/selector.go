package create

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vcnkl/rpm/dag"
	"github.com/vcnkl/rpm/models"
	envtui "github.com/vcnkl/rpm/ui/env-tui"
)

func targetSelectItems(targets []*models.Target, graph *dag.Graph, selected []string) []envtui.SelectionItem {
	selectedSet := stringSet(selected)
	useDefaultSuffixes := len(selectedSet) == 0
	items := make([]envtui.SelectionItem, 0, len(targets)+8)
	for _, target := range targets {
		ref := target.ID()
		defaults := strings.HasSuffix(target.Name, "_dev") || strings.HasSuffix(target.Name, "_serve")
		isSelected := selectedSet[ref] || (useDefaultSuffixes && defaults)
		group := "other targets"
		if defaults {
			group = "env candidates"
		}
		items = append(items, envtui.SelectionItem{
			Ref:      ref,
			Label:    ref,
			Detail:   target.BundlePath,
			Group:    group,
			Tier:     targetTier(graph, ref),
			Selected: isSelected,
			Defaults: defaults,
			Muted:    !defaults,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Group != items[j].Group {
			return items[i].Group == "env candidates"
		}
		if items[i].Tier != items[j].Tier {
			return items[i].Tier < items[j].Tier
		}
		return items[i].Ref < items[j].Ref
	})
	return withTierHeadings(items)
}

func dependencySelectItems(refs []string, selected []string) []envtui.SelectionItem {
	selectedSet := stringSet(selected)
	items := make([]envtui.SelectionItem, 0, len(refs)+8)
	for _, ref := range refs {
		bundle, name, ok := strings.Cut(ref, ":")
		if !ok {
			bundle = "dependencies"
			name = ref
		}
		items = append(items, envtui.SelectionItem{
			Ref:      ref,
			Label:    name,
			Detail:   ref,
			Group:    bundle,
			Selected: selectedSet[ref],
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Group != items[j].Group {
			return items[i].Group < items[j].Group
		}
		return items[i].Ref < items[j].Ref
	})
	return withGroupHeadings(items)
}

func withTierHeadings(items []envtui.SelectionItem) []envtui.SelectionItem {
	result := make([]envtui.SelectionItem, 0, len(items)+8)
	lastGroup := ""
	lastTier := -1
	for _, item := range items {
		if item.Group != lastGroup || item.Tier != lastTier {
			result = append(result, envtui.SelectionItem{
				Label:  fmt.Sprintf("%s / tier %d", item.Group, item.Tier),
				Group:  item.Group,
				Tier:   item.Tier,
				Header: true,
				Muted:  item.Group != "env candidates",
			})
			lastGroup = item.Group
			lastTier = item.Tier
		}
		result = append(result, item)
	}
	return result
}

func withGroupHeadings(items []envtui.SelectionItem) []envtui.SelectionItem {
	result := make([]envtui.SelectionItem, 0, len(items)+8)
	lastGroup := ""
	for _, item := range items {
		if item.Group != lastGroup {
			result = append(result, envtui.SelectionItem{Label: item.Group, Group: item.Group, Header: true})
			lastGroup = item.Group
		}
		result = append(result, item)
	}
	return result
}

func targetTier(graph *dag.Graph, ref string) int {
	if graph == nil {
		return 0
	}
	seen := map[string]bool{}
	var visit func(id string) int
	visit = func(id string) int {
		if seen[id] {
			return 0
		}
		seen[id] = true
		node, ok := graph.Nodes[id]
		if !ok {
			return 0
		}
		depth := 0
		for _, dep := range node.Deps {
			depth = max(depth, visit(dep.ID)+1)
		}
		seen[id] = false
		return depth
	}
	return visit(ref)
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = true
		}
	}
	return set
}
