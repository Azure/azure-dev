package commands

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestBasicBuild(t *testing.T) {
	testAction := ActionFunc(
		func(context.Context, *cobra.Command, []string, *azdcontext.AzdContext) error {
			return nil
		},
	)

	rootOptions := &internal.GlobalCommandOptions{
		EnvironmentName:    "test",
		EnableDebugLogging: false,
		EnableTelemetry:    true,
	}

	cmd := Build(
		testAction,
		BuildOptions{
			GlobalOptions: rootOptions,
			Use:           "test2",
			Short:         "This is a test of the builder",
			Long:          "lorem",
		})

	assert.Equal(t, cmd.Short, "This is a test of the builder")
	assert.Equal(t, cmd.Long, "lorem")
	assert.Equal(t, cmd.Use, "test2")
}
