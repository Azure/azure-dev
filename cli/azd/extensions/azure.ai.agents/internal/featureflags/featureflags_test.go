// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package featureflags

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"azureaiagent/internal/exterrors"
)

var fixture = Feature{
	ID:     "foundry.fixture",
	EnvVar: "AZD_FOUNDRY_EXT_ENABLE_TEST_FIXTURE",
}

func TestCheckEnvironmentFlag(t *testing.T) {
	tests := []struct {
		name        string
		nilFlags    bool
		declareFlag bool
		args        []string
		wantError   bool
	}{
		{name: "nil flagset", nilFlags: true},
		{name: "flag absent"},
		{name: "not changed", declareFlag: true},
		{name: "changed with name", declareFlag: true, args: []string{"--environment", "selected"}},
		{name: "changed with empty value", declareFlag: true, args: []string{"--environment", ""}, wantError: true},
		{name: "changed with whitespace", declareFlag: true, args: []string{"--environment", "  "}, wantError: true},
		{name: "short flag with name", declareFlag: true, args: []string{"-e", "selected"}},
		{name: "short flag with empty value", declareFlag: true, args: []string{"-e", ""}, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var flags *pflag.FlagSet
			if !tt.nilFlags {
				flags = pflag.NewFlagSet(tt.name, pflag.ContinueOnError)
				if tt.declareFlag {
					flags.StringP("environment", "e", "", "")
				}
				require.NoError(t, flags.Parse(tt.args))
			}

			err := CheckEnvironmentFlag(flags)

			if !tt.wantError {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
			require.Equal(t, exterrors.CodeInvalidParameter, localErr.Code)
			require.Equal(t, "the --environment flag was set to an empty value", localErr.Message)
			require.Equal(t,
				"pass an environment name with --environment, or omit the flag to use the default environment",
				localErr.Suggestion,
			)
		})
	}
}

