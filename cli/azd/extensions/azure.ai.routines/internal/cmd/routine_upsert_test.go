// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.routines/internal/exterrors"
	"azure.ai.routines/internal/pkg/routines"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

type routineUpsertClientStub struct {
	existing *routines.Routine
	getErr   error
	putErr   error
	getCalls int
	putCalls int
	putBody  *routines.Routine
}

func (s *routineUpsertClientStub) GetRoutine(
	_ context.Context,
	_ string,
) (*routines.Routine, error) {
	s.getCalls++
	return s.existing, s.getErr
}

func (s *routineUpsertClientStub) PutRoutine(
	_ context.Context,
	_ string,
	body *routines.Routine,
) (*routines.Routine, error) {
	s.putCalls++
	copy := *body
	s.putBody = &copy
	if s.putErr != nil {
		return nil, s.putErr
	}
	return &copy, nil
}

func fixedRoutineUpsertClientFactory(
	client routineUpsertClient,
) routineUpsertClientFactory {
	return func(context.Context) (routineUpsertClient, error) {
		return client, nil
	}
}

func writeRoutineManifest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "routine.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func routineWithDispatchIdentity(identity string) *routines.Routine {
	return &routines.Routine{
		Name: "nightly",
		Authorization: &routines.RoutineAuthorization{
			Identity: identity,
		},
	}
}

func TestRoutineCreateUpsertDispatchIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		manifest     string
		existing     *routines.Routine
		wantIdentity string
		wantError    bool
	}{
		{
			name:         "omitted identity preserves creator",
			manifest:     "description: updated\n",
			existing:     routineWithDispatchIdentity(routines.RoutineDispatchIdentityCreator),
			wantIdentity: routines.RoutineDispatchIdentityCreator,
		},
		{
			name: "unchanged identity is accepted",
			manifest: "description: updated\nauthorization:\n" +
				"  identity: creator\n",
			existing:     routineWithDispatchIdentity(routines.RoutineDispatchIdentityCreator),
			wantIdentity: routines.RoutineDispatchIdentityCreator,
		},
		{
			name: "different identity is rejected before PUT",
			manifest: "description: updated\nauthorization:\n" +
				"  identity: agent\n",
			existing:  routineWithDispatchIdentity(routines.RoutineDispatchIdentityCreator),
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			client := &routineUpsertClientStub{existing: test.existing}
			var output bytes.Buffer
			cmd := newRoutineCreateCommand(&azdext.ExtensionContext{})
			cmd.SetOut(&output)
			flags := &routineCreateFlags{
				name:   "nightly",
				file:   writeRoutineManifest(t, test.manifest),
				force:  true,
				output: "json",
			}

			err := runRoutineCreateWithClientFactory(
				t.Context(),
				cmd,
				flags,
				fixedRoutineUpsertClientFactory(client),
			)
			assert.Equal(t, 1, client.getCalls)

			if test.wantError {
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, exterrors.CodeConflictingArguments, localErr.Code)
				assert.Equal(t, 0, client.putCalls)
				return
			}

			require.NoError(t, err)
			require.Equal(t, 1, client.putCalls)
			require.NotNil(t, client.putBody.Authorization)
			assert.Equal(t, test.wantIdentity, client.putBody.Authorization.Identity)
			require.NotEmpty(t, output.String())
		})
	}
}

func TestRoutineCreateNewRoutineDispatchIdentity(t *testing.T) {
	t.Parallel()

	client := &routineUpsertClientStub{
		getErr: exterrors.ServiceFromStatus(
			404,
			exterrors.OpGetRoutine,
			"routine not found",
		),
	}
	var output bytes.Buffer
	cmd := newRoutineCreateCommand(&azdext.ExtensionContext{})
	cmd.SetOut(&output)
	require.NoError(t, cmd.Flags().Set(
		"dispatch-identity",
		routines.RoutineDispatchIdentityCreator,
	))
	flags := &routineCreateFlags{
		name:             "nightly",
		dispatchIdentity: routines.RoutineDispatchIdentityCreator,
		file: writeRoutineManifest(t, "description: new routine\n"+
			"triggers:\n"+
			"  default:\n"+
			"    type: schedule\n"+
			"    cron_expression: \"0 8 * * *\"\n"+
			"action:\n"+
			"  type: invoke_agent_responses_api\n"+
			"  agent_name: summarizer\n"),
		output: "json",
	}

	err := runRoutineCreateWithClientFactory(
		t.Context(),
		cmd,
		flags,
		fixedRoutineUpsertClientFactory(client),
	)
	require.NoError(t, err)
	assert.Equal(t, 1, client.getCalls)
	assert.Equal(t, 1, client.putCalls)
	require.NotNil(t, client.putBody.Authorization)
	assert.Equal(
		t,
		routines.RoutineDispatchIdentityCreator,
		client.putBody.Authorization.Identity,
	)

	var result routines.Routine
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.NotNil(t, result.Authorization)
	assert.Equal(t, routines.RoutineDispatchIdentityCreator, result.Authorization.Identity)
}

