// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordedFixture struct {
	SchemaVersion int `json:"schema_version"`
	Source        struct {
		Repository       string   `json:"repository"`
		Commit           string   `json:"commit"`
		Path             string   `json:"path"`
		SHA256           string   `json:"sha256"`
		License          string   `json:"license"`
		InteractionCount int      `json:"interaction_count"`
		Normalizations   []string `json:"normalizations"`
		Sample           *struct {
			Path        string `json:"path"`
			SHA256      string `json:"sha256"`
			Description string `json:"description"`
		} `json:"sample,omitempty"`
		UnusedInteractions []struct {
			Index  int    `json:"index"`
			Reason string `json:"reason"`
		} `json:"unused_interactions"`
	} `json:"source"`
	Interactions      []recordedInteraction `json:"interactions"`
	ReportExpectation *struct {
		Derivation     string            `json:"derivation"`
		SourceFiles    map[string]string `json:"source_files"`
		Runtime        map[string]string `json:"runtime"`
		Normalizations []string          `json:"normalizations"`
		Report         json.RawMessage   `json:"report"`
	} `json:"report_expectation,omitempty"`
	CommandExpectation *struct {
		Derivation        string                     `json:"derivation"`
		SourceFiles       map[string]string          `json:"source_files"`
		Runtime           map[string]string          `json:"runtime"`
		Normalizations    []string                   `json:"normalizations"`
		NativeDifferences []string                   `json:"native_differences"`
		Results           map[string]json.RawMessage `json:"results"`
	} `json:"command_expectation,omitempty"`
}

type recordedInteraction struct {
	SourceIndex int `json:"source_index"`
	Request     struct {
		Method   string            `json:"method"`
		URL      string            `json:"url"`
		Headers  map[string]string `json:"headers"`
		BodyMode string            `json:"body_mode"`
		Body     json.RawMessage   `json:"body"`
	} `json:"request"`
	Response struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
		Body    json.RawMessage   `json:"body"`
	} `json:"response"`
}

