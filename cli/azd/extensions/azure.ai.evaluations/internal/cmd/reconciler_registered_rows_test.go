// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisteredDatasetBindingsAreValidatedBeforeMutation(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		for _, tc := range []struct {
			name             string
			rows             string
			mapping          string
			credentialStatus int
			contentStatus    int
		}{
			{name: "required column missing", rows: `{"response":"answer"}`},
			{name: "required column missing later", rows: "{\"query\":\"first\"}\n{\"response\":\"second\"}"},
			{name: "explicit mapping missing", rows: `{"query":"hi"}`, mapping: "{{item.missing}}"},
			{name: "malformed later row", rows: "{\"query\":\"hi\"}\nnot JSON"},
			{name: "empty content", rows: "\n"},
			{name: "credentials denied", credentialStatus: http.StatusForbidden},
			{name: "content denied", contentStatus: http.StatusForbidden},
		} {
			t.Run(caller+"/"+tc.name, func(t *testing.T) {
				ec, env, service, cfg, dir := validationFixture(t)
				cfg.Datasets[0].File = ""
				cfg.Datasets[0].Version = "1.0"
				service.dataset = true
				service.registeredRows = tc.rows
				service.credentialStatus, service.contentStatus = tc.credentialStatus, tc.contentStatus
				if tc.mapping != "" {
					cfg.Evals[0].Evaluators[0].DataMapping = map[string]string{"query": tc.mapping}
				}
				require.Error(t, reconcileArtifactConfig(t, caller, ec, cfg, dir))
				for _, request := range service.requests {
					assert.True(t, strings.HasPrefix(request, "GET ") ||
						request == "POST /datasets/turn-tests/versions/1.0/credentials",
						"only content/credential reads are allowed, not publication: %s", request)
				}
				assert.Zero(t, service.createCount)
				assert.Empty(t, env.config)
				assert.Empty(t, env.values)
			})
		}
	}
}

func TestRegisteredDatasetValidationKeepsTheSettledVersion(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, env, service, cfg, dir := validationFixture(t)
			cfg.Datasets[0].File = ""
			service.dataset = true
			service.registeredRows = "{\"query\":\"first\"}\n{\"query\":\"second\"}\n"
			service.afterContentRead = func() { service.listedVersion = "2.0" }
			require.NoError(t, reconcileArtifactConfig(t, caller, ec, cfg, dir))
			assert.Equal(t, "1.0", env.stored(t, versionKey("dataset", "turn-tests")),
				"publication must not settle on a newer version than the rows preflight inspected")
			assert.Contains(t, service.requests, "POST /datasets/turn-tests/versions/1.0/credentials")
			assert.NotContains(t, service.requests, "POST /datasets/turn-tests/versions/2.0/credentials")
			assert.Equal(t, 1, service.createCount)
		})
	}
}

func TestRegisteredDatasetValidBindingsRemainIdempotent(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, env, service, cfg, dir := validationFixture(t)
			cfg.Datasets[0].File = ""
			cfg.Datasets[0].Version = "1.0"
			service.dataset = true
			service.registeredRows = "{\"query\":\"hi\",\"ground_truth\":\"yes\"}\n"
			cfg.Evals[0].Evaluators[0].DataMapping = map[string]string{"query": "{{item.ground_truth}}"}
			for range 2 {
				require.NoError(t, reconcileArtifactConfig(t, caller, ec, cfg, dir))
			}

			assert.Equal(t, 1, service.createCount)
			assert.Equal(t, "1.0", env.stored(t, versionKey("dataset", "turn-tests")))
			assert.Empty(t, env.stored(t, project.FingerprintKey("dataset", "turn-tests")),
				"inspecting a registered dataset does not publish a local file")
		})
	}
}

func TestRegisteredDatasetPreflightHonorsContentCancellation(t *testing.T) {
	ec, env, service, cfg, dir := validationFixture(t)
	cfg.Datasets[0].File = ""
	cfg.Datasets[0].Version = "1.0"
	service.dataset = true
	service.registeredRows = "{\"query\":\"hi\"}\n"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	service.afterContentRead = cancel
	err := (&evalReconciler{ec: ec}).Validate(ctx, cfg, dir)
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, env.config)
	assert.Zero(t, service.createCount)
}

func TestRegisteredDatasetPreservesSparseTargetInputRules(t *testing.T) {
	ec, _, service, cfg, dir := validationFixture(t)
	cfg.Datasets[0].File = ""
	cfg.Datasets[0].Version = "1.0"
	cfg.Evals[0].Target = &project.Target{Type: project.TargetTypeAgent, Name: "target"}
	service.dataset = true
	service.definition = `{"definition":{"data_schema":{"properties":{}}}}`
	service.registeredRows = "{\"query\":\"first\"}\n{\"response\":\"second\"}"
	require.NoError(t, reconcileArtifactConfig(t, "create", ec, cfg, dir))
	assert.Equal(t, 1, service.createCount)
}