func TestIsEnabledValidation(t *testing.T) {
	tests := []struct {
		name    string
		client  *azdext.AzdClient
		feature Feature
		want    string
	}{
		{"nil client", nil, fixture, "client must not be nil"},
		{"missing ID", &azdext.AzdClient{}, Feature{EnvVar: fixture.EnvVar}, "must not be empty"},
		{"missing EnvVar", &azdext.AzdClient{}, Feature{ID: fixture.ID}, "must not be empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enabled, err := IsEnabled(t.Context(), tt.client, tt.feature, "")
			require.False(t, enabled)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestFourSourceCombinations(t *testing.T) {
	for mask := range 16 {
		t.Run(fmt.Sprintf("%04b", mask), func(t *testing.T) {
			t.Setenv(fixture.EnvVar, fmt.Sprint(mask&1 != 0))
			configValue := "off"
			if mask&4 != 0 {
				configValue = "on"
			}
			client := newMockClient(t)
			client.expectSources(
				alphaResponse(t, configValue, "off"),
				projectWithServices(service("containerapp", fmt.Sprint(mask&8 != 0))),
				map[string]string{fixture.EnvVar: fmt.Sprint(mask&2 != 0)},
				"selected",
			)

			enabled, err := isEnabled(t.Context(), client, fixture, "selected")

			require.NoError(t, err)
			require.Equal(t, mask != 0, enabled)
		})
	}
}

func TestEveryServiceCounts(t *testing.T) {
	tests := []struct {
		name     string
		services []*azdext.ServiceConfig
		want     bool
	}{
		{"no services", nil, false},
		{"agent service", []*azdext.ServiceConfig{service("azure.ai.agent", "true")}, true},
		{"project service", []*azdext.ServiceConfig{service("azure.ai.project", "true")}, true},
		{"non-Foundry service", []*azdext.ServiceConfig{service("appservice", "true")}, true},
		{
			"multiple project services",
			[]*azdext.ServiceConfig{
				service("azure.ai.project", "false"),
				service("azure.ai.project", "true"),
			},
			true,
		},
		{
			"unrelated service enables multiple projects",
			[]*azdext.ServiceConfig{
				service("azure.ai.project", "false"),
				service("azure.ai.project", "false"),
				service("containerapp", "true"),
			},
			true,
		},
		{"empty service", []*azdext.ServiceConfig{{}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(fixture.EnvVar, "false")
			client := newMockClient(t)
			client.expectSources(
				alphaResponse(t, "off", "off"),
				projectWithServices(tt.services...),
				nil,
				"",
			)

			enabled, err := isEnabled(t.Context(), client, fixture, "")

			require.NoError(t, err)
			require.Equal(t, tt.want, enabled)
		})
	}
}

func TestConflictingServiceOrder(t *testing.T) {
	t.Setenv(fixture.EnvVar, "false")
	for enabledIndex := range 2 {
		t.Run(fmt.Sprint(enabledIndex), func(t *testing.T) {
			services := []*azdext.ServiceConfig{
				service("azure.ai.project", "false"),
				service("containerapp", "false"),
			}
			services[enabledIndex].Environment[fixture.EnvVar] = "true"
			client := newMockClient(t)

			for range 32 {
				client.expectSources(alphaResponse(t, "off", "off"), projectWithServices(services...), nil, "")
				enabled, err := isEnabled(t.Context(), client, fixture, "")
				require.NoError(t, err)
				require.True(t, enabled)
			}
		})
	}
}

func TestEnvironmentValues(t *testing.T) {
	values := []struct {
		value string
		want  bool
	}{
		{"1", true}, {"t", true}, {"T", true},
		{"true", true}, {"TRUE", true}, {"True", true},
		{"0", false}, {"f", false}, {"F", false},
		{"false", false}, {"FALSE", false}, {"False", false},
		{"", false}, {"off", false}, {"on", false},
		{"yes", false}, {" true ", false}, {"invalid", false},
	}
	for _, source := range []string{"process", "dotenv", "service"} {
		for _, tt := range values {
			t.Run(source+"/"+tt.value, func(t *testing.T) {
				t.Setenv(fixture.EnvVar, "false")
				dotenv := map[string]string{}
				svc := service("containerapp", "false")
				switch source {
				case "process":
					t.Setenv(fixture.EnvVar, tt.value)
				case "dotenv":
					dotenv[fixture.EnvVar] = tt.value
				case "service":
					svc.Environment[fixture.EnvVar] = tt.value
				}
				client := newMockClient(t)
				client.expectSources(alphaResponse(t, "off", "off"), projectWithServices(svc), dotenv, "")

				enabled, err := isEnabled(t.Context(), client, fixture, "")

				require.NoError(t, err)
				require.Equal(t, tt.want, enabled)
			})
		}
	}
}

func TestConfigValues(t *testing.T) {
	values := []struct {
		name  string
		value any
		want  bool
	}{
		{"on", "on", true},
		{"upper", "ON", true},
		{"mixed case", "On", true},
		{"off", "off", false},
		{"empty", "", false},
		{"true string", "true", false},
		{"whitespace", " on ", false},
		{"unknown string", "other", false},
		{"boolean", true, false},
		{"number", 1, false},
		{"null", nil, false},
		{"object", map[string]any{"enabled": "on"}, false},
		{"array", []string{"on"}, false},
	}
	for _, path := range []string{"feature", "all"} {
		for _, tt := range values {
			t.Run(path+"/"+tt.name, func(t *testing.T) {
				t.Setenv(fixture.EnvVar, "false")
				var featureValue, allValue any = "off", "off"
				if path == "feature" {
					featureValue = tt.value
				} else {
					allValue = tt.value
				}
				client := newMockClient(t)
				client.expectSources(
					alphaResponse(t, featureValue, allValue),
					projectWithServices(),
					nil,
					"",
				)

				enabled, err := isEnabled(t.Context(), client, fixture, "")

				require.NoError(t, err)
				require.Equal(t, path == "feature" && tt.want, enabled)
			})
		}
	}
}

func TestGlobalConfigDoesNotEnableAgentsFeatures(t *testing.T) {
	for _, feature := range All() {
		id := feature.ID
		for _, value := range []any{"on", "ON", "On", "oN", "off", "", true, nil} {
			t.Run(id+"/"+fmt.Sprint(value), func(t *testing.T) {
				alphaValues := map[string]any{
					"all": value,
					"agents": map[string]any{
						"unrelated": "on",
					},
				}

				require.False(t, configEnabled(alphaValues, id))
			})
		}
	}
}

func TestUnrelatedConfigDoesNotOverrideOptIn(t *testing.T) {
	for _, value := range []any{"on", "ON", "On", "oN", "off", "", false, nil} {
		for _, source := range []string{"config", "process", "dotenv", "service"} {
			t.Run(fmt.Sprint(value)+"/"+source, func(t *testing.T) {
				t.Setenv(fixture.EnvVar, "false")
				configValue := "off"
				dotenv := map[string]string{}
				svc := service("containerapp", "false")
				switch source {
				case "config":
					configValue = "on"
				case "process":
					t.Setenv(fixture.EnvVar, "true")
				case "dotenv":
					dotenv[fixture.EnvVar] = "true"
				case "service":
					svc.Environment[fixture.EnvVar] = "true"
				}
				response := alphaResponse(t, configValue, value)
				var alphaValues map[string]any
				require.NoError(t, json.Unmarshal(response.Value, &alphaValues))
				alphaValues["unrelated"] = value
				data, err := json.Marshal(alphaValues)
				require.NoError(t, err)
				response.Value = data
				client := newMockClient(t)
				client.expectSources(response, projectWithServices(svc), dotenv, "")

				enabled, err := isEnabled(t.Context(), client, fixture, "")

				require.NoError(t, err)
				require.True(t, enabled)
			})
		}
	}
}

func TestNonEnablingConfigDoesNotOverride(t *testing.T) {
	for _, response := range []*azdext.GetUserConfigResponse{
		{},
		{Found: true, Value: []byte(`null`)},
		{Found: true, Value: []byte(`false`)},
		{Found: true, Value: []byte(`"off"`)},
		{Found: true, Value: []byte(`["on"]`)},
		alphaResponse(t, false, "off"),
		alphaResponse(t, "off", false),
		alphaResponse(t, "off", "ON"),
		alphaResponse(t, "ON", "off"),
	} {
		t.Run(string(response.Value), func(t *testing.T) {
			t.Setenv(fixture.EnvVar, "true")
			client := newMockClient(t)
			client.expectSources(response, projectWithServices(), nil, "")

			enabled, err := isEnabled(t.Context(), client, fixture, "")

			require.NoError(t, err)
			require.True(t, enabled)
		})
	}
}

func TestNonObjectAlphaSectionIsDisabled(t *testing.T) {
	for _, value := range []string{`null`, `false`, `true`, `1`, `"on"`, `["on"]`} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(fixture.EnvVar, "false")
			client := newMockClient(t)
			client.expectSources(
				&azdext.GetUserConfigResponse{Found: true, Value: []byte(value)},
				projectWithServices(),
				nil,
				"",
			)

			enabled, err := isEnabled(t.Context(), client, fixture, "")

			require.NoError(t, err)
			require.False(t, enabled)
		})
	}
}

