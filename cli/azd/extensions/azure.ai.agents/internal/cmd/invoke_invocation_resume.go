// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
)

func (a *InvokeAction) invocationsResumeRemote(ctx context.Context) error {
	rc, err := a.resolveRemoteContext(ctx)
	if err != nil {
		return err
	}
	if rc.azdClient == nil || rc.agentKey == "" {
		if rc.azdClient != nil {
			rc.azdClient.Close()
		}
		return fmt.Errorf("Invocations --resume requires project-backed local state")
	}
	defer rc.azdClient.Close()

	store := newInvocationStateStore(rc.azdClient)
	record, err := store.Get(ctx, rc.agentKey)
	if err != nil {
		return err
	}
	if record == nil || record.InvocationID == "" {
		return fmt.Errorf(
			"no saved invocation found; run `azd ai agent invoke --protocol invocations \"<message>\"` first",
		)
	}
	token, err := a.acquireBearerToken(ctx)
	if err != nil {
		return err
	}
	retrievalURL := buildInvocationRetrievalURL(
		rc.projectEndpoint,
		rc.name,
		record.InvocationID,
		record.APIVersion,
		record.SessionID,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, retrievalURL, nil)
	if err != nil {
		return fmt.Errorf("create invocation retrieval request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	applyCustomHeaders(req, a.clientHeaders)
	applyRemoteUserIdentityHeader(req, &a.flags.userIdentityFlags)

	resp, err := (&http.Client{Timeout: a.httpTimeout()}).Do(req) //nolint:gosec // validated Foundry endpoint
	if err != nil {
		return fmt.Errorf("GET %s failed: %w", retrievalURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf(
			"GET %s failed with HTTP %d: %s\n%s\n"+
				"the agent might not support retrieval, the invocation might not be registered yet or might have expired",
			retrievalURL,
			resp.StatusCode,
			resp.Status,
			body,
		)
	}

	fmt.Printf("Agent:      %s (remote, invocations protocol)\n", rc.name)
	fmt.Printf("Invocation: %s\n\n", record.InvocationID)
	_, err = io.Copy(os.Stdout, resp.Body)
	if err != nil {
		return fmt.Errorf("write invocation retrieval response: %w", err)
	}
	return nil
}
