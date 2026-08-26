// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"azure.ai.routines/internal/pkg/routines"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingRoutineCommandClient struct {
	events    *[]string
	existing  *routines.Routine
	putResult *routines.Routine
	putErr    error
}

func (c *recordingRoutineCommandClient) GetRoutine(
	context.Context,
	string,
) (*routines.Routine, error) {
	*c.events = append(*c.events, "get")
	return c.existing, nil
}

func (c *recordingRoutineCommandClient) PutRoutine(
	_ context.Context,
	_ string,
	_ *routines.Routine,
) (*routines.Routine, error) {
	*c.events = append(*c.events, "put")
	return c.putResult, c.putErr
}

type recordingRoutineProjectWriter struct {
	events   *[]string
	applyErr error
}

func (w *recordingRoutineProjectWriter) Prepare(
	context.Context,
	*routines.Routine,
) error {
	*w.events = append(*w.events, "prepare")
	return nil
}

func (w *recordingRoutineProjectWriter) Apply(context.Context) error {
	*w.events = append(*w.events, "apply")
	return w.applyErr
}

func (w *recordingRoutineProjectWriter) Close() {
	*w.events = append(*w.events, "close")
}

func commandDependencies(
	client routineCommandClient,
	writer routineProjectWriter,
	events *[]string,
) routineCommandDependencies {
	return routineCommandDependencies{
		newClient: func(context.Context, *cobra.Command) (routineCommandClient, error) {
			*events = append(*events, "client")
			return client, nil
		},
		newProjectWriter: func(context.Context) (routineProjectWriter, error) {
			*events = append(*events, "writer")
			return writer, nil
		},
	}
}

func createFlagsForProjectAuthoring() *routineCreateFlags {
	return &routineCreateFlags{
		name:           "nightly-summary",
		trigger:        "recurring",
		cronExpression: "0 2 * * *",
		action:         "agent-response",
		agentName:      "summarizer",
		enabled:        true,
		force:          true,
		addToProject:   true,
	}
}

func TestRunRoutineCreateRemoteFailureDoesNotWriteProject(t *testing.T) {
	t.Parallel()

	events := []string{}
	client := &recordingRoutineCommandClient{events: &events, putErr: assert.AnError}
	writer := &recordingRoutineProjectWriter{events: &events}
	cmd := newRoutineCreateCommand(&azdext.ExtensionContext{})

	err := runRoutineCreateWithDependencies(
		t.Context(), cmd, createFlagsForProjectAuthoring(),
		commandDependencies(client, writer, &events),
	)
	require.Error(t, err)
	assert.Equal(t, []string{"writer", "prepare", "client", "put", "close"}, events)
}

func TestRunRoutineCreateProjectFailureReportsPartialSuccess(t *testing.T) {
	t.Parallel()

	events := []string{}
	created := &routines.Routine{Name: "nightly-summary"}
	client := &recordingRoutineCommandClient{events: &events, putResult: created}
	writer := &recordingRoutineProjectWriter{events: &events, applyErr: assert.AnError}
	cmd := newRoutineCreateCommand(&azdext.ExtensionContext{})

	err := runRoutineCreateWithDependencies(
		t.Context(), cmd, createFlagsForProjectAuthoring(),
		commandDependencies(client, writer, &events),
	)
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Contains(t, localErr.Message, "was created but could not be added")
	assert.Equal(t, []string{"writer", "prepare", "client", "put", "apply", "close"}, events)
}

func TestRunRoutineUpdateNoChangesAuthorsProjectOnly(t *testing.T) {
	t.Parallel()

	events := []string{}
	existing := &routines.Routine{Name: "nightly-summary"}
	client := &recordingRoutineCommandClient{events: &events, existing: existing}
	writer := &recordingRoutineProjectWriter{events: &events, applyErr: assert.AnError}
	cmd := newRoutineUpdateCommand(&azdext.ExtensionContext{})
	flags := &routineUpdateFlags{name: "nightly-summary", addToProject: true}

	err := runRoutineUpdateWithDependencies(
		t.Context(), cmd, flags,
		commandDependencies(client, writer, &events),
	)
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Contains(t, localErr.Message, "was not changed and could not be added")
	assert.Equal(t, []string{"writer", "client", "get", "prepare", "apply", "close"}, events)
}

func TestRunRoutineUpdateRemoteFailureDoesNotWriteProject(t *testing.T) {
	t.Parallel()

	events := []string{}
	existing := &routines.Routine{Name: "nightly-summary"}
	client := &recordingRoutineCommandClient{
		events: &events, existing: existing, putErr: assert.AnError,
	}
	writer := &recordingRoutineProjectWriter{events: &events}
	cmd := newRoutineUpdateCommand(&azdext.ExtensionContext{})
	require.NoError(t, cmd.Flags().Set("description", "updated"))
	flags := &routineUpdateFlags{
		name: "nightly-summary", description: "updated", addToProject: true,
	}

	err := runRoutineUpdateWithDependencies(
		t.Context(), cmd, flags,
		commandDependencies(client, writer, &events),
	)
	require.Error(t, err)
	assert.Equal(t, []string{"writer", "client", "get", "prepare", "put", "close"}, events)
}

func TestRunRoutineUpdateAppliesProjectAfterRemoteSuccess(t *testing.T) {
	t.Parallel()

	events := []string{}
	existing := &routines.Routine{Name: "nightly-summary"}
	updated := &routines.Routine{Name: "nightly-summary", Description: "updated"}
	client := &recordingRoutineCommandClient{
		events: &events, existing: existing, putResult: updated,
	}
	writer := &recordingRoutineProjectWriter{events: &events, applyErr: assert.AnError}
	cmd := newRoutineUpdateCommand(&azdext.ExtensionContext{})
	require.NoError(t, cmd.Flags().Set("description", "updated"))
	flags := &routineUpdateFlags{
		name: "nightly-summary", description: "updated", addToProject: true,
	}

	err := runRoutineUpdateWithDependencies(
		t.Context(), cmd, flags,
		commandDependencies(client, writer, &events),
	)
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Contains(t, localErr.Message, "was updated but could not be added")
	assert.Equal(t, []string{"writer", "client", "get", "prepare", "put", "apply", "close"}, events)
}
