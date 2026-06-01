package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTarget_ID(t *testing.T) {
	tests := []struct {
		name       string
		bundleName string
		targetName string
		expected   string
	}{
		{
			name:       "simple target",
			bundleName: "core",
			targetName: "app_build",
			expected:   "core:app_build",
		},
		{
			name:       "nested bundle name",
			bundleName: "services/api",
			targetName: "serve",
			expected:   "services/api:serve",
		},
		{
			name:       "empty names",
			bundleName: "",
			targetName: "",
			expected:   ":",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := &Target{
				BundleName: tt.bundleName,
				Name:       tt.targetName,
			}
			assert.Equal(t, tt.expected, target.ID())
		})
	}
}
