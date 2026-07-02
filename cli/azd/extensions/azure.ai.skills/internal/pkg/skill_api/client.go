// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package skill_api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
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
	// ContentTypeZip is the response content type for /skills/{name}/content
	// and /skills/{name}/versions/{version}/content. It is also the part
	// content-type the client uses when uploading a single zip via multipart.
	ContentTypeZip = "application/zip"

	// MaxDownloadBytes caps the wire size of /content responses to bound
	// memory before extraction enforces its own uncompressed cap. Set to
	// match the archive uncompressed limit; a legitimate zip is far smaller
	// after compression, so this only trips on egregious responses.
	MaxDownloadBytes = 512 * 1024 * 1024

	//nolint:gosec // OAuth scope identifier, not a credential
	BearerScope = "https://ai.azure.com/.default"

	userAgentPrefix = "azd-ext-azure-ai-skills"
)

type Client struct {
	endpoint       string
	pipeline       runtime.Pipeline
	maxDownloadCap int64
}

// NewClient returns a Skills client rooted at endpoint, using cred for
// bearer-token auth.
func NewClient(endpoint string, cred azcore.TokenCredential, extensionVersion string) *Client {
	return newClient(endpoint, cred, extensionVersion, false)
}

// WithMaxDownloadBytes overrides the per-response download size cap on the
// client. Intended for testing; production callers use the default
// MaxDownloadBytes value.
func (c *Client) WithMaxDownloadBytes(cap int64) *Client {
	c.maxDownloadCap = cap
	return c
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
		endpoint:       strings.TrimRight(endpoint, "/"),
		pipeline:       pipeline,
		maxDownloadCap: MaxDownloadBytes,
	}
}

// CreateVersionInline POSTs application/json to /skills/{name}/versions.
// If the skill does not exist yet, the service auto-creates it.
func (c *Client) CreateVersionInline(ctx context.Context, name string, req CreateVersionRequest) (*SkillVersion, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal create version request: %w", err)
	}

	httpReq, err := runtime.NewRequest(ctx, http.MethodPost, c.versionsURL(name, "", nil))
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
	return decodeJSON[SkillVersion](resp.Body)
}

