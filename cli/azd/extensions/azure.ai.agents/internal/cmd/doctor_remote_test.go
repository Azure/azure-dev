// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeJWT builds a JWT-shaped string with the provided claims payload.
// Header / signature segments are placeholders — extractUPNFromJWT only
// decodes segment[1].
func fakeJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, _ := json.Marshal(claims)
	body := base64.RawURLEncoding.EncodeToString(payload)
	sig := base64.RawURLEncoding.EncodeToString([]byte("not-a-real-signature"))
	return header + "." + body + "." + sig
}

func TestExtractUPNFromJWT(t *testing.T) {
	cases := []struct {
		name  string
		token string
		want  string
	}{
		{name: "empty", token: "", want: ""},
		{name: "not_a_jwt", token: "abc.def", want: ""},
		{name: "bad_payload", token: "abc.!!!notbase64!!!.def", want: ""},
		{
			name:  "upn_claim",
			token: fakeJWT(map[string]any{"upn": "alice@contoso.com"}),
			want:  "alice@contoso.com",
		},
		{
			name:  "unique_name_fallback",
			token: fakeJWT(map[string]any{"unique_name": "bob@fabrikam.com"}),
			want:  "bob@fabrikam.com",
		},
		{
			name:  "preferred_username_fallback",
			token: fakeJWT(map[string]any{"preferred_username": "carol@northwind.com"}),
			want:  "carol@northwind.com",
		},
		{
			name: "upn_wins_over_others",
			token: fakeJWT(map[string]any{
				"upn":                "alice@contoso.com",
				"unique_name":        "bob@fabrikam.com",
				"preferred_username": "carol@northwind.com",
			}),
			want: "alice@contoso.com",
		},
		{
			name:  "no_identity_claim",
			token: fakeJWT(map[string]any{"oid": "11111111-2222-3333-4444-555555555555"}),
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractUPNFromJWT(tc.token)
			if got != tc.want {
				t.Fatalf("extractUPNFromJWT = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMapReachabilityStatus(t *testing.T) {
	const endpoint = "https://example.foundry.azure.com"
	cases := []struct {
		name           string
		statusCode     int
		wantStatus     doctorStatus
		wantFix        string
		detailContains string
	}{
		{
			name:           "ok_200",
			statusCode:     200,
			wantStatus:     doctorOK,
			detailContains: "HTTP 200",
		},
		{
			name:           "ok_204",
			statusCode:     204,
			wantStatus:     doctorOK,
			detailContains: "HTTP 204",
		},
		{
			name:           "unauthorized_401",
			statusCode:     401,
			wantStatus:     doctorFail,
			wantFix:        "azd auth login",
			detailContains: "401",
		},
		{
			name:           "forbidden_403",
			statusCode:     403,
			wantStatus:     doctorFail,
			detailContains: "403",
		},
		{
			name:           "not_found_404",
			statusCode:     404,
			wantStatus:     doctorFail,
			wantFix:        "azd provision",
			detailContains: "404",
		},
		{
			name:           "server_error_500",
			statusCode:     500,
			wantStatus:     doctorWarn,
			detailContains: "500",
		},
		{
			name:           "server_error_503",
			statusCode:     503,
			wantStatus:     doctorWarn,
			detailContains: "503",
		},
		{
			name:           "unexpected_418",
			statusCode:     418,
			wantStatus:     doctorWarn,
			detailContains: "418",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := mapReachabilityStatus(tc.statusCode, endpoint)
			if res.Status != tc.wantStatus {
				t.Fatalf("status = %v, want %v", res.Status, tc.wantStatus)
			}
			if res.ID != "remote.reachability" {
				t.Fatalf("ID = %q, want remote.reachability", res.ID)
			}
			if tc.wantFix != "" && res.Fix != tc.wantFix {
				t.Fatalf("Fix = %q, want %q", res.Fix, tc.wantFix)
			}
			if !strings.Contains(res.Detail, tc.detailContains) {
				t.Fatalf("Detail = %q does not contain %q", res.Detail, tc.detailContains)
			}
			if !strings.Contains(res.Detail, endpoint) && tc.wantStatus != doctorOK {
				// Pass detail does not need to echo the endpoint;
				// every other status should so the user knows which
				// URL was probed.
				t.Fatalf("Detail = %q does not include endpoint %q", res.Detail, endpoint)
			}
		})
	}
}

func TestCheckReachability_SkipsWhenAuthFailed(t *testing.T) {
	a := &doctorAction{flags: &doctorFlags{}}
	res := a.checkReachability(
		t.Context(),
		remotePreconditions{endpointSet: true, endpoint: "https://example.com"},
		doctorFail,
		"",
	)
	if res.Status != doctorSkip {
		t.Fatalf("status = %v, want skip", res.Status)
	}
	if !strings.Contains(res.Detail, "authentication") {
		t.Fatalf("Detail = %q, want mention of authentication", res.Detail)
	}
}

func TestCheckReachability_SkipsWhenEndpointMissing(t *testing.T) {
	a := &doctorAction{flags: &doctorFlags{}}
	res := a.checkReachability(
		t.Context(),
		remotePreconditions{endpointSet: false},
		doctorOK,
		"bearer-token",
	)
	if res.Status != doctorSkip {
		t.Fatalf("status = %v, want skip", res.Status)
	}
	if !strings.Contains(res.Detail, "AZURE_AI_PROJECT_ENDPOINT") {
		t.Fatalf("Detail = %q, want mention of AZURE_AI_PROJECT_ENDPOINT", res.Detail)
	}
}

func TestCheckReachability_HitsFakeServer(t *testing.T) {
	// End-to-end: build a real test HTTP server returning a chosen
	// status, then assert checkReachability maps it correctly. This
	// also exercises the bearer-token header and api-version query.
	var capturedAuth string
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := &doctorAction{flags: &doctorFlags{}}
	res := a.checkReachability(
		t.Context(),
		remotePreconditions{endpointSet: true, endpoint: srv.URL},
		doctorOK,
		"fake-token",
	)
	if res.Status != doctorOK {
		t.Fatalf("status = %v, want ok; detail=%q", res.Status, res.Detail)
	}
	if capturedAuth != "Bearer fake-token" {
		t.Fatalf("Authorization header = %q, want %q", capturedAuth, "Bearer fake-token")
	}
	if !strings.Contains(capturedQuery, "api-version="+DefaultAgentAPIVersion) {
		t.Fatalf("query = %q, want api-version=%s", capturedQuery, DefaultAgentAPIVersion)
	}
	if !strings.Contains(capturedQuery, "$top=1") {
		t.Fatalf("query = %q, want $top=1", capturedQuery)
	}
}

func TestRunRemoteChecks_LocalOnlyEmitsSkipRows(t *testing.T) {
	a := &doctorAction{flags: &doctorFlags{localOnly: true}}
	results := a.runRemoteChecks(t.Context(), remotePreconditions{})
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	wantIDs := []string{"remote.auth", "remote.reachability"}
	for i, want := range wantIDs {
		if results[i].ID != want {
			t.Fatalf("results[%d].ID = %q, want %q", i, results[i].ID, want)
		}
		if results[i].Status != doctorSkip {
			t.Fatalf("results[%d].Status = %v, want skip", i, results[i].Status)
		}
		if !strings.Contains(results[i].Detail, "--local-only") {
			t.Fatalf("results[%d].Detail = %q, want mention of --local-only",
				i, results[i].Detail)
		}
	}
}

func TestRemoteSkipRows_OrderAndIDs(t *testing.T) {
	rows := remoteSkipRows("custom reason")
	if len(rows) != 2 {
		t.Fatalf("len = %d, want 2", len(rows))
	}
	if rows[0].ID != "remote.auth" || rows[1].ID != "remote.reachability" {
		t.Fatalf("unexpected IDs: %q, %q", rows[0].ID, rows[1].ID)
	}
	for _, r := range rows {
		if r.Status != doctorSkip {
			t.Fatalf("status = %v, want skip", r.Status)
		}
		if r.Detail != "custom reason" {
			t.Fatalf("Detail = %q, want %q", r.Detail, "custom reason")
		}
	}
}

func TestAuthFailResult_PointsAtAzdAuthLogin(t *testing.T) {
	res := authFailResult(stringError("token fetch boom"))
	if res.ID != "remote.auth" {
		t.Fatalf("ID = %q, want remote.auth", res.ID)
	}
	if res.Status != doctorFail {
		t.Fatalf("Status = %v, want fail", res.Status)
	}
	if res.Fix != "azd auth login" {
		t.Fatalf("Fix = %q, want %q", res.Fix, "azd auth login")
	}
	if !strings.Contains(res.Detail, "token fetch boom") {
		t.Fatalf("Detail = %q, want underlying error wrapped", res.Detail)
	}
}

// stringError is a tiny test-only error type so authFailResult can be
// exercised without importing fmt/errors helpers into the wrong file.
type stringError string

func (e stringError) Error() string { return string(e) }
