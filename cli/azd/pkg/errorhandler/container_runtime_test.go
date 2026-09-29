// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/stretchr/testify/require"
)

func TestContainerRuntimeUnavailableRule(t *testing.T) {
	for _, tt := range []struct {
		name   string
		engine tools.ContainerEngine
	}{
		{name: "Docker", engine: tools.ContainerEngineDocker},
		{name: "Podman", engine: tools.ContainerEnginePodman},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := fmt.Errorf("checking project tools: %w", &tools.MissingToolErrors{
				ToolNames: []string{tt.name},
				Errs: []error{fmt.Errorf("checking external tool: %w", &docker.ContainerEngineUnavailableError{
					Engine: tt.engine, Err: errors.New("permission denied"),
				})},
			})

			result := errorhandler.NewErrorHandlerPipeline(nil).Process(t.Context(), err)
			require.NotNil(t, result)
			require.Equal(t, "The container runtime is unavailable.", result.Message)
			require.Contains(t, result.Suggestion, "running and accessible")
			require.NotContains(t, result.Message, "not installed")
			require.Len(t, result.Links, 2)
			require.Same(t, err, result.Err)
		})
	}
}
