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
	"net/url"
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

func newTestTransport(t *testing.T, fn transportFunc) *RESTTransport {
	t.Helper()
	client, err := NewRESTTransport("https://resource.test/", &testCredential{}, &TransportOptions{Transport: fn})
	require.NoError(t, err)
	return client
}

func TestTransportOptionsValidation(t *testing.T) {
	for _, endpoint := range []string{
		"https://RESOURCE.test", " https://resource.test/ ", "https://resource.test/contentunderstanding/",
	} {
		client, err := NewRESTTransport(endpoint, &testCredential{}, nil)
		require.NoError(t, err)
		assert.Equal(t, "https://resource.test/contentunderstanding", client.endpoint)
		assert.Equal(t, DefaultAPIVersion, client.apiVersion)
	}

	for _, endpoint := range []string{
		"", "http://resource.test", "https:///path", "https://user:private-token@resource.test",
		"https://resource.test?sig=private-token", "https://resource.test#private-token", "https://resource.test?",
		"https://resource.test/analyzers", "https://resource.test:99999", "https://resource.test:0",
		"https://resource.test:invalid", "https://resource.test/%63ontentunderstanding", "https://resource.test/%",
	} {
		client, err := NewRESTTransport(endpoint, &testCredential{}, nil)
		require.Nil(t, client)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private-token")
	}
	client, err := NewRESTTransport("https://resource.test", nil, nil)
	assert.Nil(t, client)
	require.Error(t, err)
	for _, options := range []TransportOptions{
		{APIVersion: "2027-01-01-preview"}, {APIVersion: "private-token"},
		{RequestTimeout: -time.Second}, {Version: "invalid\r\nprivate-token"},
	} {
		client, err := NewRESTTransport("https://resource.test", &testCredential{}, &options)
		assert.Nil(t, client)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private-token")
	}
}

func TestContextCannotOverrideCURetrySafety(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodPost,
		http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			calls := 0
			client := newTestTransport(t, func(request *http.Request) (*http.Response, error) {
				calls++
				deadline, present := request.Context().Deadline()
				assert.True(t, present)
				assert.Greater(t, time.Until(deadline), time.Minute, "ignore caller's shorter SDK try-timeout override")
				result := response(http.StatusServiceUnavailable, "")
				result.Header.Set("x-ms-retry-after-ms", "1")
				return result, nil
			})
			ctx := policy.WithRetryOptions(t.Context(), policy.RetryOptions{
				MaxRetries: 10, RetryDelay: time.Millisecond, TryTimeout: time.Millisecond,
				ShouldRetry: func(*http.Response, error) bool { return true },
			})
			_, err := client.request(ctx, method, "/analyzers/custom", nil, nil, "")
			require.Error(t, err)
			expected := 1
			if method == http.MethodGet || method == http.MethodHead {
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
			calls := 0
			client, err := NewRESTTransport("https://resource.test", credential, &TransportOptions{
				APIVersion: version, Version: "0.0.1",
				Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
					calls++
					assert.Equal(t, "https", req.URL.Scheme)
					assert.Equal(t, "/contentunderstanding/analyzers/invoice", req.URL.Path)
					assert.Equal(t, version, req.URL.Query().Get("api-version"))
					assert.Equal(t, "Bearer private-access-token", req.Header.Get("Authorization"))
					assert.Equal(t, "application/json", req.Header.Get("Accept"))
					assert.Contains(t, req.Header.Get("User-Agent"), "azd-ext-azure-ai-content-understanding/0.0.1")
					_, parseErr := uuid.Parse(req.Header.Get("x-ms-client-request-id"))
					assert.NoError(t, parseErr)
					assert.Equal(t, "12345678-90ab-cdef-1234-567890abcdef", req.Header.Get("x-ms-correlation-request-id"))
					assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
					data, readErr := io.ReadAll(req.Body)
					assert.NoError(t, readErr)
					assert.JSONEq(t, `{"baseAnalyzerId":"prebuilt-document"}`, string(data))
					return response(http.StatusOK, `{}`), nil
				}),
			})
			require.NoError(t, err)
			query := url.Values{"api-version": []string{"override"}}
			traceID, err := trace.TraceIDFromHex("1234567890abcdef1234567890abcdef")
			require.NoError(t, err)
			ctx := trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID}))
			resp, err := client.request(ctx, http.MethodPut, "/analyzers/invoice", query,
				readSeekCloser{strings.NewReader(`{"baseAnalyzerId":"prebuilt-document"}`)}, "application/json")
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Equal(t, "override", query.Get("api-version"), "caller query must not be mutated")
			assert.Equal(t, 1, calls)
			require.Len(t, credential.options, 1)
			assert.Equal(t, []string{cognitiveServicesScope}, credential.options[0].Scopes)
			assert.Empty(t, credential.options[0].TenantID, "tenant selection belongs to the injected azd credential")
		})
	}
}

type readSeekCloser struct{ io.ReadSeeker }

func (readSeekCloser) Close() error { return nil }