func loadRecordedFixture(t *testing.T, name string) recordedFixture {
	t.Helper()
	inventory := loadParityInventory(t)
	for _, entry := range inventory.Recordings {
		if entry.Name != name {
			continue
		}
		body, err := os.ReadFile(filepath.Join("../..", entry.File))
		require.NoError(t, err)
		require.Equal(t, entry.SHA256, sha256Text(body))
		var fixture recordedFixture
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		require.NoError(t, decoder.Decode(&fixture))
		require.Equal(t, 2, fixture.SchemaVersion)
		require.Equal(t, paritySourceCommit, fixture.Source.Commit)
		require.Equal(t, inventory.Source.Repository, fixture.Source.Repository)
		require.Equal(t, "tests/playback/recordings/"+name+".yaml", fixture.Source.Path)
		require.Equal(t, entry.SourceSHA256, fixture.Source.SHA256)
		require.Equal(t, "MIT", fixture.Source.License)
		require.Positive(t, fixture.Source.InteractionCount)
		require.NotEmpty(t, fixture.Source.Normalizations)
		require.NotEmpty(t, fixture.Interactions)
		require.NoError(t, recordingHygiene(body))
		if name == "analyzer_test" {
			require.NotNil(t, fixture.ReportExpectation)
			require.NotEmpty(t, fixture.ReportExpectation.Derivation)
			require.Len(t, fixture.ReportExpectation.SourceFiles, 5)
			require.Len(t, fixture.ReportExpectation.Runtime, 3)
			for _, dependency := range []string{"python", "azure-ai-contentunderstanding", "azure-core"} {
				require.NotEmpty(t, fixture.ReportExpectation.Runtime[dependency])
			}
			require.Len(t, fixture.ReportExpectation.Normalizations, 2)
			require.True(t, json.Valid(fixture.ReportExpectation.Report))
			for path, digest := range fixture.ReportExpectation.SourceFiles {
				require.True(t, strings.HasPrefix(path, "packages/") || strings.HasPrefix(path, "tests/"))
				require.NotContains(t, path, "..")
				require.Regexp(t, "^[0-9a-f]{64}$", digest)
			}
		} else {
			require.Nil(t, fixture.ReportExpectation)
		}
		if name == "doctor" || name == "schema_suggest" {
			require.NotNil(t, fixture.CommandExpectation)
			require.NotEmpty(t, fixture.CommandExpectation.Derivation)
			require.Len(t, fixture.CommandExpectation.SourceFiles, 6)
			for path, digest := range fixture.CommandExpectation.SourceFiles {
				require.True(t, strings.HasPrefix(path, "packages/") || strings.HasPrefix(path, "tests/"))
				require.NotContains(t, path, "..")
				require.Regexp(t, "^[0-9a-f]{64}$", digest)
			}
			require.Len(t, fixture.CommandExpectation.Runtime, 3)
			for _, dependency := range []string{"python", "azure-ai-contentunderstanding", "azure-core"} {
				require.NotEmpty(t, fixture.CommandExpectation.Runtime[dependency])
			}
			require.NotEmpty(t, fixture.CommandExpectation.NativeDifferences)
			require.Len(t, fixture.CommandExpectation.Results, 2)
			for _, frontend := range []string{"azure", "azd"} {
				require.True(t, json.Valid(fixture.CommandExpectation.Results[frontend]))
			}
			if name == "doctor" {
				require.Len(t, fixture.CommandExpectation.Normalizations, 1)
			} else {
				require.Empty(t, fixture.CommandExpectation.Normalizations)
			}
		} else {
			require.Nil(t, fixture.CommandExpectation)
		}
		seen := map[int]bool{}
		last := -1
		for _, interaction := range fixture.Interactions {
			require.NoError(t, recordingRequestOrigin(interaction.Request.URL))
			require.Greater(t, interaction.SourceIndex, last)
			require.Less(t, interaction.SourceIndex, fixture.Source.InteractionCount)
			last = interaction.SourceIndex
			seen[interaction.SourceIndex] = true
			require.True(t, json.Valid(interaction.Response.Body))
			switch interaction.Request.BodyMode {
			case "none":
				require.Equal(t, "null", string(interaction.Request.Body))
			case "json":
				require.True(t, json.Valid(interaction.Request.Body))
			case "scrubbed-binary":
				require.Equal(t, `"<binary-scrubbed>"`, string(interaction.Request.Body))
				require.NotNil(t, fixture.Source.Sample)
			case "scrubbed-json":
				require.Equal(t, `"<binary-scrubbed>"`, string(interaction.Request.Body))
			default:
				t.Fatalf("recording %s has an unsupported body mode", name)
			}
		}
		for _, unused := range fixture.Source.UnusedInteractions {
			require.False(t, seen[unused.Index])
			require.Positive(t, unused.Index)
			require.Less(t, unused.Index, fixture.Source.InteractionCount)
			require.NotEmpty(t, unused.Reason)
			seen[unused.Index] = true
		}
		require.Len(t, seen, fixture.Source.InteractionCount, "every original interaction needs a disposition")
		if fixture.Source.Sample != nil {
			require.Equal(t, "sample_files/sample_invoice.pdf", fixture.Source.Sample.Path)
			require.Len(t, fixture.Source.Sample.SHA256, 64)
			require.NotEmpty(t, fixture.Source.Sample.Description)
		}
		switch name {
		case "analyzer_list":
			require.Len(t, fixture.Source.UnusedInteractions, 2)
		case "analyzer_delete":
			require.Len(t, fixture.Source.UnusedInteractions, 1)
		default:
			require.Empty(t, fixture.Source.UnusedInteractions)
		}
		return fixture
	}
	t.Fatalf("recording %s has no reviewed import entry", name)
	return recordedFixture{}
}

type recordedTransport struct {
	mu         sync.Mutex
	fixture    recordedFixture
	next       int
	binaryBody []byte
	jsonBody   json.RawMessage
	failure    error
}

type recordingMismatch struct{}

func (recordingMismatch) Error() string { return "The request does not match the offline recording." }
func (recordingMismatch) NonRetriable() {}

