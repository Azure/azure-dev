// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"azureaiagent/internal/exterrors"
)

// a2aJSONRPCVersion identifies the JSON-RPC envelope, not the A2A dialect.
const a2aJSONRPCVersion = "2.0"

// a2aJSONRPCRequest is the shared envelope around dialect-specific parameters.
type a2aJSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// a2aTextRequestBuilder constructs one dialect's complete plain-message request.
// IDs are supplied by the caller so builders are pure and independently testable.
type a2aTextRequestBuilder func(text, requestID, messageID string) a2aJSONRPCRequest

// a2aRequestBuilderForVersion is the single supported-dialect selection point for
// request construction and version validation. It never guesses from file input.
func a2aRequestBuilderForVersion(version string) (a2aTextRequestBuilder, error) {
	switch version {
	case "0.3":
		return buildA2A03TextRequest, nil
	case "1.0":
		return buildA2A10TextRequest, nil
	default:
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("invalid --a2a-version %q: supported versions are 0.3 and 1.0", version),
			"use --a2a-version 0.3 or --a2a-version 1.0",
		)
	}
}