func TestOnlyCanonicalEnvironmentKey(t *testing.T) {
	t.Setenv(fixture.EnvVar, "")
	aliases := map[string]string{
		"AZD_FOUNDRY_EXT_ENABLE_ALL":           "true",
		"AZD_ALPHA_ENABLE_AGENTS_VOICE":        "true",
		"AZD_ALPHA_ENABLE_FOUNDRY_FIXTURE":     "true",
		"AZD_ALPHA_ENABLE_FOUNDRY.FIXTURE":     "true",
		"AZD_FOUNDRY_ENABLE_TEST_FIXTURE":      "true",
		"AZD_AGENT_RESUME_SESSION_ON_DEPLOY":   "true",
		"AZD_FOUNDRY_EXT_ENABLE_AGENTS_OTHER":  "true",
		"AZD_FOUNDRY_EXT_ENABLE_MODELS_OTHER":  "true",
		"AZD_FOUNDRY_EXT_ENABLE_TEST_FIXTURE_": "true",
	}
	for _, feature := range append(All(), fixture) {
		t.Run(feature.ID, func(t *testing.T) {
			t.Setenv(feature.EnvVar, "")
			for name, value := range aliases {
				t.Setenv(name, value)
			}
			client := newMockClient(t)
			client.expectSources(
				alphaResponse(t, "off", "on"),
				projectWithServices(&azdext.ServiceConfig{Host: "containerapp", Environment: maps.Clone(aliases)}),
				aliases,
				"selected",
			)

			enabled, err := isEnabled(t.Context(), client, feature, "selected")

			require.NoError(t, err)
			require.False(t, enabled)
		})
	}
}

