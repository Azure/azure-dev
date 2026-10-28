// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const readyAnalyzer = `{
	"analyzerId":"custom","status":"ready","description":"Custom analyzer",
	"createdAt":"2026-01-02T03:04:05Z","lastModifiedAt":"2026-02-03T04:05:06Z",
	"warnings":[{"code":"TrainingWarning","message":"Check fields","target":"fieldSchema",
		"details":[{"code":"NestedWarning","message":"Nested message","target":"fields.Total"}]}],
	"fieldSchema":{"fields":{"Dynamic":{"type":"object","example":{"large":9007199254740993}}}},
	"futureProperty":{"preserve":true}
}`

type wireStep struct {
	method      string
	path        string
	query       url.Values
	contentType string
	body        string
	binary      []byte
	status      int
	response    string
	headers     http.Header
}

func newWireClient(t *testing.T, version string, steps ...wireStep) Client {
	t.Helper()
	calls := 0
	credential := &testCredential{}
	client, err := NewRESTTransport("https://resource.test", credential, &TransportOptions{
		APIVersion: version,
		Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
			require.Less(t, calls, len(steps), "unexpected request: %s %s", request.Method, request.URL.Path)
			step := steps[calls]
			calls++
			assert.Equal(t, step.method, request.Method)
			assert.Equal(t, "/contentunderstanding"+step.path, request.URL.Path)
			assert.Equal(t, "https", request.URL.Scheme)
			assert.Equal(t, "resource.test", request.URL.Host)
			query := url.Values{"api-version": {version}}
			maps.Copy(query, step.query)
			assert.Equal(t, query, request.URL.Query())
			assert.Equal(t, step.contentType, request.Header.Get("Content-Type"))
			assert.Equal(t, "application/json", request.Header.Get("Accept"))
			assert.NotEmpty(t, request.Header.Get("Authorization"))
			assert.Empty(t, request.Header.Get("If-Match"), "CU does not advertise conditional requests")
			assert.Empty(t, request.Header.Get("If-None-Match"))
			if step.body != "" || step.binary != nil {
				require.NotNil(t, request.Body)
				body, err := io.ReadAll(request.Body)
				require.NoError(t, err)
				if step.binary != nil {
					assert.Equal(t, step.binary, body)
				} else {
					assert.JSONEq(t, step.body, string(body))
				}
			} else {
				assert.True(t, request.Body == nil || request.Body == http.NoBody)
			}
			status := step.status
			if status == 0 {
				status = http.StatusOK
			}
			result := response(status, step.response)
			maps.Copy(result.Header, step.headers)
			return result, nil
		}),
	})
	require.NoError(t, err)
	client.pollInterval = time.Millisecond
	t.Cleanup(func() { assert.Equal(t, len(steps), calls, "request count") })
	return client
}

func assertReadyAnalyzer(t *testing.T, analyzer Analyzer) {
	t.Helper()
	assert.Equal(t, "custom", analyzer.ID)
	assert.Equal(t, "ready", analyzer.Status)
	assert.Equal(t, "Custom analyzer", analyzer.Description)
	assert.Equal(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), analyzer.CreatedAt)
	assert.Equal(t, time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC), analyzer.LastModifiedAt)
	require.Len(t, analyzer.Warnings, 1)
	assert.Equal(t, Diagnostic{
		Code: "TrainingWarning", Message: "Check fields", Target: "fieldSchema",
		Details: []Diagnostic{{Code: "NestedWarning", Message: "Nested message", Target: "fields.Total"}},
	}, analyzer.Warnings[0])
	assert.JSONEq(t, readyAnalyzer, string(analyzer.JSON))
	assert.Contains(t, string(analyzer.JSON), "9007199254740993", "raw JSON must not round large integers")
}

func TestAnalyzerOperationsWire(t *testing.T) {
	for _, version := range []string{DefaultAPIVersion, PreviewAPIVersion} {
		t.Run(version, func(t *testing.T) {
			t.Run("get", func(t *testing.T) {
				client := newWireClient(t, version, wireStep{
					method: http.MethodGet, path: "/analyzers/custom", response: readyAnalyzer,
				})
				result, err := client.GetAnalyzer(t.Context(), "custom")
				require.NoError(t, err)
				assertReadyAnalyzer(t, result)
			})
			for _, status := range []int{http.StatusOK, http.StatusCreated} {
				t.Run(fmt.Sprintf("create_%d", status), func(t *testing.T) {
					definition := `{"baseAnalyzerId":"prebuilt-document","fieldSchema":{"fields":{"X":{"type":"string"}}}}`
					client := newWireClient(t, version, wireStep{
						method: http.MethodPut, path: "/analyzers/custom", query: url.Values{"allowReplace": {"false"}},
						contentType: "application/json", body: definition, response: readyAnalyzer, status: status,
					})
					result, err := client.CreateAnalyzer(t.Context(), CreateAnalyzerRequest{
						AnalyzerID: "custom", Definition: json.RawMessage(definition),
					})
					require.NoError(t, err)
					assertReadyAnalyzer(t, result.Analyzer)
					assert.Nil(t, result.Usage)
				})
			}
			t.Run("delete", func(t *testing.T) {
				client := newWireClient(t, version, wireStep{
					method: http.MethodDelete, path: "/analyzers/custom", status: http.StatusNoContent,
				})
				require.NoError(t, client.DeleteAnalyzer(t.Context(), "custom"))
			})
			for _, region := range []string{"", "westus2"} {
				t.Run("grant_"+region, func(t *testing.T) {
					body := `{"targetAzureResourceId":"/target"}`
					if region != "" {
						body = `{"targetAzureResourceId":"/target","targetRegion":"westus2"}`
					}
					client := newWireClient(t, version, wireStep{
						method: http.MethodPost, path: "/analyzers/custom:grantCopyAuthorization",
						contentType: "application/json", body: body,
						response: `{"source":"https://resource.test/contentunderstanding/analyzers/custom",
							"targetAzureResourceId":"/target","expiresAt":"2027-01-01T00:00:00Z"}`,
					})
					result, err := client.GrantCopyAuthorization(t.Context(), GrantCopyAuthorizationRequest{
						AnalyzerID: "custom", TargetAzureResourceID: "/target", TargetRegion: region,
					})
					require.NoError(t, err)
					assert.Equal(t, "https://resource.test/contentunderstanding/analyzers/custom", result.Source)
					assert.Equal(t, "/target", result.TargetAzureResourceID)
					assert.Equal(t, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), result.ExpiresAt)
				})
			}
			for _, crossResource := range []bool{false, true} {
				t.Run(fmt.Sprintf("copy_cross_resource_%t", crossResource), func(t *testing.T) {
					request := CopyAnalyzerRequest{SourceAnalyzerID: "source", DestinationAnalyzerID: "custom"}
					body := `{"sourceAnalyzerId":"source"}`
					if crossResource {
						request.SourceAzureResourceID = "/source"
						request.SourceRegion = "westus2"
						body = `{"sourceAnalyzerId":"source","sourceAzureResourceId":"/source","sourceRegion":"westus2"}`
					}
					client := newWireClient(t, version, wireStep{
						method: http.MethodPost, path: "/analyzers/custom:copy", contentType: "application/json",
						query: url.Values{"allowReplace": {"false"}}, body: body, response: readyAnalyzer,
					})
					result, err := client.CopyAnalyzer(t.Context(), request)
					require.NoError(t, err)
					assertReadyAnalyzer(t, result.Analyzer)
					assert.Nil(t, result.Usage)
				})
			}
			t.Run("get_defaults", func(t *testing.T) {
				raw := `{"modelDeployments":{"gpt-4.1":"deployment"},"future":9007199254740993}`
				client := newWireClient(t, version, wireStep{
					method: http.MethodGet, path: "/defaults", response: raw,
				})
				result, err := client.GetDefaults(t.Context())
				require.NoError(t, err)
				assert.Equal(t, map[string]string{"gpt-4.1": "deployment"}, result.ModelDeployments)
				assert.Equal(t, raw, string(result.JSON))
			})
			for _, empty := range []bool{false, true} {
				t.Run(fmt.Sprintf("update_defaults_empty_%t", empty), func(t *testing.T) {
					mapping := map[string]string{"gpt-4.1": "new"}
					body := `{"modelDeployments":{"gpt-4.1":"new"}}`
					if empty {
						mapping = map[string]string{}
						body = `{"modelDeployments":{}}`
					}
					client := newWireClient(t, version, wireStep{
						method: http.MethodPatch, path: "/defaults", contentType: "application/merge-patch+json", body: body,
						response: `{"modelDeployments":{"gpt-4.1":"new","text-embedding-3-large":"existing"}}`,
					})
					result, err := client.UpdateDefaults(t.Context(), UpdateDefaultsRequest{ModelDeployments: mapping})
					require.NoError(t, err)
					assert.Contains(t, string(result.JSON), `"text-embedding-3-large":"existing"`)
					assert.Equal(t, map[string]string{
						"gpt-4.1": "new", "text-embedding-3-large": "existing",
					}, result.ModelDeployments)
				})
			}
		})
	}
}

func TestListAnalyzersPaging(t *testing.T) {
	for _, version := range []string{DefaultAPIVersion, PreviewAPIVersion} {
		for _, prefix := range []string{
			"https://RESOURCE.test/contentunderstanding/analyzers",
			"/contentunderstanding/analyzers", "analyzers", "",
		} {
			t.Run(version+"/"+prefix, func(t *testing.T) {
				link := prefix + "?nextLink=opaque%2B%2F%3D%20%26&api-version=" + version
				client := newWireClient(t, version,
					wireStep{
						method: http.MethodGet, path: "/analyzers",
						response: `{"value":[{"analyzerId":"zeta","status":"creating"}],"nextLink":"` + link + `"}`,
					},
					wireStep{
						method: http.MethodGet, path: "/analyzers", query: url.Values{"nextLink": {"opaque+/= &"}},
						response: `{"value":[],"nextLink":"?nextLink=last"}`,
					},
					wireStep{
						method: http.MethodGet, path: "/analyzers", query: url.Values{"nextLink": {"last"}},
						response: `{"value":[{"analyzerId":"alpha","status":"ready"}]}`,
					},
				)
				items, err := client.ListAnalyzers(t.Context())
				require.NoError(t, err)
				require.Len(t, items, 2)
				assert.Equal(t, "zeta", items[0].ID)
				assert.Equal(t, "alpha", items[1].ID, "adapter preserves service order")
			})
		}
	}
	client := newWireClient(t, DefaultAPIVersion, wireStep{
		method: http.MethodGet, path: "/analyzers", response: `{"value":[]}`,
	})
	items, err := client.ListAnalyzers(t.Context())
	require.NoError(t, err)
	assert.NotNil(t, items)
	assert.Empty(t, items)
}

func TestListAnalyzersPreservesServiceIdentifiers(t *testing.T) {
	for _, version := range []string{DefaultAPIVersion, PreviewAPIVersion} {
		t.Run(version, func(t *testing.T) {
			legacyID := strings.Repeat("a", 65)
			internalID := strings.Repeat("b", 80) + "@2025-11-01"
			first := fmt.Sprintf(`{"analyzerId":%q,"status":"ready","future":9007199254740993}`, legacyID)
			second := fmt.Sprintf(`{"analyzerId":%q,"status":"failed"}`, internalID)
			client := newWireClient(t, version,
				wireStep{
					method: http.MethodGet, path: "/analyzers",
					response: `{"value":[` + first + `],"nextLink":"?nextLink=second"}`,
				},
				wireStep{
					method: http.MethodGet, path: "/analyzers", query: url.Values{"nextLink": {"second"}},
					response: `{"value":[` + second + `,{"analyzerId":"custom","status":"ready"}]}`,
				},
			)
			items, err := client.ListAnalyzers(t.Context())
			require.NoError(t, err)
			require.Len(t, items, 3)
			assert.Equal(t, []string{legacyID, internalID, "custom"}, []string{items[0].ID, items[1].ID, items[2].ID})
			assert.JSONEq(t, first, string(items[0].JSON))
			assert.Contains(t, string(items[0].JSON), "9007199254740993")
			assert.JSONEq(t, second, string(items[1].JSON))
		})
	}
}

func TestGrantCopyAuthorizationWithoutSource(t *testing.T) {
	for _, version := range []string{DefaultAPIVersion, PreviewAPIVersion} {
		t.Run(version, func(t *testing.T) {
			client := newWireClient(t, version, wireStep{
				method: http.MethodPost, path: "/analyzers/custom:grantCopyAuthorization",
				contentType: "application/json",
				body:        `{"targetAzureResourceId":"/target","targetRegion":"westus2"}`,
				response: `{"targetAzureResourceId":"/target","targetRegion":"westus2",` +
					`"expiresAt":"2027-01-01T00:00:00Z"}`,
			})
			result, err := client.GrantCopyAuthorization(t.Context(), GrantCopyAuthorizationRequest{
				AnalyzerID: "custom", TargetAzureResourceID: "/target", TargetRegion: "westus2",
			})
			require.NoError(t, err)
			assert.Empty(t, result.Source)
			assert.Equal(t, "/target", result.TargetAzureResourceID)
			assert.Equal(t, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), result.ExpiresAt)
		})
	}
}

func TestListAnalyzersRejectsUnsafeContinuation(t *testing.T) {
	for _, link := range []string{
		"https://other.test/contentunderstanding/analyzers?nextLink=private-token",
		"http://resource.test/contentunderstanding/analyzers",
		"https://user:private-token@resource.test/contentunderstanding/analyzers",
		"//other.test/contentunderstanding/analyzers", "/outside", "/contentunderstanding/defaults",
		"/contentunderstanding/analyzers/custom", "/contentunderstandingevil/analyzers",
		"/contentunderstanding/../contentunderstanding/analyzers?nextLink=token",
		"/contentunderstanding/%61nalyzers?nextLink=token", "/contentunderstanding/analyzers\\private-token",
		"?api-version=other-version", "?api-version=2025-11-01&api-version=2025-11-01",
		"?nextLink=%private-token", "?nextLink=x;y", "?nextLink=x#private-token", "?nextLink=x#",
		"?api-version=2025-11-01", "https://resource.test:443/contentunderstanding/analyzers",
		"mailto:private-token", "%",
	} {
		t.Run(link, func(t *testing.T) {
			page, err := json.Marshal(map[string]any{"value": []any{}, "nextLink": link})
			require.NoError(t, err)
			client := newWireClient(t, DefaultAPIVersion, wireStep{
				method: http.MethodGet, path: "/analyzers", response: string(page),
			})
			items, err := client.ListAnalyzers(t.Context())
			require.Error(t, err)
			assert.Nil(t, items)
			assert.NotContains(t, err.Error(), "private-token")
			domain, ok := errors.AsType[*Error](err)
			require.True(t, ok)
			assert.Equal(t, ErrorService, domain.Kind)
			assert.Equal(t, "malformed_response", domain.Code)
		})
	}
}

func TestListAnalyzersRejectsRepeatedContinuation(t *testing.T) {
	page := `{"value":[],"nextLink":"?nextLink=repeat"}`
	client := newWireClient(t, DefaultAPIVersion,
		wireStep{method: http.MethodGet, path: "/analyzers", response: page},
		wireStep{
			method: http.MethodGet, path: "/analyzers", query: url.Values{"nextLink": {"repeat"}}, response: page,
		},
	)
	items, err := client.ListAnalyzers(t.Context())
	assert.Nil(t, items)
	require.Error(t, err)
}

func TestAnalyzerOperationsValidateBeforeHTTP(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(Client) error
	}{
		{"get", func(c Client) error { _, err := c.GetAnalyzer(t.Context(), "../private-token"); return err }},
		{"get_long_id", func(c Client) error {
			_, err := c.GetAnalyzer(t.Context(), strings.Repeat("a", 65))
			return err
		}},
		{"get_internal_id", func(c Client) error {
			_, err := c.GetAnalyzer(t.Context(), "custom@internal")
			return err
		}},
		{"delete", func(c Client) error { return c.DeleteAnalyzer(t.Context(), "") }},
		{"create", func(c Client) error {
			_, err := c.CreateAnalyzer(t.Context(), CreateAnalyzerRequest{AnalyzerID: "custom", Definition: []byte(`null`)})
			return err
		}},
		{"copy", func(c Client) error {
			_, err := c.CopyAnalyzer(t.Context(), CopyAnalyzerRequest{
				SourceAnalyzerID: "same", DestinationAnalyzerID: "same",
			})
			return err
		}},
		{"grant", func(c Client) error {
			_, err := c.GrantCopyAuthorization(t.Context(), GrantCopyAuthorizationRequest{AnalyzerID: "custom"})
			return err
		}},
		{"defaults", func(c Client) error {
			_, err := c.UpdateDefaults(t.Context(), UpdateDefaultsRequest{})
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newWireClient(t, DefaultAPIVersion)
			err := test.run(client)
			domain, ok := errors.AsType[*Error](err)
			require.True(t, ok)
			assert.Equal(t, ErrorValidation, domain.Kind)
			assert.NotContains(t, domain.Message, "private-token")
		})
	}
}

func TestAnalyzerHTTPFailures(t *testing.T) {
	for _, test := range []struct {
		status int
		kind   ErrorKind
	}{
		{400, ErrorValidation}, {401, ErrorAuthentication}, {403, ErrorAuthentication}, {404, ErrorNotFound},
		{409, ErrorConflict}, {413, ErrorLimit}, {429, ErrorThrottled}, {500, ErrorService},
	} {
		t.Run(fmt.Sprint(test.status), func(t *testing.T) {
			client := newWireClient(t, DefaultAPIVersion, wireStep{
				method: http.MethodPut, path: "/analyzers/custom", query: url.Values{"allowReplace": {"false"}},
				body: `{}`, contentType: "application/json", status: test.status,
				response: `{"error":{"code":"ServiceCode","message":"private-token"}}`,
				headers:  http.Header{"Retry-After": {"86400"}},
			})
			result, err := client.CreateAnalyzer(t.Context(), CreateAnalyzerRequest{
				AnalyzerID: "custom", Definition: []byte(`{}`),
			})
			assert.Empty(t, result)
			domain, ok := errors.AsType[*Error](err)
			require.True(t, ok)
			assert.Equal(t, test.kind, domain.Kind)
			assert.Equal(t, test.status, domain.StatusCode)
			assert.Equal(t, "ServiceCode", domain.Code)
			assert.Equal(t, 24*time.Hour, domain.RetryAfter)
			assert.NotContains(t, err.Error(), "private-token")
		})
	}
}

func TestAnalyzerWritesAreNotReplayed(t *testing.T) {
	for _, test := range []struct {
		name string
		step wireStep
		run  func(Client) error
	}{
		{"delete", wireStep{method: http.MethodDelete, path: "/analyzers/custom"}, func(c Client) error {
			return c.DeleteAnalyzer(t.Context(), "custom")
		}},
		{"copy", wireStep{
			method: http.MethodPost, path: "/analyzers/custom:copy", query: url.Values{"allowReplace": {"false"}},
			body: `{"sourceAnalyzerId":"source"}`, contentType: "application/json",
		}, func(c Client) error {
			_, err := c.CopyAnalyzer(t.Context(), CopyAnalyzerRequest{
				SourceAnalyzerID: "source", DestinationAnalyzerID: "custom",
			})
			return err
		}},
		{"grant", wireStep{
			method: http.MethodPost, path: "/analyzers/custom:grantCopyAuthorization",
			body: `{"targetAzureResourceId":"/target"}`, contentType: "application/json",
		}, func(c Client) error {
			_, err := c.GrantCopyAuthorization(t.Context(), GrantCopyAuthorizationRequest{
				AnalyzerID: "custom", TargetAzureResourceID: "/target",
			})
			return err
		}},
		{"defaults", wireStep{
			method: http.MethodPatch, path: "/defaults", body: `{"modelDeployments":{}}`,
			contentType: "application/merge-patch+json",
		}, func(c Client) error {
			_, err := c.UpdateDefaults(t.Context(), UpdateDefaultsRequest{ModelDeployments: map[string]string{}})
			return err
		}},
	} {
		for _, status := range []int{http.StatusServiceUnavailable, http.StatusUnauthorized, http.StatusTooManyRequests} {
			t.Run(fmt.Sprintf("%s/%d", test.name, status), func(t *testing.T) {
				step := test.step
				step.status = status
				client := newWireClient(t, DefaultAPIVersion, step)
				require.Error(t, test.run(client))
			})
		}
	}
}

func TestAnalyzerMalformedResponses(t *testing.T) {
	for _, body := range []string{
		"", "{", "null", "[]", `{}`, `{"analyzerId":"custom"}`, `{"analyzerId":"wrong","status":"ready"}`,
		`{"analyzerId":"custom","status":"ready","createdAt":"invalid"}`,
		`{"analyzerId":"custom","status":"ready","warnings":"invalid"}`, readyAnalyzer + `{}`,
	} {
		t.Run("get/"+body, func(t *testing.T) {
			client := newWireClient(t, DefaultAPIVersion, wireStep{
				method: http.MethodGet, path: "/analyzers/custom", response: body,
			})
			_, err := client.GetAnalyzer(t.Context(), "custom")
			require.Error(t, err)
			domain, ok := errors.AsType[*Error](err)
			require.True(t, ok)
			assert.Equal(t, "malformed_response", domain.Code)
		})
	}
	for _, body := range []string{
		`{}`, `{"value":null}`, `{"value":[null]}`, `{"value":[{}]}`, `{"value":[],"nextLink":5}`, "null", "{",
		`{"value":[{"analyzerId":"","status":"ready"}]}`, `{"value":[{"analyzerId":"custom"}]}`,
	} {
		t.Run("list/"+body, func(t *testing.T) {
			client := newWireClient(t, DefaultAPIVersion, wireStep{
				method: http.MethodGet, path: "/analyzers", response: body,
			})
			items, err := client.ListAnalyzers(t.Context())
			require.Error(t, err)
			assert.Nil(t, items, "malformed pages must not produce partial success")
		})
	}
	for _, body := range []string{
		`{}`, "null", `{"modelDeployments":null}`, `{"modelDeployments":{"x":5}}`,
		`{"modelDeployments":{"x":null}}`, `{"modelDeployments":{"":"deployment"}}`,
	} {
		t.Run("defaults/"+body, func(t *testing.T) {
			client := newWireClient(t, DefaultAPIVersion, wireStep{
				method: http.MethodGet, path: "/defaults", response: body,
			})
			_, err := client.GetDefaults(t.Context())
			require.Error(t, err)
		})
	}
	for _, body := range []string{
		"null", `{}`, `{"source":"source","targetAzureResourceId":"/other","expiresAt":"2027-01-01T00:00:00Z"}`,
		`{"source":"source","targetAzureResourceId":"/target","expiresAt":"invalid"}`,
		`{"targetAzureResourceId":"/other","expiresAt":"2027-01-01T00:00:00Z"}`,
		`{"targetAzureResourceId":"/target","expiresAt":"invalid"}`,
	} {
		t.Run("grant/"+body, func(t *testing.T) {
			client := newWireClient(t, DefaultAPIVersion, wireStep{
				method: http.MethodPost, path: "/analyzers/custom:grantCopyAuthorization",
				contentType: "application/json", body: `{"targetAzureResourceId":"/target"}`, response: body,
			})
			_, err := client.GrantCopyAuthorization(t.Context(), GrantCopyAuthorizationRequest{
				AnalyzerID: "custom", TargetAzureResourceID: "/target",
			})
			require.Error(t, err)
		})
	}
}

func TestAnalyzerReadsPreserveHTTPFailures(t *testing.T) {
	client := newWireClient(t, DefaultAPIVersion, wireStep{
		method: http.MethodGet, path: "/analyzers/custom", status: http.StatusNotFound,
	})
	_, err := client.GetAnalyzer(t.Context(), "custom")
	domain, ok := errors.AsType[*Error](err)
	require.True(t, ok)
	assert.Equal(t, ErrorNotFound, domain.Kind)

	client = newWireClient(t, DefaultAPIVersion, wireStep{
		method: http.MethodGet, path: "/defaults", status: http.StatusForbidden,
	})
	_, err = client.GetDefaults(t.Context())
	domain, ok = errors.AsType[*Error](err)
	require.True(t, ok)
	assert.Equal(t, ErrorAuthentication, domain.Kind)

	client = newWireClient(t, DefaultAPIVersion,
		wireStep{
			method: http.MethodGet, path: "/analyzers",
			response: `{"value":[{"analyzerId":"first","status":"ready"}],"nextLink":"?nextLink=second"}`,
		},
		wireStep{
			method: http.MethodGet, path: "/analyzers", query: url.Values{"nextLink": {"second"}},
			status: http.StatusForbidden,
		},
	)
	items, err := client.ListAnalyzers(t.Context())
	require.Error(t, err)
	assert.Nil(t, items, "a failed later page must not look like a complete list")
}

func TestAnalyzerResponseFailures(t *testing.T) {
	operations := []struct {
		name string
		run  func(Client) error
	}{
		{"list", func(c Client) error { _, err := c.ListAnalyzers(t.Context()); return err }},
		{"get", func(c Client) error { _, err := c.GetAnalyzer(t.Context(), "custom"); return err }},
		{"create", func(c Client) error {
			_, err := c.CreateAnalyzer(t.Context(), CreateAnalyzerRequest{
				AnalyzerID: "custom", Definition: []byte(`{}`),
			})
			return err
		}},
		{"delete", func(c Client) error { return c.DeleteAnalyzer(t.Context(), "custom") }},
		{"grant", func(c Client) error {
			_, err := c.GrantCopyAuthorization(t.Context(), GrantCopyAuthorizationRequest{
				AnalyzerID: "custom", TargetAzureResourceID: "/target",
			})
			return err
		}},
		{"copy", func(c Client) error {
			_, err := c.CopyAnalyzer(t.Context(), CopyAnalyzerRequest{
				SourceAnalyzerID: "source", DestinationAnalyzerID: "custom",
			})
			return err
		}},
		{"get_defaults", func(c Client) error { _, err := c.GetDefaults(t.Context()); return err }},
		{"update_defaults", func(c Client) error {
			_, err := c.UpdateDefaults(t.Context(), UpdateDefaultsRequest{ModelDeployments: map[string]string{}})
			return err
		}},
	}
	for _, operation := range operations {
		for _, scenario := range []string{"status", "read", "http_error", "invalid_json"} {
			if scenario == "invalid_json" && operation.name == "delete" {
				continue
			}
			t.Run(operation.name+"/"+scenario, func(t *testing.T) {
				calls := 0
				client := newRESTTransport(t, DefaultAPIVersion, func(*http.Request) (*http.Response, error) {
					calls++
					status := http.StatusOK
					if operation.name == "delete" {
						status = http.StatusNoContent
					}
					result := response(status, `{"private-token":`)
					switch scenario {
					case "status":
						result.StatusCode = http.StatusAccepted
					case "read":
						result.Body = brokenBody{}
					case "http_error":
						result.StatusCode = http.StatusNotFound
					}
					return result, nil
				})
				err := operation.run(client)
				require.Error(t, err)
				expectedCalls := 1
				if scenario == "read" &&
					(operation.name == "list" || operation.name == "get" || operation.name == "get_defaults") {
					expectedCalls = 3
				}
				assert.Equal(t, expectedCalls, calls)
				assert.NotContains(t, err.Error(), "private-token")
				assert.NotContains(t, err.Error(), "private-read-error")
				assert.NotContains(t, err.Error(), "private-close-error")
			})
		}
	}
}

type closeErrorBody struct{ io.Reader }

func (closeErrorBody) Close() error { return errors.New("private-close-error") }

func TestAnalyzerInvalidDefinitionDoesNotReachHTTP(t *testing.T) {
	client := newRESTTransport(t, DefaultAPIVersion, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid JSON must not reach the transport")
		return nil, nil
	})
	_, err := client.CreateAnalyzer(t.Context(), CreateAnalyzerRequest{AnalyzerID: "custom", Definition: []byte(`{`)})
	require.Error(t, err)
}

func TestAnalyzerResponsesCloseBodies(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusAccepted} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			body := &measuredBody{reader: strings.NewReader(readyAnalyzer)}
			client := newRESTTransport(t, DefaultAPIVersion, func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Body: body, Request: request}, nil
			})
			_, err := client.GetAnalyzer(t.Context(), "custom")
			assert.Equal(t, status != http.StatusOK, err != nil)
			assert.True(t, body.closed)
		})
	}
}
