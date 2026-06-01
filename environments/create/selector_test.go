package create

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vcnkl/rpm/dag"
	"github.com/vcnkl/rpm/models"
)

func TestTargetSelectItemsDefaultEnvSuffixes(t *testing.T) {
	targets := []*models.Target{
		target("api", "build", nil),
		target("api", "web_dev", nil),
		target("worker", "jobs_serve", nil),
	}
	graph := graphFor(targets)

	items := targetSelectItems(targets, graph, nil)

	assert.Equal(t, []string{"api:web_dev", "worker:jobs_serve"}, selectedRefs(items))
	assert.Equal(t, "env candidates / tier 0", items[0].label)
}

func TestTargetSelectItemsUseExistingSelectionForEdit(t *testing.T) {
	targets := []*models.Target{
		target("api", "build", nil),
		target("api", "web_dev", nil),
	}
	graph := graphFor(targets)

	items := targetSelectItems(targets, graph, []string{"api:build"})

	assert.Equal(t, []string{"api:build"}, selectedRefs(items))
}

func TestTargetSelectItemsTierByDependencyGraph(t *testing.T) {
	targets := []*models.Target{
		target("api", "web_dev", []string{":build"}),
		target("api", "build", nil),
	}
	graph := graphFor(targets)

	items := targetSelectItems(targets, graph, nil)

	tiers := map[string]int{}
	for _, item := range items {
		if !item.header {
			tiers[item.ref] = item.tier
		}
	}
	assert.Equal(t, 0, tiers["api:build"])
	assert.Equal(t, 1, tiers["api:web_dev"])
}

func TestSelectorModelMovesAndTogglesSelectableItems(t *testing.T) {
	model := selectorModel{
		items: []selectItem{
			{label: "header", header: true},
			{ref: "api:one"},
			{label: "header 2", header: true},
			{ref: "api:two"},
		},
	}
	model.cursor = model.firstSelectable()

	assert.Equal(t, 1, model.cursor)
	model.move(1)
	assert.Equal(t, 3, model.cursor)
	model.toggle()

	assert.Equal(t, []string{"api:two"}, model.selectedRefs())
}

func TestDependencySelectItemsGroupByBundle(t *testing.T) {
	items := dependencySelectItems([]string{"web:redis", "api:postgres"}, []string{"web:redis"})

	assert.Equal(t, "api", items[0].label)
	assert.Equal(t, "web", items[2].label)
	assert.Equal(t, []string{"web:redis"}, selectedRefs(items))
}

func selectedRefs(items []selectItem) []string {
	model := selectorModel{items: items}
	return model.selectedRefs()
}

func target(bundle string, name string, deps []string) *models.Target {
	return &models.Target{
		Name:       name,
		BundleName: bundle,
		BundlePath: "apps/" + bundle,
		Deps:       deps,
	}
}

func graphFor(targets []*models.Target) *dag.Graph {
	graph := dag.NewGraph()
	bundles := map[string]*models.Bundle{}
	for _, target := range targets {
		graph.AddTarget(target)
		bundle := bundles[target.BundleName]
		if bundle == nil {
			bundle = &models.Bundle{Name: target.BundleName}
			bundles[target.BundleName] = bundle
		}
		bundle.Targets = append(bundle.Targets, target)
	}
	if err := graph.Resolve(bundles); err != nil {
		panic(err)
	}
	return graph
}