func TestEnvironmentSelection(t *testing.T) {
	t.Setenv(fixture.EnvVar, "false")
	t.Setenv("AZD_ENVIRONMENT", "ambient-default")
	for _, name := range []string{"", "explicit-selection"} {
		t.Run(name, func(t *testing.T) {
			client := newMockClient(t)
			client.expectSources(
				alphaResponse(t, "off", "off"),
				projectWithServices(),
				map[string]string{fixture.EnvVar: fmt.Sprint(name != "")},
				name,
			)

			enabled, err := isEnabled(t.Context(), client, fixture, name)

			require.NoError(t, err)
			require.Equal(t, name != "", enabled)
		})
	}
}

func TestAbsentScopeSentinelsMatchHost(t *testing.T) {
	require.Equal(t, azdcontext.ErrNoProject.Error(), errNoProject.Error())
	require.Equal(t, environment.ErrDefaultEnvironmentNotFound.Error(), errDefaultEnvironmentNotFound.Error())
	require.Equal(t, environment.ErrNotFound.Error(), errEnvironmentNotFound.Error())
}

func TestInitOptionalScopes(t *testing.T) {
	for _, scope := range []string{"no project", "new named environment", "existing environment"} {
		for _, source := range []string{"off", "process", "config", "service", "dotenv"} {
			if (scope == "no project" && source == "service") ||
				(scope != "existing environment" && source == "dotenv") {
				continue
			}
			t.Run(scope+"/"+source, func(t *testing.T) {
				t.Setenv(fixture.EnvVar, fmt.Sprint(source == "process"))
				value := "off"
				if source == "config" {
					value = "on"
				}
				client := newMockClient(t)
				client.expectConfig(alphaResponse(t, value, "off"), nil)
				if scope == "no project" {
					client.expectProject(nil, status.Error(codes.Unknown, azdcontext.ErrNoProject.Error()))
				} else {
					client.expectProject(&azdext.GetProjectResponse{
						Project: projectWithServices(service("appservice", fmt.Sprint(source == "service"))),
					}, nil)
					if scope == "new named environment" {
						client.expectDotenv("selected", nil, status.Error(codes.Unknown, environment.ErrNotFound.Error()))
					} else {
						client.expectDotenv("selected", &azdext.KeyValueListResponse{
							KeyValues: []*azdext.KeyValue{{Key: fixture.EnvVar, Value: fmt.Sprint(source == "dotenv")}},
						}, nil)
					}
				}
				ctx := WithInitScope(WithEnvironment(t.Context(), "selected"))
				enabled, err := isEnabled(ctx, client, fixture, Environment(ctx))
				require.NoError(t, err)
				require.Equal(t, source != "off", enabled)
				require.Equal(t, "selected", Environment(ctx))
			})
		}
	}
}

