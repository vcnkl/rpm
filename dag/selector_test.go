package dag

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcnkl/rpm/models"
)

func setupTestGraph() *Graph {
	g := NewGraph()
	targets := []*models.Target{
		{Name: "app_build", BundleName: "core", BundlePath: "internal/core"},
		{Name: "app_dev", BundleName: "core", BundlePath: "internal/core"},
		{Name: "app_test", BundleName: "core", BundlePath: "internal/core"},
		{Name: "server_build", BundleName: "api", BundlePath: "internal/api"},
		{Name: "server_dev", BundleName: "api", BundlePath: "internal/api"},
		{Name: "cli_build", BundleName: "tools", BundlePath: "tools/cli"},
	}
	for _, t := range targets {
		g.AddTarget(t)
	}
	return g
}

func TestNewSelector(t *testing.T) {
	g := NewGraph()
	s := NewSelector(g, "/repo/root")
	assert.NotNil(t, s)
	assert.Equal(t, g, s.graph)
	assert.Equal(t, "/repo/root", s.repoRoot)
}

func TestSelector_SelectBySuffix(t *testing.T) {
	g := setupTestGraph()
	s := NewSelector(g, "/repo")

	tests := []struct {
		name          string
		suffix        string
		expectedCount int
		expectedIDs   []string
	}{
		{
			name:          "select build targets",
			suffix:        "_build",
			expectedCount: 3,
			expectedIDs:   []string{"core:app_build", "api:server_build", "tools:cli_build"},
		},
		{
			name:          "select dev targets",
			suffix:        "_dev",
			expectedCount: 2,
			expectedIDs:   []string{"core:app_dev", "api:server_dev"},
		},
		{
			name:          "select test targets",
			suffix:        "_test",
			expectedCount: 1,
			expectedIDs:   []string{"core:app_test"},
		},
		{
			name:          "no matching suffix",
			suffix:        "_image",
			expectedCount: 0,
			expectedIDs:   []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selected := s.SelectBySuffix(tt.suffix)
			assert.Equal(t, tt.expectedCount, len(selected))
			ids := make([]string, len(selected))
			for i, n := range selected {
				ids[i] = n.ID
			}
			assert.ElementsMatch(t, tt.expectedIDs, ids)
		})
	}
}

func TestSelector_SelectByIDs(t *testing.T) {
	g := setupTestGraph()
	s := NewSelector(g, "/repo")

	tests := []struct {
		name        string
		ids         []string
		expectError bool
		expectedIDs []string
	}{
		{
			name:        "single valid ID",
			ids:         []string{"core:app_build"},
			expectError: false,
			expectedIDs: []string{"core:app_build"},
		},
		{
			name:        "multiple valid IDs",
			ids:         []string{"core:app_build", "api:server_build"},
			expectError: false,
			expectedIDs: []string{"core:app_build", "api:server_build"},
		},
		{
			name:        "invalid ID",
			ids:         []string{"nonexistent:target"},
			expectError: true,
			expectedIDs: nil,
		},
		{
			name:        "empty IDs",
			ids:         []string{},
			expectError: false,
			expectedIDs: []string{},
		},
		{
			name:        "mix of valid and invalid",
			ids:         []string{"core:app_build", "nonexistent:target"},
			expectError: true,
			expectedIDs: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selected, err := s.SelectByIDs(tt.ids)
			if tt.expectError {
				require.Error(t, err)
				var targetErr *TargetNotFoundError
				assert.ErrorAs(t, err, &targetErr)
			} else {
				require.NoError(t, err)
				ids := make([]string, len(selected))
				for i, n := range selected {
					ids[i] = n.ID
				}
				assert.ElementsMatch(t, tt.expectedIDs, ids)
			}
		})
	}
}

