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
	"os"
)

func (a *InvokeAction) responsesSteerRemote(ctx context.Context) error {
	body, bodyLabel, err := a.resolveBody()
	if err != nil {
		return err
	}
	rc, store, current, err := a.resolveSavedBackgroundResponse(ctx)
	if err != nil {
		return err
	}
	defer rc.azdClient.Close()

	rc.bearerToken, err = a.acquireBearerToken(ctx)
	if err != nil {
		return err
	}

	requestBody, err := buildConversationContinuationRequest(string(body), current)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("marshal steering request: %w", err)
	}

	responseURL := buildResponsesURL(rc.projectEndpoint, rc.name, rc.apiVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, responseURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create steering request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+rc.bearerToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	applyCustomHeaders(req, a.clientHeaders)
	applyRemoteUserIdentityHeader(req, &a.flags.userIdentityFlags)

	resp, err := backgroundHTTPClient().Do(req) //nolint:gosec // validated Foundry endpoint
	if err != nil {
		return fmt.Errorf("POST %s failed: %w", responseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		responseBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST %s failed with HTTP %d: %s\n%s", responseURL, resp.StatusCode, resp.Status, responseBody)
	}

	effectiveSessionID := current.SessionID
	if effectiveSessionID == "" {
		effectiveSessionID = resp.Header.Get("x-agent-session-id")
	}
	captureResponseSession(ctx, rc.azdClient, rc.agentKey, current.SessionID, resp, "")

	fmt.Printf("Agent:        %s (remote)\n", rc.name)
	fmt.Printf("Message:      %s\n", bodyLabel)
	printSessionStatus("Session:      ", effectiveSessionID)
	fmt.Printf("Conversation: %s\n\n", current.ConversationID)

	progressPersister := newBackgroundProgressPersister(
		store,
		rc.agentKey,
		effectiveSessionID,
		current.ConversationID,
		os.Stdout,
	)
	streamErr := readResponsesSSE(
		ctx,
		resp.Body,
		os.Stdout,
		rc.name,
		responsesSSEOptions{
			requireTerminal: true,
			onProgress: func(progress responsesStreamProgress) error {
				return progressPersister.Apply(ctx, progress)
			},
		},
	)
	var flushErr error
	if ctx.Err() == nil {
		flushErr = progressPersister.Flush(ctx)
	}
	closeErr := progressPersister.Close()
	if streamErr != nil && ctx.Err() == nil && progressPersister.latest.ResponseID != "" &&
		!isTerminalResponseStatus(progressPersister.latest.Status) && isRetryableBackgroundStreamError(streamErr) &&
		flushErr == nil && closeErr == nil {
		latest := progressPersister.latest
		return a.followBackgroundResponse(ctx, rc, store, latest, os.Stdout)
	}
	return errors.Join(streamErr, flushErr, closeErr)
}

func buildConversationContinuationRequest(
	input string,
	current *savedBackgroundResponse,
) (map[string]any, error) {
	if current.ConversationID == "" {
		return nil, fmt.Errorf("saved background Response has no conversation ID for --steer")
	}
	requestBody := map[string]any{
		"input":        input,
		"stream":       true,
		"store":        true,
		"background":   true,
		"conversation": map[string]string{"id": current.ConversationID},
	}
	if current.SessionID != "" {
		requestBody["agent_session_id"] = current.SessionID
	}
	return requestBody, nil
}
