// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package skill_api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

const (
	// DataPlaneAPIVersion is the api-version query parameter applied to every
	// request. The Skills surface lives under the `v1` data-plane API version;
	// the preview opt-in is communicated separately via the `Foundry-Features`
	// header (see SkillsPreviewOptIn below).
	DataPlaneAPIVersion = "v1"

	// FoundryFeaturesHeader is the HTTP header that carries the preview opt-in.
	FoundryFeaturesHeader = "Foundry-Features"
	// SkillsPreviewOptIn is the required Foundry-Features value for all skill
	// operations in this preview. The TypeSpec marks every skill route with
	// WithRequiredFoundryPreviewHeader<FoundryFeaturesOptInKeys.skills_v1_preview>.
	SkillsPreviewOptIn = "Skills=V1Preview"

	// ContentTypeJSON is the request/response content type used for the JSON
	// surface.
	ContentTypeJSON = "application/json"
	// ContentTypeZip is the upload content type for POST /skills:import. The
	// TypeSpec declares `application/gzip`, but the live service returns
	// 415 Unsupported Media Type on gzip and accepts ZIP per the public docs.
	// See https://learn.microsoft.com/azure/foundry/agents/how-to/tools/skills.
	ContentTypeZip = "application/zip"
	// ContentTypeGzip is the response content type observed on
	// GET /skills/{name}:download. The same TypeSpec / docs mismatch applies
	// in reverse: docs say zip, server returns gzip. We accept either.
	ContentTypeGzip = "application/gzip"

	// BearerScope is the Azure AD scope used for the bearer-token policy.
	// Matches the scope used by the rest of the Foundry AI extension surface.
	//nolint:gosec // OAuth scope identifier, not a credential
	BearerScope = "https://ai.azure.com/.default"

	// userAgentPrefix is the User-Agent value baseline; callers append their
	// own version via the userAgent parameter to NewClient.
	userAgentPrefix = "azd-ext-azure-ai-skills"
)

// Client is the typed REST client for the Foundry Skills data-plane surface.
// All methods include the required preview header and api-version query
// parameter automatically.
type Client struct {
	endpoint string
	pipeline runtime.Pipeline
}

// NewClient returns a Skills client rooted at endpoint (already validated by
// the caller), using cred for bearer-token auth. extensionVersion is appended
// to the User-Agent value emitted by the pipeline.
func NewClient(endpoint string, cred azcore.TokenCredential, extensionVersion string) *Client {
	return newClient(endpoint, cred, extensionVersion, false)
}

func newClient(endpoint string, cred azcore.TokenCredential, extensionVersion string, allowHTTP bool) *Client {
	userAgent := userAgentPrefix
	if extensionVersion != "" {
		userAgent += "/" + extensionVersion
	}

	clientOptions := &policy.ClientOptions{
		// IncludeBody is intentionally false: skill create/update bodies carry
		// user-authored description and instructions. Body logging will be
		// enabled in a follow-up once a sanitizer is in place.
		Logging:                         policy.LogOptions{IncludeBody: false},
		InsecureAllowCredentialWithHTTP: allowHTTP,
		PerCallPolicies: []policy.Policy{
			runtime.NewBearerTokenPolicy(cred, []string{BearerScope}, &policy.BearerTokenOptions{
				InsecureAllowCredentialWithHTTP: allowHTTP,
			}),
			azsdk.NewMsCorrelationPolicy(),
			azsdk.NewUserAgentPolicy(userAgent),
		},
	}

	pipeline := runtime.NewPipeline(
		"azure-ai-skills",
		"v1.0.0",
		runtime.PipelineOptions{},
		clientOptions,
	)

	return &Client{
		endpoint: strings.TrimRight(endpoint, "/"),
		pipeline: pipeline,
	}
}

