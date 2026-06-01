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
			{Name: "serve", BundleName: "core"},
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
			name:       "existing target serve",
			targetName: "serve",
			wantFound:  true,
			wantName:   "serve",
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
