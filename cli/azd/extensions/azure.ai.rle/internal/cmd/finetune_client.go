// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// finetuneJobsPath is the public fine-tuning job creation route. The API version is
// carried in the "v1" URL segment (UrlSegmentApiVersionReader), so no api-version query
// parameter is sent. See finetunesapi service/AzureOpenAI.Api/Controllers/V1/FineTuningController.cs
// and service/AzureOpenAI.Api/Controllers/Base/FineTuningControllerBase.cs (ControllerName = "fine_tuning").
const finetuneJobsPath = "/openai/v1/fine_tuning/jobs"

// finetuneTokenScope is the Cognitive Services / Azure OpenAI resource scope.
const finetuneTokenScope = "https://cognitiveservices.azure.com/.default" //nolint:gosec // OAuth scope, not a credential

// finetuneMethodTypeRleEnvironment selects the RL-environment fine-tuning method: the
// named RLE, not a grader, supplies the reward signal. This method is currently hidden
// from the public Swagger surface ([SwaggerIgnore] on FineTuningMethodType.RLEnvironment
// and RLEnvironmentMethodRequest in finetunesapi) and only completes for base models the
// service has enabled for Loom-backed RL-environment training
// (FineTuningConfiguration.RLEnvironmentSupportedModels).
const finetuneMethodTypeRleEnvironment = "rl_environment"

type finetuneClient struct {
	baseUrl    string
	credential azcore.TokenCredential
	httpClient *http.Client
}

var createFinetuneClient = newFinetuneClient

type finetuneRleEnvironmentConfig struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	MaxEpisodeSteps *int   `json:"max_episode_steps,omitempty"`
}

type finetuneMethodRequest struct {
	Type           string                       `json:"type"`
	RleEnvironment finetuneRleEnvironmentConfig `json:"rl_environment"`
}

// finetuneJobCreationRequest mirrors finetunesapi's FineTuningJobCreation. training_file
// is intentionally optional: an RL-environment job "carries no training file, grader,
// tools or response format" because the environment owns reward computation.
type finetuneJobCreationRequest struct {
	Model          string                 `json:"model"`
	TrainingFile   string                 `json:"training_file,omitempty"`
	ValidationFile *string                `json:"validation_file,omitempty"`
	Suffix         *string                `json:"suffix,omitempty"`
	Method         *finetuneMethodRequest `json:"method,omitempty"`
}

type finetuneJobResource struct {
	Id             string `json:"id"`
	Status         string `json:"status,omitempty"`
	Model          string `json:"model,omitempty"`
	FineTunedModel string `json:"fine_tuned_model,omitempty"`
	TrainingFile   string `json:"training_file,omitempty"`
	ValidationFile string `json:"validation_file,omitempty"`
}

type finetuneHTTPError struct {
	statusCode int
	body       string
}

func (e *finetuneHTTPError) Error() string {
	return fmt.Sprintf("fine-tuning API returned HTTP %d: %s", e.statusCode, strings.TrimSpace(e.body))
}

func finetuneServiceError(err error) error {
	result := &azdext.ServiceError{
		Message:     err.Error(),
		ServiceName: "finetunesapi",
		Suggestion: "Verify the fine-tuning API endpoint, that the base model is enabled for RL-environment " +
			"training, and that the RLE name/version are published in the project set by FOUNDRY_PROJECT_ENDPOINT.",
	}
	var httpErr *finetuneHTTPError
	if errors.As(err, &httpErr) {
		result.StatusCode = httpErr.statusCode
		switch {
		case httpErr.statusCode == http.StatusUnauthorized || httpErr.statusCode == http.StatusForbidden:
			result.Suggestion = "Verify your Azure sign-in and access to the fine-tuning resource, then retry."
		case httpErr.statusCode == http.StatusBadRequest:
			result.Suggestion = "The rl_environment method requires a Loom-eligible base model and a ready, " +
				"published RLE version in the Foundry project set by FOUNDRY_PROJECT_ENDPOINT. Check the error " +
				"detail above and retry."
		}
	}
	return result
}

func newFinetuneClient(endpoint string) (*finetuneClient, error) {
	normalizedEndpoint, err := normalizeFinetuneEndpoint(endpoint)
	if err != nil {
		return nil, err
	}

	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("create Azure credential: %w", err)
	}

	return newFinetuneClientWithCredential(normalizedEndpoint, credential), nil
}

func newFinetuneClientWithCredential(endpoint string, credential azcore.TokenCredential) *finetuneClient {
	return &finetuneClient{
		baseUrl:    strings.TrimRight(endpoint, "/"),
		credential: credential,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// createJob submits a fine-tuning job. azureAIProject identifies the Foundry project that
// owns the named RLE; the service only reads FoundryAIProjectName from a proxy-only header,
// so a direct caller instead sends azureai-project plus azureai-project-is-default=true
// (see finetunesapi FineTuningControllerBase.cs, the FoundryProxyTransform fallback path).
func (c *finetuneClient) createJob(
	ctx context.Context,
	request finetuneJobCreationRequest,
	azureAIProject string,
) (*finetuneJobResource, error) {
	var result finetuneJobResource
	headers := map[string]string{
		"azureai-project":            azureAIProject,
		"azureai-project-is-default": "true",
	}
	if err := c.do(ctx, http.MethodPost, finetuneJobsPath, headers, request, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *finetuneClient) do(
	ctx context.Context,
	method string,
	path string,
	headers map[string]string,
	body any,
	target any,
) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	requestUrl, err := url.Parse(c.baseUrl + path)
	if err != nil {
		return fmt.Errorf("create request URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, requestUrl.String(), reader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if !strings.EqualFold(req.URL.Scheme, "https") {
		return errors.New("fine-tuning API authentication requires an HTTPS endpoint")
	}
	authorization, err := c.authorizationHeader(ctx)
	if err != nil {
		return fmt.Errorf("authenticate to fine-tuning API: %w", err)
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Accept", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call fine-tuning API %s: %w", c.baseUrl, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read fine-tuning API response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &finetuneHTTPError{statusCode: resp.StatusCode, body: string(respBody)}
	}

	if target == nil || len(respBody) == 0 {
		return nil
	}

	if err := json.Unmarshal(respBody, target); err != nil {
		return fmt.Errorf("decode fine-tuning API response: %w", err)
	}

	return nil
}

func (c *finetuneClient) authorizationHeader(ctx context.Context) (string, error) {
	token, err := c.credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{finetuneTokenScope},
	})
	if err != nil {
		return "", err
	}
	return "Bearer " + token.Token, nil
}
