// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package defaults

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"testing"

	"azure.ai.content-understanding/internal/cuclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseModels(t *testing.T) {
	mapping, err := ParseModels([]string{" gpt-5.2 = first ", "gpt-5.2=last", "embedding=deployment=variant"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"gpt-5.2": "last", "embedding": "deployment=variant"}, mapping)
	empty, err := ParseModels(nil)
	require.NoError(t, err)
	assert.NotNil(t, empty)
	for _, raw := range []string{"", "private-value", "=private-value", "model=", " = "} {
		_, err := ParseModels([]string{raw})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private-value")
	}
}

func TestAliasesPreferencesFallbackAndInputOwnership(t *testing.T) {
	for _, test := range []struct {
		name    string
		input   map[string]string
		aliases map[string]string
	}{
		{"preferred", map[string]string{
			"gpt-4.1": "older", "gpt-5.2": "full", "gpt-4o-mini": "older-mini",
			"gpt-4.1-mini": "middle-mini", "gpt-5.2-mini": "mini",
			"text-embedding-3-large": "large", "text-embedding-3-small": "small",
			Completion: "stale", CompletionMini: "stale", Embedding: "stale",
		}, map[string]string{Completion: "full", CompletionMini: "mini", Embedding: "large"}},
		{"sorted fallback", map[string]string{
			"z-model": "z", "a-model": "a", "z-mini": "zm", "a-mini": "am",
			"text-embedding-z": "ez", "text-embedding-a": "ea",
		}, map[string]string{Completion: "a", CompletionMini: "am", Embedding: "ea"}},
		{"middle mini", map[string]string{
			"gpt-4o-mini": "older", "gpt-4.1-mini": "newer",
		}, map[string]string{Completion: "newer", CompletionMini: "newer"}},
		{"only mini", map[string]string{
			"gpt-4o-mini": "mini",
		}, map[string]string{Completion: "mini", CompletionMini: "mini"}},
		{"only full", map[string]string{
			"gpt-4.1": "full",
		}, map[string]string{Completion: "full", CompletionMini: "full"}},
		{"aliases only", map[string]string{
			Completion: "custom", CompletionMini: "custom-mini", Embedding: "custom-embedding",
		}, map[string]string{Completion: "custom", CompletionMini: "custom-mini", Embedding: "custom-embedding"}},
		{"empty", map[string]string{}, map[string]string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := maps.Clone(test.input)
			after := WithAliases(test.input)
			assert.Equal(t, before, test.input)
			for key, value := range test.aliases {
				assert.Equal(t, value, after[key])
			}
			if len(test.aliases) == 0 {
				assert.Empty(t, after)
			}
		})
	}
}

func TestMissingRequirements(t *testing.T) {
	assert.Len(t, MissingRequirements(nil), 3)
	assert.Len(t, MissingRequirements(map[string]string{"gpt-5.2": "deployment"}), 2)
	assert.Len(t, MissingRequirements(map[string]string{CompletionMini: "mini", Embedding: "embedding"}), 1)
	assert.Empty(t, MissingRequirements(map[string]string{
		Completion: "completion", CompletionMini: "mini", Embedding: "embedding",
	}))
	assert.Empty(t, MissingRequirements(WithAliases(map[string]string{
		"gpt-5.2": "deployment", "text-embedding-3-large": "embedding",
	})))
}

type fakeClient struct {
	cuclient.Client
	current  cuclient.Defaults
	getErr   error
	writeErr error
	read     int
	writes   []cuclient.UpdateDefaultsRequest
	onRead   func()
	result   *cuclient.Defaults
}

func (f *fakeClient) GetDefaults(context.Context) (cuclient.Defaults, error) {
	f.read++
	if f.onRead != nil {
		f.onRead()
	}
	return f.current, f.getErr
}

func (f *fakeClient) UpdateDefaults(
	_ context.Context, request cuclient.UpdateDefaultsRequest,
) (cuclient.Defaults, error) {
	f.writes = append(f.writes, request)
	if f.result != nil {
		return *f.result, f.writeErr
	}
	return cuclient.Defaults{ModelDeployments: maps.Clone(request.ModelDeployments)}, f.writeErr
}

