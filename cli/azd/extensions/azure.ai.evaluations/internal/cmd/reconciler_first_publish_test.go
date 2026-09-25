// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalEvaluatorFirstPublicationFromEmptyVersions(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, env, service, cfg, dir := newCatalogPinFixture(t)
			service.latest = ""
			cfg.Evaluators[0].Version = ""
			cfg.Evaluators[0].Source = "custom.json"
			require.NoError(t, os.WriteFile(filepath.Join(dir, "custom.json"),
				[]byte(`{"type":"rubric","dimensions":[{"id":"clarity","weight":5}]}`), 0o600))
			require.NoError(t, reconcileArtifactConfig(t, caller, ec, cfg, dir))
			require.NoError(t, reconcileArtifactConfig(t, caller, ec, cfg, dir))
			assert.Equal(t, 1, service.publishes, "only the first invocation may publish version 1")
			assert.Len(t, service.created, 1)
			assert.Equal(t, "1", env.stored(t, versionKey("evaluator", "custom")))
		})
	}
}

func TestAtomicEvaluatorCreateAcceptsEmptyVersionsButUpdateDoesNot(t *testing.T) {
	for _, verb := range []string{"create", "update"} {
		t.Run(verb, func(t *testing.T) {
			quickEvaluatorSettle(t)
			ec, _, service, _, _ := newCatalogPinFixture(t)
			service.latest = ""
			cmd := jsonCmd(t, "json")
			cmd.SetContext(t.Context())
			cmd.SetOut(new(bytes.Buffer))
			body, err := normalizeRubricBody("custom",
				[]byte(`{"type":"rubric","dimensions":[{"id":"clarity","weight":5}]}`))
			require.NoError(t, err)
			err = (&evaluatorWriteAction{cmd: cmd, verb: verb, name: "custom"}).write(t.Context(), ec, body)
			if verb == "create" {
				require.NoError(t, err)
				assert.Equal(t, 1, service.publishes)
			} else {
				require.Error(t, err)
				assert.Zero(t, service.publishes)
				assert.GreaterOrEqual(t, len(service.reads), evaluatorListingSettleAttempts)
			}
		})
	}
}

func TestFirstPublicationRequiresAValidCompleteVersionListing(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", http.StatusUnauthorized, ""},
		{"forbidden", http.StatusForbidden, ""},
		{"rate limited", http.StatusTooManyRequests, ""},
		{"service failure", http.StatusServiceUnavailable, ""},
		{"malformed", 0, `not JSON`},
		{"missing list", 0, `{}`},
		{"null list", 0, `{"value":null}`},
		{"missing version", 0, `{"value":[{"name":"custom"}]}`},
	}
	for _, caller := range []string{"create", "up"} {
		for _, tc := range cases {
			t.Run(caller+"/"+tc.name, func(t *testing.T) {
				ec, env, service, cfg, dir := newCatalogPinFixture(t)
				service.listStatus, service.listBody = tc.status, tc.body
				cfg.Evaluators[0].Version = ""
				cfg.Evaluators[0].Source = "custom.json"
				require.NoError(t, os.WriteFile(filepath.Join(dir, "custom.json"),
					[]byte(`{"type":"rubric","dimensions":[{"id":"clarity","weight":5}]}`), 0o600))
				require.Error(t, reconcileArtifactConfig(t, caller, ec, cfg, dir))
				assert.Zero(t, service.publishes)
				assert.Empty(t, service.created)
				assert.Empty(t, env.config)
			})
		}
	}
}

func TestRegisteredOnlyEvaluatorWithEmptyVersionsIsNotPublished(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, env, service, cfg, dir := newCatalogPinFixture(t)
			service.latest = ""
			cfg.Evaluators[0].Version = ""
			require.Error(t, reconcileArtifactConfig(t, caller, ec, cfg, dir))
			assert.Zero(t, service.publishes)
			assert.Empty(t, service.created)
			assert.Empty(t, env.config)
		})
	}
}
