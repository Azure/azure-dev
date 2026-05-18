// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"net/http"

	"github.com/spf13/cobra"

	"azureaiagent/internal/pkg/agents/agent_api"
)

// isolationHeaderFlags holds Foundry user/chat isolation header flag values.
type isolationHeaderFlags struct {
	userIsolationKey string
	chatIsolationKey string
}

func addIsolationHeaderFlags(cmd *cobra.Command, flags *isolationHeaderFlags) {
	cmd.Flags().StringVar(
		&flags.userIsolationKey,
		"user-isolation-key",
		"",
		"Foundry user isolation key header value ("+agent_api.AgentUserIsolationKeyHeader+")",
	)
	cmd.Flags().StringVar(
		&flags.chatIsolationKey,
		"chat-isolation-key",
		"",
		"Foundry chat isolation key header value ("+agent_api.AgentChatIsolationKeyHeader+")",
	)
}

func (f *isolationHeaderFlags) sessionRequestOptions() *agent_api.SessionRequestOptions {
	if f == nil || (f.userIsolationKey == "" && f.chatIsolationKey == "") {
		return nil
	}
	return &agent_api.SessionRequestOptions{
		UserIsolationKey: f.userIsolationKey,
		ChatIsolationKey: f.chatIsolationKey,
	}
}

func (f *isolationHeaderFlags) sessionRequestOptionsWithSessionKey(
	sessionIsolationKey string,
) *agent_api.SessionRequestOptions {
	options := f.sessionRequestOptions()
	if sessionIsolationKey == "" {
		return options
	}
	if options == nil {
		options = &agent_api.SessionRequestOptions{}
	}
	options.SessionIsolationKey = sessionIsolationKey
	return options
}

func applyIsolationHeaders(req *http.Request, flags *isolationHeaderFlags) {
	if options := flags.sessionRequestOptions(); options != nil {
		options.ApplyHeaders(req.Header)
	}
}