// CreateInline creates a skill from a JSON body (inline or parsed SKILL.md).
func (c *Client) CreateInline(ctx context.Context, req CreateRequest) (*Skill, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal create request: %w", err)
	}

	httpReq, err := runtime.NewRequest(ctx, http.MethodPost, c.buildURL("/skills", nil))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	if err := setJSONBody(httpReq, body); err != nil {
		return nil, err
	}
	addStandardHeaders(httpReq)

	resp, err := c.pipeline.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated) {
		return nil, runtime.NewResponseError(resp)
	}

	return decodeSkill(resp.Body)
}

// CreatePackage uploads a ZIP archive to POST /skills:import. The CLI does
// not inspect the archive's contents beyond an optional name-claim peek for
// the --force guard; server-side validation owns archive contents otherwise.
func (c *Client) CreatePackage(ctx context.Context, archive io.ReadSeeker, archiveSize int64) (*Skill, error) {
	httpReq, err := runtime.NewRequest(ctx, http.MethodPost, c.buildURL("/skills:import", nil))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	if err := httpReq.SetBody(streaming(archive), ContentTypeZip); err != nil {
		return nil, fmt.Errorf("set request body: %w", err)
	}
	httpReq.Raw().ContentLength = archiveSize
	httpReq.Raw().Header.Set("Content-Type", ContentTypeZip)
	addStandardHeaders(httpReq)

	resp, err := c.pipeline.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated) {
		return nil, runtime.NewResponseError(resp)
	}

	return decodeSkill(resp.Body)
}

// Get returns the metadata for a skill.
func (c *Client) Get(ctx context.Context, name string) (*Skill, error) {
	httpReq, err := runtime.NewRequest(ctx, http.MethodGet, c.skillURL(name, "", nil))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	addStandardHeaders(httpReq)

	resp, err := c.pipeline.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return nil, runtime.NewResponseError(resp)
	}

	return decodeSkill(resp.Body)
}

// Update merges req into the existing skill via POST /skills/{name}.
// The caller is responsible for the GET-merge-POST pattern; this method
// simply sends the supplied body.
func (c *Client) Update(ctx context.Context, name string, req UpdateRequest) (*Skill, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal update request: %w", err)
	}

	httpReq, err := runtime.NewRequest(ctx, http.MethodPost, c.skillURL(name, "", nil))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	if err := setJSONBody(httpReq, body); err != nil {
		return nil, err
	}
	addStandardHeaders(httpReq)

	resp, err := c.pipeline.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return nil, runtime.NewResponseError(resp)
	}

	return decodeSkill(resp.Body)
}

// Delete removes a skill. The service returns a small JSON body describing
// the deletion which the caller may use.
func (c *Client) Delete(ctx context.Context, name string) (*DeleteResponse, error) {
	httpReq, err := runtime.NewRequest(ctx, http.MethodDelete, c.skillURL(name, "", nil))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	addStandardHeaders(httpReq)

	resp, err := c.pipeline.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusNoContent) {
		return nil, runtime.NewResponseError(resp)
	}

	if resp.StatusCode == http.StatusNoContent {
		return &DeleteResponse{Name: name, Deleted: true}, nil
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return &DeleteResponse{Name: name, Deleted: true}, nil
	}

	var dr DeleteResponse
	if err := json.Unmarshal(raw, &dr); err != nil {
		return nil, fmt.Errorf("unmarshal delete response: %w", err)
	}
	if dr.Name == "" {
		dr.Name = name
	}
	return &dr, nil
}

// List fetches one page of skills using the supplied options. The returned
// PagedSkills includes pagination cursors the caller can use to fetch more.
func (c *Client) List(ctx context.Context, opts ListOptions, afterCursor string) (*PagedSkills, error) {
	q := url.Values{}
	if opts.Top > 0 {
		q.Set("limit", strconv.Itoa(opts.Top))
	}
	if opts.OrderBy != "" {
		q.Set("order", opts.OrderBy)
	}
	if afterCursor != "" {
		q.Set("after", afterCursor)
	}

	httpReq, err := runtime.NewRequest(ctx, http.MethodGet, c.buildURL("/skills", q))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	addStandardHeaders(httpReq)

	resp, err := c.pipeline.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return nil, runtime.NewResponseError(resp)
	}

	var wire pagedSkillsWire
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		return nil, fmt.Errorf("decode list response: %w", err)
	}
	paged := wire.toPagedSkills()
	return &paged, nil
}