func TestInitScopePreservesSourceErrors(t *testing.T) {
	detailedAbsence, err := status.New(codes.Unknown, environment.ErrNotFound.Error()).WithDetails(
		&errdetails.ErrorInfo{Reason: "load_failed"},
	)
	require.NoError(t, err)
	detailedProjectAbsence, err := status.New(codes.Unknown, azdcontext.ErrNoProject.Error()).WithDetails(
		&errdetails.ErrorInfo{Reason: "load_failed"},
	)
	require.NoError(t, err)
	for _, tc := range []struct {
		name  string
		stage string
		cause error
	}{
		{"config transport", "config", status.Error(codes.Unavailable, "connection lost")},
		{"config permission", "config", &fs.PathError{Op: "read", Path: "config.json", Err: fs.ErrPermission}},
		{"config canceled", "config", context.Canceled},
		{"project permission", "project", status.Error(codes.PermissionDenied, "project denied")},
		{"project decoding", "project", errors.New("decoding azure.yaml")},
		{"project transport", "project", status.Error(codes.Unavailable, azdcontext.ErrNoProject.Error())},
		{"project not found status", "project", status.Error(codes.NotFound, azdcontext.ErrNoProject.Error())},
		{"project prefixed absence", "project", fmt.Errorf("loading: %w", azdcontext.ErrNoProject)},
		{"project detailed absence", "project", detailedProjectAbsence.Err()},
		{"project canceled", "project", context.Canceled},
		{"environment permission", "dotenv", status.Error(codes.PermissionDenied, "environment denied")},
		{"environment decoding", "dotenv", errors.New("decoding dotenv")},
		{"environment transport", "dotenv", status.Error(codes.Unavailable, environment.ErrNotFound.Error())},
		{"environment not found status", "dotenv", status.Error(codes.NotFound, environment.ErrNotFound.Error())},
		{"environment detailed absence", "dotenv", detailedAbsence.Err()},
		{"environment prefixed absence", "dotenv", fmt.Errorf("reading: %w", environment.ErrNotFound)},
		{"environment default absence", "dotenv", environment.ErrDefaultEnvironmentNotFound},
		{"project disappeared", "dotenv", azdcontext.ErrNoProject},
		{"environment canceled", "dotenv", context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(fixture.EnvVar, "true")
			client := newMockClient(t)
			if tc.stage == "config" {
				client.expectConfig(nil, tc.cause)
			} else {
				client.expectConfig(alphaResponse(t, "on", "off"), nil)
				if tc.stage == "project" {
					client.expectProject(nil, tc.cause)
				} else {
					client.expectProject(&azdext.GetProjectResponse{Project: projectWithServices()}, nil)
					client.expectDotenv("selected", nil, tc.cause)
				}
			}
			enabled, err := isEnabled(WithInitScope(t.Context()), client, fixture, "selected")
			require.False(t, enabled)
			require.ErrorIs(t, err, tc.cause)
		})
	}
}

func TestInitScopeMalformedResponses(t *testing.T) {
	for _, stage := range []string{"config", "decode", "project", "empty project", "dotenv"} {
		t.Run(stage, func(t *testing.T) {
			t.Setenv(fixture.EnvVar, "true")
			client := newMockClient(t)
			switch stage {
			case "config":
				client.expectConfig(nil, nil)
			case "decode":
				client.expectConfig(&azdext.GetUserConfigResponse{Found: true, Value: []byte("{")}, nil)
			default:
				client.expectConfig(alphaResponse(t, "on", "off"), nil)
				switch stage {
				case "project":
					client.expectProject(nil, nil)
				case "empty project":
					client.expectProject(&azdext.GetProjectResponse{}, nil)
				case "dotenv":
					client.expectProject(&azdext.GetProjectResponse{Project: projectWithServices()}, nil)
					client.expectDotenv("selected", nil, nil)
				}
			}
			enabled, err := isEnabled(WithInitScope(t.Context()), client, fixture, "selected")
			require.False(t, enabled)
			if stage == "decode" {
				require.ErrorContains(t, err, "decoding alpha user config")
			} else {
				require.ErrorContains(t, err, "missing")
				require.ErrorContains(t, err, "response")
			}
		})
	}
}

func TestOptionalScopes(t *testing.T) {
	tests := []struct {
		name       string
		projectErr error
		dotenvErr  error
	}{
		{"no project", azdcontext.ErrNoProject, nil},
		{"no project over gRPC", status.Error(codes.Unknown, azdcontext.ErrNoProject.Error()), nil},
		{"no default environment", nil, environment.ErrDefaultEnvironmentNotFound},
		{
			"no default environment over gRPC",
			nil,
			status.Error(codes.Unknown, environment.ErrDefaultEnvironmentNotFound.Error()),
		},
	}
	for _, tt := range tests {
		for _, positive := range []bool{false, true} {
			t.Run(tt.name+"/"+fmt.Sprint(positive), func(t *testing.T) {
				t.Setenv(fixture.EnvVar, "false")
				client := newMockClient(t)
				value := "off"
				if positive && tt.projectErr != nil {
					value = "on"
				}
				client.expectConfig(alphaResponse(t, value, "off"), nil)
				if tt.projectErr != nil {
					client.expectProject(nil, tt.projectErr)
				} else {
					client.expectProject(&azdext.GetProjectResponse{
						Project: projectWithServices(service("appservice", fmt.Sprint(positive))),
					}, nil)
					client.expectDotenv("", nil, tt.dotenvErr)
				}

				enabled, err := isEnabled(t.Context(), client, fixture, "")

				require.NoError(t, err)
				require.Equal(t, positive, enabled)
			})
		}
	}
}

