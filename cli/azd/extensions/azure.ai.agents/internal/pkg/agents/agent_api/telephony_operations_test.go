// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type telephonyContractTransport struct {
	do       func(*http.Request) (*http.Response, error)
	requests int
}

func (t *telephonyContractTransport) Do(req *http.Request) (*http.Response, error) {
	t.requests++
	return t.do(req)
}

func telephonyResponse(req *http.Request, status int, body string, headers http.Header) *http.Response {
	canonicalHeaders := http.Header{}
	for name, values := range headers {
		for _, value := range values {
			canonicalHeaders.Add(name, value)
		}
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     canonicalHeaders,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func newTelephonyContractClient(
	t *testing.T,
	do func(*http.Request) (*http.Response, error),
) (*AgentClient, *telephonyContractTransport) {
	t.Helper()
	transport := &telephonyContractTransport{do: do}
	return newTestClient("https://test.example.com/api/projects/proj", transport), transport
}

func requireJSONBody(t *testing.T, req *http.Request, expected string) {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.JSONEq(t, expected, string(body))
}

func TestListTelephonyBindings_Contract(t *testing.T) {
	client, transport := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "/api/projects/proj/agents/voice%20agent%2Bteam/telephony/bindings", req.URL.EscapedPath())
		require.Equal(t, map[string][]string{
			"after":       {"opaque/after+cursor"},
			"api-version": {AgentEndpointAPIVersion},
			"limit":       {"25"},
			"order":       {"desc"},
			"provider":    {TelephonyProviderTwilio},
			"status":      {"active"},
		}, map[string][]string(req.URL.Query()))
		require.Empty(t, req.Header.Get("Foundry-Features"))
		require.Nil(t, req.Body)
		return telephonyResponse(req, http.StatusOK, `{
			"data":[{"id":"binding-1","provider":"twilio","connection_name":"conn","status":"active","incoming_call_url":"https://example.test/call","phone_number":"+14255550123","etag":"\"list-etag\""}],
			"first_id":"binding-1","last_id":"binding-9","has_more":true
		}`, nil), nil
	})

	page, err := client.ListTelephonyBindings(t.Context(), "voice agent+team", TelephonyBindingListOptions{
		Provider: TelephonyProviderTwilio,
		Status:   "active",
		Limit:    25,
		Order:    "desc",
		After:    "opaque/after+cursor",
	})

	require.NoError(t, err)
	require.Equal(t, "binding-1", page.FirstID)
	require.Equal(t, "binding-9", page.LastID)
	require.True(t, page.HasMore)
	require.Len(t, page.Data, 1)
	require.Equal(t, "+14255550123", page.Data[0].PhoneNumber)
	require.Equal(t, 1, transport.requests)
}

func TestListTelephonyBindings_ToleratesMissingStatusDuringRollout(t *testing.T) {
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		return telephonyResponse(req, http.StatusOK,
			`{"data":[{"id":"binding","provider":"twilio","connection_name":"conn","incoming_call_url":"https://example.test/call","phone_number":"+14255550123","etag":"\"etag\""}],"has_more":false}`, nil), nil
	})
	page, err := client.ListTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{})
	require.NoError(t, err)
	require.Empty(t, page.Data[0].Status)
}

func TestListTelephonyBindings_BeforeCursorAndOmittedOptions(t *testing.T) {
	t.Run("before cursor", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			require.Equal(t, "before-id", req.URL.Query().Get("before"))
			require.Empty(t, req.URL.Query().Get("after"))
			return telephonyResponse(req, http.StatusOK, `{"data":[],"has_more":false}`, nil), nil
		})
		_, err := client.ListTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{Before: "before-id"})
		require.NoError(t, err)
	})

	t.Run("omitted options", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			require.Equal(t, map[string][]string{"api-version": {AgentEndpointAPIVersion}}, map[string][]string(req.URL.Query()))
			return telephonyResponse(req, http.StatusOK, `{"data":[],"has_more":false}`, nil), nil
		})
		_, err := client.ListTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{})
		require.NoError(t, err)
	})
}

