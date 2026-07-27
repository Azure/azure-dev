// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"net/http"

	"github.com/spf13/cobra"

	"azureaiagent/internal/pkg/agents/agent_api"
)

// userIdentityFlags holds the user identity header flag value.
type userIdentityFlags struct {
	userIdentity string
}

func addUserIdentityFlag(cmd *cobra.Command, flags *userIdentityFlags) {
	cmd.Flags().StringVar(
		&flags.userIdentity,
		"user-identity",
		"",
		"User identity header value (sent as "+agent_api.AgentUserIDHeader+" for local invocations "+
			"and "+agent_api.UserIdentityHeader+" for remote requests)",
	)
}

func (f *userIdentityFlags) sessionRequestOptions() *agent_api.SessionRequestOptions {
	if f == nil || f.userIdentity == "" {
		return nil
	}
	return &agent_api.SessionRequestOptions{
		UserIdentity: f.userIdentity,
	}
}

// applyRemoteUserIdentityHeader sets the remote user identity header (x-ms-user-identity)
// on a request destined for Foundry.
func applyRemoteUserIdentityHeader(req *http.Request, flags *userIdentityFlags) {
	if options := flags.sessionRequestOptions(); options != nil {
		options.ApplyHeaders(req.Header)
	}
}

// applyLocalUserIdentityHeader sets the local user identity header (x-agent-user-id)
// on a request destined for a locally running agent.
func applyLocalUserIdentityHeader(req *http.Request, flags *userIdentityFlags) {
	if flags != nil && flags.userIdentity != "" {
		req.Header.Set(agent_api.AgentUserIDHeader, flags.userIdentity)
	}
}

// applyLocalCallIDHeader sets the local call ID header (x-agent-foundry-call-id)
// on a request destined for a locally running agent. The call ID is local-only
// and is never sent on remote (Foundry) invokes.
func applyLocalCallIDHeader(req *http.Request, callID string) {
	if callID != "" {
		req.Header.Set(agent_api.AgentFoundryCallIDHeader, callID)
	}
}