func TestProcessOptInWithoutProject(t *testing.T) {
	t.Setenv(fixture.EnvVar, "true")
	client := newMockClient(t)
	client.expectConfig(&azdext.GetUserConfigResponse{}, nil)
	client.expectProject(nil, status.Error(codes.Unknown, azdcontext.ErrNoProject.Error()))

	enabled, err := isEnabled(t.Context(), client, fixture, "")

	require.NoError(t, err)
	require.True(t, enabled)
}

func TestSourceErrorsAreReturned(t *testing.T) {
	detailedAbsence, err := status.New(codes.Unknown, azdcontext.ErrNoProject.Error()).WithDetails(
		&errdetails.ErrorInfo{Reason: "load_failed"},
	)
	require.NoError(t, err)
	tests := []struct {
		name            string
		stage           string
		cause           error
		environmentName string
	}{
		{"config transport", "config", status.Error(codes.Unavailable, "connection lost"), ""},
		{"config read", "config", &fs.PathError{Op: "read", Path: "config.json", Err: fs.ErrPermission}, ""},
		{"project transport", "project", status.Error(codes.Unavailable, "connection lost"), ""},
		{"project decoding", "project", errors.New("decoding azure.yaml"), ""},
		{"project read", "project", &fs.PathError{Op: "read", Path: "azure.yaml", Err: fs.ErrPermission}, ""},
		{"project not found status", "project", status.Error(codes.NotFound, "project file disappeared"), ""},
		{
			"project similar message",
			"project",
			status.Error(codes.Unknown, "loading project: "+azdcontext.ErrNoProject.Error()),
			"",
		},
		{"project status has details", "project", detailedAbsence.Err(), ""},
		{"explicit environment without project", "project", azdcontext.ErrNoProject, "selected"},
		{"dotenv transport", "dotenv", status.Error(codes.Unavailable, "connection lost"), ""},
		{"dotenv decoding", "dotenv", errors.New("decoding dotenv"), ""},
		{"dotenv read", "dotenv", &fs.PathError{Op: "read", Path: ".env", Err: fs.ErrPermission}, ""},
		{"invalid default environment", "dotenv", environment.ErrNotFound, ""},
		{"invalid explicit environment", "dotenv", environment.ErrNotFound, "selected"},
		{"explicit no default sentinel", "dotenv", environment.ErrDefaultEnvironmentNotFound, "selected"},
		{
			"explicit no default sentinel over gRPC",
			"dotenv",
			status.Error(codes.Unknown, environment.ErrDefaultEnvironmentNotFound.Error()),
			"selected",
		},
		{
			"untyped default absence with context",
			"dotenv",
			status.Error(codes.Unknown, "reading state: "+environment.ErrDefaultEnvironmentNotFound.Error()),
			"",
		},
		{"dotenv not found status", "dotenv", status.Error(codes.NotFound, "dotenv missing"), ""},
		{"project disappeared during dotenv read", "dotenv", azdcontext.ErrNoProject, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(fixture.EnvVar, "true")
			client := newMockClient(t)
			configResponse := alphaResponse(t, "on", "off")
			switch tt.stage {
			case "config":
				client.expectConfig(nil, tt.cause)
			case "project":
				client.expectConfig(configResponse, nil)
				client.expectProject(nil, tt.cause)
			case "dotenv":
				client.expectConfig(configResponse, nil)
				client.expectProject(&azdext.GetProjectResponse{Project: projectWithServices()}, nil)
				client.expectDotenv(tt.environmentName, nil, tt.cause)
			}

			enabled, err := isEnabled(t.Context(), client, fixture, tt.environmentName)

			require.False(t, enabled)
			require.ErrorIs(t, err, tt.cause)
			require.ErrorContains(t, err, fixture.ID)
			switch tt.stage {
			case "config":
				require.ErrorContains(t, err, "reading user config")
			case "project":
				require.ErrorContains(t, err, "loading project")
			case "dotenv":
				require.ErrorContains(t, err, "reading azd environment")
			}
		})
	}
}