func TestRoutineCreateTableOutputIncludesDispatchIdentity(t *testing.T) {
	t.Parallel()

	client := &routineUpsertClientStub{
		getErr: exterrors.ServiceFromStatus(
			404,
			exterrors.OpGetRoutine,
			"routine not found",
		),
	}
	var output bytes.Buffer
	cmd := newRoutineCreateCommand(&azdext.ExtensionContext{})
	cmd.SetOut(&output)
	flags := &routineCreateFlags{
		name: "nightly",
		file: writeRoutineManifest(t, "description: new routine\n"+
			"authorization:\n"+
			"  identity: creator\n"+
			"triggers:\n"+
			"  default:\n"+
			"    type: schedule\n"+
			"    cron_expression: \"0 8 * * *\"\n"+
			"action:\n"+
			"  type: invoke_agent_responses_api\n"+
			"  agent_name: summarizer\n"),
		output: "table",
	}

	err := runRoutineCreateWithClientFactory(
		t.Context(),
		cmd,
		flags,
		fixedRoutineUpsertClientFactory(client),
	)
	require.NoError(t, err)
	assert.Contains(t, output.String(), "Routine 'nightly' created.")
	assert.Contains(t, output.String(), "Dispatch identity:")
	assert.Contains(t, output.String(), routines.RoutineDispatchIdentityCreator)
}

func TestRoutineUpdateManifestDispatchIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		manifest     string
		existing     *routines.Routine
		wantIdentity string
		wantError    bool
	}{
		{
			name:         "omitted identity preserves creator",
			manifest:     "description: updated\n",
			existing:     routineWithDispatchIdentity(routines.RoutineDispatchIdentityCreator),
			wantIdentity: routines.RoutineDispatchIdentityCreator,
		},
		{
			name: "unchanged identity is accepted",
			manifest: "description: updated\nauthorization:\n" +
				"  identity: creator\n",
			existing:     routineWithDispatchIdentity(routines.RoutineDispatchIdentityCreator),
			wantIdentity: routines.RoutineDispatchIdentityCreator,
		},
		{
			name: "different identity is rejected before PUT",
			manifest: "description: updated\nauthorization:\n" +
				"  identity: agent\n",
			existing:  routineWithDispatchIdentity(routines.RoutineDispatchIdentityCreator),
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			client := &routineUpsertClientStub{existing: test.existing}
			var output bytes.Buffer
			cmd := newRoutineUpdateCommand(&azdext.ExtensionContext{})
			cmd.SetOut(&output)
			flags := &routineUpdateFlags{
				name:   "nightly",
				file:   writeRoutineManifest(t, test.manifest),
				output: "json",
			}

			err := runRoutineUpdateWithClientFactory(
				t.Context(),
				cmd,
				flags,
				fixedRoutineUpsertClientFactory(client),
			)
			assert.Equal(t, 1, client.getCalls)

			if test.wantError {
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, exterrors.CodeConflictingArguments, localErr.Code)
				assert.Equal(t, 0, client.putCalls)
				return
			}

			require.NoError(t, err)
			require.Equal(t, 1, client.putCalls)
			require.NotNil(t, client.putBody.Authorization)
			assert.Equal(t, test.wantIdentity, client.putBody.Authorization.Identity)
			require.NotEmpty(t, output.String())
		})
	}
}

func TestRoutineUpdateTableOutputIncludesDispatchIdentity(t *testing.T) {
	t.Parallel()

	client := &routineUpsertClientStub{
		existing: &routines.Routine{
			Name:        "nightly",
			Description: "old description",
			Enabled:     new(true),
			Authorization: &routines.RoutineAuthorization{
				Identity: routines.RoutineDispatchIdentityCreator,
			},
		},
	}
	var output bytes.Buffer
	cmd := newRoutineUpdateCommand(&azdext.ExtensionContext{})
	cmd.SetOut(&output)
	flags := &routineUpdateFlags{
		name:   "nightly",
		file:   writeRoutineManifest(t, "description: updated description\n"),
		output: "table",
	}

	err := runRoutineUpdateWithClientFactory(
		t.Context(),
		cmd,
		flags,
		fixedRoutineUpsertClientFactory(client),
	)
	require.NoError(t, err)
	assert.Contains(t, output.String(), "Routine 'nightly' updated")
	assert.Contains(t, output.String(), "Dispatch identity:")
	assert.Contains(t, output.String(), routines.RoutineDispatchIdentityCreator)
}

