// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"io"
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
		for _, outcome := range []string{"success", "failure"} {
			t.Run(command+"/"+outcome, func(t *testing.T) {
				previewer := &clearingPreviewerConsole{t: t}
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
						if _, err := previewer.PreviewerOutputWriter().Write([]byte("buffered hook output\n")); err != nil {
							return nil, err
						}
						return serviceEventMessageResponse(eventName), nil
					},
					func(console input.Console, output *bytes.Buffer) input.Console {
						previewer.Console = console
						previewer.output = output
						return previewer
					},
				)

				failure := errors.New("deployment failed")
				if outcome == "failure" {
					fixture.manager.deploy = func(_ context.Context, serviceName string) error {
						if serviceName == "web" {
							return failure
						}
						return nil
					}
				}
				_, err := fixture.run(t.Context())
				if outcome == "failure" {
					require.ErrorIs(t, err, failure)
				} else {
					require.NoError(t, err)
				}
				require.Equal(t, 1, previewer.resumeCount)

				text := serviceEventMessageOutputText(t, fixture.consoleOutput.String())
				table := strings.LastIndex(text, "\n  Service")
				postdeploy := strings.LastIndex(text, "api (postdeploy): after-deploy guidance")
				require.GreaterOrEqual(t, table, 0)
				require.Greater(t, postdeploy, table)
				require.Contains(t, text, "api (predeploy): before-deploy guidance")
				require.Greater(t, strings.Index(text, "buffered hook output"), table)
				require.Equal(t, 2, strings.Count(text, "buffered hook output"))
			})
		}
	}
}

type clearingPreviewerConsole struct {
	input.Console
	output      *bytes.Buffer
	paused      bool
	resumeCount int
	t           *testing.T
	buffer      bytes.Buffer
}

func (c *clearingPreviewerConsole) PreviewerOutputWriter() io.Writer {
	return &c.buffer
}

func (c *clearingPreviewerConsole) FlushPreviewerOutput() {
	_, err := c.buffer.WriteTo(c.output)
	require.NoError(c.t, err)
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
