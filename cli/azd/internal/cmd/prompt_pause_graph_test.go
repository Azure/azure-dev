// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd_test

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/stretchr/testify/require"
)

const singleServiceProject = `name: test-project
services:
  web:
    project: web
    language: js
    host: containerapp
`

var eraseTableRe = regexp.MustCompile(`\x1b\[\d+A\x1b\[J$`)

// Regression test for https://github.com/Azure/azure-dev/issues/10252: a prompt shown while
// a service deploys must pause the live progress table in both `azd deploy` and `azd up`.
func TestDeployCommandsPauseProgressTableDuringPrompt(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	for _, command := range []string{"deploy", "up"} {
		t.Run(command, func(t *testing.T) {
			fixture := newServiceEventMessageCommandFixture(
				t, command, serviceEventMessageFormatter(output.NoneFormat), singleServiceProject, nil,
			)

			var beforePrompt, duringPrompt, afterPrompt string
			fixture.manager.deploy = func(context.Context, string) error {
				// Let the ticker draw the table at least once.
				time.Sleep(1500 * time.Millisecond)

				// Beginning the interaction waits for the tracker's lock, which orders the
				// buffer reads below after any write made by the ticker goroutine.
				endInteraction := input.BeginInteraction()
				beforePrompt = fixture.consoleOutput.String()

				// Hold the prompt open across another tick.
				time.Sleep(1500 * time.Millisecond)
				duringPrompt = fixture.consoleOutput.String()

				endInteraction()
				afterPrompt = fixture.consoleOutput.String()
				return nil
			}

			_, err := fixture.run(t.Context())
			require.NoError(t, err)

			require.Contains(t, beforePrompt, "Service", "the table must render before the prompt")
			require.Regexp(t, eraseTableRe, beforePrompt, "the table must be erased when the prompt starts")
			require.Equal(t, beforePrompt, duringPrompt, "the table must not redraw while the prompt is open")
			resumed := strings.TrimPrefix(afterPrompt, duringPrompt)
			require.True(t, strings.HasPrefix(resumed, "\r\n"), "the table must resume on a new row")
			require.Contains(t, resumed, "Service", "the table must redraw after the prompt")
		})
	}
}
