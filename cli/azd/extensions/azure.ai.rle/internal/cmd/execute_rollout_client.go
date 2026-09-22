// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"azure.ai.rle/internal/rollouts"
)

// executeRolloutHeader is the classic AzureML "forwarded user token" header. RLE's
// ExecuteRollout controller requires it and forwards it, unmodified, as the bearer
// token Capture Proxy uses to open a Loom-backed session
// (vienna EntryPoints/Controllers/V1/ExecuteRolloutController.cs GetForwardedLoomBearerToken;
// EntryPoints/Services/ExecuteRollout/ExecuteRolloutService.cs OpenSessionAsync). The Foundry
// project gateway accepts the same https://ai.azure.com/.default token already used for
// Authorization, per vienna's local-dev Execute Rollout walkthrough
// (src/azureml-api/src/RLE/README.md "Run a local Gym Execute Rollout").
const executeRolloutHeader = "aml-user-token" //nolint:gosec // header name, not a credential

// rolloutModelSelection binds one rollout to a real, currently active Loom training
// session and sampler checkpoint. loom_session_id/checkpoint_id must never be
// fabricated; the CLI provisions them by calling Loom itself (see loom_session_client.go)
// so callers of `azd ai rle rollout` never handle Loom identifiers directly.
//
// ProjectEndpoint names the Foundry project this rollout samples through. Vienna PR
// !2310739 ("Let each rollout name the Foundry project it samples through") moved this
// off a single deployment-wide `rleCaptureProxyLoomProjectEndpoint` spec parameter and
// onto RolloutModelSelection, forwarded per rollout exactly like CheckpointID. The Loom
// Capture Proxy backend now rejects a rollout with HTTP 400
// ("project_endpoint is required for the Loom Capture Proxy backend.") if this is empty.
type rolloutModelSelection struct {
	ModelName       string `json:"model_name,omitempty"`
	RendererName    string `json:"renderer_name,omitempty"`
	LoomSessionID   string `json:"loom_session_id,omitempty"`
	CheckpointID    string `json:"checkpoint_id,omitempty"`
	SequenceID      *int64 `json:"sequence_id,omitempty"`
	ProjectEndpoint string `json:"project_endpoint,omitempty"`
}

// executeRolloutRequest mirrors vienna's ExecuteRolloutRequest
// (EntryPoints/Models/ExecuteRolloutApiModels.cs). Task is the opaque JSONL record sent
// unchanged to the sandbox reset operation (Gym/OpenEnv); AgentInput is the agent-visible
// input required only by Harness targets.
type executeRolloutRequest struct {
	RolloutID  string                 `json:"rollout_id"`
	Task       json.RawMessage        `json:"task,omitempty"`
	AgentInput json.RawMessage        `json:"agent_input,omitempty"`
	Model      *rolloutModelSelection `json:"model,omitempty"`
}

type executeRolloutResponse = rollouts.Response

// executeRollout runs one isolated rollout of an exact, published environment version.
// loomBearerToken is forwarded unchanged via the aml-user-token header.
func (c *rleClient) executeRollout(
	ctx context.Context,
	environmentName string,
	environmentVersion string,
	loomBearerToken string,
	request executeRolloutRequest,
) (*executeRolloutResponse, error) {
	path := fmt.Sprintf(
		"%s/%s/versions/%s:executeRollout",
		environmentCollectionPath,
		url.PathEscape(environmentName),
		url.PathEscape(environmentVersion),
	)
	headers := map[string]string{
		executeRolloutHeader: loomBearerToken,
	}
	var result executeRolloutResponse
	if err := c.doWithHeaders(ctx, http.MethodPost, path, headers, request, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