func (transport *recordedTransport) Do(request *http.Request) (*http.Response, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	reject := func() (*http.Response, error) {
		transport.failure = recordingMismatch{}
		return nil, transport.failure
	}
	if transport.next >= len(transport.fixture.Interactions) || transport.failure != nil {
		return reject()
	}
	if err := request.Context().Err(); err != nil {
		transport.failure = err
		return nil, err
	}
	interaction := transport.fixture.Interactions[transport.next]
	expected, err := url.Parse(interaction.Request.URL)
	if err != nil {
		return reject()
	}
	if request.Method != interaction.Request.Method ||
		request.URL.Scheme != "https" || !strings.EqualFold(request.URL.Host, expected.Host) ||
		request.URL.User != nil || request.URL.Fragment != "" || request.URL.EscapedPath() != expected.EscapedPath() {
		return reject()
	}
	actualQuery, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return reject()
	}
	expectedQuery, err := url.ParseQuery(expected.RawQuery)
	if err != nil || actualQuery.Encode() != expectedQuery.Encode() {
		return reject()
	}
	if request.Header.Get("Authorization") != "Bearer playback-token" ||
		request.Header.Get("Ocp-Apim-Subscription-Key") != "" || request.Header.Get("Api-Key") != "" {
		return reject()
	}
	for name, value := range interaction.Request.Headers {
		if request.Header.Get(name) != value {
			return reject()
		}
	}
	var body []byte
	if request.Body != nil {
		body, err = io.ReadAll(io.LimitReader(request.Body, 1024*1024+1))
		if err != nil || len(body) > 1024*1024 {
			return reject()
		}
	}
	expectedBody := interaction.Request.Body
	switch interaction.Request.BodyMode {
	case "none":
		if len(body) != 0 {
			return reject()
		}
	case "scrubbed-binary":
		if !bytes.Equal(expectedBody, []byte(`"<binary-scrubbed>"`)) ||
			len(transport.binaryBody) == 0 || !bytes.Equal(body, transport.binaryBody) {
			return reject()
		}
	case "json", "scrubbed-json":
		if interaction.Request.BodyMode == "scrubbed-json" {
			if !bytes.Equal(expectedBody, []byte(`"<binary-scrubbed>"`)) {
				return reject()
			}
			expectedBody = transport.jsonBody
		}
		actual, actualErr := recordedJSON(body)
		wanted, wantedErr := recordedJSON(expectedBody)
		if actualErr != nil || wantedErr != nil || !reflect.DeepEqual(actual, wanted) {
			return reject()
		}
	default:
		return reject()
	}
	headers := http.Header{}
	for name, value := range interaction.Response.Headers {
		headers.Set(name, value)
	}
	responseBody := interaction.Response.Body
	if interaction.Response.Status == http.StatusNoContent {
		if !bytes.Equal(responseBody, []byte("null")) {
			return reject()
		}
		responseBody = nil
	}
	transport.next++
	return &http.Response{
		Request: request, StatusCode: interaction.Response.Status, Header: headers,
		Body: io.NopCloser(bytes.NewReader(responseBody)),
	}, nil
}

func (transport *recordedTransport) complete() error {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.failure != nil {
		return transport.failure
	}
	if transport.next != len(transport.fixture.Interactions) {
		return errors.New("The offline recording has an unconsumed interaction.")
	}
	return nil
}

func recordedJSON(body []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("The recorded body must contain exactly one JSON value.")
	}
	return value, nil
}

type recordedCredential struct{ calls int }