// CreateVersionFromZip uploads a single .zip archive as multipart/form-data
// to /skills/{name}/versions. The server extracts the archive and validates
// the contents (SKILL.md, etc.).
func (c *Client) CreateVersionFromZip(
	ctx context.Context, name, fileName string, archive io.Reader, makeDefault bool,
) (*SkillVersion, error) {
	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)

	// files[] part — a single zip with application/zip content type.
	partHeader := textproto.MIMEHeader{}
	partHeader.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="files"; filename=%q`, fileName))
	partHeader.Set("Content-Type", ContentTypeZip)
	part, err := mw.CreatePart(partHeader)
	if err != nil {
		return nil, fmt.Errorf("create multipart files part: %w", err)
	}
	if _, err := io.Copy(part, archive); err != nil {
		return nil, fmt.Errorf("copy archive to multipart: %w", err)
	}

	if makeDefault {
		if err := mw.WriteField("default", "true"); err != nil {
			return nil, fmt.Errorf("write multipart default field: %w", err)
		}
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	httpReq, err := runtime.NewRequest(ctx, http.MethodPost, c.versionsURL(name, "", nil))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	if err := httpReq.SetBody(streaming(bytes.NewReader(buf.Bytes())), mw.FormDataContentType()); err != nil {
		return nil, fmt.Errorf("set request body: %w", err)
	}
	httpReq.Raw().ContentLength = int64(buf.Len())
	addStandardHeaders(httpReq)

	resp, err := c.pipeline.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated) {
		return nil, runtime.NewResponseError(resp)
	}
	return decodeJSON[SkillVersion](resp.Body)
}

// GetSkill returns the metadata for a skill.
func (c *Client) GetSkill(ctx context.Context, name string) (*Skill, error) {
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
	return decodeJSON[Skill](resp.Body)
}

// UpdateSkillDefaultVersion repoints the skill's default_version to an
// existing version identifier. The skill resource carries no other mutable
// metadata; per-version content is immutable, so all other updates go
// through CreateVersionInline / CreateVersionFromZip.
func (c *Client) UpdateSkillDefaultVersion(ctx context.Context, name, version string) (*Skill, error) {
	body, err := json.Marshal(UpdateSkillRequest{DefaultVersion: version})
	if err != nil {
		return nil, fmt.Errorf("marshal update skill request: %w", err)
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
	return decodeJSON[Skill](resp.Body)
}

// DeleteSkill removes a skill and all of its versions.
func (c *Client) DeleteSkill(ctx context.Context, name string) (*DeleteSkillResponse, error) {
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
		return &DeleteSkillResponse{Name: name, Deleted: true}, nil
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return &DeleteSkillResponse{Name: name, Deleted: true}, nil
	}

	var dr DeleteSkillResponse
	if err := json.Unmarshal(raw, &dr); err != nil {
		return nil, fmt.Errorf("unmarshal delete response: %w", err)
	}
	if dr.Name == "" {
		dr.Name = name
	}
	return &dr, nil
}

// ListSkills fetches one page of skills.
func (c *Client) ListSkills(ctx context.Context, opts ListOptions, afterCursor string) (*PagedResult[Skill], error) {
	q := pagingQuery(opts, afterCursor)
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
	return decodeJSON[PagedResult[Skill]](resp.Body)
}

// ListAllSkills fetches every page and returns the flattened slice. If limit
// is positive, ListAllSkills stops once that many items are collected.
func (c *Client) ListAllSkills(ctx context.Context, opts ListOptions, limit int) ([]Skill, error) {
	var all []Skill
	cursor := ""
	for {
		page, err := c.ListSkills(ctx, opts, cursor)
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

// GetSkillVersion retrieves a specific version envelope.
func (c *Client) GetSkillVersion(ctx context.Context, name, version string) (*SkillVersion, error) {
	httpReq, err := runtime.NewRequest(ctx, http.MethodGet, c.versionsURL(name, "/"+url.PathEscape(version), nil))
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
	return decodeJSON[SkillVersion](resp.Body)
}

// ListSkillVersions fetches one page of versions for a skill.
func (c *Client) ListSkillVersions(
	ctx context.Context, name string, opts ListOptions, afterCursor string,
) (*PagedResult[SkillVersion], error) {
	q := pagingQuery(opts, afterCursor)
	httpReq, err := runtime.NewRequest(ctx, http.MethodGet, c.versionsURL(name, "", q))
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
	return decodeJSON[PagedResult[SkillVersion]](resp.Body)
}

// DeleteSkillVersion deletes a single version.
func (c *Client) DeleteSkillVersion(ctx context.Context, name, version string) (*DeleteSkillVersionResponse, error) {
	httpReq, err := runtime.NewRequest(ctx, http.MethodDelete, c.versionsURL(name, "/"+url.PathEscape(version), nil))
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
		return &DeleteSkillVersionResponse{Name: name, Version: version, Deleted: true}, nil
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return &DeleteSkillVersionResponse{Name: name, Version: version, Deleted: true}, nil
	}

	var dr DeleteSkillVersionResponse
	if err := json.Unmarshal(raw, &dr); err != nil {
		return nil, fmt.Errorf("unmarshal delete response: %w", err)
	}
	if dr.Name == "" {
		dr.Name = name
	}
	if dr.Version == "" {
		dr.Version = version
	}
	return &dr, nil
}

// DownloadSkillContent fetches the zip content for the default version of a
// skill. The server always returns application/zip.
func (c *Client) DownloadSkillContent(ctx context.Context, name string) ([]byte, error) {
	return c.downloadContent(ctx, c.skillURL(name, "/content", nil))
}

// DownloadVersionContent fetches the zip content for a specific version.
func (c *Client) DownloadVersionContent(ctx context.Context, name, version string) ([]byte, error) {
	return c.downloadContent(ctx, c.versionsURL(name, "/"+url.PathEscape(version)+"/content", nil))
}

func (c *Client) downloadContent(ctx context.Context, fullURL string) ([]byte, error) {
	httpReq, err := runtime.NewRequest(ctx, http.MethodGet, fullURL)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	addStandardHeaders(httpReq)
	httpReq.Raw().Header.Set("Accept", ContentTypeZip)

	resp, err := c.pipeline.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return nil, runtime.NewResponseError(resp)
	}

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		if !strings.HasPrefix(strings.ToLower(ct), ContentTypeZip) {
			return nil, fmt.Errorf("unexpected download content type %q (want %s)", ct, ContentTypeZip)
		}
	}

	cap := c.maxDownloadCap

	// Fail fast on a server-declared oversize before reading the body.
	if resp.ContentLength > cap {
		return nil, fmt.Errorf(
			"download size %d exceeds the %d byte limit",
			resp.ContentLength, cap,
		)
	}

	// Read one extra byte so we can distinguish "exactly at limit" from
	// "tried to send more than the limit".
	body, err := io.ReadAll(io.LimitReader(resp.Body, cap+1))
	if err != nil {
		return nil, fmt.Errorf("read download body: %w", err)
	}
	if int64(len(body)) > cap {
		return nil, fmt.Errorf("download exceeds the %d byte limit", cap)
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
	return c.buildURL("/skills/"+url.PathEscape(name)+suffix, extraQuery)
}

func (c *Client) versionsURL(name, suffix string, extraQuery url.Values) string {
	return c.buildURL("/skills/"+url.PathEscape(name)+"/versions"+suffix, extraQuery)
}

func pagingQuery(opts ListOptions, afterCursor string) url.Values {
	q := url.Values{}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Order != "" {
		q.Set("order", opts.Order)
	}
	if afterCursor != "" {
		q.Set("after", afterCursor)
	}
	return q
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

func decodeJSON[T any](body io.Reader) (*T, error) {
	var out T
	if err := json.NewDecoder(body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &out, nil
}

type readSeekNopCloser struct{ io.ReadSeeker }

func (readSeekNopCloser) Close() error { return nil }

func streaming(rs io.ReadSeeker) io.ReadSeekCloser {
	return readSeekNopCloser{rs}
}