func TestListAllTelephonyBindings_UsesLastIDAsAfterCursor(t *testing.T) {
	requests := 0
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			require.Empty(t, req.URL.Query().Get("after"))
			return telephonyResponse(req, http.StatusOK,
				`{"data":[{"id":"one","provider":"twilio","connection_name":"conn","status":"active","incoming_call_url":"https://example.test/one","phone_number":"+14255550123","etag":"\"one\""}],"last_id":"cursor-one","has_more":true}`, nil), nil
		}
		require.Equal(t, "cursor-one", req.URL.Query().Get("after"))
		return telephonyResponse(req, http.StatusOK,
			`{"data":[{"id":"two","provider":"twilio","connection_name":"conn","status":"active","incoming_call_url":"https://example.test/two","phone_number":"+14255550124","etag":"\"two\""}],"last_id":"cursor-two","has_more":false}`, nil), nil
	})

	bindings, err := client.ListAllTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{Limit: 1})
	require.NoError(t, err)
	require.Equal(t, []string{"one", "two"}, []string{bindings[0].ID, bindings[1].ID})
	require.Equal(t, 2, requests)
}

func TestListAllTelephonyBindings_RejectsUnsafePaging(t *testing.T) {
	t.Run("before cursor", func(t *testing.T) {
		client, transport := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			t.Fatal("unexpected request")
			return nil, nil
		})
		_, err := client.ListAllTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{Before: "cursor"})
		require.EqualError(t, err, "before is not supported when listing all telephony bindings")
		require.Zero(t, transport.requests)
	})

	t.Run("repeated cursor", func(t *testing.T) {
		client, transport := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusOK,
				`{"data":[],"last_id":"same","has_more":true}`, nil), nil
		})
		_, err := client.ListAllTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{After: "same"})
		require.ErrorContains(t, err, "repeated last_id")
		require.Equal(t, 1, transport.requests)
	})
}

func TestGetGATelephonyBinding_EscapesOpaqueIDAndReturnsETag(t *testing.T) {
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "/api/projects/proj/agents/agent/telephony/bindings/id%2Fwith%2Bplus%3Fand%25percent", req.URL.EscapedPath())
		require.Empty(t, req.Header.Get("Foundry-Features"))
		return telephonyResponse(req, http.StatusOK,
			`{"id":"id/with+plus?and%percent","provider":"twilio","connection_name":"conn","status":"active","incoming_call_url":"https://example.test/call","phone_number":"+14255550123"}`,
			http.Header{"ETag": {`"binding-etag"`}}), nil
	})

	binding, err := client.GetGATelephonyBinding(t.Context(), "agent", "id/with+plus?and%percent")

	require.NoError(t, err)
	require.Equal(t, "id/with+plus?and%percent", binding.ID)
	require.Equal(t, `"binding-etag"`, binding.ETag)
}

