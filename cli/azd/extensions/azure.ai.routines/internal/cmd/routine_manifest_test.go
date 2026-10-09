// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.routines/internal/exterrors"
	"azure.ai.routines/internal/pkg/routines"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── readRoutineManifest ──────────────────────────────────────────────────────

func TestReadRoutineManifest_JSON(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Name:          "test-routine",
		Description:   "a test routine",
		Authorization: &routines.RoutineAuthorization{Identity: routines.RoutineDispatchIdentityCreator},
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "schedule", CronExpression: "0 8 * * 1-5"},
		},
		Action: &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentName: "my-agent-name"},
	}
	data, err := json.Marshal(r)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "routine.json")
	require.NoError(t, os.WriteFile(path, data, 0600))

	got, err := readRoutineManifest(path)
	require.NoError(t, err)
	assert.Equal(t, "test-routine", got.Name)
	assert.Equal(t, "a test routine", got.Description)
	require.NotNil(t, got.Authorization)
	assert.Equal(t, routines.RoutineDispatchIdentityCreator, got.Authorization.Identity)
	assert.Equal(t, "schedule", got.Triggers["default"].Type)
	assert.Equal(t, "0 8 * * 1-5", got.Triggers["default"].CronExpression)
	require.NotNil(t, got.Action)
	assert.Equal(t, "my-agent-name", got.Action.AgentName)
}

func TestReadRoutineManifest_YAML(t *testing.T) {
	t.Parallel()
	yaml := `name: yaml-routine
description: yaml desc
authorization:
  identity: creator
triggers:
  default:
    type: timer
    at: "2026-01-01T00:00:00Z"
action:
  type: invoke_agent_responses_api
  agentName: yaml-agent-name
`
	path := filepath.Join(t.TempDir(), "routine.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0600))

	got, err := readRoutineManifest(path)
	require.NoError(t, err)
	assert.Equal(t, "yaml-routine", got.Name)
	require.NotNil(t, got.Authorization)
	assert.Equal(t, routines.RoutineDispatchIdentityCreator, got.Authorization.Identity)
	assert.Equal(t, "timer", got.Triggers["default"].Type)
	require.NotNil(t, got.Action)
	assert.Equal(t, "yaml-agent-name", got.Action.AgentName)
}

func TestReadRoutineManifest_InvalidAuthorizationIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		file    string
		content string
	}{
		{
			name:    "unsupported JSON identity",
			file:    "routine.json",
			content: `{"authorization":{"identity":"service"}}`,
		},
		{
			name:    "empty YAML identity",
			file:    "routine.yaml",
			content: "authorization:\n  identity: \"\"\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), test.file)
			require.NoError(t, os.WriteFile(path, []byte(test.content), 0o600))

			_, err := readRoutineManifest(path)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, exterrors.CodeInvalidRoutineManifest, localErr.Code)
			assert.Contains(t, localErr.Message, "authorization.identity")
			assert.Contains(t, localErr.Suggestion, "agent or creator")
		})
	}
}

func TestReadRoutineManifest_RejectsSnakeCaseJSONAndYAML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		extension string
		contents  string
		wantKey   string
		wantNew   string
	}{
		{
			name: "json", extension: ".json",
			contents: `{"action":{"type":"invoke_agent_responses_api","agent_name":"agent"}}`,
			wantKey:  "agent_name", wantNew: "agentName",
		},
		{
			name: "yaml", extension: ".yaml",
			contents: "triggers:\n  default:\n    type: schedule\n    cron_expression: \"0 9 * * *\"\n",
			wantKey:  "cron_expression", wantNew: "cronExpression",
		},
		{
			name: "yaml numeric trigger", extension: ".yaml",
			contents: "triggers:\n  1:\n    type: schedule\n    cron_expression: \"0 9 * * *\"\n",
			wantKey:  "cron_expression", wantNew: "cronExpression",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "routine"+test.extension)
			require.NoError(t, os.WriteFile(path, []byte(test.contents), 0o600))

			_, err := readRoutineManifest(path)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, exterrors.CodeInvalidRoutineManifest, localErr.Code)
			assert.Contains(t, localErr.Message, test.wantKey)
			assert.Contains(t, localErr.Message, test.wantNew)
		})
	}
}