func (credential *recordedCredential) GetToken(
	ctx context.Context, options policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	if err := ctx.Err(); err != nil {
		return azcore.AccessToken{}, err
	}
	if !slices.Equal(options.Scopes, []string{"https://cognitiveservices.azure.com/.default"}) {
		return azcore.AccessToken{}, errors.New("Unexpected offline credential scope.")
	}
	credential.calls++
	return azcore.AccessToken{Token: "playback-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

var recordedCommandCases = []struct {
	name, recording string
	args            []string
}{
	{"defaults-show-json", "defaults_get", []string{"defaults", "show"}},
	{"analyzer-list-json", "analyzer_list", []string{"analyzer", "list"}},
	{"analyzer-list-custom-sorted", "analyzer_list",
		[]string{"analyzer", "list", "--kind", "custom", "--sort-by", "analyzerId"}},
	{"analyzer-list-created-at", "analyzer_list", []string{"analyzer", "list", "--sort-by", "createdAt"}},
}

func newRecordedCommand(t *testing.T, transport *recordedTransport, analyzer string) *cobra.Command {
	t.Helper()
	_ = newDefaultsFixture(t)
	credential := &recordedCredential{}
	opens, closes := 0, 0
	t.Cleanup(func() {
		require.NoError(t, transport.complete())
		assert.Equal(t, 1, opens)
		assert.Equal(t, 1, closes)
		assert.Equal(t, len(transport.fixture.Interactions), credential.calls)
	})
	return newRootCommand(func(
		_ *cobra.Command, extCtx *azdext.ExtensionContext, options config.Options,
	) (*serviceSession, error) {
		opens++
		require.Equal(t, "https://sanitized.services.ai.azure.com", options.Endpoint)
		require.Equal(t, cuclient.DefaultAPIVersion, options.APIVersion)
		require.Equal(t, analyzer, options.DefaultAnalyzer)
		client, err := cuclient.NewRESTTransport(options.Endpoint, credential,
			&cuclient.TransportOptions{APIVersion: options.APIVersion, Transport: transport})
		if err != nil {
			return nil, err
		}
		return &serviceSession{client: client, apiVersion: options.APIVersion, analyzer: analyzer,
			endpoint: options.Endpoint, environment: extCtx.Environment,
			close: func() { closes++ }}, nil
	})
}

func TestRecordedServiceCommands(t *testing.T) {
	for _, test := range recordedCommandCases {
		t.Run(test.name, func(t *testing.T) {
			for _, format := range []string{"default", "json", "none"} {
				t.Run(format, func(t *testing.T) {
					fixture := loadRecordedFixture(t, test.recording)
					transport := &recordedTransport{fixture: fixture}
					root := newRecordedCommand(t, transport, "")
					var stdout, stderr bytes.Buffer
					root.SetOut(&stdout)
					root.SetErr(&stderr)
					args := append(slices.Clone(test.args), "-o", format, "--debug", "--no-prompt",
						"--endpoint", "https://sanitized.services.ai.azure.com", "--api-version", cuclient.DefaultAPIVersion)
					require.NoError(t, ValidateInvocation(root, args))
					root.SetArgs(args)
					require.NoError(t, root.ExecuteContext(t.Context()))
					require.NoError(t, transport.complete())
					assert.Empty(t, stderr.String())
					if format == "none" {
						assert.Empty(t, stdout.String())
						return
					}
					expected := fixture.Interactions[len(fixture.Interactions)-1].Response.Body
					if test.recording == "analyzer_list" {
						var result struct {
							Value []json.RawMessage `json:"value"`
						}
						require.NoError(t, json.Unmarshal(expected, &result))
						require.Len(t, result.Value, 88)
						key := func(raw json.RawMessage, name string) string {
							var value map[string]json.RawMessage
							require.NoError(t, json.Unmarshal(raw, &value))
							var result string
							require.NoError(t, json.Unmarshal(value[name], &result))
							return result
						}
						if test.name == "analyzer-list-custom-sorted" {
							for _, item := range result.Value {
								require.True(t, strings.HasPrefix(key(item, "analyzerId"), "prebuilt-"))
							}
							result.Value = []json.RawMessage{}
						}
						sortBy := "analyzerId"
						if test.name == "analyzer-list-created-at" {
							sortBy = "createdAt"
						}
						slices.SortStableFunc(result.Value, func(a, b json.RawMessage) int {
							return strings.Compare(key(a, sortBy), key(b, sortBy))
						})
						var err error
						expected, err = json.Marshal(result.Value)
						require.NoError(t, err)
					}
					wanted, err := recordedJSON(expected)
					require.NoError(t, err)
					actual, err := recordedJSON(stdout.Bytes())
					require.NoError(t, err)
					assert.Equal(t, wanted, actual, "compare complete results, not just IDs or mapping keys")
				})
			}
		})
	}
}

func TestRecordedTransportRejectsUnexpectedRequests(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer server.Close()
	for _, test := range []struct {
		name   string
		change func(*http.Request)
	}{
		{"host", func(r *http.Request) { r.URL, _ = url.Parse(server.URL) }},
		{"method", func(r *http.Request) { r.Method = http.MethodPost }},
		{"path", func(r *http.Request) { r.URL.Path += "/unexpected" }},
		{"query", func(r *http.Request) { r.URL.RawQuery += "&unexpected=secret" }},
		{"version", func(r *http.Request) { r.URL.RawQuery = "api-version=2026-06-01-preview" }},
		{"header", func(r *http.Request) { r.Header.Del("Accept") }},
		{"body", func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader(`{"unexpected":true}`)) }},
		{"credential", func(r *http.Request) { r.Header.Set("Authorization", "Bearer secret") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := loadRecordedFixture(t, "defaults_get")
			transport := &recordedTransport{fixture: fixture}
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, fixture.Interactions[0].Request.URL, nil)
			require.NoError(t, err)
			request.Header.Set("Accept", "application/json")
			request.Header.Set("Authorization", "Bearer playback-token")
			test.change(request)
			response, err := transport.Do(request)
			require.Error(t, err)
			require.Nil(t, response)
			assert.NotContains(t, err.Error(), "secret")
			require.Error(t, transport.complete())
		})
	}
	assert.Zero(t, hits.Load(), "unexpected origins never reach a real HTTP transport")
	fixture := loadRecordedFixture(t, "defaults_get")
	transport := &recordedTransport{fixture: fixture}
	require.ErrorContains(t, transport.complete(), "unconsumed")
	client, err := cuclient.NewRESTTransport(server.URL, &recordedCredential{},
		&cuclient.TransportOptions{Transport: transport})
	require.NoError(t, err)
	_, err = client.GetDefaults(t.Context())
	require.Error(t, err)
	assert.Zero(t, hits.Load(), "the production REST client cannot fall back to network on playback mismatch")
	transport = &recordedTransport{fixture: fixture}
	client, err = cuclient.NewRESTTransport("https://sanitized.services.ai.azure.com", &recordedCredential{},
		&cuclient.TransportOptions{Transport: transport})
	require.NoError(t, err)
	_, err = client.GetDefaults(t.Context())
	require.NoError(t, err)
	_, err = client.GetDefaults(t.Context())
	require.Error(t, err, "unexpected repeated calls cannot reuse an exhausted recording")
	require.Error(t, transport.complete())
}

