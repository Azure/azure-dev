// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"azureaiagent/internal/pkg/recordproxy"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var testRepeatability = TelephonyRepeatabilityOptions{
	RequestID: "00000000-0000-0000-0000-000000000001",
	FirstSent: time.Date(2026, time.October, 13, 8, 11, 12, 0, time.UTC),
}

type telephonyContractTransport struct {
	do       func(*http.Request) (*http.Response, error)
	requests int
}

type cancelingResponseBody struct {
	cancel context.CancelFunc
}

func (b *cancelingResponseBody) Read([]byte) (int, error) {
	b.cancel()
	return 0, context.Canceled
}

func (*cancelingResponseBody) Close() error { return nil }

type countingResponseBody struct {
	remaining int64
	read      int64
	closed    bool
}

func (b *countingResponseBody) Read(buffer []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	count := min(int64(len(buffer)), b.remaining)
	for i := range count {
		buffer[i] = 'x'
	}
	b.remaining -= count
	b.read += count
	return int(count), nil
}

func (b *countingResponseBody) Close() error {
	b.closed = true
	return nil
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
			"api-version": {"v1"},
			"limit":       {"25"},
			"order":       {"desc"},
			"provider":    {TelephonyProviderTwilio},
			"status":      {"active"},
		}, map[string][]string(req.URL.Query()))
		require.Empty(t, req.Header.Get("Foundry-Features"))
		require.Nil(t, req.Body)
		return telephonyResponse(req, http.StatusOK, `{
			"data":[{
				"id":"binding-1","provider":"twilio","connection_name":"conn","status":"active",
				"incoming_call_url":"https://example.test/call","phone_number":"+14255550123",
				"etag":"\"list-etag\""
			}],
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
	require.NotNil(t, page.HasMore)
	require.True(t, *page.HasMore)
	require.Len(t, page.Data, 1)
	require.Equal(t, "+14255550123", page.Data[0].PhoneNumber)
	require.Equal(t, 1, transport.requests)
}

func TestListTelephonyBindings_DoesNotRetryTransientResponse(t *testing.T) {
	requests := 0
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		requests++
		return telephonyResponse(req, http.StatusServiceUnavailable, strings.Repeat("x", 128*1024), nil), nil
	})
	_, err := client.ListTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{})
	require.Error(t, err)
	require.Equal(t, 1, requests)
	require.NotContains(t, err.Error(), "xxxxx")
}

func TestTelephonyPipelineLimitsTransportReadsBeforeBodyDownload(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		maxRead int64
		call    func(*AgentClient) error
	}{
		{
			name: "success", status: http.StatusOK, maxRead: maxTelephonyResponseSize + 1,
			call: func(client *AgentClient) error {
				_, err := client.ListTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{})
				return err
			},
		},
		{
			name: "error", status: http.StatusBadRequest, maxRead: maxTelephonyErrorResponseSize + 1,
			call: func(client *AgentClient) error {
				_, err := client.ListTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{})
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &countingResponseBody{remaining: 8 * 1024 * 1024}
			client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: tt.status,
					Status:     http.StatusText(tt.status),
					Header:     http.Header{},
					Body:       body,
					Request:    req,
				}, nil
			})
			require.Error(t, tt.call(client))
			require.Equal(t, tt.maxRead, body.read)
			require.True(t, body.closed)
		})
	}
}

func TestTelephonyResponseLimitTransport_BypassesNoncanonicalAndHandlesNil(t *testing.T) {
	t.Run("noncanonical path", func(t *testing.T) {
		body := &countingResponseBody{remaining: 1024}
		transport := &telephonyResponseLimitTransport{next: &telephonyContractTransport{
			do: func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: body, Request: req}, nil
			},
		}}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
			"https://example.test/telephony/bindings-evil", nil)
		require.NoError(t, err)
		resp, err := transport.Do(req)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, resp.Body)
		require.NoError(t, err)
		require.Equal(t, int64(1024), body.read)
	})

	t.Run("nil response", func(t *testing.T) {
		transport := &telephonyResponseLimitTransport{next: &telephonyContractTransport{
			do: func(req *http.Request) (*http.Response, error) { return nil, nil },
		}}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
			"https://example.test/telephony/bindings", nil)
		require.NoError(t, err)
		resp, err := transport.Do(req)
		require.NoError(t, err)
		require.Nil(t, resp)
	})
}

func TestCanonicalTelephonyPath_ScansPastNamesContainingTelephony(t *testing.T) {
	for _, path := range []string{
		"/api/projects/telephony/agents/telephony/telephony/bindings",
		"/api/projects/telephony/agents/telephony/telephony/transfer_targets",
	} {
		require.True(t, isCanonicalTelephonyPath(path), path)
	}
	require.False(t, isCanonicalTelephonyPath("/api/projects/telephony/agents/telephony"))
	require.False(t, isCanonicalTelephonyPath("/telephony/bindings-evil"))
}

func TestCanonicalTelephonyClient_DoesNotFollowRedirects(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			requests := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests++
				if req.URL.Path == "/redirected" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.Header().Set("Location", serverURL(req)+"/redirected")
				w.WriteHeader(status)
			}))
			defer server.Close()

			originalTransport := recordproxy.Transport
			recordproxy.Transport = server.Client().Transport
			t.Cleanup(func() { recordproxy.Transport = originalTransport })
			client := NewAgentClient(server.URL+"/api/projects/proj", fakeCredential{})
			err := client.DeleteGATelephonyBinding(t.Context(), "agent", "binding", `"etag"`)
			require.Error(t, err)
			require.Equal(t, 1, requests)
		})
	}
}

func serverURL(req *http.Request) string {
	return "https://" + req.Host
}

func TestListTelephonyBindings_RejectsMissingStatus(t *testing.T) {
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		return telephonyResponse(req, http.StatusOK, `{
			"data":[{
				"id":"binding","provider":"twilio","connection_name":"conn",
				"incoming_call_url":"https://example.test/call","phone_number":"+14255550123",
				"etag":"\"etag\""
			}],"has_more":false}`, nil), nil
	})
	_, err := client.ListTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{})
	require.ErrorContains(t, err, "missing required status")
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
			require.Equal(t, map[string][]string{"api-version": {"v1"}}, map[string][]string(req.URL.Query()))
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
			return telephonyResponse(req, http.StatusOK, `{
				"data":[{"id":"one","provider":"twilio","connection_name":"conn","status":"active",
				"incoming_call_url":"https://example.test/one","phone_number":"+14255550123","etag":"\"one\""}],
				"last_id":"cursor-one","has_more":true}`, nil), nil
		}
		require.Equal(t, "cursor-one", req.URL.Query().Get("after"))
		return telephonyResponse(req, http.StatusOK, `{
			"data":[{"id":"two","provider":"twilio","connection_name":"conn","status":"active",
			"incoming_call_url":"https://example.test/two","phone_number":"+14255550124","etag":"\"two\""}],
			"last_id":"cursor-two","has_more":false}`, nil), nil
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
		require.Equal(t,
			"/api/projects/proj/agents/agent/telephony/bindings/id%2Fwith%2Bplus%3Fand%25percent",
			req.URL.EscapedPath())
		require.Empty(t, req.Header.Get("Foundry-Features"))
		return telephonyResponse(req, http.StatusOK, `{
			"id":"id/with+plus?and%percent","provider":"twilio","connection_name":"conn","status":"active",
			"incoming_call_url":"https://example.test/call","phone_number":"+14255550123"}`,
			http.Header{"ETag": {`"binding-etag"`}}), nil
	})

	binding, err := client.GetGATelephonyBinding(t.Context(), "agent", "id/with+plus?and%percent")

	require.NoError(t, err)
	require.Equal(t, "id/with+plus?and%percent", binding.ID)
	require.Equal(t, `"binding-etag"`, binding.ETag)
}

func TestGetGATelephonyBinding_RejectsMismatchedResponseID(t *testing.T) {
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		return telephonyResponse(req, http.StatusOK, `{
			"id":"other","provider":"twilio","connection_name":"conn","status":"active",
			"incoming_call_url":"https://example.test/call","phone_number":"+14255550123"}`,
			http.Header{"ETag": {`"etag"`}}), nil
	})
	_, err := client.GetGATelephonyBinding(t.Context(), "agent", "requested")
	require.ErrorContains(t, err, "does not match")
}

func TestCreateGATelephonyBinding_ProviderBodiesAndRepeatability(t *testing.T) {
	firstSent := time.Date(2026, time.October, 13, 10, 11, 12, 0, time.FixedZone("offset", 2*60*60))
	tests := []struct {
		name               string
		request            TelephonyBindingCreateRequest
		expected           string
		responseProvider   string
		responseConnection string
		responseFields     string
	}{
		{
			name: "Twilio",
			request: &CreateTwilioTelephonyBindingRequest{
				ConnectionName: "twilio-conn",
				PhoneNumber:    "+14255550123", Label: "Support",
			},
			expected: `{
				"provider":"twilio","connection_name":"twilio-conn",
				"phone_number":"+14255550123","label":"Support"}`,
			responseProvider:   "twilio",
			responseConnection: "twilio-conn",
			responseFields:     `"phone_number":"+14255550123"`,
		},
		{
			name: "Teams",
			request: &CreateTeamsPhoneTelephonyBindingRequest{
				ConnectionName:          "teams-conn",
				ResourceAccountObjectID: "00000000-0000-0000-0000-000000000001",
				PhoneNumber:             "+14255550124", Label: "Sales",
			},
			expected: `{
				"provider":"teams_phone_extensibility","connection_name":"teams-conn",
				"resource_account_object_id":"00000000-0000-0000-0000-000000000001",
				"phone_number":"+14255550124","label":"Sales"}`,
			responseProvider:   "teams_phone_extensibility",
			responseConnection: "teams-conn",
			responseFields:     `"resource_account_object_id":"00000000-0000-0000-0000-000000000001"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodPost, req.Method)
				require.Equal(t, "/api/projects/proj/agents/agent/telephony/bindings", req.URL.Path)
				require.Equal(t, "application/json", req.Header.Get("Content-Type"))
				require.Equal(t, "00000000-0000-0000-0000-000000000001",
					req.Header.Get("Repeatability-Request-ID"))
				require.Equal(t,
					firstSent.UTC().Format(http.TimeFormat),
					req.Header.Get("Repeatability-First-Sent"),
				)
				require.Empty(t, req.Header.Get("Foundry-Features"))
				requireJSONBody(t, req, tt.expected)
				response := fmt.Sprintf(`{
					"id":"created","provider":%q,"connection_name":%q,"status":"active",
					"incoming_call_url":"https://example.test/call",%s}`,
					tt.responseProvider, tt.responseConnection, tt.responseFields)
				return telephonyResponse(req, http.StatusCreated, response,
					http.Header{"ETag": {`"created-etag"`}}), nil
			})

			binding, err := client.CreateGATelephonyBinding(t.Context(), "agent", tt.request, TelephonyRepeatabilityOptions{
				RequestID: "00000000-0000-0000-0000-000000000001", FirstSent: firstSent,
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

	_, err := client.CreateGATelephonyBinding(t.Context(), "agent", &CreateTwilioTelephonyBindingRequest{
		ConnectionName: "conn", PhoneNumber: "+14255550123",
	}, testRepeatability)
	require.Error(t, err)
}

func TestCreateGATelephonyBinding_RejectsMismatchedResponseProvider(t *testing.T) {
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		return telephonyResponse(req, http.StatusCreated, `{
			"id":"created","provider":"teams_phone_extensibility","connection_name":"conn","status":"active",
			"incoming_call_url":"https://example.test/call",
			"resource_account_object_id":"00000000-0000-0000-0000-000000000001"}`,
			http.Header{"ETag": {`"etag"`}}), nil
	})

	_, err := client.CreateGATelephonyBinding(t.Context(), "agent", &CreateTwilioTelephonyBindingRequest{
		ConnectionName: "conn", PhoneNumber: "+14255550123",
	}, testRepeatability)
	var unknown *TelephonyWriteOutcomeUnknownError
	require.ErrorAs(t, err, &unknown)
}

