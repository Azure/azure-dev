// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/log"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }

type testCredential struct {
	options []policy.TokenRequestOptions
	err     error
	empty   bool
}

func (c *testCredential) GetToken(_ context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.options = append(c.options, options)
	token := "private-access-token"
	if c.empty {
		token = ""
	}
	return azcore.AccessToken{Token: token, ExpiresOn: time.Now().Add(time.Hour)}, c.err
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestTransportOptionsValidation(t *testing.T) {
	for _, endpoint := range []string{
		"https://RESOURCE.services.ai.azure.com", " https://resource.services.ai.azure.com/ ",
		"https://resource.services.ai.azure.com/contentunderstanding/",
	} {
		client, err := NewRESTTransport(endpoint, &testCredential{}, nil)
		require.NoError(t, err)
		assert.Equal(t, "https://resource.services.ai.azure.com/contentunderstanding", client.endpoint)
		assert.Equal(t, DefaultAPIVersion, client.apiVersion)
	}
	for _, endpoint := range []string{
		"", "http://resource.test", "https:///path", "https://user:password@resource.test",
		"https://resource.test?sig=private-token", "https://resource.test#private-token", "https://resource.test?",
		"https://resource.test/analyzers", "https://resource.test:99999", "https://resource.test:0",
		"https://resource.test:invalid", "https://resource.test/%63ontentunderstanding", "https://resource.test/%",
	} {
		client, err := NewRESTTransport(endpoint, &testCredential{}, nil)
		require.Nil(t, client)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private-token")
	}
	client, err := NewRESTTransport("https://resource.services.ai.azure.com", nil, nil)
	assert.Nil(t, client)
	require.Error(t, err)
	for _, options := range []TransportOptions{
		{APIVersion: "2027-01-01"}, {APIVersion: "private-token"},
		{RequestTimeout: -time.Second}, {Version: "invalid\r\nprivate-token"},
	} {
		client, err := NewRESTTransport("https://resource.services.ai.azure.com", &testCredential{}, &options)
		assert.Nil(t, client)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private-token")
	}
}

func transportOperations() []struct {
	name string
	read bool
	run  func(context.Context, *RESTTransport) error
} {
	return []struct {
		name string
		read bool
		run  func(context.Context, *RESTTransport) error
	}{
		{"get", true, func(ctx context.Context, c *RESTTransport) error {
			_, err := c.GetAnalyzer(ctx, "custom")
			return err
		}},
		{"create", false, func(ctx context.Context, c *RESTTransport) error {
			_, err := c.CreateAnalyzer(ctx, CreateAnalyzerRequest{AnalyzerID: "custom", Definition: []byte(`{}`)})
			return err
		}},
		{"copy", false, func(ctx context.Context, c *RESTTransport) error {
			_, err := c.CopyAnalyzer(ctx, CopyAnalyzerRequest{SourceAnalyzerID: "source", DestinationAnalyzerID: "custom"})
			return err
		}},
		{"grant", false, func(ctx context.Context, c *RESTTransport) error {
			_, err := c.GrantCopyAuthorization(ctx, GrantCopyAuthorizationRequest{
				AnalyzerID: "custom", TargetAzureResourceID: "/target",
			})
			return err
		}},
		{"update", false, func(ctx context.Context, c *RESTTransport) error {
			_, err := c.UpdateDefaults(ctx, UpdateDefaultsRequest{ModelDeployments: map[string]string{}})
			return err
		}},
		{"delete", false, func(ctx context.Context, c *RESTTransport) error { return c.DeleteAnalyzer(ctx, "custom") }},
		{"analyze", false, func(ctx context.Context, c *RESTTransport) error {
			_, err := c.Analyze(ctx, AnalyzeRequest{
				AnalyzerID: "custom", Mode: AnalysisModeAsync, Input: Input{URL: "https://input.test/a"},
			})
			return err
		}},
		{"inline", false, func(ctx context.Context, c *RESTTransport) error {
			_, err := c.Analyze(ctx, AnalyzeRequest{
				AnalyzerID: "custom", Mode: AnalysisModeInline, Input: Input{URL: "https://input.test/a"},
			})
			return err
		}},
	}
}

func TestContextCannotOverrideCURetrySafety(t *testing.T) {
	for _, operation := range transportOperations() {
		t.Run(operation.name, func(t *testing.T) {
			calls := 0
			client := newRESTTransport(t, PreviewAPIVersion, func(request *http.Request) (*http.Response, error) {
				calls++
				deadline, present := request.Context().Deadline()
				assert.True(t, present)
				assert.Greater(t, time.Until(deadline), time.Minute)
				result := response(http.StatusServiceUnavailable, "")
				result.Header.Set("x-ms-retry-after-ms", "1")
				return result, nil
			})
			ctx := policy.WithRetryOptions(t.Context(), policy.RetryOptions{
				MaxRetries: 10, RetryDelay: time.Millisecond, TryTimeout: time.Millisecond,
				ShouldRetry: func(*http.Response, error) bool { return true },
			})
			require.Error(t, operation.run(ctx, client))
			expected := 1
			if operation.read {
				expected = 3
			}
			assert.Equal(t, expected, calls)
		})
	}
}

func TestPipelineHeadersAndVersions(t *testing.T) {
	for _, version := range []string{DefaultAPIVersion, PreviewAPIVersion} {
		t.Run(version, func(t *testing.T) {
			credential := &testCredential{}
			client, err := NewRESTTransport("https://resource.test", credential, &TransportOptions{
				APIVersion: version, Version: "0.0.1",
				Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
					assert.Equal(t, "https", req.URL.Scheme)
					assert.Equal(t, "/contentunderstanding/analyzers/invoice", req.URL.Path)
					assert.Equal(t, version, req.URL.Query().Get("api-version"))
					assert.Equal(t, "false", req.URL.Query().Get("allowReplace"))
					assert.True(t, strings.HasPrefix(req.Header.Get("Authorization"), "Bearer "))
					assert.Equal(t, "application/json", req.Header.Get("Accept"))
					assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
					assert.Contains(t, req.Header.Get("User-Agent"), "azd-ext-azure-ai-content-understanding/0.0.1")
					_, err := uuid.Parse(req.Header.Get("x-ms-client-request-id"))
					assert.NoError(t, err)
					assert.Equal(t, "12345678-90ab-cdef-1234-567890abcdef", req.Header.Get("x-ms-correlation-request-id"))
					data, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					assert.JSONEq(t, `{"baseAnalyzerId":"prebuilt-document"}`, string(data))
					return response(http.StatusOK, `{"analyzerId":"invoice","status":"ready"}`), nil
				}),
			})
			require.NoError(t, err)
			traceID, err := trace.TraceIDFromHex("1234567890abcdef1234567890abcdef")
			require.NoError(t, err)
			ctx := trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID}))
			_, err = client.CreateAnalyzer(ctx, CreateAnalyzerRequest{
				AnalyzerID: "invoice", Definition: []byte(`{"baseAnalyzerId":"prebuilt-document"}`),
			})
			require.NoError(t, err)
			require.Len(t, credential.options, 1)
			assert.Equal(t, []string{cognitiveServicesScope}, credential.options[0].Scopes)
			assert.Empty(t, credential.options[0].TenantID)
		})
	}
}