func TestReadRoutineManifest_FileNotFound(t *testing.T) {
	t.Parallel()
	_, err := readRoutineManifest("/nonexistent/path/routine.yaml")
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Contains(t, localErr.Suggestion, "verify the path exists")
	assert.NotContains(t, localErr.Suggestion, "--file")
}

func TestReadRoutineManifest_UnsupportedExtension(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "routine.toml")
	require.NoError(t, os.WriteFile(path, []byte("name = 'x'"), 0600))

	_, err := readRoutineManifest(path)
	assert.Error(t, err)
}

// ─── mergeRoutineFromFile ─────────────────────────────────────────────────────

func TestMergeRoutineFromFile_FileFieldsMergedWhenBodyEmpty(t *testing.T) {
	t.Parallel()
	body := &routines.Routine{Name: "from-cli"}
	file := &routines.Routine{
		Description:   "from file",
		Authorization: &routines.RoutineAuthorization{Identity: routines.RoutineDispatchIdentityCreator},
		Triggers:      map[string]routines.RoutineTrigger{"default": {Type: "schedule", CronExpression: "* * * * *"}},
		Action:        &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentName: "a"},
	}
	mergeRoutineFromFile(body, file)

	assert.Equal(t, "from-cli", body.Name, "name must not be overwritten by file")
	assert.Equal(t, "from file", body.Description)
	require.NotNil(t, body.Authorization)
	assert.Equal(t, routines.RoutineDispatchIdentityCreator, body.Authorization.Identity)
	assert.Equal(t, "schedule", body.Triggers["default"].Type)
	require.NotNil(t, body.Action)
	assert.Equal(t, "a", body.Action.AgentName)
}

func TestMergeRoutineFromFile_BodyFieldsWinOverFile(t *testing.T) {
	t.Parallel()
	body := &routines.Routine{
		Name:          "from-cli",
		Description:   "cli description",
		Enabled:       new(true),
		Authorization: &routines.RoutineAuthorization{Identity: routines.RoutineDispatchIdentityAgent},
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "timer", At: "2026-01-01T00:00:00Z"},
		},
		Action: &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentName: "cli-agent"},
	}
	file := &routines.Routine{
		Description:   "file description",
		Authorization: &routines.RoutineAuthorization{Identity: routines.RoutineDispatchIdentityCreator},
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "schedule", CronExpression: "* * * * *"},
		},
		Action: &routines.RoutineAction{Type: "invoke_agent_invocations_api", AgentEndpointID: "ep"},
	}
	mergeRoutineFromFile(body, file)

	assert.Equal(t, "cli description", body.Description, "body description must win")
	require.NotNil(t, body.Authorization)
	assert.Equal(t, routines.RoutineDispatchIdentityAgent, body.Authorization.Identity)
	assert.Equal(t, "timer", body.Triggers["default"].Type, "body trigger must win")
	require.NotNil(t, body.Action)
	assert.Equal(t, "cli-agent", body.Action.AgentName, "body action must win")
}

// ─── overwriteRoutineFromFile ──────────────────────────────────────────────────

func TestOverwriteRoutineFromFile_ManifestWins(t *testing.T) {
	t.Parallel()
	existing := &routines.Routine{
		Name:        "fetched-name",
		Description: "old description",
		Enabled:     new(true),
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "timer", At: "2026-01-01T00:00:00Z"},
		},
		Action: &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentName: "old-agent"},
	}
	file := &routines.Routine{
		Description: "new description from manifest",
		Enabled:     new(false),
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "schedule", CronExpression: "0 9 * * *"},
		},
		Action: &routines.RoutineAction{Type: "invoke_agent_invocations_api", AgentEndpointID: "new-ep"},
	}
	n := overwriteRoutineFromFile(existing, file)

	assert.Equal(t, 4, n, "all four fields should be counted as changed")
	assert.Equal(t, "fetched-name", existing.Name, "name must not be overwritten by file")
	assert.Equal(t, "new description from manifest", existing.Description)
	require.NotNil(t, existing.Enabled)
	assert.False(t, *existing.Enabled)
	assert.Equal(t, "schedule", existing.Triggers["default"].Type)
	require.NotNil(t, existing.Action)
	assert.Equal(t, "invoke_agent_invocations_api", existing.Action.Type)
}