func TestRecordedJSONMatchingPreservesNumbersAndRejectsBodies(t *testing.T) {
	for _, test := range []struct {
		name, expected, actual string
		match                  bool
	}{
		{"order", `{"a":1,"b":2}`, `{"b":2,"a":1}`, true},
		{"number", `{"n":9007199254740993}`, `{"n":9007199254740992}`, false},
		{"extra", `{"a":1}`, `{"a":1,"b":2}`, false},
		{"trailing", `{"a":1}`, `{"a":1} {}`, false},
		{"malformed", `{"a":1}`, `{`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := loadRecordedFixture(t, "defaults_get")
			fixture.Interactions[0].Request.Method = http.MethodPost
			fixture.Interactions[0].Request.BodyMode = "json"
			fixture.Interactions[0].Request.Body = json.RawMessage(test.expected)
			transport := &recordedTransport{fixture: fixture}
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.Interactions[0].Request.URL,
				strings.NewReader(test.actual))
			require.NoError(t, err)
			request.Header.Set("Accept", "application/json")
			request.Header.Set("Authorization", "Bearer playback-token")
			response, err := transport.Do(request)
			if test.match {
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				require.NoError(t, transport.complete())
			} else {
				require.Error(t, err)
				require.Nil(t, response)
				require.Error(t, transport.complete())
			}
		})
	}
}

var recordingPrivateText = regexp.MustCompile(
	`(?i)(/home/|/Users/|[a-z]:\\Users\\|dev\.azure\.com|visualstudio\.com|coreai-microsoft|msazure)`)