func TestRetrySafetyMatrix(t *testing.T) {
	for _, operation := range transportOperations() {
		for _, failure := range []string{"503", "transport", "401-challenge"} {
			t.Run(operation.name+"/"+failure, func(t *testing.T) {
				calls := 0
				var ids []string
				client := newRESTTransport(t, PreviewAPIVersion, func(req *http.Request) (*http.Response, error) {
					calls++
					ids = append(ids, req.Header.Get("x-ms-client-request-id"))
					if calls > 1 {
						return response(http.StatusOK, `{"analyzerId":"custom","status":"ready"}`), nil
					}
					if failure == "transport" {
						return nil, errors.New("private-transport-detail")
					}
					result := response(http.StatusServiceUnavailable, `{}`)
					result.Header.Set("x-ms-retry-after-ms", "1")
					if failure == "401-challenge" {
						result.StatusCode = http.StatusUnauthorized
						result.Header.Set("WWW-Authenticate", `Bearer error="insufficient_claims", claims="e30="`)
					}
					return result, nil
				})
				err := operation.run(t.Context(), client)
				if operation.read && failure != "401-challenge" {
					require.NoError(t, err)
					assert.Equal(t, 2, calls)
					assert.Equal(t, ids[0], ids[1])
				} else {
					require.Error(t, err)
					assert.Equal(t, 1, calls, "writes and authentication challenges must not replay")
				}
			})
		}
	}
}

func TestRetryStatusAndRetryAfter(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 413, 429, 500, 502, 503, 504, 408} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			client := newRESTTransport(t, DefaultAPIVersion, func(*http.Request) (*http.Response, error) {
				calls++
				result := response(status, `{}`)
				result.Header.Set("Retry-After", "3600")
				return result, nil
			})
			_, err := client.GetAnalyzer(t.Context(), "custom")
			require.Error(t, err)
			assert.Equal(t, 1, calls)
			var domain *Error
			require.ErrorAs(t, err, &domain)
			assert.Equal(t, time.Hour, domain.RetryAfter)
		})
	}
}

