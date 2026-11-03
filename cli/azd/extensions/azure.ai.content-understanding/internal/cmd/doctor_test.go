// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/defaults"
	"azure.ai.content-understanding/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctorHealthyGoldenReadOnlyAndContextLabel(t *testing.T) {
	for _, environment := range []string{"dev", ""} {
		f := newDefaultsFixture(t)
		f.environment = environment
		stdout, stderr, err := f.execute(t.Context(), "doctor", "-o", "json")
		require.NoError(t, err)
		assert.Empty(t, stderr)
		assert.Equal(t, []string{"get"}, f.client.calls)
		assert.Equal(t, 1, f.closes)
		var result doctorResult
		require.NoError(t, json.Unmarshal([]byte(stdout), &result))
		assert.True(t, result.Ready)
		assert.Equal(t, "Microsoft Entra ID", result.Authentication)
		assert.Nil(t, result.Analyzer)
		assert.Empty(t, result.Failures)
		assert.Empty(t, result.Missing)
		if environment == "" {
			assert.Equal(t, "projectless", result.Profile)
		} else {
			assert.Equal(t, environment, result.Profile)
			golden, err := os.ReadFile("testdata/doctor.json")
			require.NoError(t, err)
			assert.JSONEq(t, string(golden), stdout)
		}
	}
}

func TestDoctorHumanReadableDefault(t *testing.T) {
	for _, environment := range []string{"dev", ""} {
		for _, test := range []struct {
			name string
			args []string
		}{
			{name: "implicit"},
			{name: "explicit", args: []string{"--output", "default"}},
			{name: "unattended", args: []string{"--no-prompt"}},
		} {
			t.Run(environment+"/"+test.name, func(t *testing.T) {
				f := newDefaultsFixture(t)
				f.environment, f.analyzer = environment, "prebuilt-invoice"
				f.sources = map[string]string{
					"CU_ENDPOINT": "user config", "CU_API_VERSION": "built-in", "CU_DEFAULT_ANALYZER": "process",
				}
				stdout, stderr, err := f.execute(t.Context(), append([]string{"doctor"}, test.args...)...)
				require.NoError(t, err)
				assert.Empty(t, stderr)
				context := environment
				if context == "" {
					context = "projectless"
				}
				assert.Equal(t, fmt.Sprintf(`Content Understanding diagnostics

[OK] Configuration resolved
  Context: %s
  CU_ENDPOINT: https://configured.services.ai.azure.com/contentunderstanding (user config)
  CU_API_VERSION: 2025-11-01 (built-in)
  CU_DEFAULT_ANALYZER: prebuilt-invoice (process)

[OK] Authentication and CU service access (Microsoft Entra ID)

Model deployments:
  gpt-4.1: old-completion
  prebuilt-analyzer-completion: old-completion
  prebuilt-analyzer-completion-mini: old-completion
  prebuilt-analyzer-embedding: embedding
  text-embedding-3-large: embedding

[OK] Generative readiness: ready
`, context), stdout)
				assert.Equal(t, []string{"get"}, f.client.calls)
				assert.Equal(t, 1, f.closes)
			})
		}
	}
}

func TestDoctorHumanReadableMissingDefaults(t *testing.T) {
	for _, notSet := range []bool{false, true} {
		f := newDefaultsFixture(t)
		f.client.current = cuclient.Defaults{ModelDeployments: map[string]string{}}
		if notSet {
			f.client.getErr = &cuclient.Error{Kind: cuclient.ErrorNotFound, Code: "DefaultsNotSet", StatusCode: 404}
		}
		stdout, stderr, err := f.execute(t.Context(), "doctor")
		require.NoError(t, err, "incomplete generative configuration must not change the exit status")
		assert.Empty(t, stderr)
		assert.Contains(t, stdout, "CU_DEFAULT_ANALYZER: (not set)")
		assert.Contains(t, stdout, "[OK] Authentication and CU service access")
		assert.Contains(t, stdout, "(none configured)")
		assert.Contains(t, stdout, "[WARN] Generative readiness: not ready")
		for _, missing := range defaults.MissingRequirements(nil) {
			assert.Contains(t, stdout, "  - "+missing+"\n")
		}
		assert.Equal(t, 3, strings.Count(stdout, "  - "))
		assert.Contains(t, stdout, "azd ai cu defaults set --model MODEL=DEPLOYMENT")
		assert.Contains(t, stdout, "azd ai cu doctor --fix-defaults")
		assert.Contains(t, stdout, "Non-generative analyzers may still be usable.")
		assert.Equal(t, []string{"get"}, f.client.calls)
		assert.Equal(t, 1, f.closes)
	}
}

