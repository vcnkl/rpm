package spec

import "github.com/vcwx/rpm/models"

type RuntimeOptions struct {
	NoReload bool
}

func BlueprintWithRuntimeOptions(blueprint *models.EnvironmentBlueprint, opts RuntimeOptions) *models.EnvironmentBlueprint {
	next := *blueprint
	next.Variables = copyStringMap(blueprint.Variables)
	next.Before = append([]string{}, blueprint.Before...)
	next.Targets = append([]models.EnvironmentTarget{}, blueprint.Targets...)
	for i := range next.Targets {
		next.Targets[i].Env = copyStringMap(next.Targets[i].Env)
	}
	next.DependencyPolicy = models.DependencyPolicy{
		Enabled: blueprint.DependencyPolicy.Enabled,
		Include: append([]string{}, blueprint.DependencyPolicy.Include...),
		Exclude: append([]string{}, blueprint.DependencyPolicy.Exclude...),
	}
	if opts.NoReload {
		next.ReloadPolicy.Enabled = false
		for i := range next.Targets {
			value := false
			next.Targets[i].Reload = &value
		}
	}
	return &next
}

func copyStringMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
