package create

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vcnkl/rpm/dag"
	"github.com/vcnkl/rpm/models"
	envtui "github.com/vcnkl/rpm/ui/env-tui"
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
	assert.Equal(t, "env candidates / tier 0", items[0].Label)
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
		if !item.Header {
			tiers[item.Ref] = item.Tier
		}
	}
	assert.Equal(t, 0, tiers["api:build"])
	assert.Equal(t, 1, tiers["api:web_dev"])
}

func TestDependencySelectItemsGroupByBundle(t *testing.T) {
	items := dependencySelectItems([]string{"web:redis", "api:postgres"}, []string{"web:redis"})

	assert.Equal(t, "api", items[0].Label)
	assert.Equal(t, "web", items[2].Label)
	assert.Equal(t, []string{"web:redis"}, selectedRefs(items))
}

func selectedRefs(items []envtui.SelectionItem) []string {
	var refs []string
	for _, item := range items {
		if item.Selected && !item.Header {
			refs = append(refs, item.Ref)
		}
	}
	sort.Strings(refs)
	return refs
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