func TestDoctorHumanReadableFixUsesUpdatedMappings(t *testing.T) {
	f := newDefaultsFixture(t)
	f.client.current = cuclient.Defaults{ModelDeployments: map[string]string{}}
	f.models["text-embedding-3-large"] = "fixed-embedding"
	stdout, stderr, err := f.execute(t.Context(), "doctor", "--fix-defaults", "--no-prompt")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "gpt-5.2: environment-deployment")
	assert.Contains(t, stdout, "text-embedding-3-large: fixed-embedding")
	assert.Contains(t, stdout, "[OK] Generative readiness: ready")
	assert.Equal(t, []string{"get", "update"}, f.client.calls)
	assert.Equal(t, 1, f.closes)
}

func TestDoctorNoneRetainsChecksAndErrors(t *testing.T) {
	for _, mode := range []string{"healthy", "incomplete", "failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newDefaultsFixture(t)
			switch mode {
			case "incomplete":
				f.client.current = cuclient.Defaults{ModelDeployments: map[string]string{}}
			case "failure":
				f.client.getErr = &cuclient.Error{Kind: cuclient.ErrorAuthentication,
					Code: "Unauthorized", Message: "Authentication failed.", StatusCode: 401}
			}
			stdout, stderr, err := f.execute(t.Context(), "doctor", "--output", "none")
			if mode == "failure" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
			assert.Equal(t, []string{"get"}, f.client.calls)
			assert.Equal(t, 1, f.closes)
		})
	}
}

func TestDoctorEndpointOutputRedactsCredentials(t *testing.T) {
	for _, format := range []string{"default", "json", "none"} {
		t.Run(format, func(t *testing.T) {
			f := newDefaultsFixture(t)
			f.endpoint = "https://private-user:private-pass@configured.example?sig=private-sig#private-fragment"
			stdout, stderr, err := f.execute(t.Context(), "doctor", "--output", format)
			require.NoError(t, err)
			assert.Empty(t, stderr)
			if format == "none" {
				assert.Empty(t, stdout)
			} else {
				assert.Contains(t, stdout, "https://configured.example")
			}
			for _, secret := range []string{"private-user", "private-pass", "private-sig", "private-fragment"} {
				assert.NotContains(t, stdout, secret)
			}
		})
	}
}

func TestDoctorHelpExplainsOutputFormats(t *testing.T) {
	f := newDefaultsFixture(t)
	stdout, stderr, err := f.execute(t.Context(), "doctor", "--help")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "default (human-readable)")
	assert.Contains(t, stdout, "azd ai cu doctor --output json")
	assert.NotContains(t, stdout, "default (complete JSON)")
	assert.Zero(t, f.opens)
}

func TestDoctorReportsEffectiveSettingSources(t *testing.T) {
	f := newDefaultsFixture(t)
	f.analyzer = "prebuilt-invoice"
	f.sources = map[string]string{
		"CU_ENDPOINT": "user config", "CU_API_VERSION": "built-in", "CU_DEFAULT_ANALYZER": "azd environment: dev",
	}
	stdout, stderr, err := f.execute(t.Context(), "doctor", "--output", "json")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	var result doctorResult
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	assert.Equal(t, f.sources, result.Sources)
	require.NotNil(t, result.Analyzer)
	assert.Equal(t, "prebuilt-invoice", *result.Analyzer)
}

