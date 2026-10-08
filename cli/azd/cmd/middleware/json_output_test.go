// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/stretchr/testify/require"
)

func TestJsonOutputMiddleware_WritesCommandBoundaryAndConsoleEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	eventWriter := output.NewJsonEventWriter(path)
	consoleOutput := &strings.Builder{}
	console := input.NewConsoleWithJsonEventWriter(
		true,
		false,
		input.Writers{Output: consoleOutput},
		input.ConsoleHandles{
			Stdin:  strings.NewReader(""),
			Stdout: consoleOutput,
			Stderr: io.Discard,
		},
		&output.NoneFormatter{},
		nil,
		eventWriter,
	)
	middleware := NewJsonOutputMiddleware(
		&Options{CommandPath: "azd deploy"},
		eventWriter,
		console,
	)

	_, err := middleware.Run(t.Context(), func(ctx context.Context) (*actions.ActionResult, error) {
		console.Message(ctx, "deploying api")
		return &actions.ActionResult{}, nil
	})
	require.NoError(t, err)

	file, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, file.Close())
	})

	var eventTypes []contracts.EventDataType
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event contracts.EventEnvelope
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &event))
		eventTypes = append(eventTypes, event.Type)
	}
	require.NoError(t, scanner.Err())
	require.Equal(t, []contracts.EventDataType{
		contracts.CommandStartEventDataType,
		contracts.ConsoleMessageEventDataType,
		contracts.CommandEndEventDataType,
	}, eventTypes)
	require.Equal(t, "deploying api\n", consoleOutput.String())
}

func TestJsonOutputMiddleware_InvalidPathDoesNotRunAction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "events.jsonl")
	eventWriter := output.NewJsonEventWriter(path)
	consoleOutput := &strings.Builder{}
	console := input.NewConsole(
		true,
		false,
		input.Writers{Output: consoleOutput},
		input.ConsoleHandles{
			Stdin:  strings.NewReader(""),
			Stdout: consoleOutput,
			Stderr: io.Discard,
		},
		&output.NoneFormatter{},
		nil,
	)
	middleware := NewJsonOutputMiddleware(
		&Options{CommandPath: "azd deploy"},
		eventWriter,
		console,
	)
	actionRan := false

	_, err := middleware.Run(t.Context(), func(context.Context) (*actions.ActionResult, error) {
		actionRan = true
		return &actions.ActionResult{}, nil
	})

	require.Error(t, err)
	require.False(t, actionRan)
	require.Contains(t, consoleOutput.String(), "opening JSON output file")
}
