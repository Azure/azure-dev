// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// loomPolicyType is the only policy type RLE implements. It is sent explicitly rather than
// left for the service to assume: the discriminator is what lets a second backend be added
// without overloading these same fields, so a request that omits it is rejected.
const loomPolicyType = "loom"

// rolloutPolicy names where a rollout's weights come from. `type` selects the backend and the
// remaining fields are read according to it; for "loom" that is a real, currently active
// training session and sampler checkpoint. SessionID/CheckpointID must never be fabricated;
// the CLI provisions them by calling Loom itself (see loom_session_client.go) so callers of
// `azd ai rle rollout` never handle those identifiers directly.
//
// ProjectEndpoint names the Foundry project this rollout samples through. Vienna PR
// !2310739 ("Let each rollout name the Foundry project it samples through") moved this
// off a single deployment-wide `rleCaptureProxyLoomProjectEndpoint` spec parameter and
// onto the per-rollout policy, forwarded exactly like CheckpointID. The service rejects a
// rollout with HTTP 400 ("policy.project_endpoint is required ...") if this is empty.
type rolloutPolicy struct {
	Type            string `json:"type"`
	ModelName       string `json:"model_name,omitempty"`
	ProjectEndpoint string `json:"project_endpoint,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	CheckpointID    string `json:"checkpoint_id,omitempty"`
	SequenceID      *int64 `json:"sequence_id,omitempty"`
}

// rolloutSamplingOptions carries how completions are rendered, which is a Capture Proxy
// concern common to every policy type rather than a property of one. The CLI names no
// renderer today, so this is omitted entirely and the service selects a compatible default;
// sending an empty renderer_name instead would be rejected.
type rolloutSamplingOptions struct {
	RendererName string `json:"renderer_name,omitempty"`
}

// executeRolloutRequest mirrors vienna's ExecuteRolloutRequest
// (EntryPoints/Models/ExecuteRolloutApiModels.cs). Task is the opaque JSONL record sent
// unchanged to the sandbox reset operation (Gym/OpenEnv); AgentInput is the agent-visible
// input required only by Harness targets.
type executeRolloutRequest struct {
	RolloutID  string                  `json:"rollout_id"`
	Task       json.RawMessage         `json:"task,omitempty"`
	AgentInput json.RawMessage         `json:"agent_input,omitempty"`
	Policy     *rolloutPolicy          `json:"policy,omitempty"`
	Sampling   *rolloutSamplingOptions `json:"sampling,omitempty"`
}

type executeRolloutResponse = rollouts.Response

// executeRollout runs one isolated rollout of an exact, published environment version.
// loomBearerToken is forwarded unchanged via the aml-user-token header.
//
// The rollout travels over the WebSocket transport, which the Foundry data-plane gateway
// does not terminate at roughly 120 seconds the way it terminates the HTTP request. A
// Harness rollout regularly runs longer than that, so on HTTP it returns a 408 with no
// result even though the rollout itself succeeded. Where the upgrade is unavailable -- the
// gateway rejects it, or the service predates the route -- the handshake fails before any
// rollout is requested, so the HTTP path still runs and behavior is unchanged. A failure
// after the upgrade is never retried on HTTP: the rollout may already have executed.
func (c *rleClient) executeRollout(
	ctx context.Context,
	environmentName string,
	environmentVersion string,
	loomBearerToken string,
	request executeRolloutRequest,
	errOut io.Writer,
) (*executeRolloutResponse, error) {
	response, err := c.executeRolloutOverWebSocket(
		ctx, environmentName, environmentVersion, loomBearerToken, request)
	if err == nil {
		return response, nil
	}
	handshakeErr, ok := errors.AsType[*executeRolloutHandshakeError](err)
	if !ok {
		return nil, err
	}
	if errOut != nil {
		fmt.Fprintf(
			errOut,
			"Warning: %v\n"+
				"Falling back to the HTTP transport, which the gateway ends after about 120 seconds.\n",
			handshakeErr,
		)
	}
	return c.executeRolloutOverHTTP(ctx, environmentName, environmentVersion, loomBearerToken, request)
}

func (c *rleClient) executeRolloutOverHTTP(
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