func TestReadRetryExhaustion(t *testing.T) {
	calls := 0
	client := newRESTTransport(t, DefaultAPIVersion, func(*http.Request) (*http.Response, error) {
		calls++
		result := response(http.StatusServiceUnavailable, `{"error":{"code":"Unavailable"}}`)
		result.Header.Set("x-ms-retry-after-ms", "1")
		return result, nil
	})
	_, err := client.GetAnalyzer(t.Context(), "custom")
	require.Error(t, err)
	assert.Equal(t, 3, calls)
	var domain *Error
	require.ErrorAs(t, err, &domain)
	assert.Equal(t, "Unavailable", domain.Code)
}

func TestTransientReadStatusRetries(t *testing.T) {
	for _, status := range []int{408, 429, 500, 502, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			client := newRESTTransport(t, DefaultAPIVersion, func(*http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					result := response(status, `{}`)
					result.Header.Set("x-ms-retry-after-ms", "1")
					return result, nil
				}
				return response(http.StatusOK, `{"analyzerId":"custom","status":"ready"}`), nil
			})
			_, err := client.GetAnalyzer(t.Context(), "custom")
			require.NoError(t, err)
			assert.Equal(t, 2, calls)
		})
	}
}

func TestLogicalRequestsUseDistinctIDs(t *testing.T) {
	var ids []string
	client := newRESTTransport(t, DefaultAPIVersion, func(req *http.Request) (*http.Response, error) {
		ids = append(ids, req.Header.Get("x-ms-client-request-id"))
		return response(http.StatusOK, `{"analyzerId":"a..b","status":"ready"}`), nil
	})
	for range 2 {
		_, err := client.GetAnalyzer(t.Context(), "a..b")
		require.NoError(t, err)
	}
	require.Len(t, ids, 2)
	assert.NotEqual(t, ids[0], ids[1])
}

func TestPipelineCancellationAndAuthentication(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("private-credential-detail")} {
		t.Run(cause.Error(), func(t *testing.T) {
			calls := 0
			client, err := NewRESTTransport("https://resource.test", &testCredential{err: cause}, &TransportOptions{
				Transport: transportFunc(func(*http.Request) (*http.Response, error) {
					calls++
					return response(200, `{}`), nil
				}),
			})
			require.NoError(t, err)
			_, err = client.GetAnalyzer(t.Context(), "custom")
			require.Error(t, err)
			assert.Zero(t, calls)
			assert.NotContains(t, err.Error(), "private-credential-detail")
			if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
				assert.ErrorIs(t, err, cause)
			}
		})
	}
	credential := &testCredential{empty: true}
	client, err := NewRESTTransport("https://resource.services.ai.azure.com", credential, nil)
	require.NoError(t, err)
	_, err = client.GetAnalyzer(t.Context(), "custom")
	var domain *Error
	require.ErrorAs(t, err, &domain)
	assert.Equal(t, ErrorAuthentication, domain.Kind)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = client.GetAnalyzer(ctx, "custom")
	assert.ErrorIs(t, err, context.Canceled)
	assert.Len(t, credential.options, 1)
}

func TestTimeoutAndInFlightCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "cancel"}[cancel], func(t *testing.T) {
			ctx, stop := context.WithCancel(t.Context())
			defer stop()
			calls := 0
			client, err := NewRESTTransport("https://resource.test", &testCredential{}, &TransportOptions{
				RequestTimeout: 10 * time.Millisecond,
				Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
					calls++
					if cancel {
						stop()
					}
					<-req.Context().Done()
					return nil, req.Context().Err()
				}),
			})
			require.NoError(t, err)
			_, err = client.CreateAnalyzer(ctx, CreateAnalyzerRequest{AnalyzerID: "custom", Definition: []byte(`{}`)})
			if cancel {
				assert.ErrorIs(t, err, context.Canceled)
			} else {
				assert.ErrorIs(t, err, context.DeadlineExceeded)
			}
			assert.Equal(t, 1, calls)
		})
	}
}

type measuredBody struct {
	reader io.Reader
	read   int
	closed bool
}

func (b *measuredBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += n
	return n, err
}

func (b *measuredBody) Close() error { b.closed = true; return nil }

func TestBoundedErrorBodyAndRedaction(t *testing.T) {
	for _, payload := range []string{
		`{"error":{"code":"Forbidden","message":"private-body-token","details":[{"message":"private-detail"}]}}`,
		`private malformed response`, strings.Repeat("private-body-token", maxErrorBodyBytes),
		`{"error":{"code":"invalid code private-token"}}`,
	} {
		body := &measuredBody{reader: strings.NewReader(payload)}
		client := newRESTTransport(t, DefaultAPIVersion, func(*http.Request) (*http.Response, error) {
			result := response(http.StatusForbidden, "")
			result.Header.Set("Authorization", "private-header")
			result.Body = body
			return result, nil
		})
		_, err := client.GetAnalyzer(t.Context(), "custom")
		var domain *Error
		require.ErrorAs(t, err, &domain)
		assert.Equal(t, ErrorAuthentication, domain.Kind)
		assert.Equal(t, http.StatusForbidden, domain.StatusCode)
		assert.NotContains(t, err.Error(), "private")
		assert.Nil(t, domain.Cause)
		assert.True(t, body.closed)
		assert.LessOrEqual(t, body.read, maxErrorBodyBytes+1)
	}
}