func TestOverwriteRoutineFromFile_EmptyManifestChangesNothing(t *testing.T) {
	t.Parallel()
	existing := &routines.Routine{
		Name:        "my-routine",
		Description: "keep this",
	}
	n := overwriteRoutineFromFile(existing, &routines.Routine{})
	assert.Equal(t, 0, n)
	assert.Equal(t, "keep this", existing.Description)
}

func TestRoutineAuthorizationForUpsert(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		existing     *routines.Routine
		requested    *routines.RoutineAuthorization
		wantIdentity string
		wantNilAuth  bool
		wantError    bool
	}{
		{
			name: "new routine keeps requested creator identity",
			requested: &routines.RoutineAuthorization{
				Identity: routines.RoutineDispatchIdentityCreator,
			},
			wantIdentity: routines.RoutineDispatchIdentityCreator,
		},
		{
			name: "existing identity is retained when omitted",
			existing: &routines.Routine{
				Authorization: &routines.RoutineAuthorization{
					Identity: routines.RoutineDispatchIdentityCreator,
				},
			},
			wantIdentity: routines.RoutineDispatchIdentityCreator,
		},
		{
			name: "same existing identity is accepted",
			existing: &routines.Routine{
				Authorization: &routines.RoutineAuthorization{
					Identity: routines.RoutineDispatchIdentityCreator,
				},
			},
			requested: &routines.RoutineAuthorization{
				Identity: routines.RoutineDispatchIdentityCreator,
			},
			wantIdentity: routines.RoutineDispatchIdentityCreator,
		},
		{
			name:        "agent is default when stored authorization is omitted",
			existing:    &routines.Routine{},
			requested:   &routines.RoutineAuthorization{Identity: routines.RoutineDispatchIdentityAgent},
			wantNilAuth: true,
		},
		{
			name: "empty stored authorization is normalized to the agent default",
			existing: &routines.Routine{
				Authorization: &routines.RoutineAuthorization{},
			},
			wantNilAuth: true,
		},
		{
			name:      "creator cannot replace default agent identity",
			existing:  &routines.Routine{},
			requested: &routines.RoutineAuthorization{Identity: routines.RoutineDispatchIdentityCreator},
			wantError: true,
		},
		{
			name: "agent cannot replace creator identity",
			existing: &routines.Routine{
				Authorization: &routines.RoutineAuthorization{
					Identity: routines.RoutineDispatchIdentityCreator,
				},
			},
			requested: &routines.RoutineAuthorization{
				Identity: routines.RoutineDispatchIdentityAgent,
			},
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := routineAuthorizationForUpsert("my-routine", test.existing, test.requested)
			if test.wantError {
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, exterrors.CodeConflictingArguments, localErr.Code)
				assert.Contains(t, localErr.Message, "cannot be changed")
				assert.Contains(t, localErr.Suggestion, "azd ai routine delete")
				assert.Contains(t, localErr.Suggestion, "recreate it")
				return
			}

			require.NoError(t, err)
			if test.wantNilAuth {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, test.wantIdentity, got.Identity)
		})
	}
}

func TestRoutineAuthorizationForUpsertSuggestionOmitsRoutineName(t *testing.T) {
	t.Parallel()

	names := []string{
		"routine$()",
		"routine`backtick`",
		`routine"quoted`,
		"routine'quoted",
		"routine with spaces",
		"routine%VAR%",
	}
	var expectedSuggestion string
	for i, name := range names {
		t.Run(fmt.Sprintf("hostile-name-%d", i), func(t *testing.T) {
			_, err := routineAuthorizationForUpsert(
				name,
				&routines.Routine{},
				&routines.RoutineAuthorization{
					Identity: routines.RoutineDispatchIdentityCreator,
				},
			)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, exterrors.CodeConflictingArguments, localErr.Code)
			require.Contains(t, localErr.Message, fmt.Sprintf("%q", name))
			require.Contains(t, localErr.Suggestion, "azd ai routine delete")
			require.Contains(t, localErr.Suggestion, "this routine's name")
			require.NotContains(t, localErr.Suggestion, name)

			if i == 0 {
				expectedSuggestion = localErr.Suggestion
				return
			}
			assert.Equal(t, expectedSuggestion, localErr.Suggestion)
		})
	}
}