func TestDoctorMissingGenerativeDefaultsIsNotConnectivityFailure(t *testing.T) {
	for _, notSet := range []bool{false, true} {
		f := newDefaultsFixture(t)
		f.client.current = cuclient.Defaults{ModelDeployments: map[string]string{}}
		if notSet {
			f.client.getErr = &cuclient.Error{Kind: cuclient.ErrorNotFound, Code: "DefaultsNotSet", StatusCode: 404}
		}
		stdout, stderr, err := f.execute(t.Context(), "doctor", "--output", "json")
		require.NoError(t, err)
		assert.Empty(t, stderr)
		var result doctorResult
		require.NoError(t, json.Unmarshal([]byte(stdout), &result))
		assert.False(t, result.Ready)
		assert.Len(t, result.Missing, 3)
		assert.Empty(t, result.Failures)
		assert.Equal(t, []string{"get"}, f.client.calls)
		assert.NotNil(t, result.Models)
	}
}

func TestDoctorFixUsesResolvedEnvironmentMappingsAndServiceResponse(t *testing.T) {
	f := newDefaultsFixture(t)
	f.analyzer = "prebuilt-invoice"
	before := defaults.Merge(nil, f.models)
	stdout, stderr, err := f.execute(t.Context(), "doctor", "--fix-defaults", "--no-prompt",
		"-e", "selected", "--output", "json")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, []string{"get", "update"}, f.client.calls)
	assert.Equal(t, before, f.models)
	assert.Equal(t, "environment-deployment", f.client.updates[0].ModelDeployments[defaults.Completion])
	assert.Equal(t, "embedding", f.client.updates[0].ModelDeployments[defaults.Embedding])
	var result doctorResult
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	assert.True(t, result.Ready)
	assert.Equal(t, "selected", result.Profile)
	require.NotNil(t, result.Analyzer)
	assert.Equal(t, "prebuilt-invoice", *result.Analyzer)
	f = newDefaultsFixture(t)
	_, _, err = f.execute(t.Context(), "doctor", "--fix-defaults", "-o", "none")
	require.NoError(t, err)
	assert.Len(t, f.client.updates, 1)
}

func TestDoctorFixWithUnsetDefaultsAndEmptyMappings(t *testing.T) {
	for _, hasModels := range []bool{false, true} {
		f := newDefaultsFixture(t)
		f.client.getErr = &cuclient.Error{Kind: cuclient.ErrorNotFound, Code: "DefaultsNotSet"}
		f.client.current = cuclient.Defaults{}
		if !hasModels {
			f.models = nil
		}
		stdout, stderr, err := f.execute(t.Context(), "doctor", "--fix-defaults", "--output", "json")
		assert.Empty(t, stderr)
		if hasModels {
			require.NoError(t, err)
			assert.Len(t, f.client.updates, 1)
			assert.Contains(t, stdout, `"ready": false`, "a completion mapping alone is not generative readiness")
		} else {
			require.Error(t, err)
			assert.Empty(t, stdout)
			assert.Empty(t, f.client.updates)
		}
	}
}