func TestResponseErrorCategories(t *testing.T) {
	for _, test := range []struct {
		status int
		kind   ErrorKind
	}{
		{300, ErrorService}, {400, ErrorValidation}, {401, ErrorAuthentication}, {403, ErrorAuthentication},
		{404, ErrorNotFound}, {409, ErrorConflict}, {413, ErrorLimit}, {429, ErrorThrottled}, {500, ErrorService},
	} {
		client := newRESTTransport(t, DefaultAPIVersion, func(*http.Request) (*http.Response, error) {
			result := response(test.status, `{"error":{"code":"BodyCode"}}`)
			result.Header.Set("x-ms-error-code", "HeaderCode")
			return result, nil
		})
		err := client.DeleteAnalyzer(t.Context(), "custom")
		var domain *Error
		require.ErrorAs(t, err, &domain)
		assert.Equal(t, test.kind, domain.Kind)
		assert.Equal(t, "HeaderCode", domain.Code)
		assert.NotEmpty(t, domain.Suggestion)
	}
}

func TestPipelineLogsRedactTokensAndBodies(t *testing.T) {
	var messages []string
	log.SetListener(func(_ log.Event, message string) { messages = append(messages, message) })
	log.SetEvents(log.EventRequest, log.EventResponse)
	t.Cleanup(func() {
		log.SetListener(nil)
		log.SetEvents()
	})
	client := newRESTTransport(t, DefaultAPIVersion, func(*http.Request) (*http.Response, error) {
		result := response(403, `{"error":{"code":"Forbidden","message":"private-response-body"}}`)
		result.Header.Set("Set-Cookie", "private-cookie")
		return result, nil
	})
	_, err := client.Analyze(t.Context(), AnalyzeRequest{
		AnalyzerID: "custom", Mode: AnalysisModeAsync,
		Input: Input{URL: "https://input.test/a?sig=private-query-value"},
	})
	require.Error(t, err)
	require.NotEmpty(t, messages)
	for _, secret := range []string{
		"private-access-token", "private-response-body", "private-cookie", "private-query-value",
	} {
		assert.NotContains(t, strings.Join(messages, "\n"), secret)
	}
}

type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("private-read-error") }
func (brokenBody) Close() error             { return errors.New("private-close-error") }

func TestBrokenAndMissingResponses(t *testing.T) {
	for _, fn := range []transportFunc{
		func(*http.Request) (*http.Response, error) { return nil, nil },
		func(*http.Request) (*http.Response, error) {
			result := response(403, "")
			result.Body = brokenBody{}
			return result, nil
		},
		func(*http.Request) (*http.Response, error) {
			result := response(403, "")
			result.Body = closeErrorBody{strings.NewReader(`{}`)}
			return result, nil
		},
		func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 403, Header: http.Header{}}, nil
		},
	} {
		client := newRESTTransport(t, DefaultAPIVersion, fn)
		err := client.DeleteAnalyzer(t.Context(), "custom")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private")
	}
}

func TestDefaultTransportDoesNotFollowRedirect(t *testing.T) {
	targetCalls := 0
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	transport := newHTTPTransport(time.Second)
	transport.Transport = source.Client().Transport
	client, err := NewRESTTransport(source.URL, &testCredential{}, &TransportOptions{Transport: transport})
	require.NoError(t, err)
	_, err = client.GetAnalyzer(t.Context(), "custom")
	require.Error(t, err)
	assert.Zero(t, targetCalls)
}

func TestRetryAfterParsing(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, test := range []struct {
		value string
		want  time.Duration
	}{
		{"", 0}, {"invalid", 0}, {"-1", 0}, {"0", 0}, {"5", 5 * time.Second},
		{"9223372036854775807", 24 * time.Hour},
		{now.Add(time.Minute).Format(http.TimeFormat), time.Minute},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0},
	} {
		assert.Equal(t, test.want, retryAfter(test.value, now))
	}
}

func TestBoundedTransportDoesNotBufferSuccess(t *testing.T) {
	body := &measuredBody{reader: bytes.NewReader([]byte(`{"result":"private-content"}`))}
	transport := boundedErrorTransport{inner: transportFunc(func(*http.Request) (*http.Response, error) {
		result := response(200, "")
		result.Body = body
		return result, nil
	})}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://resource.test", nil)
	require.NoError(t, err)
	result, err := transport.Do(request)
	require.NoError(t, err)
	assert.Zero(t, body.read)
	require.NoError(t, result.Body.Close())
	assert.True(t, body.closed)
}