func TestMalformedResponses(t *testing.T) {
	tests := []struct {
		name  string
		stage string
		value []byte
		want  string
	}{
		{"nil config", "config", nil, "missing response"},
		{"nil project response", "project", nil, "missing project response"},
		{"nil project", "empty project", nil, "missing project response"},
		{"nil dotenv", "dotenv", nil, "missing response"},
		{"empty config JSON", "decode", nil, "decoding alpha user config"},
		{"malformed config JSON", "decode", []byte(`{"foundry":`), "decoding alpha user config"},
		{
			"config JSON does not disclose values",
			"decode",
			[]byte(`{"secret":"private-fixture-value"} trailing-data`),
			"decoding alpha user config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(fixture.EnvVar, "true")
			client := newMockClient(t)
			switch tt.stage {
			case "config":
				client.expectConfig(nil, nil)
			case "decode":
				client.expectConfig(&azdext.GetUserConfigResponse{Found: true, Value: tt.value}, nil)
			default:
				client.expectConfig(alphaResponse(t, "on", "off"), nil)
				switch tt.stage {
				case "project":
					client.expectProject(nil, nil)
				case "empty project":
					client.expectProject(&azdext.GetProjectResponse{}, nil)
				case "dotenv":
					client.expectProject(&azdext.GetProjectResponse{Project: projectWithServices()}, nil)
					client.expectDotenv("", nil, nil)
				}
			}

			enabled, err := isEnabled(t.Context(), client, fixture, "")

			require.False(t, enabled)
			require.ErrorContains(t, err, tt.want)
			require.NotContains(t, err.Error(), "private-fixture-value")
			if tt.stage == "decode" {
				_, ok := errors.AsType[*json.SyntaxError](err)
				require.True(t, ok)
			}
		})
	}
}

func TestInvocationSnapshotsDoNotLeakOrMutate(t *testing.T) {
	for _, source := range []string{"process", "dotenv", "config", "service"} {
		t.Run(source, func(t *testing.T) {
			t.Setenv(fixture.EnvVar, "false")
			client := newMockClient(t)
			for _, positive := range []bool{true, false} {
				configValue := "off"
				dotenv := map[string]string{fixture.EnvVar: "false", "UNRELATED": "private-fixture-value"}
				svc := service("containerapp", "false")
				if positive {
					switch source {
					case "process":
						t.Setenv(fixture.EnvVar, "true")
					case "dotenv":
						dotenv[fixture.EnvVar] = "true"
					case "config":
						configValue = "on"
					case "service":
						svc.Environment[fixture.EnvVar] = "true"
					}
				} else {
					t.Setenv(fixture.EnvVar, "false")
				}
				configResponse := alphaResponse(t, configValue, "off")
				configBefore := bytes.Clone(configResponse.Value)
				dotenvBefore := maps.Clone(dotenv)
				serviceBefore := maps.Clone(svc.Environment)
				processBefore := os.Getenv(fixture.EnvVar)
				client.expectSources(configResponse, projectWithServices(svc), dotenv, "selected")

				enabled, err := isEnabled(t.Context(), client, fixture, "selected")

				require.NoError(t, err)
				require.Equal(t, positive, enabled)
				require.Equal(t, configBefore, configResponse.Value)
				require.Equal(t, dotenvBefore, dotenv)
				require.Equal(t, serviceBefore, svc.Environment)
				require.Equal(t, processBefore, os.Getenv(fixture.EnvVar))
			}
		})
	}
}

type contextKey struct{}

func TestContextIsPropagated(t *testing.T) {
	t.Setenv(fixture.EnvVar, "false")
	ctx := context.WithValue(t.Context(), contextKey{}, "invocation")
	client := newMockClient(t)
	client.On("UserConfig.Get", ctx, &azdext.GetUserConfigRequest{Path: "alpha"}).
		Return(&azdext.GetUserConfigResponse{}, nil).Once()
	client.On("Project.Get", ctx, &azdext.EmptyRequest{}).
		Return(&azdext.GetProjectResponse{Project: projectWithServices()}, nil).Once()
	client.On("Environment.GetValues", ctx, &azdext.GetEnvironmentRequest{Name: "selected"}).
		Return(&azdext.KeyValueListResponse{}, nil).Once()

	enabled, err := isEnabled(ctx, client, fixture, "selected")

	require.NoError(t, err)
	require.False(t, enabled)
}