func TestDoctorRequiredFailuresAreErrorsAndRedactCauses(t *testing.T) {
	for _, failure := range []error{
		&cuclient.Error{Kind: cuclient.ErrorAuthentication, Code: "Unauthorized", Message: "Authentication failed.",
			Cause: errors.New("Bearer private-auth-token")},
		&cuclient.Error{
			Kind: cuclient.ErrorNotFound, Code: "ResourceNotFound", Message: "Resource missing.", StatusCode: 404,
		},
		&cuclient.Error{Kind: cuclient.ErrorThrottled, Code: "TooManyRequests", Message: "The service is throttled."},
		errors.New("private token=private"),
		context.DeadlineExceeded,
	} {
		f := newDefaultsFixture(t)
		f.client.getErr = failure
		stdout, stderr, err := f.execute(t.Context(), "doctor", "--fix-defaults")
		require.Error(t, err)
		assert.Empty(t, stdout)
		assert.Empty(t, stderr)
		assert.NotContains(t, err.Error(), "private")
		assert.Empty(t, f.client.updates)
		assert.Equal(t, 1, f.closes)
	}
	f := newDefaultsFixture(t)
	f.client.updateErr = errors.New("private write failure")
	stdout, _, err := f.execute(t.Context(), "doctor", "--fix-defaults")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "private")
	assert.Empty(t, stdout)
}

func TestDoctorSetupErrorsDistinguishContextAndInvalidServiceSettings(t *testing.T) {
	for _, test := range []struct {
		code, suggestion string
	}{
		{exterrors.CodeMissingTenant, "AZURE_TENANT_ID"},
		{exterrors.CodeEnvironmentUnavailable, "azd env list/select"},
		{"missing_endpoint", "azd env set"},
		{"invalid_endpoint", "azd env set"},
		{"unsupported_api_version", "azd ai cu config set"},
	} {
		t.Run(test.code, func(t *testing.T) {
			f := newDefaultsFixture(t)
			f.openErr = &azdext.LocalError{Code: test.code, Message: "Safe configuration failure."}
			stdout, stderr, err := f.execute(t.Context(), "doctor")
			require.Error(t, err)
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Contains(t, local.Suggestion, test.suggestion)
			assert.NotContains(t, local.Suggestion, "CU profile")
			assert.Empty(t, f.client.calls)
		})
	}
}

func TestDoctorActualProjectlessConfigurationErrorsNeverReachService(t *testing.T) {
	for _, test := range []struct {
		name, endpoint, tenant, version, key, code string
	}{
		{"invalid endpoint", "http://configured.example?sig=private", "tenant", "", "", "invalid_endpoint"},
		{"credential-bearing endpoint", "https://private-user:private-pass@configured.example?sig=private#private",
			"tenant", "", "", "invalid_endpoint"},
		{"invalid version", "https://configured.example", "tenant", "private", "", "unsupported_api_version"},
		{"blocked key", "https://configured.example", "tenant", "", "private-key", "unsupported_authentication"},
	} {
		t.Run(test.name, func(t *testing.T) {
			startHost(t, &deploymentServer{get: func(context.Context) (*azdext.GetDeploymentContextResponse, error) {
				t.Error("projectless diagnostics must not read default deployment context")
				return nil, errors.New("unexpected deployment call")
			}})
			t.Setenv("CU_ENDPOINT", test.endpoint)
			if test.tenant != "" {
				t.Setenv("AZURE_TENANT_ID", test.tenant)
			}
			if test.key != "" {
				t.Setenv("CU_API_KEY", test.key)
			}
			args := []string{"doctor"}
			if test.version != "" {
				args = append(args, "--api-version", test.version)
			}
			stdout, stderr, err := executeRoot(t, args...)
			require.Error(t, err)
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
			assert.NotContains(t, err.Error(), "private")
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, test.code, local.Code)
		})
	}
}

func TestDoctorCancellationAndMalformedDefaults(t *testing.T) {
	for _, mode := range []string{"before open", "during open", "during get", "malformed", "open failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newDefaultsFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "before open":
				cancel()
			case "during open":
				f.onOpen = cancel
			case "during get":
				f.client.onGet = cancel
			case "malformed":
				f.client.current = cuclient.Defaults{}
			case "open failure":
				f.openErr = errors.New("private host detail")
			}
			stdout, stderr, err := f.execute(ctx, "doctor", "--fix-defaults")
			require.Error(t, err)
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
			assert.Empty(t, f.client.updates)
			assert.NotContains(t, err.Error(), "private")
		})
	}
}
