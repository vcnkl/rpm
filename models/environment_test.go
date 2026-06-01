package models_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vcnkl/rpm/models"
)

func TestDependencyInstanceMode_Valid(t *testing.T) {
	tests := []struct {
		name  string
		mode  models.DependencyInstanceMode
		valid bool
	}{
		{name: "shared", mode: models.DependencyInstanceModeShared, valid: true},
		{name: "dedicated", mode: models.DependencyInstanceModeDedicated, valid: true},
		{name: "empty", mode: "", valid: false},
		{name: "invalid", mode: "global", valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, tt.mode.Valid())
		})
	}
}

func TestDefaultDependencyInstanceMode(t *testing.T) {
	assert.Equal(t, models.DependencyInstanceModeShared, models.DefaultDependencyInstanceMode(""))
	assert.Equal(t, models.DependencyInstanceModeDedicated, models.DefaultDependencyInstanceMode(models.DependencyInstanceModeDedicated))
}