var recordingSecretText = regexp.MustCompile(
	`(?i)(authorization|api[-_]?key|subscription[-_]?key|client_secret|password|token|sig|` +
		`ocp-apim-subscription-key|set-cookie|x-ms-client-request-id|x-ms-request-id)\s*["']?\s*[:=]`)
var recordingSecretValueText = regexp.MustCompile(
	`(?i)(api[-_]?key|authorization|sig|subscription[-_]?key)` +
		`["'=:\s\v\x{001c}-\x{001f}\x{0085}\p{Z}]+[A-Za-z0-9%+/_-]{8,}`)

// Python's Unicode IGNORECASE also folds dotted and dotless I.
var recordingCaseFold = strings.NewReplacer("\u0130", "i", "\u0131", "i")
var recordingURLText = regexp.MustCompile(`(?i)https?://[^\s"\\]+`)
var recordingSensitiveQuery = map[string]bool{
	"api-key": true, "code": true, "se": true, "sig": true, "si": true, "sip": true,
	"skoid": true, "sks": true, "skt": true, "sktid": true, "skv": true, "sp": true,
	"spr": true, "sr": true, "srt": true, "st": true, "subscription-key": true, "sv": true,
}

func recordingRequestOrigin(raw string) error {
	link, err := url.Parse(raw)
	if err != nil || link.Scheme != "https" || link.Host != "sanitized.services.ai.azure.com" || link.User != nil {
		return errors.New("Recording request does not use the reviewed placeholder HTTPS origin.")
	}
	return nil
}

func recordingHygiene(data []byte) error {
	value, err := recordedJSON(data)
	if err != nil {
		return err
	}
	var inspect func(any) error
	inspect = func(value any) error {
		switch value := value.(type) {
		case string:
			folded := recordingCaseFold.Replace(value)
			if recordingPrivateText.MatchString(folded) || recordingSecretText.MatchString(folded) ||
				recordingSecretValueText.MatchString(folded) {
				return errors.New("Recording contains private or credential-like content; manual review required.")
			}
			for _, text := range recordingURLText.FindAllString(value, -1) {
				link, err := url.Parse(text)
				if err != nil {
					return errors.New("Recording contains an invalid URL; manual review required.")
				}
				host := strings.ToLower(link.Hostname())
				if strings.HasSuffix(host, ".services.ai.azure.com") && host != "sanitized.services.ai.azure.com" {
					return errors.New("Recording contains an unreviewed CU resource hostname.")
				}
				query, err := url.ParseQuery(link.RawQuery)
				if err != nil {
					return errors.New("Recording contains an invalid URL query; manual review required.")
				}
				for key, items := range query {
					if recordingSensitiveQuery[strings.ToLower(key)] {
						return errors.New("Recording contains a credential-like URL query.")
					}
					for _, item := range items {
						if err := inspect(item); err != nil {
							return err
						}
					}
				}
			}
		case map[string]any:
			for key, item := range value {
				if recordingSecretText.MatchString(key + "=") {
					return errors.New("Recording contains a credential-like key.")
				}
				if err := inspect(item); err != nil {
					return err
				}
			}
		case []any:
			for _, item := range value {
				if err := inspect(item); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return inspect(value)
}

func TestRecordedFixtureHygiene(t *testing.T) {
	for _, recording := range loadParityInventory(t).Recordings {
		t.Run(recording.Name, func(t *testing.T) { _ = loadRecordedFixture(t, recording.Name) })
	}
	for _, secret := range []string{
		"https://storage.example/file?sig=private",
		"https://storage.example/customer's-file?sig=private",
		"https://storage.example/file?name=customer's-file&sig=private",
		"/home/customer/input.pdf", `C:\Users\customer\input.pdf`, "Authorization: Bearer private",
		"api-key=private", "https://dev.azure.com/private",
	} {
		body, err := json.Marshal(map[string]string{"content": secret})
		require.NoError(t, err)
		require.Error(t, recordingHygiene(body))
	}
	require.Error(t, recordingHygiene([]byte(`{"api-key":"private"}`)))
	require.NoError(t, recordingHygiene([]byte(`{"content":"public sample"}`)))
}