func routineWithScheduleAndAgentResp() *routines.Routine {
	return &routines.Routine{
		Name:        "my-routine",
		Description: "old desc",
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "schedule", CronExpression: "0 8 * * *", TimeZone: "UTC"},
		},
		Action: &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentName: "old-agent-name"},
	}
}

func TestApplyUpdateFlags_Description(t *testing.T) {
	t.Parallel()
	r := routineWithScheduleAndAgentResp()
	n, err := applyUpdateFlags(r, routineUpdateChanges{
		description: "new desc", descChanged: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, "new desc", r.Description)
}

func TestApplyUpdateFlags_TimeZone(t *testing.T) {
	t.Parallel()
	r := routineWithScheduleAndAgentResp()
	n, err := applyUpdateFlags(r, routineUpdateChanges{
		timeZone: "America/New_York", tzChanged: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, "America/New_York", r.Triggers["default"].TimeZone)
}

func TestApplyUpdateFlags_AgentNameClearsEndpointID(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Action:   &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentEndpointID: "old-ep"},
		Triggers: map[string]routines.RoutineTrigger{"default": {Type: "schedule"}},
	}
	n, err := applyUpdateFlags(r, routineUpdateChanges{
		agentName: "new-agent-name", agentNameChanged: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.NotNil(t, r.Action)
	assert.Equal(t, "new-agent-name", r.Action.AgentName)
	assert.Empty(t, r.Action.AgentEndpointID, "setting agent-name should clear agent-endpoint-id")
}

func TestApplyUpdateFlags_AgentEndpointIDClearsID(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Action:   &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentName: "old-agent-name"},
		Triggers: map[string]routines.RoutineTrigger{"default": {Type: "schedule"}},
	}
	n, err := applyUpdateFlags(r, routineUpdateChanges{
		agentEndpointID: "new-ep", agentEpChanged: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.NotNil(t, r.Action)
	assert.Equal(t, "new-ep", r.Action.AgentEndpointID)
	assert.Empty(t, r.Action.AgentName, "setting agent-endpoint-id should clear agent-name")
}

func TestApplyUpdateFlags_MutuallyExclusiveAgentFields(t *testing.T) {
	t.Parallel()
	r := routineWithScheduleAndAgentResp()
	_, err := applyUpdateFlags(r, routineUpdateChanges{
		agentName: "new-agent-name", agentEndpointID: "new-ep",
		agentNameChanged: true, agentEpChanged: true,
	})
	assert.Error(t, err)
}

func TestApplyUpdateFlags_NoChangesReturnsZero(t *testing.T) {
	t.Parallel()
	r := routineWithScheduleAndAgentResp()
	n, err := applyUpdateFlags(r, routineUpdateChanges{})
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

// Spec PR #43498: agent-invoke now accepts agent_name. The pre-PR test that
// rejected this is replaced by one that allows it.
func TestApplyUpdateFlags_AgentNameAllowedForAgentInvoke(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Action:   &routines.RoutineAction{Type: "invoke_agent_invocations_api", AgentEndpointID: "ep"},
		Triggers: map[string]routines.RoutineTrigger{"default": {Type: "timer"}},
	}
	n, err := applyUpdateFlags(r, routineUpdateChanges{
		agentName: "new-agent-name", agentNameChanged: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.NotNil(t, r.Action)
	assert.Equal(t, "new-agent-name", r.Action.AgentName)
	assert.Empty(t, r.Action.AgentEndpointID)
}

func TestApplyUpdateFlags_ConvIDRejectedForAgentInvoke(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Action:   &routines.RoutineAction{Type: "invoke_agent_invocations_api", AgentEndpointID: "ep"},
		Triggers: map[string]routines.RoutineTrigger{"default": {Type: "timer"}},
	}
	_, err := applyUpdateFlags(r, routineUpdateChanges{
		conversationID: "conv-1", convIDChanged: true,
	})
	assert.Error(t, err, "--conversation-id must be rejected for agent-invoke actions")
}

func TestApplyUpdateFlags_SessIDRejectedForAgentResponse(t *testing.T) {
	t.Parallel()
	r := routineWithScheduleAndAgentResp()
	_, err := applyUpdateFlags(r, routineUpdateChanges{
		sessionID: "sess-1", sessIDChanged: true,
	})
	assert.Error(t, err, "--session-id must be rejected for agent-response actions")
}

func TestApplyUpdateFlags_TimeZoneRejectedForTimer(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Action:   &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentName: "a"},
		Triggers: map[string]routines.RoutineTrigger{"default": {Type: "timer", At: "2026-01-01T00:00:00Z"}},
	}
	_, err := applyUpdateFlags(r, routineUpdateChanges{
		timeZone: "UTC", tzChanged: true,
	})
	assert.Error(t, err, "--time-zone is not applicable to timer triggers")
}

func TestApplyUpdateFlags_GithubIssueFields(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Action: &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentName: "a"},
		Triggers: map[string]routines.RoutineTrigger{"default": {
			Type:         "github_issue",
			ConnectionID: "old-conn",
			Owner:        "old-owner",
			Repository:   "old-repo",
			IssueEvent:   "opened",
		}},
	}
	n, err := applyUpdateFlags(r, routineUpdateChanges{
		owner: "new-owner", ownerChanged: true,
		issueEvent: "closed", eventChanged: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, "new-owner", r.Triggers["default"].Owner)
	assert.Equal(t, "closed", r.Triggers["default"].IssueEvent)
}

