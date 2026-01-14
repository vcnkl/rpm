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
			targetName: "server_dev",
			expected:   "services/api:server_dev",
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

func TestTarget_HasSuffix(t *testing.T) {
	tests := []struct {
		name       string
		targetName string
		suffix     string
		expected   bool
	}{
		{
			name:       "has _build suffix",
			targetName: "app_build",
			suffix:     "_build",
			expected:   true,
		},
		{
			name:       "has _dev suffix",
			targetName: "server_dev",
			suffix:     "_dev",
			expected:   true,
		},
		{
			name:       "has _test suffix",
			targetName: "api_test",
			suffix:     "_test",
			expected:   true,
		},
		{
			name:       "no matching suffix",
			targetName: "app_build",
			suffix:     "_dev",
			expected:   false,
		},
		{
			name:       "empty suffix",
			targetName: "app_build",
			suffix:     "",
			expected:   true,
		},
		{
			name:       "suffix longer than name",
			targetName: "app",
			suffix:     "_build",
			expected:   false,
		},
		{
			name:       "exact match",
			targetName: "_build",
			suffix:     "_build",
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := &Target{Name: tt.targetName}
			assert.Equal(t, tt.expected, target.HasSuffix(tt.suffix))
		})
	}
}
