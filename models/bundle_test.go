package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBundle_Target(t *testing.T) {
	bundle := &Bundle{
		Name: "core",
		Targets: []*Target{
			{Name: "app_build", BundleName: "core"},
			{Name: "app_dev", BundleName: "core"},
			{Name: "app_test", BundleName: "core"},
		},
	}

	tests := []struct {
		name       string
		targetName string
		wantFound  bool
		wantName   string
	}{
		{
			name:       "existing target app_build",
			targetName: "app_build",
			wantFound:  true,
			wantName:   "app_build",
		},
		{
			name:       "existing target app_dev",
			targetName: "app_dev",
			wantFound:  true,
			wantName:   "app_dev",
		},
		{
			name:       "non-existing target",
			targetName: "nonexistent",
			wantFound:  false,
			wantName:   "",
		},
		{
			name:       "empty target name",
			targetName: "",
			wantFound:  false,
			wantName:   "",
		},
		{
			name:       "partial match should not work",
			targetName: "app",
			wantFound:  false,
			wantName:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, found := bundle.Target(tt.targetName)
			assert.Equal(t, tt.wantFound, found)
			if found {
				assert.Equal(t, tt.wantName, target.Name)
			} else {
				assert.Nil(t, target)
			}
		})
	}
}

func TestBundle_TargetsByType(t *testing.T) {
	bundle := &Bundle{
		Name: "core",
		Targets: []*Target{
			{Name: "app_build", BundleName: "core"},
			{Name: "api_build", BundleName: "core"},
			{Name: "app_dev", BundleName: "core"},
			{Name: "app_test", BundleName: "core"},
			{Name: "api_test", BundleName: "core"},
		},
	}

	tests := []struct {
		name          string
		suffix        string
		expectedCount int
		expectedNames []string
	}{
		{
			name:          "build targets",
			suffix:        "_build",
			expectedCount: 2,
			expectedNames: []string{"app_build", "api_build"},
		},
		{
			name:          "dev targets",
			suffix:        "_dev",
			expectedCount: 1,
			expectedNames: []string{"app_dev"},
		},
		{
			name:          "test targets",
			suffix:        "_test",
			expectedCount: 2,
			expectedNames: []string{"app_test", "api_test"},
		},
		{
			name:          "no matching suffix",
			suffix:        "_image",
			expectedCount: 0,
			expectedNames: []string{},
		},
		{
			name:          "empty suffix returns all",
			suffix:        "",
			expectedCount: 5,
			expectedNames: []string{"app_build", "api_build", "app_dev", "app_test", "api_test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targets := bundle.TargetsByType(tt.suffix)
			assert.Equal(t, tt.expectedCount, len(targets))

			names := make([]string, len(targets))
			for i, target := range targets {
				names[i] = target.Name
			}
			assert.ElementsMatch(t, tt.expectedNames, names)
		})
	}
}

func TestHasSuffix(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		suffix   string
		expected bool
	}{
		{
			name:     "has suffix",
			s:        "app_build",
			suffix:   "_build",
			expected: true,
		},
		{
			name:     "no suffix",
			s:        "app_build",
			suffix:   "_dev",
			expected: false,
		},
		{
			name:     "empty suffix",
			s:        "app_build",
			suffix:   "",
			expected: true,
		},
		{
			name:     "suffix longer than string",
			s:        "app",
			suffix:   "_build",
			expected: false,
		},
		{
			name:     "exact match",
			s:        "_build",
			suffix:   "_build",
			expected: true,
		},
		{
			name:     "empty string and suffix",
			s:        "",
			suffix:   "",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, hasSuffix(tt.s, tt.suffix))
		})
	}
}