func TestCreateGATelephonyBinding_RejectsMismatchedResponseIdentity(t *testing.T) {
	tests := []struct {
		name     string
		request  TelephonyBindingCreateRequest
		response string
	}{
		{
			name: "Twilio phone",
			request: &CreateTwilioTelephonyBindingRequest{
				ConnectionName: "conn", PhoneNumber: "+14255550123",
			},
			response: `{
				"id":"created","provider":"twilio","connection_name":"conn","status":"active",
				"incoming_call_url":"https://example.test/call","phone_number":"+14255550124"}`,
		},
		{
			name: "Teams resource account",
			request: &CreateTeamsPhoneTelephonyBindingRequest{
				ConnectionName:          "conn",
				ResourceAccountObjectID: "00000000-0000-0000-0000-000000000001",
			},
			response: `{
				"id":"created","provider":"teams_phone_extensibility","connection_name":"conn",
				"status":"active","incoming_call_url":"https://example.test/call",
				"resource_account_object_id":"00000000-0000-0000-0000-000000000002"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
				return telephonyResponse(req, http.StatusCreated, tt.response,
					http.Header{"ETag": {`"etag"`}}), nil
			})
			_, err := client.CreateGATelephonyBinding(
				t.Context(), "agent", tt.request, testRepeatability)
			var unknown *TelephonyWriteOutcomeUnknownError
			require.ErrorAs(t, err, &unknown)
		})
	}
}

func TestCreateGATelephonyBinding_DoesNotRetryTransientResponse(t *testing.T) {
	requests := 0
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		requests++
		return telephonyResponse(req, http.StatusServiceUnavailable,
			`{"error":{"message":"retry"}}`, nil), nil
	})

	_, err := client.CreateGATelephonyBinding(t.Context(), "agent", &CreateTwilioTelephonyBindingRequest{
		ConnectionName: "conn", PhoneNumber: "+14255550123",
	}, testRepeatability)
	var unknown *TelephonyWriteOutcomeUnknownError
	require.ErrorAs(t, err, &unknown)
	require.Equal(t, 1, requests)
}

func TestUpdateGATelephonyBinding_MergePatchIfMatchAndETag(t *testing.T) {
	status := "suspended"
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPatch, req.Method)
		require.Equal(t, "/api/projects/proj/agents/agent/telephony/bindings/binding%2F1", req.URL.EscapedPath())
		require.Equal(t, "application/merge-patch+json", req.Header.Get("Content-Type"))
		require.Equal(t, `"old-etag"`, req.Header.Get("If-Match"))
		requireJSONBody(t, req, `{"status":"suspended","label":"new label"}`)
		return telephonyResponse(req, http.StatusOK, `{
			"id":"binding/1","provider":"twilio","connection_name":"conn","status":"suspended",
			"incoming_call_url":"https://example.test/call","phone_number":"+14255550123",
			"label":"new label"}`,
			http.Header{"ETag": {`"new-etag"`}}), nil
	})

	binding, err := client.UpdateGATelephonyBinding(t.Context(), "agent", "binding/1", UpdateTelephonyBindingRequest{
		Status: &status, Label: NewNullableString("new label"),
	}, `"old-etag"`)

	require.NoError(t, err)
	require.Equal(t, "suspended", binding.Status)
	require.Equal(t, `"new-etag"`, binding.ETag)
}

func TestUpdateGATelephonyBinding_MismatchedIDHasUnknownOutcome(t *testing.T) {
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		return telephonyResponse(req, http.StatusOK, `{
			"id":"other","provider":"twilio","connection_name":"conn","status":"active",
			"incoming_call_url":"https://example.test/call","phone_number":"+14255550123"}`,
			http.Header{"ETag": {`"new"`}}), nil
	})
	_, err := client.UpdateGATelephonyBinding(
		t.Context(), "agent", "requested", UpdateTelephonyBindingRequest{}, `"old"`)
	var unknown *TelephonyWriteOutcomeUnknownError
	require.ErrorAs(t, err, &unknown)
}

func TestUpdateGATelephonyBinding_ExplicitNullClearsNullableFields(t *testing.T) {
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		requireJSONBody(t, req, `{"label":null,"phone_number":null}`)
		return telephonyResponse(req, http.StatusOK, `{
				"id":"binding","provider":"teams_phone_extensibility","connection_name":"conn","status":"active",
				"incoming_call_url":"https://example.test/call",
				"resource_account_object_id":"00000000-0000-0000-0000-000000000001"}`,
			http.Header{"ETag": {`"new-etag"`}}), nil
	})

	_, err := client.UpdateGATelephonyBinding(t.Context(), "agent", "binding", UpdateTelephonyBindingRequest{
		Label: &NullableString{}, PhoneNumber: &NullableString{},
	}, `"old-etag"`)
	require.NoError(t, err)
}

func TestUpdateGATelephonyBinding_AllowsEmptyPhoneNumber(t *testing.T) {
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		requireJSONBody(t, req, `{"phone_number":""}`)
		return telephonyResponse(req, http.StatusOK, `{
			"id":"binding","provider":"teams_phone_extensibility","connection_name":"conn","status":"active",
			"incoming_call_url":"https://example.test/call",
			"resource_account_object_id":"00000000-0000-0000-0000-000000000001"}`,
			http.Header{"ETag": {`"new-etag"`}}), nil
	})

	_, err := client.UpdateGATelephonyBinding(t.Context(), "agent", "binding", UpdateTelephonyBindingRequest{
		PhoneNumber: NewNullableString(""),
	}, `"old-etag"`)
	require.NoError(t, err)
}

func TestTelephonyBindingLabelsEnforceMaxLength(t *testing.T) {
	client, transport := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP request")
		return nil, nil
	})
	label := strings.Repeat("界", 129)
	_, err := client.CreateGATelephonyBinding(t.Context(), "agent", &CreateTwilioTelephonyBindingRequest{
		ConnectionName: "conn", PhoneNumber: "+14255550123", Label: label,
	}, testRepeatability)
	require.ErrorContains(t, err, "128")
	_, err = client.UpdateGATelephonyBinding(t.Context(), "agent", "binding", UpdateTelephonyBindingRequest{
		Label: NewNullableString(label),
	}, `"etag"`)
	require.ErrorContains(t, err, "128")
	require.Zero(t, transport.requests)
}

func TestTelephonyBindingSchemaBoundaries(t *testing.T) {
	client, transport := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		return telephonyResponse(req, http.StatusCreated, `{
			"id":"binding","provider":"twilio","connection_name":"conn","status":"active",
			"incoming_call_url":"https://example.test/call","phone_number":"+14255550123"}`,
			http.Header{"ETag": {`"etag"`}}), nil
	})

	_, err := client.CreateGATelephonyBinding(t.Context(), "agent", &CreateTwilioTelephonyBindingRequest{
		ConnectionName: "conn", PhoneNumber: "+14255550123", Label: strings.Repeat("界", 128),
	}, testRepeatability)
	require.NoError(t, err)

	_, err = client.UpdateGATelephonyBinding(t.Context(), "agent", "binding", UpdateTelephonyBindingRequest{
		PhoneNumber: NewNullableString(strings.Repeat("界", 65)),
	}, `"etag"`)
	require.ErrorContains(t, err, "64")
	require.Equal(t, 1, transport.requests)
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

func TestTelephonyWrite_Unexpected2xxHasUnknownOutcome(t *testing.T) {
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		return telephonyResponse(req, http.StatusCreated, `{}`, nil), nil
	})
	_, err := client.UpdateGATelephonyBinding(
		t.Context(), "agent", "binding", UpdateTelephonyBindingRequest{}, `"etag"`)
	var unknown *TelephonyWriteOutcomeUnknownError
	require.ErrorAs(t, err, &unknown)
}

func TestTelephonyWrite_TransientStatusHasUnknownOutcome(t *testing.T) {
	for _, status := range []int{
		http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
				return telephonyResponse(req, status, `{"phone_number":"+14255550123"}`, nil), nil
			})
			_, err := client.UpdateGATelephonyBinding(
				t.Context(), "agent", "binding", UpdateTelephonyBindingRequest{}, `"etag"`)
			var unknown *TelephonyWriteOutcomeUnknownError
			require.ErrorAs(t, err, &unknown)
			require.NotContains(t, err.Error(), "+14255550123")
		})
	}
}

func TestConditionalTelephonyWrites_AreNotRetried(t *testing.T) {
	tests := []struct {
		name string
		call func(*AgentClient) error
	}{
		{name: "patch", call: func(client *AgentClient) error {
			_, err := client.UpdateGATelephonyBinding(
				t.Context(), "agent", "binding", UpdateTelephonyBindingRequest{}, `"etag"`)
			return err
		}},
		{name: "delete", call: func(client *AgentClient) error {
			return client.DeleteGATelephonyBinding(t.Context(), "agent", "binding", `"etag"`)
		}},
		{name: "put transfer targets", call: func(client *AgentClient) error {
			_, err := client.ReplaceTelephonyTransferTargets(t.Context(), "agent", TelephonyTransferTargets{
				TransferTargets: []TelephonyTransferTarget{{
					Name: "teams", Description: "Teams desk",
					Destination: TelephonyTransferDestination{
						Kind: TelephonyTransferDestinationTeams, Value: "resource-account",
					},
				}},
			}, `"etag"`)
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, transport := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
				return nil, errors.New("connection reset after dispatch")
			})
			err := tt.call(client)
			require.Error(t, err)
			var unknown *TelephonyWriteOutcomeUnknownError
			require.ErrorAs(t, err, &unknown)
			require.NotContains(t, err.Error(), "connection reset")
			require.Equal(t, 1, transport.requests)
		})
	}
}

func TestTelephonyTransferTargets_GetAndReplaceContracts(t *testing.T) {
	t.Run("get", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodGet, req.Method)
			require.Equal(t, "/api/projects/proj/agents/agent/telephony/transfer_targets", req.URL.Path)
			return telephonyResponse(req, http.StatusOK, `{
				"transfer_targets":[{
					"name":"support","description":"Support desk",
					"destination":{"kind":"pstn","value":"+14255550123"}
				}]}`,
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
			requireJSONBody(t, req, `{
				"transfer_targets":[{
					"name":"sales","description":"Sales desk",
					"destination":{"kind":"sip","value":"sip:sales@example.test"}
				}]}`)
			return telephonyResponse(req, http.StatusOK, `{
				"transfer_targets":[{
					"name":"sales","description":"Sales desk",
					"destination":{"kind":"sip","value":"sip:sales@example.test"}
				}]}`,
				http.Header{"ETag": {`"targets-2"`}}), nil
		})
		targets, err := client.ReplaceTelephonyTransferTargets(t.Context(), "agent", TelephonyTransferTargets{
			ETag: `"must-not-be-serialized"`,
			TransferTargets: []TelephonyTransferTarget{{
				Name: "sales", Description: "Sales desk",
				Destination: TelephonyTransferDestination{
					Kind: TelephonyTransferDestinationSIP, Value: "sip:sales@example.test",
				},
			}},
		}, `"targets-1"`)
		require.NoError(t, err)
		require.Equal(t, `"targets-2"`, targets.ETag)
	})
}

func TestTelephonyTransportErrorDoesNotExposeRequestURL(t *testing.T) {
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "Get", URL: req.URL.String(), Err: errors.New("network failure")}
	})
	_, err := client.GetGATelephonyBinding(t.Context(), "sensitive-agent", "sensitive-binding")
	require.EqualError(t, err, "sending telephony request failed")
	require.NotContains(t, err.Error(), "sensitive")
}

func TestTelephonyTransportErrorPreservesContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		return nil, req.Context().Err()
	})
	_, err := client.GetTelephonyTransferTargets(ctx, "agent")
	require.ErrorIs(t, err, context.Canceled)
}

func TestTelephonyWriteCancellationHasUnknownOutcome(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		return nil, req.Context().Err()
	})
	_, err := client.UpdateGATelephonyBinding(
		ctx, "agent", "binding", UpdateTelephonyBindingRequest{}, `"etag"`)
	var unknown *TelephonyWriteOutcomeUnknownError
	require.ErrorAs(t, err, &unknown)
	require.ErrorIs(t, err, context.Canceled)
}

func TestTelephonyWriteResponseReadCancellationHasUnknownOutcome(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"ETag": {`"etag"`}},
			Body:       &cancelingResponseBody{cancel: cancel},
			Request:    req,
		}, nil
	})
	_, err := client.UpdateGATelephonyBinding(
		ctx, "agent", "binding", UpdateTelephonyBindingRequest{}, `"etag"`)
	var unknown *TelephonyWriteOutcomeUnknownError
	require.ErrorAs(t, err, &unknown)
	require.ErrorIs(t, err, context.Canceled)
}

func TestReplaceTelephonyTransferTargets_AcceptsHostOnlySIPAndUnicodeTeams(t *testing.T) {
	client, transport := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		return telephonyResponse(req, http.StatusOK, `{
			"transfer_targets":[
				{"name":"sip","description":"SIP","destination":{"kind":"sip","value":"sip:example.com"}},
				{"name":"teams","description":"Teams","destination":{"kind":"teams","value":"团队"}}
			]}`, http.Header{"ETag": {`"new"`}}), nil
	})

	result, err := client.ReplaceTelephonyTransferTargets(t.Context(), "agent", TelephonyTransferTargets{
		TransferTargets: []TelephonyTransferTarget{
			{
				Name: "sip", Description: "SIP",
				Destination: TelephonyTransferDestination{
					Kind: TelephonyTransferDestinationSIP, Value: "sip:example.com",
				},
			},
			{
				Name: "teams", Description: "Teams",
				Destination: TelephonyTransferDestination{
					Kind: TelephonyTransferDestinationTeams, Value: "团队",
				},
			},
		},
	}, `"old"`)
	require.NoError(t, err)
	require.Equal(t, `"new"`, result.ETag)
	require.Equal(t, 1, transport.requests)
}

func TestReplaceTelephonyTransferTargets_AcceptsStandardURIFormatValues(t *testing.T) {
	values := []string{
		"SIP:user@example.test",
		"sip:example.test",
		"sip:user@[2001:db8::1];maddr=[2001:db8::2]",
		"sip:user@example.test:+5060",
		"sips:user@example.test",
	}
	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
				body := fmt.Sprintf(`{
					"transfer_targets":[{
						"name":"sip","description":"SIP",
						"destination":{"kind":"sip","value":%q}
					}]}`, value)
				return telephonyResponse(req, http.StatusOK, body,
					http.Header{"ETag": {`"new"`}}), nil
			})
			_, err := client.ReplaceTelephonyTransferTargets(t.Context(), "agent", TelephonyTransferTargets{
				TransferTargets: []TelephonyTransferTarget{{
					Name: "sip", Description: "SIP",
					Destination: TelephonyTransferDestination{
						Kind: TelephonyTransferDestinationSIP, Value: value,
					},
				}},
			}, `"old"`)
			require.NoError(t, err)
		})
	}
}

func TestGetTelephonyTransferTargets_PreservesUnknownDestinationKind(t *testing.T) {
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		return telephonyResponse(req, http.StatusOK, `{
			"transfer_targets":[{
				"name":"future","description":"Future destination",
				"destination":{"kind":"future_kind","value":"future-value"}
			}]}`, http.Header{"ETag": {`"etag"`}}), nil
	})

	targets, err := client.GetTelephonyTransferTargets(t.Context(), "agent")
	require.NoError(t, err)
	require.Equal(t, "future_kind", targets.TransferTargets[0].Destination.Kind)
}

func TestGetTelephonyTransferTargets_AllowsUnknownKindWithoutValue(t *testing.T) {
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		return telephonyResponse(req, http.StatusOK, `{
			"transfer_targets":[{
				"name":"future","description":"Future destination",
				"destination":{"kind":"future_kind"}
			}]}`, http.Header{"ETag": {`"etag"`}}), nil
	})

	targets, err := client.GetTelephonyTransferTargets(t.Context(), "agent")
	require.NoError(t, err)
	require.Equal(t, "future_kind", targets.TransferTargets[0].Destination.Kind)
	require.Empty(t, targets.TransferTargets[0].Destination.Value)
}

func TestGetTelephonyTransferTargets_PreservesUnknownStructuredValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "string", value: `"future-value"`},
		{name: "object", value: `{"agent_name":"transfer-agent"}`},
		{name: "array", value: `["one",{"two":2}]`},
		{name: "null", value: `null`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := fmt.Sprintf(`{
				"transfer_targets":[{
					"name":"future","description":"Future destination",
					"destination":{"kind":"future_kind","value":%s}
				}]}`, tt.value)
			client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
				return telephonyResponse(req, http.StatusOK, response,
					http.Header{"ETag": {`"etag"`}}), nil
			})

			targets, err := client.GetTelephonyTransferTargets(t.Context(), "agent")
			require.NoError(t, err)
			require.JSONEq(t, tt.value, string(targets.TransferTargets[0].Destination.RawValue))
			roundTrip, err := json.Marshal(targets.TransferTargets[0].Destination)
			require.NoError(t, err)
			require.JSONEq(t,
				fmt.Sprintf(`{"kind":"future_kind","value":%s}`, tt.value), string(roundTrip))
		})
	}
}

func TestUnknownTransferDestination_PreservesCompleteObject(t *testing.T) {
	original := `{
		"kind":"future_kind",
		"value":{"agent_name":"transfer-agent"},
		"routing_hint":"warm-transfer",
		"options":{"priority":2}
	}`
	var destination TelephonyTransferDestination
	require.NoError(t, json.Unmarshal([]byte(original), &destination))
	require.Equal(t, "future_kind", destination.Kind)
	require.JSONEq(t, original, string(destination.RawObject))

	encoded, err := json.Marshal(destination)
	require.NoError(t, err)
	require.JSONEq(t, original, string(encoded))
}

func TestUnknownTransferDestination_RejectsRawObjectKindMismatch(t *testing.T) {
	var destination TelephonyTransferDestination
	require.NoError(t, json.Unmarshal(
		[]byte(`{"kind":"future_kind","future_field":true}`),
		&destination,
	))
	destination.Kind = "different_kind"
	_, err := json.Marshal(destination)
	require.ErrorContains(t, err, "does not match kind")
}

func TestUnknownTransferDestination_PreservesOmittedValue(t *testing.T) {
	var destination TelephonyTransferDestination
	require.NoError(t, json.Unmarshal([]byte(`{"kind":"future_kind"}`), &destination))
	require.False(t, destination.HasValue)
	encoded, err := json.Marshal(destination)
	require.NoError(t, err)
	require.JSONEq(t, `{"kind":"future_kind"}`, string(encoded))
}

func TestKnownTransferDestinationRejectsStructuredValue(t *testing.T) {
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		return telephonyResponse(req, http.StatusOK, `{
			"transfer_targets":[{
				"name":"pstn","description":"PSTN",
				"destination":{"kind":"pstn","value":{"phone":"+14255550123"}}
			}]}`, http.Header{"ETag": {`"etag"`}}), nil
	})
	_, err := client.GetTelephonyTransferTargets(t.Context(), "agent")
	require.ErrorContains(t, err, "must be a string")
}

func TestKnownTransferDestinationRejectsNullValue(t *testing.T) {
	var destination TelephonyTransferDestination
	err := json.Unmarshal([]byte(`{"kind":"sip","value":null}`), &destination)
	require.ErrorContains(t, err, "must be a string")
}

func TestTransferDestinationRepeatedUnmarshalClearsPreviousValue(t *testing.T) {
	var destination TelephonyTransferDestination
	require.NoError(t, json.Unmarshal(
		[]byte(`{"kind":"future_kind","value":{"agent_name":"transfer-agent"}}`),
		&destination,
	))
	require.NotEmpty(t, destination.RawValue)
	require.True(t, destination.HasValue)

	require.NoError(t, json.Unmarshal([]byte(`{"kind":"future_kind"}`), &destination))
	require.Empty(t, destination.RawValue)
	require.JSONEq(t, `{"kind":"future_kind"}`, string(destination.RawObject))
	require.Empty(t, destination.Value)
	require.False(t, destination.HasValue)
}

func TestTelephonyTransferTargets_RequireDestinationKindInResponses(t *testing.T) {
	response := `{
		"transfer_targets":[{
			"name":"missing","description":"Missing kind","destination":{"value":"value"}
		}]}`

	t.Run("get", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusOK, response,
				http.Header{"ETag": {`"etag"`}}), nil
		})
		_, err := client.GetTelephonyTransferTargets(t.Context(), "agent")
		require.ErrorContains(t, err, "required fields")
	})

	t.Run("put", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusOK, response,
				http.Header{"ETag": {`"etag"`}}), nil
		})
		_, err := client.ReplaceTelephonyTransferTargets(t.Context(), "agent", TelephonyTransferTargets{
			TransferTargets: []TelephonyTransferTarget{{
				Name: "sip", Description: "SIP",
				Destination: TelephonyTransferDestination{
					Kind: TelephonyTransferDestinationSIP, Value: "sip:user@example.test",
				},
			}},
		}, `"old"`)
		var unknown *TelephonyWriteOutcomeUnknownError
		require.ErrorAs(t, err, &unknown)
	})
}

func TestReplaceTelephonyTransferTargets_AllowsUnknownResponseKindWithoutValue(t *testing.T) {
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		return telephonyResponse(req, http.StatusOK, `{
			"transfer_targets":[{
				"name":"future","description":"Future destination",
				"destination":{"kind":"future_kind"}
			}]}`, http.Header{"ETag": {`"new"`}}), nil
	})

	targets, err := client.ReplaceTelephonyTransferTargets(t.Context(), "agent", TelephonyTransferTargets{
		TransferTargets: []TelephonyTransferTarget{{
			Name: "sip", Description: "SIP",
			Destination: TelephonyTransferDestination{
				Kind: TelephonyTransferDestinationSIP, Value: "sip:user@example.test",
			},
		}},
	}, `"old"`)
	require.NoError(t, err)
	require.Equal(t, "future_kind", targets.TransferTargets[0].Destination.Kind)
	require.Empty(t, targets.TransferTargets[0].Destination.Value)
}

func TestTelephonyTransferTargets_RejectNullDestinationKind(t *testing.T) {
	client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		return telephonyResponse(req, http.StatusOK, `{
			"transfer_targets":[{
				"name":"missing","description":"Null kind",
				"destination":{"kind":null,"value":"value"}
			}]}`, http.Header{"ETag": {`"etag"`}}), nil
	})
	_, err := client.GetTelephonyTransferTargets(t.Context(), "agent")
	require.ErrorContains(t, err, "required fields")
}

func TestTelephonyStringLimitsCountUnicodeCodePoints(t *testing.T) {
	connection := strings.Repeat("界", 256)
	phone := strings.Repeat("界", 64)
	description := strings.Repeat("界", 1024)
	client, transport := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPatch {
			return telephonyResponse(req, http.StatusOK, `{
				"id":"binding","provider":"teams_phone_extensibility","connection_name":"conn",
				"status":"active","incoming_call_url":"https://example.test/call",
				"resource_account_object_id":"00000000-0000-0000-0000-000000000001"}`,
				http.Header{"ETag": {`"new"`}}), nil
		}
		return telephonyResponse(req, http.StatusOK, `{
			"transfer_targets":[{
				"name":"teams","description":"ok","destination":{"kind":"teams","value":"团队"}
			}]}`, http.Header{"ETag": {`"new"`}}), nil
	})

	_, err := client.UpdateGATelephonyBinding(t.Context(), "agent", "binding", UpdateTelephonyBindingRequest{
		ConnectionName: &connection, PhoneNumber: NewNullableString(phone),
	}, `"etag"`)
	require.NoError(t, err)

	_, err = client.ReplaceTelephonyTransferTargets(t.Context(), "agent", TelephonyTransferTargets{
		TransferTargets: []TelephonyTransferTarget{{
			Name: "teams", Description: description,
			Destination: TelephonyTransferDestination{Kind: TelephonyTransferDestinationTeams, Value: "团队"},
		}},
	}, `"etag"`)
	require.NoError(t, err)
	require.Equal(t, 2, transport.requests)
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
	_, err = client.CreateGATelephonyBinding(
		ctx, "agent", &CreateTwilioTelephonyBindingRequest{}, TelephonyRepeatabilityOptions{})
	require.EqualError(t, err, "Twilio connection name and phone number are required")
	_, err = client.CreateGATelephonyBinding(
		ctx, "agent", &CreateTeamsPhoneTelephonyBindingRequest{}, TelephonyRepeatabilityOptions{})
	require.EqualError(t, err, "Teams connection name and resource account object ID are required")
	_, err = client.UpdateGATelephonyBinding(ctx, "agent", "", UpdateTelephonyBindingRequest{}, `"etag"`)
	require.EqualError(t, err, "binding ID is required")
	_, err = client.UpdateGATelephonyBinding(ctx, "agent", "binding", UpdateTelephonyBindingRequest{}, "")
	require.EqualError(t, err, "If-Match is required")
	invalidStatus := "inactive"
	_, err = client.UpdateGATelephonyBinding(
		ctx, "agent", "binding", UpdateTelephonyBindingRequest{Status: &invalidStatus}, `"etag"`)
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

func TestGATelephonyOperations_RejectInvalidSemanticInputs(t *testing.T) {
	client, transport := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP request")
		return nil, nil
	})
	ctx := t.Context()

	_, err := client.CreateGATelephonyBinding(ctx, "agent", &CreateTwilioTelephonyBindingRequest{
		ConnectionName: "conn", PhoneNumber: "not-e164",
	}, testRepeatability)
	require.ErrorContains(t, err, "valid E.164")
	_, err = client.CreateGATelephonyBinding(ctx, "agent", &CreateTeamsPhoneTelephonyBindingRequest{
		ConnectionName: "conn", ResourceAccountObjectID: "not-a-uuid",
	}, testRepeatability)
	require.ErrorContains(t, err, "must be a UUID")
	_, err = client.CreateGATelephonyBinding(ctx, "agent", &CreateTwilioTelephonyBindingRequest{
		ConnectionName: "conn", PhoneNumber: "+14255550123",
	}, TelephonyRepeatabilityOptions{RequestID: "not-a-uuid", FirstSent: testRepeatability.FirstSent})
	require.ErrorContains(t, err, "repeatability request ID must be a UUID")
	_, err = client.CreateGATelephonyBinding(ctx, "agent", &CreateTwilioTelephonyBindingRequest{
		ConnectionName: "conn", PhoneNumber: "+14255550123",
	}, TelephonyRepeatabilityOptions{})
	require.ErrorContains(t, err, "repeatability request ID is required")

	empty := ""
	_, err = client.UpdateGATelephonyBinding(ctx, "agent", "binding", UpdateTelephonyBindingRequest{
		ConnectionName: &empty,
	}, `"etag"`)
	require.ErrorContains(t, err, "connection name")
	base := TelephonyTransferTarget{
		Name: "support", Description: "Support desk",
		Destination: TelephonyTransferDestination{
			Kind: TelephonyTransferDestinationPSTN, Value: "+14255550123",
		},
	}
	for name, targets := range map[string][]TelephonyTransferTarget{
		"duplicate names": {base, base},
		"invalid PSTN": {{
			Name: "pstn", Description: "PSTN",
			Destination: TelephonyTransferDestination{Kind: TelephonyTransferDestinationPSTN, Value: "123"},
		}},
		"relative URI": {{
			Name: "sip", Description: "SIP",
			Destination: TelephonyTransferDestination{Kind: TelephonyTransferDestinationSIP, Value: "relative/path"},
		}},
		"SIP with space": {{
			Name: "sip", Description: "SIP",
			Destination: TelephonyTransferDestination{
				Kind: TelephonyTransferDestinationSIP, Value: "sip:user name@example.test",
			},
		}},
		"SIP with malformed escape": {{
			Name: "sip", Description: "SIP",
			Destination: TelephonyTransferDestination{
				Kind: TelephonyTransferDestinationSIP, Value: "sip:user%zz@example.test",
			},
		}},
		"SIP with truncated escape": {{
			Name: "sip", Description: "SIP",
			Destination: TelephonyTransferDestination{
				Kind: TelephonyTransferDestinationSIP, Value: "sip:user%@example.test",
			},
		}},
		"unknown write kind": {{
			Name: "future", Description: "Future",
			Destination: TelephonyTransferDestination{Kind: "future_kind"},
		}},
		"known kind missing value": {{
			Name: "pstn", Description: "PSTN",
			Destination: TelephonyTransferDestination{Kind: TelephonyTransferDestinationPSTN},
		}},
		"invalid UTF-8 Teams": {{
			Name: "teams", Description: "Teams",
			Destination: TelephonyTransferDestination{
				Kind: TelephonyTransferDestinationTeams, Value: string([]byte{0xff}),
			},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := client.ReplaceTelephonyTransferTargets(ctx, "agent", TelephonyTransferTargets{
				TransferTargets: targets,
			}, `"etag"`)
			require.Error(t, err)
		})
	}
	require.Zero(t, transport.requests)
}

func TestListTelephonyBindings_InvalidOptions(t *testing.T) {
	tests := []struct {
		name    string
		options TelephonyBindingListOptions
		err     string
	}{
		{
			name: "negative limit", options: TelephonyBindingListOptions{Limit: -1},
			err: "limit must be between 1 and 100, or omitted",
		},
		{
			name: "limit too large", options: TelephonyBindingListOptions{Limit: 101},
			err: "limit must be between 1 and 100, or omitted",
		},
		{name: "invalid order", options: TelephonyBindingListOptions{Order: "newest"}, err: "order must be asc or desc"},
		{
			name: "both cursors", options: TelephonyBindingListOptions{After: "a", Before: "b"},
			err: "after and before cannot both be set",
		},
		{
			name: "invalid provider", options: TelephonyBindingListOptions{Provider: "sip"},
			err: "provider must be twilio or teams_phone_extensibility",
		},
		{
			name: "invalid status", options: TelephonyBindingListOptions{Status: "inactive"},
			err: "status must be active or suspended",
		},
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
			return telephonyResponse(req, http.StatusBadRequest,
				`{"error":{"message":"sensitive +14255550123"}}`, nil), nil
		})
		_, err := client.GetTelephonyTransferTargets(t.Context(), "agent")
		require.Error(t, err)
		require.NotContains(t, err.Error(), "+14255550123")
	})

	t.Run("trailing JSON", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusOK, `{"data":[],"has_more":false} {}`, nil), nil
		})
		_, err := client.ListTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{})
		require.ErrorContains(t, err, "multiple values")
	})

	t.Run("invalid UTF-8", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusOK, string([]byte{'{', '}', 0xff}), nil), nil
		})
		_, err := client.ListTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{})
		require.ErrorContains(t, err, "not valid UTF-8")
	})

	t.Run("oversized success body", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusOK,
				strings.Repeat("x", maxTelephonyResponseSize+1), nil), nil
		})
		_, err := client.ListTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{})
		require.ErrorContains(t, err, "exceeds the size limit")
	})

	t.Run("missing binding ETag", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusOK, `{
				"id":"binding","provider":"twilio","connection_name":"conn","status":"active",
				"incoming_call_url":"https://example.test/call","phone_number":"+14255550123"}`, nil), nil
		})
		_, err := client.GetGATelephonyBinding(t.Context(), "agent", "binding")
		require.ErrorContains(t, err, "missing required ETag")
	})

	t.Run("missing list item ETag", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusOK, `{
				"data":[{
					"id":"binding","provider":"twilio","connection_name":"conn","status":"active",
					"incoming_call_url":"https://example.test/call","phone_number":"+14255550123"
				}],"has_more":false}`, nil), nil
		})
		_, err := client.ListTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{})
		require.ErrorContains(t, err, "missing required ETag")
	})

	t.Run("missing has_more", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusOK, `{"data":[]}`, nil), nil
		})
		_, err := client.ListAllTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{})
		require.ErrorContains(t, err, "missing required has_more")
	})

	t.Run("unknown response status", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusOK, `{
				"data":[{"id":"binding","provider":"twilio","connection_name":"conn","status":"inactive",
				"incoming_call_url":"https://example.test/call","phone_number":"+14255550123","etag":"\"etag\""}],
				"has_more":false}`, nil), nil
		})
		page, err := client.ListTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{})
		require.NoError(t, err)
		require.Equal(t, "inactive", page.Data[0].Status)
	})

	t.Run("missing status rejected outside list", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusOK, `{
				"id":"binding","provider":"twilio","connection_name":"conn",
				"incoming_call_url":"https://example.test/call","phone_number":"+14255550123"}`,
				http.Header{"ETag": {`"etag"`}}), nil
		})
		_, err := client.GetGATelephonyBinding(t.Context(), "agent", "binding")
		require.ErrorContains(t, err, "missing required status")
	})

	t.Run("unknown response provider", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return telephonyResponse(req, http.StatusOK, `{
				"data":[{"id":"binding","provider":"sip","connection_name":"conn","status":"active",
				"incoming_call_url":"https://example.test/call","etag":"\"etag\""}],"has_more":false}`, nil), nil
		})
		page, err := client.ListTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{})
		require.NoError(t, err)
		require.Equal(t, "sip", page.Data[0].Provider)
	})

	for name, body := range map[string]string{
		"invalid Twilio phone": `{
			"data":[{
				"id":"binding","provider":"twilio","connection_name":"conn","status":"active",
				"incoming_call_url":"https://example.test/call","phone_number":"123","etag":"\"etag\""
			}],"has_more":false}`,
		"invalid Teams resource ID": `{
			"data":[{
				"id":"binding","provider":"teams_phone_extensibility","connection_name":"conn",
				"status":"active","incoming_call_url":"https://example.test/call",
				"resource_account_object_id":"not-a-uuid","etag":"\"etag\""
			}],"has_more":false}`,
		"invalid incoming URL": `{
			"data":[{
				"id":"binding","provider":"twilio","connection_name":"conn","status":"active",
				"incoming_call_url":"not-a-url","phone_number":"+14255550123","etag":"\"etag\""
			}],"has_more":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
				return telephonyResponse(req, http.StatusOK, body, nil), nil
			})
			_, err := client.ListTelephonyBindings(t.Context(), "agent", TelephonyBindingListOptions{})
			require.Error(t, err)
		})
	}

	t.Run("transport error", func(t *testing.T) {
		client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("network unavailable")
		})
		_, err := client.GetTelephonyTransferTargets(t.Context(), "agent")
		require.EqualError(t, err, "sending telephony request failed")
	})
}

func TestTelephonyWrites_UnknownOutcomeAfterSuccessfulStatus(t *testing.T) {
	tests := []struct {
		name string
		body string
		etag string
	}{
		{name: "missing body", body: "", etag: `"etag"`},
		{name: "malformed JSON", body: "{", etag: `"etag"`},
		{name: "trailing JSON", body: `{} {}`, etag: `"etag"`},
		{name: "missing ETag", body: `{
			"id":"binding","provider":"twilio","connection_name":"conn","status":"active",
			"incoming_call_url":"https://example.test/call","phone_number":"+14255550123"}`},
		{name: "invalid response", body: `{
			"id":"binding","provider":"sip","connection_name":"conn","status":"active",
			"incoming_call_url":"not-a-url"}`, etag: `"etag"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := newTelephonyContractClient(t, func(req *http.Request) (*http.Response, error) {
				return telephonyResponse(req, http.StatusOK, tt.body, http.Header{"ETag": {tt.etag}}), nil
			})
			_, err := client.UpdateGATelephonyBinding(
				t.Context(), "agent", "binding", UpdateTelephonyBindingRequest{}, `"old"`)
			var unknown *TelephonyWriteOutcomeUnknownError
			require.ErrorAs(t, err, &unknown)
		})
	}
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
