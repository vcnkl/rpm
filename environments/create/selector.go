package create

import (
	"sort"
	"strings"

	envtui "github.com/vcnkl/rpm/environments/tui"
	"github.com/vcnkl/rpm/models"
)

func targetSelectItems(targets []*models.Target, selected []string) []envtui.SelectionItem {
	selectedSet := stringSet(selected)
	useDefaults := len(selectedSet) == 0
	grouped := make(map[string][]envtui.SelectionItem)
	for _, target := range targets {
		ref := target.ID()
		isSelected := selectedSet[ref] || useDefaults
		grouped[target.BundleName] = append(grouped[target.BundleName], envtui.SelectionItem{
			Ref:      ref,
			Label:    target.Name,
			Detail:   ref,
			Group:    target.BundleName,
			Status:   "selected",
			Selected: isSelected,
			Defaults: useDefaults,
		})
	}
	return groupedTargetItems(grouped)
}

func beforeSelectItems(targets []*models.Target, mainRefs []string, selected []string) []envtui.SelectionItem {
	mainSet := stringSet(mainRefs)
	selectedSet := stringSet(selected)
	grouped := make(map[string][]envtui.SelectionItem)
	for _, target := range targets {
		ref := target.ID()
		if mainSet[ref] {
			continue
		}
		grouped[target.BundleName] = append(grouped[target.BundleName], envtui.SelectionItem{
			Ref:      ref,
			Label:    target.Name,
			Detail:   ref,
			Group:    target.BundleName,
			Status:   "disabled",
			Selected: selectedSet[ref],
		})
	}
	return groupedTargetItems(grouped)
}

func dependencySelectItems(refs []string, selected []string) []envtui.SelectionItem {
	selectedSet := stringSet(selected)
	grouped := make(map[string][]envtui.SelectionItem)
	for _, ref := range refs {
		bundle, name, ok := strings.Cut(ref, ":")
		if !ok {
			bundle = "dependencies"
			name = ref
		}
		grouped[bundle] = append(grouped[bundle], envtui.SelectionItem{
			Ref:      ref,
			Label:    name,
			Detail:   ref,
			Group:    bundle,
			Selected: selectedSet[ref],
		})
	}
	return groupedTargetItems(grouped)
}

func groupedTargetItems(grouped map[string][]envtui.SelectionItem) []envtui.SelectionItem {
	groups := make([]string, 0, len(grouped))
	for group := range grouped {
		groups = append(groups, group)
	}
	sort.Strings(groups)

	items := make([]envtui.SelectionItem, 0)
	for _, group := range groups {
		children := grouped[group]
		sort.Slice(children, func(i, j int) bool {
			return children[i].Ref < children[j].Ref
		})
		allSelected := len(children) > 0
		for _, child := range children {
			if !child.Selected {
				allSelected = false
				break
			}
		}
		items = append(items, envtui.SelectionItem{
			Label:      group,
			Group:      group,
			Selected:   allSelected,
			Expandable: true,
			Expanded:   true,
		})
		items = append(items, children...)
	}
	return items
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