func TestRetrySafetyMatrix(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut,
		http.MethodPost, http.MethodPatch, http.MethodDelete} {
		for _, failure := range []string{"503", "transport", "401-challenge"} {
			t.Run(method+"/"+failure, func(t *testing.T) {
				calls := 0
				var ids []string
				client := newTestTransport(t, func(req *http.Request) (*http.Response, error) {
					calls++
					ids = append(ids, req.Header.Get("x-ms-client-request-id"))
					if calls > 1 {
						return response(http.StatusOK, `{}`), nil
					}
					switch failure {
					case "transport":
						return nil, errors.New("private-transport-detail")
					case "401-challenge":
						resp := response(http.StatusUnauthorized, `{}`)
						resp.Header.Set("WWW-Authenticate", `Bearer error="insufficient_claims", claims="e30="`)
						return resp, nil
					default:
						return response(http.StatusServiceUnavailable, `{}`), nil
					}
				})
				resp, err := client.request(t.Context(), method, "/analyzers", nil, nil, "")
				read := method == http.MethodGet || method == http.MethodHead
				if read && failure != "401-challenge" {
					require.NoError(t, err)
					require.NoError(t, resp.Body.Close())
					assert.Equal(t, 2, calls)
					assert.Equal(t, ids[0], ids[1], "retries retain the logical request ID")
				} else {
					require.Error(t, err)
					assert.Nil(t, resp)
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
			client := newTestTransport(t, func(*http.Request) (*http.Response, error) {
				calls++
				resp := response(status, `{}`)
				// An excessive Retry-After must stop retrying, not retry earlier than instructed.
				resp.Header.Set("Retry-After", "3600")
				return resp, nil
			})
			_, err := client.request(t.Context(), http.MethodGet, "/analyzers", nil, nil, "")
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
	client := newTestTransport(t, func(*http.Request) (*http.Response, error) {
		calls++
		return response(http.StatusServiceUnavailable, `{"error":{"code":"Unavailable"}}`), nil
	})
	_, err := client.request(t.Context(), http.MethodGet, "/analyzers", nil, nil, "")
	require.Error(t, err)
	assert.Equal(t, 3, calls, "initial attempt plus two retries")
	var domain *Error
	require.ErrorAs(t, err, &domain)
	assert.Equal(t, "Unavailable", domain.Code)
}

func TestTransientReadStatusRetries(t *testing.T) {
	for _, status := range []int{408, 429, 500, 502, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			client := newTestTransport(t, func(*http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return response(status, `{}`), nil
				}
				return response(http.StatusOK, `{}`), nil
			})
			resp, err := client.request(t.Context(), http.MethodGet, "/analyzers", nil, nil, "")
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Equal(t, 2, calls)
		})
	}
}

func TestLogicalRequestsUseDistinctIDs(t *testing.T) {
	var ids []string
	client := newTestTransport(t, func(req *http.Request) (*http.Response, error) {
		ids = append(ids, req.Header.Get("x-ms-client-request-id"))
		return response(http.StatusOK, `{}`), nil
	})
	for range 2 {
		resp, err := client.request(t.Context(), http.MethodGet, "/analyzers/a..b", nil, nil, "")
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
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
			_, err = client.request(t.Context(), http.MethodPost, "/analyzers", nil, nil, "")
			require.Error(t, err)
			assert.Zero(t, calls)
			assert.NotContains(t, err.Error(), "private-credential-detail")
			if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
				assert.ErrorIs(t, err, cause)
			}
		})
	}
	credential := &testCredential{empty: true}
	client, err := NewRESTTransport("https://resource.test", credential, nil)
	require.NoError(t, err)
	_, err = client.request(t.Context(), http.MethodGet, "/analyzers", nil, nil, "")
	var domain *Error
	require.ErrorAs(t, err, &domain)
	assert.Equal(t, ErrorAuthentication, domain.Kind)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = client.request(ctx, http.MethodGet, "/analyzers", nil, nil, "")
	assert.ErrorIs(t, err, context.Canceled)
	assert.Len(t, credential.options, 1, "pre-cancelled requests must not acquire tokens")
}

func TestTimeoutAndInFlightCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		ctx, stop := context.WithCancel(t.Context())
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
		_, err = client.request(ctx, http.MethodPost, "/analyzers", nil, nil, "")
		stop()
		if cancel {
			assert.ErrorIs(t, err, context.Canceled)
		} else {
			assert.ErrorIs(t, err, context.DeadlineExceeded)
		}
		assert.Equal(t, 1, calls)
	}
}

