// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"

	"azureaiagent/internal/version"
)

const (
	skillsApiVersion    = "v1"
	skillsFeatureHeader = "Skills=V1Preview"
)

// FoundrySkillsClient registers Agent-Skills bundles with the Foundry skill
// data-plane so they can be referenced from a toolbox version. It is the
// primary (registration) path for turning a local skills/ folder into
// toolbox-attached skills.
type FoundrySkillsClient struct {
	endpoint string
	pipeline runtime.Pipeline
}

// NewFoundrySkillsClient creates a client rooted at a Foundry project endpoint.
func NewFoundrySkillsClient(endpoint string, cred azcore.TokenCredential) *FoundrySkillsClient {
	userAgent := fmt.Sprintf("azd-ext-azure-ai-agents/%s", version.Version)

	clientOptions := &policy.ClientOptions{
		Logging: policy.LogOptions{
			AllowedHeaders: []string{azsdk.MsCorrelationIdHeader, "X-Request-Id"},
		},
		PerCallPolicies: []policy.Policy{
			runtime.NewBearerTokenPolicy(cred, []string{"https://ai.azure.com/.default"}, nil),
			azsdk.NewMsCorrelationPolicy(),
			azsdk.NewUserAgentPolicy(userAgent),
		},
	}

	pipeline := runtime.NewPipeline(
		"azure-ai-agents",
		"v1.0.0",
		runtime.PipelineOptions{},
		clientOptions,
	)

	return &FoundrySkillsClient{
		endpoint: strings.TrimRight(endpoint, "/"),
		pipeline: pipeline,
	}
}

// SkillVersionObject is the response for a registered skill version.
type SkillVersionObject struct {
	Id          string `json:"id"`
	SkillId     string `json:"skill_id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	CreatedAt   int64  `json:"created_at"`
}

// SkillInlineContent carries the skill definition inline for the JSON create
// path. Description is the one-line summary; Instructions is the skill body
// (the Markdown under the SKILL.md frontmatter) injected into the agent.
type SkillInlineContent struct {
	Description  string `json:"description,omitempty"`
	Instructions string `json:"instructions"`
}

// CreateSkillVersionRequest is the body for registering a skill version via the
// JSON inline-content path. The skill name comes from the URL path; the version
// is assigned by the service. Multi-file bundles (references/, assets/) require
// the ZIP/multipart upload path instead.
type CreateSkillVersionRequest struct {
	InlineContent SkillInlineContent `json:"inline_content"`
}

// CreateSkillVersion registers (or updates) a skill at the given name and
// returns the created version. When the skill does not exist it is created.
func (c *FoundrySkillsClient) CreateSkillVersion(
	ctx context.Context,
	skillName string,
	request *CreateSkillVersionRequest,
) (*SkillVersionObject, error) {
	targetURL := fmt.Sprintf(
		"%s/skills/%s/versions?api-version=%s",
		c.endpoint, url.PathEscape(skillName), skillsApiVersion,
	)

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, targetURL)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Raw().Header.Set("Foundry-Features", skillsFeatureHeader)
	if err := req.SetBody(
		streaming.NopCloser(bytes.NewReader(payload)),
		"application/json",
	); err != nil {
		return nil, fmt.Errorf("setting request body: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()
	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated) {
		return nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	var result SkillVersionObject
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &result, nil
}

// PromoteSkillVersion updates the skill's default_version, making it the
// version resolved by references that omit an explicit version (including the
// Foundry portal's skill view). Creating a skill version does NOT
// automatically promote it — every version after the first must be promoted
// explicitly for consumers to see it as the active content.
//
// POST {endpoint}/skills/{name}?api-version=v1
func (c *FoundrySkillsClient) PromoteSkillVersion(
	ctx context.Context,
	skillName string,
	version string,
) error {
	targetURL := fmt.Sprintf(
		"%s/skills/%s?api-version=%s",
		c.endpoint, url.PathEscape(skillName), skillsApiVersion,
	)

	payload, err := json.Marshal(map[string]string{"default_version": version})
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, targetURL)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Raw().Header.Set("Foundry-Features", skillsFeatureHeader)
	if err := req.SetBody(
		streaming.NopCloser(bytes.NewReader(payload)),
		"application/json",
	); err != nil {
		return fmt.Errorf("setting request body: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()
	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return runtime.NewResponseError(resp)
	}
	return nil
}