func TestRoutineShowTableOutputIncludesDispatchIdentity(t *testing.T) {
	t.Parallel()

	client := &routineUpsertClientStub{
		existing: routineWithDispatchIdentity(
			routines.RoutineDispatchIdentityCreator,
		),
	}
	var output bytes.Buffer
	cmd := newRoutineShowCommand(&azdext.ExtensionContext{})
	cmd.SetOut(&output)

	err := runRoutineShowWithClientFactory(
		t.Context(),
		cmd,
		"nightly",
		"table",
		fixedRoutineUpsertClientFactory(client),
	)
	require.NoError(t, err)
	assert.Equal(t, 1, client.getCalls)
	assert.Contains(t, output.String(), "Dispatch identity:")
	assert.Contains(t, output.String(), routines.RoutineDispatchIdentityCreator)
}

func TestRoutineShowJSONOutputUsesCommandWriter(t *testing.T) {
	t.Parallel()

	client := &routineUpsertClientStub{
		existing: routineWithDispatchIdentity(
			routines.RoutineDispatchIdentityCreator,
		),
	}
	var output bytes.Buffer
	cmd := newRoutineShowCommand(&azdext.ExtensionContext{})
	cmd.SetOut(&output)

	err := runRoutineShowWithClientFactory(
		t.Context(),
		cmd,
		"nightly",
		"json",
		fixedRoutineUpsertClientFactory(client),
	)
	require.NoError(t, err)
	assert.Equal(t, 1, client.getCalls)

	var result routines.Routine
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	assert.Equal(t, "nightly", result.Name)
	require.NotNil(t, result.Authorization)
	assert.Equal(t, routines.RoutineDispatchIdentityCreator, result.Authorization.Identity)
}

func TestRoutineShowJSONOutputReturnsWriterError(t *testing.T) {
	t.Parallel()

	writeErr := errors.New("write failed")
	client := &routineUpsertClientStub{
		existing: routineWithDispatchIdentity(
			routines.RoutineDispatchIdentityCreator,
		),
	}
	cmd := newRoutineShowCommand(&azdext.ExtensionContext{})
	cmd.SetOut(routineSummaryFailingWriter{err: writeErr})

	err := runRoutineShowWithClientFactory(
		t.Context(),
		cmd,
		"nightly",
		"json",
		fixedRoutineUpsertClientFactory(client),
	)
	require.ErrorIs(t, err, writeErr)
	assert.Equal(t, 1, client.getCalls)
}

func TestRoutineServiceDeployDispatchIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		serviceAuth      map[string]any
		existingIdentity string
		wantIdentity     string
		wantError        bool
	}{
		{
			name:             "omitted identity preserves creator",
			existingIdentity: routines.RoutineDispatchIdentityCreator,
			wantIdentity:     routines.RoutineDispatchIdentityCreator,
		},
		{
			name: "unchanged identity is accepted",
			serviceAuth: map[string]any{
				"authorization": map[string]any{"identity": "creator"},
			},
			existingIdentity: routines.RoutineDispatchIdentityCreator,
			wantIdentity:     routines.RoutineDispatchIdentityCreator,
		},
		{
			name: "different identity is rejected before PUT",
			serviceAuth: map[string]any{
				"authorization": map[string]any{"identity": "creator"},
			},
			existingIdentity: routines.RoutineDispatchIdentityAgent,
			wantError:        true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			properties, err := structpb.NewStruct(test.serviceAuth)
			require.NoError(t, err)
			serviceConfig := &azdext.ServiceConfig{
				Name:                 "nightly",
				Host:                 aiRoutineHost,
				AdditionalProperties: properties,
			}
			client := &routineUpsertClientStub{}
			if test.existingIdentity != "" {
				client.existing = routineWithDispatchIdentity(test.existingIdentity)
			} else {
				client.existing = &routines.Routine{Name: "nightly"}
			}

			target := &routineServiceTarget{}
			_, err = target.deployWithClientFactory(
				t.Context(),
				serviceConfig,
				nil,
				nil,
				nil,
				fixedRoutineUpsertClientFactory(client),
			)
			assert.Equal(t, 1, client.getCalls)

			if test.wantError {
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, exterrors.CodeConflictingArguments, localErr.Code)
				assert.Equal(t, 0, client.putCalls)
				return
			}

			require.NoError(t, err)
			require.Equal(t, 1, client.putCalls)
			require.NotNil(t, client.putBody.Authorization)
			assert.Equal(t, test.wantIdentity, client.putBody.Authorization.Identity)
		})
	}
}
