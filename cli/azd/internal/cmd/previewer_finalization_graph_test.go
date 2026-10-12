// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/stretchr/testify/require"
)

func TestDeployCommandsCleanPreviewerBeforeFinalOutput(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("AZD_EXT_DEBUG", "false")
	t.Setenv("AZD_DEBUG", "false")

	for _, command := range []string{"deploy", "up"} {
		t.Run(command, func(t *testing.T) {
			previewer := &clearingPreviewerConsole{}
			fixture := newServiceEventMessageCommandFixture(
				t,
				command,
				serviceEventMessageFormatter(output.NoneFormat),
				serviceEventMessageProject,
				func(
					_ context.Context,
					eventName string,
					args *azdext.ServiceEventArgs,
				) (*azdext.BetaServiceEventResponse, error) {
					if args.Service.Name != "api" {
						return nil, nil
					}
					return serviceEventMessageResponse(eventName), nil
				},
				func(console input.Console, output *bytes.Buffer) input.Console {
					previewer.Console = console
					previewer.output = output
					return previewer
				},
			)

			_, err := fixture.run(t.Context())
			require.NoError(t, err)
			require.Equal(t, 1, previewer.resumeCount)

			text := serviceEventMessageOutputText(t, fixture.consoleOutput.String())
			table := strings.LastIndex(text, "\n  Service")
			postdeploy := strings.LastIndex(text, "api (postdeploy): after-deploy guidance")
			require.GreaterOrEqual(t, table, 0)
			require.Greater(t, postdeploy, table)
			require.Contains(t, text, "api (predeploy): before-deploy guidance")
		})
	}
}

type clearingPreviewerConsole struct {
	input.Console
	output      *bytes.Buffer
	paused      bool
	resumeCount int
}

func (c *clearingPreviewerConsole) PausePreviewer() {
	c.paused = true
}

func (c *clearingPreviewerConsole) ResumePreviewer() {
	if !c.paused {
		return
	}
	c.paused = false
	c.resumeCount++
	c.output.Reset()
}
