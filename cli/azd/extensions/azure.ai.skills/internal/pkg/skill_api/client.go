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
	// DataPlaneAPIVersion: skills live under v1; preview opt-in is via the
	// Foundry-Features header (SkillsPreviewOptIn).
	DataPlaneAPIVersion = "v1"

	FoundryFeaturesHeader = "Foundry-Features"
	SkillsPreviewOptIn    = "Skills=V1Preview"

	ContentTypeJSON = "application/json"
	// ContentTypeZip is the upload content type for POST /skills:import. The
	// TypeSpec declares application/gzip, but the live service returns 415 on
	// gzip and accepts ZIP per the public docs:
	// https://learn.microsoft.com/azure/foundry/agents/how-to/tools/skills.
	ContentTypeZip = "application/zip"
	// ContentTypeGzip is the observed response content type on
	// GET /skills/{name}:download. We accept either format on the wire.
	ContentTypeGzip = "application/gzip"

	//nolint:gosec // OAuth scope identifier, not a credential
	BearerScope = "https://ai.azure.com/.default"

	userAgentPrefix = "azd-ext-azure-ai-skills"
)

type Client struct {
	endpoint string
	pipeline runtime.Pipeline
}

// NewClient returns a Skills client rooted at endpoint, using cred for
// bearer-token auth.
func NewClient(endpoint string, cred azcore.TokenCredential, extensionVersion string) *Client {
	return newClient(endpoint, cred, extensionVersion, false)
}

func newClient(endpoint string, cred azcore.TokenCredential, extensionVersion string, allowHTTP bool) *Client {
	userAgent := userAgentPrefix
	if extensionVersion != "" {
		userAgent += "/" + extensionVersion
	}

	clientOptions := &policy.ClientOptions{
		// IncludeBody is intentionally false: skill bodies carry user-authored
		// description / instructions and we don't yet have a sanitizer.
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

// CreatePackage uploads a ZIP archive to POST /skills:import.
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

// Update sends req as POST /skills/{name}. Caller does GET-merge-POST.
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

// Delete removes a skill.
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

// List fetches one page of skills.
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

// ListAll fetches every page and returns the flattened slice. If limit is
// positive, ListAll stops once that many items are collected.
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

// Download fetches the skill package and returns the raw bytes. Accepts
// either ContentTypeZip or ContentTypeGzip (the service is asymmetric); the
// caller uses DetectArchiveFormat to interpret the bytes.
func (c *Client) Download(ctx context.Context, name string) ([]byte, error) {
	httpReq, err := runtime.NewRequest(ctx, http.MethodGet, c.skillURL(name, ":download", nil))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	addStandardHeaders(httpReq)
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

type readSeekNopCloser struct{ io.ReadSeeker }

func (readSeekNopCloser) Close() error { return nil }

func streaming(rs io.ReadSeeker) io.ReadSeekCloser {
	return readSeekNopCloser{rs}
}