func TestCreateGATelephonyBinding_ProviderBodiesAndRepeatability(t *testing.T) {
	firstSent := time.Date(2026, time.October, 13, 10, 11, 12, 0, time.FixedZone("offset", 2*60*60))
	tests := []struct {
		name     string
		request  TelephonyBindingCreateRequest
		expected string
	}{
		{
			name: "Twilio",
			request: &CreateTwilioTelephonyBindingRequest{
				ConnectionName: "twilio-conn",
				PhoneNumber:    "+14255550123", Label: "Support",
			},
			expected: `{"provider":"twilio","connection_name":"twilio-conn","phone_number":"+14255550123","label":"Support"}`,
		},
		{
			name: "Teams",
			request: &CreateTeamsPhoneTelephonyBindingRequest{
				ConnectionName:          "teams-conn",
				ResourceAccountObjectID: "00000000-0000-0000-0000-000000000001",
				PhoneNumber:             "+14255550124", Label: "Sales",
			},
			expected: `{"provider":"teams_phone_extensibility","connection_name":"teams-conn","resource_account_object_id":"00000000-0000-0000-0000-000000000001","phone_number":"+14255550124","label":"Sales"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodPost, req.Method)
				require.Equal(t, "/api/projects/proj/agents/agent/telephony/bindings", req.URL.Path)
				require.Equal(t, "application/json", req.Header.Get("Content-Type"))
				require.Equal(t, "request-id", req.Header.Get("Repeatability-Request-ID"))
				require.Equal(t, "Tue, 13 Oct 2026 08:11:12 GMT", req.Header.Get("Repeatability-First-Sent"))
				require.Empty(t, req.Header.Get("Foundry-Features"))
				requireJSONBody(t, req, tt.expected)
				return telephonyResponse(req, http.StatusCreated,
					`{"id":"created","provider":"twilio","connection_name":"conn","status":"active","incoming_call_url":"https://example.test/call","phone_number":"+14255550123"}`,
					http.Header{"ETag": {`"created-etag"`}}), nil
			})

			binding, err := client.CreateGATelephonyBinding(t.Context(), "agent", tt.request, TelephonyRepeatabilityOptions{
				RequestID: "request-id", FirstSent: firstSent,
			})
			require.NoError(t, err)
			require.Equal(t, `"created-etag"`, binding.ETag)
		})
	}
}

func TestCreateGATelephonyBinding_RequiresExact201(t *testing.T) {
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		return telephonyResponse(req, http.StatusOK, `{"id":"wrong-status"}`, nil), nil
	})

	_, err := client.CreateGATelephonyBinding(t.Context(), "agent", &CreateTwilioTelephonyBindingRequest{}, TelephonyRepeatabilityOptions{})
	require.Error(t, err)
}

func TestCreateGATelephonyBinding_RetryReusesRepeatabilityHeaders(t *testing.T) {
	requests := 0
	var requestID, firstSent, body string
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		requests++
		payload, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		if requests == 1 {
			requestID = req.Header.Get("Repeatability-Request-ID")
			firstSent = req.Header.Get("Repeatability-First-Sent")
			body = string(payload)
			return telephonyResponse(req, http.StatusServiceUnavailable, `{"error":{"message":"retry"}}`, nil), nil
		}
		require.Equal(t, requestID, req.Header.Get("Repeatability-Request-ID"))
		require.Equal(t, firstSent, req.Header.Get("Repeatability-First-Sent"))
		require.JSONEq(t, body, string(payload))
		return telephonyResponse(req, http.StatusCreated,
			`{"id":"created","provider":"twilio","connection_name":"conn","status":"active","incoming_call_url":"https://example.test/call","phone_number":"+14255550123"}`,
			http.Header{"ETag": {`"etag"`}}), nil
	})

	_, err := client.CreateGATelephonyBinding(t.Context(), "agent", &CreateTwilioTelephonyBindingRequest{
		ConnectionName: "conn", PhoneNumber: "+14255550123",
	}, TelephonyRepeatabilityOptions{})
	require.NoError(t, err)
	require.Equal(t, 2, requests)
	require.NotEmpty(t, requestID)
	require.NotEmpty(t, firstSent)
}

func TestUpdateGATelephonyBinding_MergePatchIfMatchAndETag(t *testing.T) {
	status := "suspended"
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPatch, req.Method)
		require.Equal(t, "/api/projects/proj/agents/agent/telephony/bindings/binding%2F1", req.URL.EscapedPath())
		require.Equal(t, "application/merge-patch+json", req.Header.Get("Content-Type"))
		require.Equal(t, `"old-etag"`, req.Header.Get("If-Match"))
		requireJSONBody(t, req, `{"status":"suspended","label":"new label"}`)
		return telephonyResponse(req, http.StatusOK,
			`{"id":"binding/1","provider":"twilio","connection_name":"conn","status":"suspended","incoming_call_url":"https://example.test/call","phone_number":"+14255550123","label":"new label"}`,
			http.Header{"ETag": {`"new-etag"`}}), nil
	})

	binding, err := client.UpdateGATelephonyBinding(t.Context(), "agent", "binding/1", UpdateTelephonyBindingRequest{
		Status: &status, Label: NewNullableString("new label"),
	}, `"old-etag"`)

	require.NoError(t, err)
	require.Equal(t, "suspended", binding.Status)
	require.Equal(t, `"new-etag"`, binding.ETag)
}

func TestUpdateGATelephonyBinding_ExplicitNullClearsNullableFields(t *testing.T) {
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		requireJSONBody(t, req, `{"label":null,"phone_number":null}`)
		return telephonyResponse(req, http.StatusOK,
			`{"id":"binding","provider":"teams_phone_extensibility","connection_name":"conn","status":"active","incoming_call_url":"https://example.test/call","resource_account_object_id":"00000000-0000-0000-0000-000000000001"}`,
			http.Header{"ETag": {`"new-etag"`}}), nil
	})

	_, err := client.UpdateGATelephonyBinding(t.Context(), "agent", "binding", UpdateTelephonyBindingRequest{
		Label: &NullableString{}, PhoneNumber: &NullableString{},
	}, `"old-etag"`)
	require.NoError(t, err)
}

func TestDeleteGATelephonyBinding_Requires204AndSendsIfMatch(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodDelete, req.Method)
			require.Equal(t, `"etag"`, req.Header.Get("If-Match"))
			require.Nil(t, req.Body)
			return telephonyResponse(req, http.StatusNoContent, "", nil), nil
		})
		require.NoError(t, client.DeleteGATelephonyBinding(t.Context(), "agent", "binding", `"etag"`))
	})

	t.Run("other success status rejected", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusOK, "", nil), nil
		})
		require.Error(t, client.DeleteGATelephonyBinding(t.Context(), "agent", "binding", `"etag"`))
	})
}

func TestConditionalTelephonyWrites_AreNotRetried(t *testing.T) {
	tests := []struct {
		name string
		call func(*AgentClient) error
	}{
		{name: "patch", call: func(client *AgentClient) error {
			_, err := client.UpdateGATelephonyBinding(t.Context(), "agent", "binding", UpdateTelephonyBindingRequest{}, `"etag"`)
			return err
		}},
		{name: "delete", call: func(client *AgentClient) error {
			return client.DeleteGATelephonyBinding(t.Context(), "agent", "binding", `"etag"`)
		}},
		{name: "put transfer targets", call: func(client *AgentClient) error {
			_, err := client.ReplaceTelephonyTransferTargets(t.Context(), "agent", TelephonyTransferTargets{
				TransferTargets: []TelephonyTransferTarget{{
					Name: "teams", Description: "Teams desk",
					Destination: TelephonyTransferDestination{Kind: TelephonyTransferDestinationTeams, Value: "resource-account"},
				}},
			}, `"etag"`)
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, transport := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
				return telephonyResponse(req, http.StatusServiceUnavailable, `{"error":{"message":"retry"}}`, nil), nil
			})
			require.Error(t, tt.call(client))
			require.Equal(t, 1, transport.requests)
		})
	}
}

func TestTelephonyTransferTargets_GetAndReplaceContracts(t *testing.T) {
	t.Run("get", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodGet, req.Method)
			require.Equal(t, "/api/projects/proj/agents/agent/telephony/transfer_targets", req.URL.Path)
			return telephonyResponse(req, http.StatusOK,
				`{"transfer_targets":[{"name":"support","description":"Support desk","destination":{"kind":"pstn","value":"+14255550123"}}]}`,
				http.Header{"ETag": {`"targets-1"`}}), nil
		})
		targets, err := client.GetTelephonyTransferTargets(t.Context(), "agent")
		require.NoError(t, err)
		require.Equal(t, `"targets-1"`, targets.ETag)
		require.Equal(t, "+14255550123", targets.TransferTargets[0].Destination.Value)
	})

	t.Run("replace", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodPut, req.Method)
			require.Equal(t, "application/json", req.Header.Get("Content-Type"))
			require.Equal(t, `"targets-1"`, req.Header.Get("If-Match"))
			requireJSONBody(t, req, `{"transfer_targets":[{"name":"sales","description":"Sales desk","destination":{"kind":"sip","value":"sip:sales@example.test"}}]}`)
			return telephonyResponse(req, http.StatusOK,
				`{"transfer_targets":[{"name":"sales","description":"Sales desk","destination":{"kind":"sip","value":"sip:sales@example.test"}}]}`,
				http.Header{"ETag": {`"targets-2"`}}), nil
		})
		targets, err := client.ReplaceTelephonyTransferTargets(t.Context(), "agent", TelephonyTransferTargets{
			ETag: `"must-not-be-serialized"`,
			TransferTargets: []TelephonyTransferTarget{{
				Name: "sales", Description: "Sales desk",
				Destination: TelephonyTransferDestination{Kind: TelephonyTransferDestinationSIP, Value: "sip:sales@example.test"},
			}},
		}, `"targets-1"`)
		require.NoError(t, err)
		require.Equal(t, `"targets-2"`, targets.ETag)
	})
}

func TestGATelephonyOperations_RequiredInputs(t *testing.T) {
	client, transport := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP request")
		return nil, nil
	})
	ctx := t.Context()

	_, err := client.ListTelephonyBindings(ctx, "", TelephonyBindingListOptions{})
	require.EqualError(t, err, "agent name is required")
	_, err = client.GetGATelephonyBinding(ctx, "agent", "")
	require.EqualError(t, err, "binding ID is required")
	_, err = client.CreateGATelephonyBinding(ctx, "agent", nil, TelephonyRepeatabilityOptions{})
	require.EqualError(t, err, "binding request is required")
	var typedNil *CreateTwilioTelephonyBindingRequest
	_, err = client.CreateGATelephonyBinding(ctx, "agent", typedNil, TelephonyRepeatabilityOptions{})
	require.EqualError(t, err, "binding request is required")
	_, err = client.CreateGATelephonyBinding(ctx, "agent", &CreateTwilioTelephonyBindingRequest{}, TelephonyRepeatabilityOptions{})
	require.EqualError(t, err, "Twilio connection name and phone number are required")
	_, err = client.CreateGATelephonyBinding(ctx, "agent", &CreateTeamsPhoneTelephonyBindingRequest{}, TelephonyRepeatabilityOptions{})
	require.EqualError(t, err, "Teams connection name and resource account object ID are required")
	_, err = client.UpdateGATelephonyBinding(ctx, "agent", "", UpdateTelephonyBindingRequest{}, `"etag"`)
	require.EqualError(t, err, "binding ID is required")
	_, err = client.UpdateGATelephonyBinding(ctx, "agent", "binding", UpdateTelephonyBindingRequest{}, "")
	require.EqualError(t, err, "If-Match is required")
	invalidStatus := "inactive"
	_, err = client.UpdateGATelephonyBinding(ctx, "agent", "binding", UpdateTelephonyBindingRequest{Status: &invalidStatus}, `"etag"`)
	require.EqualError(t, err, "status must be active or suspended")
	require.EqualError(t, client.DeleteGATelephonyBinding(ctx, "agent", "", `"etag"`), "binding ID is required")
	require.EqualError(t, client.DeleteGATelephonyBinding(ctx, "agent", "binding", ""), "If-Match is required")
	_, err = client.GetTelephonyTransferTargets(ctx, "")
	require.EqualError(t, err, "agent name is required")
	_, err = client.ReplaceTelephonyTransferTargets(ctx, "agent", TelephonyTransferTargets{}, "")
	require.EqualError(t, err, "If-Match is required")
	require.Zero(t, transport.requests)
}

func TestGATelephonyOperations_RejectDotPathSegments(t *testing.T) {
	client, transport := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP request")
		return nil, nil
	})
	for _, value := range []string{".", ".."} {
		_, err := client.ListTelephonyBindings(t.Context(), value, TelephonyBindingListOptions{})
		require.ErrorContains(t, err, "cannot be dot path segments")
		_, err = client.GetGATelephonyBinding(t.Context(), "agent", value)
		require.ErrorContains(t, err, "cannot be dot path segments")
	}
	require.Zero(t, transport.requests)
}

func TestListTelephonyBindings_InvalidOptions(t *testing.T) {
	tests := []struct {
		name    string
		options TelephonyBindingListOptions
		err     string
	}{
		{name: "negative limit", options: TelephonyBindingListOptions{Limit: -1}, err: "limit must be between 1 and 100, or omitted"},
		{name: "limit too large", options: TelephonyBindingListOptions{Limit: 101}, err: "limit must be between 1 and 100, or omitted"},
		{name: "invalid order", options: TelephonyBindingListOptions{Order: "newest"}, err: "order must be asc or desc"},
		{name: "both cursors", options: TelephonyBindingListOptions{After: "a", Before: "b"}, err: "after and before cannot both be set"},
		{name: "invalid provider", options: TelephonyBindingListOptions{Provider: "sip"}, err: "provider must be twilio or teams_phone_extensibility"},
		{name: "invalid status", options: TelephonyBindingListOptions{Status: "inactive"}, err: "status must be active or suspended"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, transport := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
				t.Fatal("unexpected HTTP request")
				return nil, nil
			})
			_, err := client.ListTelephonyBindings(t.Context(), "agent", tt.options)
			require.EqualError(t, err, tt.err)
			require.Zero(t, transport.requests)
		})
	}
}

func TestGATelephonyOperations_ResponseFailures(t *testing.T) {
	t.Run("malformed response", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusOK, `{`, nil), nil
		})
		_, err := client.GetGATelephonyBinding(t.Context(), "agent", "binding")
		require.ErrorContains(t, err, "invalid telephony response JSON")
	})

	t.Run("missing response body", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusOK, "", nil), nil
		})
		_, err := client.ListTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{})
		require.EqualError(t, err, "telephony response body is required")
	})

	t.Run("error status", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusBadRequest, `{"error":{"message":"bad request"}}`, nil), nil
		})
		_, err := client.GetTelephonyTransferTargets(t.Context(), "agent")
		require.Error(t, err)
	})

	t.Run("trailing JSON", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusOK, `{"data":[],"has_more":false} {}`, nil), nil
		})
		_, err := client.ListTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{})
		require.ErrorContains(t, err, "multiple values")
	})

	t.Run("missing binding ETag", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusOK,
				`{"id":"binding","provider":"twilio","connection_name":"conn","status":"active","incoming_call_url":"https://example.test/call","phone_number":"+14255550123"}`, nil), nil
		})
		_, err := client.GetGATelephonyBinding(t.Context(), "agent", "binding")
		require.ErrorContains(t, err, "missing required ETag")
	})

	t.Run("missing list item ETag", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusOK,
				`{"data":[{"id":"binding","provider":"twilio","connection_name":"conn","status":"active","incoming_call_url":"https://example.test/call","phone_number":"+14255550123"}],"has_more":false}`, nil), nil
		})
		_, err := client.ListTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{})
		require.ErrorContains(t, err, "missing required ETag")
	})

	t.Run("unknown response status", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusOK,
				`{"data":[{"id":"binding","provider":"twilio","connection_name":"conn","status":"inactive","incoming_call_url":"https://example.test/call","phone_number":"+14255550123","etag":"\"etag\""}],"has_more":false}`, nil), nil
		})
		_, err := client.ListTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{})
		require.ErrorContains(t, err, "invalid status")
	})

	t.Run("transport error", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("network unavailable")
		})
		_, err := client.GetTelephonyTransferTargets(context.Background(), "agent")
		require.ErrorContains(t, err, "sending telephony request")
		require.ErrorContains(t, err, "network unavailable")
	})
}

func TestLegacyCreateTelephonyBinding_StillAccepts200AndUsesPreviewContract(t *testing.T) {
	client, transport := newCaptureClient(http.StatusOK, `{"id":"legacy","provider":"twilio"}`)

	_, err := client.CreateTelephonyBinding(t.Context(), "agent", &TelephonyBindingRequest{
		Provider: TelephonyProviderTwilio, Identifier: "+14255550123",
	}, TelephonyBindingAPIVersion, "regional.example.test")

	require.NoError(t, err)
	require.Len(t, transport.requests, 1)
	require.Equal(t, "/api/projects/proj/agents/agent/telephony", transport.requests[0].URL.Path)
	require.Equal(t, voiceAgentsPreviewFeature, transport.requests[0].Header.Get("Foundry-Features"))
	require.Equal(t, "regional.example.test", transport.requests[0].Header.Get("x-ms-overridden-host"))
}