func service(host, value string) *azdext.ServiceConfig {
	return &azdext.ServiceConfig{Host: host, Environment: map[string]string{fixture.EnvVar: value}}
}

func projectWithServices(services ...*azdext.ServiceConfig) *azdext.ProjectConfig {
	project := &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{}}
	for i, svc := range services {
		project.Services[fmt.Sprintf("service-%d", i)] = svc
	}
	return project
}

func alphaResponse(t *testing.T, value, all any) *azdext.GetUserConfigResponse {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"foundry": map[string]any{"fixture": value},
		"all":     all,
	})
	require.NoError(t, err)
	return &azdext.GetUserConfigResponse{Found: true, Value: data}
}

type mockClient struct {
	mock.Mock
	t *testing.T
}

func newMockClient(t *testing.T) *mockClient {
	t.Helper()
	client := &mockClient{t: t}
	client.Test(t)
	t.Cleanup(func() { client.AssertExpectations(t) })
	return client
}

func (c *mockClient) Project() azdext.ProjectServiceClient {
	return &mockProjectClient{client: c}
}

func (c *mockClient) Environment() azdext.EnvironmentServiceClient {
	return &mockEnvironmentClient{client: c}
}

func (c *mockClient) UserConfig() azdext.UserConfigServiceClient {
	return &mockUserConfigClient{client: c}
}

func (c *mockClient) expectConfig(response *azdext.GetUserConfigResponse, err error) {
	c.On("UserConfig.Get", mock.Anything, &azdext.GetUserConfigRequest{Path: "alpha"}).
		Return(response, err).Once()
}

func (c *mockClient) expectProject(response *azdext.GetProjectResponse, err error) {
	c.On("Project.Get", mock.Anything, &azdext.EmptyRequest{}).Return(response, err).Once()
}

func (c *mockClient) expectDotenv(name string, response *azdext.KeyValueListResponse, err error) {
	c.On("Environment.GetValues", mock.Anything, &azdext.GetEnvironmentRequest{Name: name}).
		Return(response, err).Once()
}

func (c *mockClient) expectSources(
	configResponse *azdext.GetUserConfigResponse,
	project *azdext.ProjectConfig,
	dotenv map[string]string,
	name string,
) {
	c.expectConfig(configResponse, nil)
	c.expectProject(&azdext.GetProjectResponse{Project: project}, nil)
	values := &azdext.KeyValueListResponse{}
	for key, value := range dotenv {
		values.KeyValues = append(values.KeyValues, &azdext.KeyValue{Key: key, Value: value})
	}
	c.expectDotenv(name, values, nil)
}

type mockProjectClient struct {
	azdext.ProjectServiceClient
	client *mockClient
}

func (c *mockProjectClient) Get(
	ctx context.Context,
	request *azdext.EmptyRequest,
	_ ...grpc.CallOption,
) (*azdext.GetProjectResponse, error) {
	args := c.client.MethodCalled("Project.Get", ctx, request)
	response, ok := args.Get(0).(*azdext.GetProjectResponse)
	require.True(c.client.t, ok)
	return response, args.Error(1)
}

type mockEnvironmentClient struct {
	azdext.EnvironmentServiceClient
	client *mockClient
}

func (c *mockEnvironmentClient) GetValues(
	ctx context.Context,
	request *azdext.GetEnvironmentRequest,
	_ ...grpc.CallOption,
) (*azdext.KeyValueListResponse, error) {
	args := c.client.MethodCalled("Environment.GetValues", ctx, request)
	response, ok := args.Get(0).(*azdext.KeyValueListResponse)
	require.True(c.client.t, ok)
	return response, args.Error(1)
}

type mockUserConfigClient struct {
	azdext.UserConfigServiceClient
	client *mockClient
}

func (c *mockUserConfigClient) Get(
	ctx context.Context,
	request *azdext.GetUserConfigRequest,
	_ ...grpc.CallOption,
) (*azdext.GetUserConfigResponse, error) {
	args := c.client.MethodCalled("UserConfig.Get", ctx, request)
	response, ok := args.Get(0).(*azdext.GetUserConfigResponse)
	require.True(c.client.t, ok)
	return response, args.Error(1)
}