func TestSelector_SelectByBundleWithSuffix(t *testing.T) {
	g := setupTestGraph()
	s := NewSelector(g, "/repo")

	tests := []struct {
		name          string
		bundleName    string
		suffix        string
		expectedCount int
		expectedIDs   []string
	}{
		{
			name:          "core bundle build targets",
			bundleName:    "core",
			suffix:        "_build",
			expectedCount: 1,
			expectedIDs:   []string{"core:app_build"},
		},
		{
			name:          "core bundle all targets with empty suffix",
			bundleName:    "core",
			suffix:        "",
			expectedCount: 3,
			expectedIDs:   []string{"core:app_build", "core:app_dev", "core:app_test"},
		},
		{
			name:          "api bundle dev targets",
			bundleName:    "api",
			suffix:        "_dev",
			expectedCount: 1,
			expectedIDs:   []string{"api:server_dev"},
		},
		{
			name:          "nonexistent bundle",
			bundleName:    "nonexistent",
			suffix:        "_build",
			expectedCount: 0,
			expectedIDs:   []string{},
		},
		{
			name:          "bundle with no matching suffix",
			bundleName:    "tools",
			suffix:        "_dev",
			expectedCount: 0,
			expectedIDs:   []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selected := s.SelectByBundleWithSuffix(tt.bundleName, tt.suffix)
			assert.Equal(t, tt.expectedCount, len(selected))
			ids := make([]string, len(selected))
			for i, n := range selected {
				ids[i] = n.ID
			}
			assert.ElementsMatch(t, tt.expectedIDs, ids)
		})
	}
}

func TestSelector_ResolveTargetRefs(t *testing.T) {
	g := setupTestGraph()
	s := NewSelector(g, "/repo")

	tests := []struct {
		name        string
		refs        []string
		suffix      string
		expectedIDs []string
	}{
		{
			name:        "bundle name expands to all matching targets",
			refs:        []string{"core"},
			suffix:      "_build",
			expectedIDs: []string{"core:app_build"},
		},
		{
			name:        "bundle name with dev suffix",
			refs:        []string{"core"},
			suffix:      "_dev",
			expectedIDs: []string{"core:app_dev"},
		},
		{
			name:        "full target ID used as-is",
			refs:        []string{"core:app_build"},
			suffix:      "_build",
			expectedIDs: []string{"core:app_build"},
		},
		{
			name:        "partial target ID gets suffix appended",
			refs:        []string{"core:app"},
			suffix:      "_build",
			expectedIDs: []string{"core:app_build"},
		},
		{
			name:        "partial target ID with suffix already",
			refs:        []string{"core:app_dev"},
			suffix:      "_build",
			expectedIDs: []string{"core:app_dev"},
		},
		{
			name:        "multiple refs mixed types",
			refs:        []string{"core", "api:server"},
			suffix:      "_build",
			expectedIDs: []string{"core:app_build", "api:server_build"},
		},
		{
			name:        "nonexistent bundle returns ref as-is",
			refs:        []string{"nonexistent"},
			suffix:      "_build",
			expectedIDs: []string{"nonexistent"},
		},
		{
			name:        "empty refs",
			refs:        []string{},
			suffix:      "_build",
			expectedIDs: []string{},
		},
		{
			name:        "nonexistent target ref kept as-is",
			refs:        []string{"core:nonexistent"},
			suffix:      "_build",
			expectedIDs: []string{"core:nonexistent"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved := s.ResolveTargetRefs(tt.refs, tt.suffix)
			assert.ElementsMatch(t, tt.expectedIDs, resolved)
		})
	}
}

func TestTargetNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		expected string
	}{
		{
			name:     "simple ID",
			id:       "core:app",
			expected: "dependency not found: core:app",
		},
		{
			name:     "empty ID",
			id:       "",
			expected: "dependency not found: ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &TargetNotFoundError{ID: tt.id}
			assert.Equal(t, tt.expected, err.Error())
		})
	}
}

func TestSimpleGlobMatch(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		path     string
		expected bool
	}{
		{
			name:     "match all go files",
			pattern:  "/repo/src/**/*.go",
			path:     "/repo/src/pkg/main.go",
			expected: true,
		},
		{
			name:     "no match wrong extension",
			pattern:  "/repo/src/**/*.go",
			path:     "/repo/src/pkg/main.py",
			expected: false,
		},
		{
			name:     "match with empty suffix",
			pattern:  "/repo/src/**",
			path:     "/repo/src/anything",
			expected: true,
		},
		{
			name:     "no match wrong prefix",
			pattern:  "/repo/src/**/*.go",
			path:     "/other/src/main.go",
			expected: false,
		},
		{
			name:     "match with slash suffix",
			pattern:  "/repo/src/**/",
			path:     "/repo/src/pkg/main.go",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := simpleGlobMatch(tt.pattern, tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}
