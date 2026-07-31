package create

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"

	envtui "github.com/vcwx/rpm/environments/tui"
	"github.com/vcwx/rpm/models"
)

func TestTargetSelectItemsGroupsRunnableTargetsByBundle(t *testing.T) {
	targets := []*models.Target{
		target("worker", "jobs_serve"),
		target("api", "web_dev"),
	}

	items := targetSelectItems(targets, nil)

	assert.Equal(t, []string{"api", "api:web_dev", "worker", "worker:jobs_serve"}, labels(items))
	assert.Equal(t, []string{"api:web_dev", "worker:jobs_serve"}, selectedRefs(items))
	assert.True(t, items[0].Expandable)
	assert.True(t, items[0].Expanded)
}

func TestTargetSelectItemsUseExistingSelectionForEdit(t *testing.T) {
	targets := []*models.Target{
		target("api", "api_dev"),
		target("worker", "jobs_serve"),
	}

	items := targetSelectItems(targets, []string{"worker:jobs_serve"})

	assert.Equal(t, []string{"worker:jobs_serve"}, selectedRefs(items))
}

func TestBeforeSelectItemsShowOtherTargets(t *testing.T) {
	targets := []*models.Target{
		target("api", "api_dev"),
		target("api", "migrate"),
		target("worker", "jobs_serve"),
	}

	items := beforeSelectItems(targets, []string{"api:api_dev"}, []string{"api:migrate"})

	assert.Equal(t, []string{"api", "api:migrate", "worker", "worker:jobs_serve"}, labels(items))
	assert.Equal(t, []string{"api:migrate"}, selectedRefs(items))
	assert.Equal(t, "disabled", items[1].Status)
}

func TestDependencySelectItemsGroupByBundle(t *testing.T) {
	items := dependencySelectItems([]string{"redis", "postgres"}, []string{"redis"})

	assert.Equal(t, []string{"dependencies", "postgres", "redis"}, labels(items))
	assert.Equal(t, []string{"redis"}, selectedRefs(items))
}

func labels(items []envtui.SelectionItem) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item.Ref != "" {
			result = append(result, item.Ref)
			continue
		}
		result = append(result, item.Label)
	}
	return result
}

func selectedRefs(items []envtui.SelectionItem) []string {
	var refs []string
	for _, item := range items {
		if item.Selected && !item.Header && !item.Expandable {
			refs = append(refs, item.Ref)
		}
	}
	sort.Strings(refs)
	return refs
}

func target(bundle string, name string) *models.Target {
	return &models.Target{
		Name:       name,
		BundleName: bundle,
		BundlePath: "apps/" + bundle,
	}
}
