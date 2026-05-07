// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package repository

import (
	"bytes"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

func newCapturedTestConsole(t *testing.T, interactions []string) input.Console {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	t.Cleanup(func() {
		if !t.Failed() {
			return
		}

		if stdout.Len() > 0 {
			t.Logf("console stdout:\n%s", stdout.String())
		}
		if stderr.Len() > 0 {
			t.Logf("console stderr:\n%s", stderr.String())
		}
	})

	return input.NewConsole(
		false,
		false,
		input.Writers{Output: &stdout},
		input.ConsoleHandles{
			Stderr: &stderr,
			Stdin:  strings.NewReader(strings.Join(interactions, "\n") + "\n"),
			Stdout: &stdout,
		},
		nil,
		nil,
	)
}