func TestApplyMergeReplaceAndResponseRetention(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprint(replace), func(t *testing.T) {
			current := map[string]string{"gpt-4.1": "old", "text-embedding-3-large": "embedding"}
			client := &fakeClient{current: cuclient.Defaults{ModelDeployments: current}}
			result, err := Apply(t.Context(), client, map[string]string{"gpt-4.1": "new"}, replace)
			require.NoError(t, err)
			require.Len(t, client.writes, 1)
			assert.Equal(t, "new", client.writes[0].ModelDeployments[Completion])
			assert.Equal(t, "new", client.writes[0].ModelDeployments[CompletionMini])
			if replace {
				assert.Zero(t, client.read)
				assert.NotContains(t, result.ModelDeployments, Embedding)
			} else {
				assert.Equal(t, 1, client.read)
				assert.Equal(t, "embedding", result.ModelDeployments[Embedding])
			}
			assert.Equal(t, "old", current["gpt-4.1"])
		})
	}
}

func TestOnlyExplicitDefaultsNotSetAllowsInitialization(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		init bool
	}{
		{"not set", &cuclient.Error{Kind: cuclient.ErrorNotFound, Code: "DefaultsNotSet", StatusCode: 404}, true},
		{"validation", &cuclient.Error{Kind: cuclient.ErrorValidation, Code: "DefaultsNotSet", StatusCode: 400}, true},
		{"wrapped", fmt.Errorf("wrapped: %w", &cuclient.Error{Kind: cuclient.ErrorService, Code: "DefaultsNotSet"}), true},
		{"not found", &cuclient.Error{Kind: cuclient.ErrorNotFound, Code: "ResourceNotFound", StatusCode: 404}, false},
		{"auth", &cuclient.Error{Kind: cuclient.ErrorAuthentication, Code: "DefaultsNotSet", StatusCode: 401}, false},
		{"wrong kind", &cuclient.Error{Kind: cuclient.ErrorAuthentication, Code: "DefaultsNotSet"}, false},
		{"server", &cuclient.Error{Kind: cuclient.ErrorService, Code: "DefaultsNotSet", StatusCode: 500}, false},
		{"text only", errors.New("DefaultsNotSet secret"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeClient{getErr: test.err}
			_, err := Apply(t.Context(), client, map[string]string{"gpt-5.2": "model"}, false)
			assert.Equal(t, test.init, IsNotSet(test.err))
			if test.init {
				require.NoError(t, err)
				assert.Len(t, client.writes, 1)
			} else {
				require.ErrorIs(t, err, test.err)
				assert.Empty(t, client.writes)
			}
		})
	}
}

func TestApplyInvalidResponsesErrorsAndCancellation(t *testing.T) {
	models := map[string]string{"model": "deployment"}
	for _, current := range []cuclient.Defaults{
		{}, {ModelDeployments: map[string]string{"model": ""}},
		{ModelDeployments: models, JSON: json.RawMessage(`null`)},
		{ModelDeployments: models, JSON: json.RawMessage(`{"modelDeployments":{"other":"model"}}`)},
	} {
		client := &fakeClient{current: current}
		_, err := Apply(t.Context(), client, models, false)
		require.Error(t, err)
		assert.Empty(t, client.writes)
	}
	client := &fakeClient{current: cuclient.Defaults{ModelDeployments: models}}
	_, err := Apply(t.Context(), client, nil, false)
	require.Error(t, err)
	assert.Zero(t, client.read)
	_, err = Apply(t.Context(), client, map[string]string{"model": ""}, false)
	require.Error(t, err)
	assert.Zero(t, client.read)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client.onRead = cancel
	_, err = Apply(ctx, client, models, false)
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, client.writes)
	_, err = Apply(ctx, client, models, false)
	require.ErrorIs(t, err, context.Canceled)
	client.writeErr = context.DeadlineExceeded
	_, err = Apply(t.Context(), client, models, true)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	client.writeErr, client.result = nil, &cuclient.Defaults{}
	_, err = Apply(t.Context(), client, models, true)
	require.Error(t, err)
}

func TestFullDefaultsJSON(t *testing.T) {
	raw := json.RawMessage(`{"modelDeployments":{"model":"deployment"},"future":{"count":9007199254740993}}`)
	result, err := JSON(cuclient.Defaults{ModelDeployments: map[string]string{"model": "deployment"}, JSON: raw})
	require.NoError(t, err)
	assert.Equal(t, raw, result)
	result, err = JSON(cuclient.Defaults{ModelDeployments: map[string]string{}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"modelDeployments":{}}`, string(result))
	assert.False(t, IsNotSet(nil))
	assert.Equal(t, map[string]string{"model": "explicit"},
		Merge(map[string]string{"model": "environment"}, map[string]string{"model": "explicit"}))
}
