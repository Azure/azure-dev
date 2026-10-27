// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package analyzercopy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/cuclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type copyFake struct {
	cuclient.Client
	side     string
	calls    *[]string
	failures map[string]error
	source   json.RawMessage
	exists   bool
	result   cuclient.AnalyzerResponse
	grant    cuclient.GrantCopyAuthorizationRequest
	copy     cuclient.CopyAnalyzerRequest
	after    func(string)
}

func (f *copyFake) record(action string) error {
	key := f.side + ":" + action
	*f.calls = append(*f.calls, key)
	if f.after != nil {
		f.after(key)
	}
	return f.failures[key]
}

func (f *copyFake) GetAnalyzer(_ context.Context, id string) (cuclient.Analyzer, error) {
	if err := f.record("get:" + id); err != nil {
		return cuclient.Analyzer{}, err
	}
	if f.side == "source" {
		return cuclient.Analyzer{ID: id, JSON: f.source}, nil
	}
	if id == "destination" && !f.exists {
		return cuclient.Analyzer{}, &cuclient.Error{Kind: cuclient.ErrorNotFound, StatusCode: 404}
	}
	return cuclient.Analyzer{ID: id}, nil
}

func (f *copyFake) GrantCopyAuthorization(
	_ context.Context, request cuclient.GrantCopyAuthorizationRequest,
) (cuclient.CopyAuthorization, error) {
	f.grant = request
	return cuclient.CopyAuthorization{Source: "private-grant-secret"}, f.record("grant")
}

func (f *copyFake) CopyAnalyzer(
	_ context.Context, request cuclient.CopyAnalyzerRequest,
) (cuclient.AnalyzerResponse, error) {
	f.copy = request
	return f.result, f.record("copy")
}

func copyFixture() (*copyFake, *copyFake, *[]string) {
	calls := []string{}
	source := &copyFake{
		side: "source", calls: &calls, failures: map[string]error{}, source: json.RawMessage(`{"analyzerId":"source"}`),
	}
	destination := &copyFake{
		side: "destination", calls: &calls, failures: source.failures,
		result: cuclient.AnalyzerResponse{Analyzer: cuclient.Analyzer{
			ID: "destination", Status: "ready",
			JSON: json.RawMessage(`{"analyzerId":"destination","warnings":[{"code":"Notice"}],"unknown":9007199254740993}`),
		}},
	}
	return source, destination, &calls
}

func crossRequest() Request {
	return Request{
		SourceID: "source", DestinationID: "destination",
		SourceResourceID: "/source/account", SourceRegion: "westus",
		DestinationResourceID: "/destination/account", DestinationRegion: "eastus",
	}
}

func TestCopySameAndCrossResourceOrdering(t *testing.T) {
	for _, cross := range []bool{false, true} {
		t.Run(map[bool]string{false: "same", true: "cross"}[cross], func(t *testing.T) {
			source, destination, calls := copyFixture()
			request := Request{SourceID: "source", DestinationID: "destination"}
			if cross {
				request = crossRequest()
				source.source = json.RawMessage(`{"config":{"contentCategories":{"z":{"analyzerId":"z"},
					"a":{"analyzerId":"a"},"duplicate":{"analyzerId":"a"},"builtin":{"analyzerId":"prebuilt-document"}}}}`)
			}
			result, err := Copy(t.Context(), source, destination, request)
			require.NoError(t, err)
			assert.Equal(t, destination.result, result)
			assert.NotContains(t, string(result.Analyzer.JSON), "private-grant-secret")
			if cross {
				assert.Equal(t, []string{"source:get:source", "destination:get:a", "destination:get:z",
					"destination:get:destination", "source:grant", "destination:copy"}, *calls)
				assert.Equal(t, cuclient.GrantCopyAuthorizationRequest{
					AnalyzerID: "source", TargetAzureResourceID: request.DestinationResourceID, TargetRegion: "eastus",
				}, source.grant)
			} else {
				assert.Equal(t, []string{"source:get:source", "destination:get:destination", "destination:copy"}, *calls)
				assert.Empty(t, source.grant.AnalyzerID)
			}
			assert.Equal(t, cuclient.CopyAnalyzerRequest{
				SourceAnalyzerID: "source", DestinationAnalyzerID: "destination",
				SourceAzureResourceID: request.SourceResourceID, SourceRegion: request.SourceRegion,
			}, destination.copy)
		})
	}
}

func TestCopyFailuresStopBeforeLaterSideEffects(t *testing.T) {
	for _, test := range []struct {
		name, stage  string
		kind         cuclient.ErrorKind
		status       int
		expectedCode string
	}{
		{"source missing", "source:get:source", cuclient.ErrorNotFound, 404, "copy_source_not_found"},
		{"source auth", "source:get:source", cuclient.ErrorAuthentication, 401, "copy_source_authorization_failed"},
		{"dependency auth", "destination:get:dependency", cuclient.ErrorAuthentication,
			403, "copy_destination_authorization_failed"},
		{"dependency throttle", "destination:get:dependency", cuclient.ErrorThrottled, 429, "original"},
		{"destination auth", "destination:get:destination", cuclient.ErrorAuthentication,
			403, "copy_destination_authorization_failed"},
		{"destination throttle", "destination:get:destination", cuclient.ErrorThrottled, 429, "original"},
		{"destination service failure", "destination:get:destination", cuclient.ErrorService, 500, "original"},
		{"grant auth", "source:grant", cuclient.ErrorAuthentication, 403, "copy_source_authorization_failed"},
		{"grant failure", "source:grant", cuclient.ErrorService, 500, "original"},
		{"copy auth", "destination:copy", cuclient.ErrorAuthentication, 401, "copy_destination_authorization_failed"},
		{"copy race", "destination:copy", cuclient.ErrorConflict, 409, "copy_destination_exists"},
		{"copy throttle", "destination:copy", cuclient.ErrorThrottled, 429, "original"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, destination, calls := copyFixture()
			source.source = json.RawMessage(`{"config":{"contentCategories":[{"analyzerId":"dependency"}]}}`)
			original := &cuclient.Error{Kind: test.kind, Code: "original", StatusCode: test.status, Message: "safe original"}
			source.failures[test.stage] = original
			result, err := Copy(t.Context(), source, destination, crossRequest())
			require.Error(t, err)
			domain, ok := errors.AsType[*cuclient.Error](err)
			require.True(t, ok)
			assert.Equal(t, test.expectedCode, domain.Code)
			assert.Equal(t, test.stage, (*calls)[len(*calls)-1])
			assert.Empty(t, result.Analyzer.JSON)
			if test.stage == "destination:copy" && test.kind == cuclient.ErrorAuthentication {
				assert.Contains(t, domain.Suggestion, "expired")
			}
		})
	}
}

