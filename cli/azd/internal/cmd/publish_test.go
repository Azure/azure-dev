// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
)

func TestValidateImagePassthroughPublishFlags(t *testing.T) {
	passthroughService := &project.ServiceConfig{
		Name: "api",
		Docker: project.DockerProjectOptions{
			ImagePassthrough: true,
		},
	}
	normalService := &project.ServiceConfig{Name: "web"}

	tests := []struct {
		name          string
		services      []*project.ServiceConfig
		flags         *PublishFlags
		errorContains string
	}{
		{
			name:     "passthrough without overrides",
			services: []*project.ServiceConfig{passthroughService},
			flags:    &PublishFlags{},
		},
		{
			name:     "normal service with from package",
			services: []*project.ServiceConfig{normalService},
			flags:    &PublishFlags{FromPackage: "api:v1"},
		},
		{
			name:          "passthrough with from package",
			services:      []*project.ServiceConfig{passthroughService},
			flags:         &PublishFlags{FromPackage: "registry.example.com/team/api:v2"},
			errorContains: "--from-package is not supported by azd publish",
		},
		{
			name:          "passthrough with destination override",
			services:      []*project.ServiceConfig{passthroughService},
			flags:         &PublishFlags{To: "registry.example.com/team/api:v2"},
			errorContains: "--to is not supported by azd publish",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateImagePassthroughPublishFlags(tt.services, tt.flags)
			if tt.errorContains == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.errorContains)
			}
		})
	}
}