func TestPipelineRequestValidation(t *testing.T) {
	calls := 0
	client := newTestTransport(t, func(*http.Request) (*http.Response, error) {
		calls++
		return response(200, `{}`), nil
	})
	for _, path := range []string{"https://other.test/secret", "//other.test/secret", "analyzers",
		"/../secret", "/analyzers?sig=private-token", "/analyzers#fragment", "/%2e%2e/secret", "/%"} {
		_, err := client.request(t.Context(), http.MethodGet, path, nil, nil, "")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private-token")
	}
	_, err := client.request(t.Context(), "INVALID METHOD", "/analyzers", nil, nil, "")
	require.Error(t, err)
	_, err = client.request(t.Context(), http.MethodPost, "/analyzers", nil,
		readSeekCloser{strings.NewReader("{}")}, "")
	require.Error(t, err)
	assert.Zero(t, calls)
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
		client := newTestTransport(t, func(*http.Request) (*http.Response, error) {
			resp := response(http.StatusForbidden, "")
			resp.Header.Set("Authorization", "Bearer private-header-token")
			resp.Body = body
			return resp, nil
		})
		_, err := client.request(t.Context(), http.MethodPost, "/analyzers", nil, nil, "")
		var domain *Error
		require.ErrorAs(t, err, &domain)
		assert.Equal(t, ErrorAuthentication, domain.Kind)
		assert.Equal(t, http.StatusForbidden, domain.StatusCode)
		assert.NotContains(t, err.Error(), "private")
		assert.Nil(t, domain.Cause, "raw service response must not be retained in an error chain")
		assert.True(t, body.closed)
		assert.LessOrEqual(t, body.read, maxErrorBodyBytes+1, "bound before azcore buffers error bodies")
	}
}

func TestResponseErrorCategories(t *testing.T) {
	for _, tt := range []struct {
		status int
		kind   ErrorKind
	}{
		{300, ErrorService}, {400, ErrorValidation}, {401, ErrorAuthentication}, {403, ErrorAuthentication},
		{404, ErrorNotFound}, {409, ErrorConflict}, {413, ErrorLimit}, {429, ErrorThrottled}, {500, ErrorService},
	} {
		client := newTestTransport(t, func(*http.Request) (*http.Response, error) {
			resp := response(tt.status, `{"error":{"code":"BodyCode"}}`)
			resp.Header.Set("x-ms-error-code", "HeaderCode")
			return resp, nil
		})
		_, err := client.request(t.Context(), http.MethodPost, "/analyzers", nil, nil, "")
		var domain *Error
		require.ErrorAs(t, err, &domain)
		assert.Equal(t, tt.kind, domain.Kind)
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
	client := newTestTransport(t, func(*http.Request) (*http.Response, error) {
		resp := response(403, `{"error":{"code":"Forbidden","message":"private-response-body"}}`)
		resp.Header.Set("Set-Cookie", "private-cookie")
		return resp, nil
	})
	_, err := client.request(t.Context(), http.MethodPost, "/analyzers",
		url.Values{"secret": []string{"private-query-value"}},
		readSeekCloser{strings.NewReader(`{"content":"private-request-body"}`)}, "application/json")
	require.Error(t, err)
	require.NotEmpty(t, messages)
	combined := strings.Join(messages, "\n")
	for _, secret := range []string{
		"private-access-token", "private-response-body", "private-request-body", "private-cookie", "private-query-value",
	} {
		assert.NotContains(t, combined, secret)
	}
}

type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("private-read-error") }
func (brokenBody) Close() error             { return errors.New("private-close-error") }

func TestBrokenAndMissingResponses(t *testing.T) {
	for _, fn := range []transportFunc{
		func(*http.Request) (*http.Response, error) { return nil, nil },
		func(*http.Request) (*http.Response, error) {
			resp := response(500, "")
			resp.Body = brokenBody{}
			return resp, nil
		},
	} {
		client := newTestTransport(t, fn)
		_, err := client.request(t.Context(), http.MethodPost, "/analyzers", nil, nil, "")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private")
	}
	client := newTestTransport(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Header: http.Header{}}, nil
	})
	_, err := client.request(t.Context(), http.MethodPost, "/analyzers", nil, nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
}

func TestDefaultTransportDoesNotFollowRedirect(t *testing.T) {
	targetCalls := 0
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	_, err = client.request(t.Context(), http.MethodGet, "/analyzers", nil, nil, "")
	require.Error(t, err)
	assert.Zero(t, targetCalls)
}

func TestRetryAfterParsing(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tt := range []struct {
		value string
		want  time.Duration
	}{
		{"", 0}, {"invalid", 0}, {"-1", 0}, {"0", 0}, {"5", 5 * time.Second},
		{"9223372036854775807", 24 * time.Hour},
		{now.Add(time.Minute).Format(http.TimeFormat), time.Minute},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0},
	} {
		assert.Equal(t, tt.want, retryAfter(tt.value, now))
	}
}

func TestSuccessBodyRemainsStreaming(t *testing.T) {
	body := &measuredBody{reader: bytes.NewReader([]byte(`{"result":"private-content"}`))}
	client := newTestTransport(t, func(*http.Request) (*http.Response, error) {
		resp := response(200, "")
		resp.Body = body
		return resp, nil
	})
	resp, err := client.request(t.Context(), http.MethodGet, "/analyzers", nil, nil, "")
	require.NoError(t, err)
	assert.Zero(t, body.read)
	require.NoError(t, resp.Body.Close())
	assert.True(t, body.closed)
}