// ListAll fetches every page transparently and returns the flattened slice.
// If limit is positive, ListAll stops as soon as that many items are collected.
func (c *Client) ListAll(ctx context.Context, opts ListOptions, limit int) ([]Skill, error) {
	var all []Skill
	cursor := ""
	for {
		page, err := c.List(ctx, opts, cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		if limit > 0 && len(all) >= limit {
			return all[:limit], nil
		}
		if !page.HasMore || page.LastID == "" {
			return all, nil
		}
		cursor = page.LastID
	}
}

// Download fetches the skill package and returns the raw bytes.
//
// The Foundry Skills service is asymmetric about archive format: uploads
// (`POST /skills:import`) reject `application/gzip` with 415 and require
// `application/zip`, but downloads return `application/gzip`. Rather than
// pin a single Content-Type, this client accepts either and leaves format
// detection (via magic bytes) to the caller — see DetectArchiveFormat.
func (c *Client) Download(ctx context.Context, name string) ([]byte, error) {
	httpReq, err := runtime.NewRequest(ctx, http.MethodGet, c.skillURL(name, ":download", nil))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	addStandardHeaders(httpReq)
	// Accept both formats; service ignores the value but the negotiation
	// matters for intermediaries that might transform the body.
	httpReq.Raw().Header.Set("Accept", ContentTypeZip+", "+ContentTypeGzip)

	resp, err := c.pipeline.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return nil, runtime.NewResponseError(resp)
	}

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		lc := strings.ToLower(ct)
		if !strings.HasPrefix(lc, ContentTypeZip) && !strings.HasPrefix(lc, ContentTypeGzip) {
			return nil, fmt.Errorf("unexpected download content type %q (want %s or %s)", ct, ContentTypeZip, ContentTypeGzip)
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read download body: %w", err)
	}
	return body, nil
}

// --- URL and header helpers ---

func (c *Client) buildURL(path string, extraQuery url.Values) string {
	q := url.Values{}
	q.Set("api-version", DataPlaneAPIVersion)
	for k, vs := range extraQuery {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	return c.endpoint + path + "?" + q.Encode()
}

// skillURL builds a per-skill URL with optional action suffix
// (e.g. ":download"). The skill name is URL-path-escaped to handle service-
// accepted characters safely; CLI-side validation rejects most of these.
func (c *Client) skillURL(name, suffix string, extraQuery url.Values) string {
	path := "/skills/" + url.PathEscape(name) + suffix
	return c.buildURL(path, extraQuery)
}

func setJSONBody(req *policy.Request, body []byte) error {
	if err := req.SetBody(streaming(bytes.NewReader(body)), ContentTypeJSON); err != nil {
		return fmt.Errorf("set request body: %w", err)
	}
	return nil
}

func addStandardHeaders(req *policy.Request) {
	h := req.Raw().Header
	h.Set(FoundryFeaturesHeader, SkillsPreviewOptIn)
	if h.Get("Accept") == "" {
		h.Set("Accept", ContentTypeJSON)
	}
}

func decodeSkill(body io.Reader) (*Skill, error) {
	var wire skillWire
	if err := json.NewDecoder(body).Decode(&wire); err != nil {
		return nil, fmt.Errorf("decode skill response: %w", err)
	}
	s := wire.toSkill()
	return &s, nil
}

// streaming wraps an io.ReadSeeker into the io.ReadSeekCloser required by
// runtime.NewRequest's SetBody. We never close the underlying reader; the
// caller owns its lifecycle (the SDK reads then ignores Close on this shim).
type readSeekNopCloser struct{ io.ReadSeeker }

func (readSeekNopCloser) Close() error { return nil }

func streaming(rs io.ReadSeeker) io.ReadSeekCloser {
	return readSeekNopCloser{rs}
}