func TestCopyDependenciesAndExistingDestinationNeverGrantOrCopy(t *testing.T) {
	for _, mode := range []string{"missing dependencies", "existing destination", "not-found text is not absence"} {
		t.Run(mode, func(t *testing.T) {
			source, destination, calls := copyFixture()
			if mode == "missing dependencies" {
				source.source = json.RawMessage(`{"config":{"contentCategories":[{"analyzerId":"z"},{"analyzerId":"a"}]}}`)
				for _, id := range []string{"z", "a"} {
					source.failures["destination:get:"+id] = &cuclient.Error{Kind: cuclient.ErrorNotFound, StatusCode: 404}
				}
			} else if mode == "existing destination" {
				destination.exists = true
			} else {
				source.failures["destination:get:destination"] = errors.New("not found; private backend secret")
			}
			_, err := Copy(t.Context(), source, destination, crossRequest())
			require.Error(t, err)
			assert.NotContains(t, *calls, "source:grant")
			assert.NotContains(t, *calls, "destination:copy")
			if mode == "missing dependencies" {
				assert.Contains(t, err.Error(), "a, z")
				assert.NotContains(t, *calls, "destination:get:destination")
			}
		})
	}
}

func TestCopyCancellationAtEveryStage(t *testing.T) {
	for _, stage := range []string{"before", "source:get:source", "destination:get:dependency",
		"destination:get:destination", "source:grant", "destination:copy"} {
		t.Run(stage, func(t *testing.T) {
			source, destination, calls := copyFixture()
			source.source = json.RawMessage(`{"config":{"contentCategories":[{"analyzerId":"dependency"}]}}`)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			source.after = func(key string) {
				if key == stage {
					cancel()
				}
			}
			destination.after = source.after
			if stage == "before" {
				cancel()
			}
			_, err := Copy(ctx, source, destination, crossRequest())
			require.ErrorIs(t, err, context.Canceled)
			if stage == "before" {
				assert.Empty(t, *calls)
			} else {
				assert.Equal(t, stage, (*calls)[len(*calls)-1])
			}
		})
	}
}

func TestCopyInputValidationAndFailedResult(t *testing.T) {
	for _, request := range []Request{
		{SourceID: "source", DestinationID: "source"},
		{SourceID: "bad/id", DestinationID: "destination"},
		{SourceID: "source", DestinationID: "destination", SourceResourceID: "source"},
	} {
		source, destination, calls := copyFixture()
		_, err := Copy(t.Context(), source, destination, request)
		require.Error(t, err)
		assert.Empty(t, *calls)
	}
	source, destination, _ := copyFixture()
	destination.result.Analyzer.Status = "FAILED"
	_, err := Copy(t.Context(), source, destination, crossRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed status")
	source, destination, _ = copyFixture()
	destination.failures["destination:copy"] = &cuclient.Error{
		Kind: cuclient.ErrorService, Code: "analyzer_operation_failed", Message: "The analyzer operation failed.",
	}
	_, err = Copy(t.Context(), source, destination, crossRequest())
	require.Error(t, err)
	domain, ok := errors.AsType[*cuclient.Error](err)
	require.True(t, ok)
	assert.Equal(t, "copy_failed", domain.Code)
	assert.Contains(t, domain.Suggestion, "destination resource")
}

func TestCustomDependenciesWireShapesAndInvalidDefinitions(t *testing.T) {
	for _, raw := range []string{
		`{}`, `{"config":null}`, `{"config":{}}`, `{"config":{"contentCategories":null}}`,
		`{"config":{"contentCategories":[]}}`, `{"config":{"contentCategories":{}}}`,
		`{"config":{"contentCategories":[{},{"analyzerId":null},{"analyzerId":"prebuilt-document"}]}}`,
	} {
		got, err := customDependencies(json.RawMessage(raw))
		require.NoError(t, err)
		assert.Empty(t, got)
	}
	got, err := customDependencies(json.RawMessage(
		`{"config":{"content_categories":[{"analyzer_id":"b"},{"analyzer_id":"a"},{"analyzer_id":"a"}]}}`))
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, got)
	for _, raw := range []string{
		`null`, `[]`, `bad secret`, `{"config":[]}`, `{"config":{"contentCategories":"secret"}}`,
		`{"config":{"contentCategories":[null]}}`, `{"config":{"contentCategories":[{"analyzerId":42}]}}`,
		`{"config":{"contentCategories":[{"analyzerId":"bad/secret"}]}}`,
	} {
		_, err := customDependencies(json.RawMessage(raw))
		require.Error(t, err)
		assert.False(t, strings.Contains(err.Error(), "secret"))
	}
}
