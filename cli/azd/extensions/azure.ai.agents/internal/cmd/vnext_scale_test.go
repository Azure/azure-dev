// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azureaiagent/internal/project"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsVNextEnabled(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     bool
	}{
		{name: "enabled with true", envValue: "true", want: true},
		{name: "enabled with 1", envValue: "1", want: true},
		{name: "enabled with TRUE", envValue: "TRUE", want: true},
		{name: "disabled with false", envValue: "false", want: false},
		{name: "disabled with 0", envValue: "0", want: false},
		{name: "invalid value falls back", envValue: "notabool", want: false},
		{name: "unset falls back", envValue: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv("enableHostedAgentVNext", tt.envValue)
			}

			got := isVNextEnabled(t.Context())
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestVNextConditionalScale(t *testing.T) {
	tests := []struct {
		name      string
		envValue  string
		wantScale bool
	}{
		{
			name:      "vnext disabled - scale defaults applied",
			envValue:  "",
			wantScale: true,
		},
		{
			name:      "vnext enabled - scale omitted",
			envValue:  "true",
			wantScale: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv("enableHostedAgentVNext", tt.envValue)
			}

			// Mirrors the pattern used in init.go, init_from_code.go, and listen.go
			container := &project.ContainerSettings{
				Resources: &project.ResourceSettings{
					Memory: project.DefaultMemory,
					Cpu:    project.DefaultCpu,
				},
			}

			if !isVNextEnabled(t.Context()) {
				container.Scale = &project.ScaleSettings{
					MinReplicas: project.DefaultMinReplicas,
					MaxReplicas: project.DefaultMaxReplicas,
				}
			}

			if tt.wantScale {
				assert.NotNil(t, container.Scale, "Scale should be set when vnext is disabled")
				assert.Equal(t, project.DefaultMinReplicas, container.Scale.MinReplicas)
				assert.Equal(t, project.DefaultMaxReplicas, container.Scale.MaxReplicas)
			} else {
				assert.Nil(t, container.Scale, "Scale should be nil when vnext is enabled")
			}
		})
	}
}

func TestVNextPreservesExistingScale(t *testing.T) {
	// Mirrors the listen.go pattern: existing scale settings are always preserved,
	// regardless of vnext status.
	tests := []struct {
		name          string
		envValue      string
		existingScale *project.ScaleSettings
		wantScale     bool
		wantMin       int
		wantMax       int
	}{
		{
			name:      "vnext enabled, no existing scale - omitted",
			envValue:  "true",
			wantScale: false,
		},
		{
			name:          "vnext enabled, existing scale - preserved",
			envValue:      "true",
			existingScale: &project.ScaleSettings{MinReplicas: 2, MaxReplicas: 5},
			wantScale:     true,
			wantMin:       2,
			wantMax:       5,
		},
		{
			name:      "vnext disabled, no existing scale - defaults applied",
			envValue:  "",
			wantScale: true,
			wantMin:   project.DefaultMinReplicas,
			wantMax:   project.DefaultMaxReplicas,
		},
		{
			name:          "vnext disabled, existing scale - preserved",
			envValue:      "",
			existingScale: &project.ScaleSettings{MinReplicas: 3, MaxReplicas: 10},
			wantScale:     true,
			wantMin:       3,
			wantMax:       10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv("enableHostedAgentVNext", tt.envValue)
			}

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
			} else if !isVNextEnabled(t.Context()) {
				result.Scale = &project.ScaleSettings{
					MinReplicas: project.DefaultMinReplicas,
					MaxReplicas: project.DefaultMaxReplicas,
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