func TestApplyUpdateFlags_GithubIssueRejectedForSchedule(t *testing.T) {
	t.Parallel()
	r := routineWithScheduleAndAgentResp()
	_, err := applyUpdateFlags(r, routineUpdateChanges{
		owner: "x", ownerChanged: true,
	})
	assert.Error(t, err, "--owner is not applicable to schedule triggers")
}

func TestApplyUpdateFlags_CustomParameters(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Action: &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentName: "a"},
		Triggers: map[string]routines.RoutineTrigger{"default": {
			Type:     "custom",
			Provider: "stripe",
		}},
	}
	n, err := applyUpdateFlags(r, routineUpdateChanges{
		parametersJSON: `{"x":1}`, paramsChanged: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, float64(1), (*r.Triggers["default"].Parameters)["x"])
}

func TestApplyUpdateFlags_CustomParametersBadJSON(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Action: &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentName: "a"},
		Triggers: map[string]routines.RoutineTrigger{"default": {
			Type:     "custom",
			Provider: "stripe",
		}},
	}
	_, err := applyUpdateFlags(r, routineUpdateChanges{
		parametersJSON: "not-json", paramsChanged: true,
	})
	assert.Error(t, err)
}

// ─── getTrigger / getAction ───────────────────────────────────────────────────

func TestGetTrigger_NilWhenEmpty(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{}
	assert.Nil(t, getTrigger(r))
}

func TestGetTrigger_ReturnsCopy(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "schedule", CronExpression: "0 9 * * *"},
		},
	}
	trig := getTrigger(r)
	require.NotNil(t, trig)
	assert.Equal(t, "schedule", trig.Type)
	trig.CronExpression = "changed"
	assert.Equal(t, "0 9 * * *", r.Triggers["default"].CronExpression)
}

func TestGetAction_NilWhenEmpty(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{}
	assert.Nil(t, getAction(r))
}

func TestGetAction_ReturnsCopy(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Action: &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentName: "orig-agent-name"},
	}
	act := getAction(r)
	require.NotNil(t, act)
	act.AgentName = "changed"
	assert.Equal(t, "orig-agent-name", r.Action.AgentName)
}
