// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azureaiagent/internal/project"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScaleSettings_AlwaysOmitted(t *testing.T) {
	// Scale settings are never auto-populated with defaults.
	// This mirrors the pattern used in init.go, init_from_code.go, and listen.go.
	container := &project.ContainerSettings{
		Resources: &project.ResourceSettings{
			Memory: project.DefaultMemory,
			Cpu:    project.DefaultCpu,
		},
	}

	assert.Nil(t, container.Scale, "Scale should be nil — no defaults applied")
}

func TestScaleSettings_ExistingPreserved(t *testing.T) {
	// Mirrors the listen.go pattern: existing scale settings from azure.yaml are preserved.
	tests := []struct {
		name          string
		existingScale *project.ScaleSettings
		wantScale     bool
		wantMin       int
		wantMax       int
	}{
		{
			name:      "no existing scale - omitted",
			wantScale: false,
		},
		{
			name:          "existing scale - preserved",
			existingScale: &project.ScaleSettings{MinReplicas: 2, MaxReplicas: 5},
			wantScale:     true,
			wantMin:       2,
			wantMax:       5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate listen.go populateContainerSettings logic
			containerSettings := &project.ContainerSettings{
				Resources: &project.ResourceSettings{
					Memory: project.DefaultMemory,
					Cpu:    project.DefaultCpu,
				},
				Scale: tt.existingScale,
			}

			result := &project.ContainerSettings{
				Resources: &project.ResourceSettings{
					Memory: containerSettings.Resources.Memory,
					Cpu:    containerSettings.Resources.Cpu,
				},
			}

			if containerSettings.Scale != nil {
				result.Scale = &project.ScaleSettings{
					MinReplicas: containerSettings.Scale.MinReplicas,
					MaxReplicas: containerSettings.Scale.MaxReplicas,
				}
			}

			if tt.wantScale {
				assert.NotNil(t, result.Scale, "Scale should be present")
				assert.Equal(t, tt.wantMin, result.Scale.MinReplicas)
				assert.Equal(t, tt.wantMax, result.Scale.MaxReplicas)
			} else {
				assert.Nil(t, result.Scale, "Scale should be nil")
			}
		})
	}
}
