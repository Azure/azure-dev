// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

// a2a10SendMessageRequest is the public A2A 1.0 SendMessage parameter shape.
// See https://a2a-protocol.org/v1.0.0/specification/#9-json-rpc-protocol-binding.
type a2a10SendMessageRequest struct {
	Message       a2a10Message                  `json:"message"`
	Configuration a2a10MessageSendConfiguration `json:"configuration"`
}

type a2a10Message struct {
	MessageID string          `json:"messageId"`
	Role      string          `json:"role"`
	Parts     []a2a10TextPart `json:"parts"`
}

type a2a10TextPart struct {
	Text string `json:"text"`
}

type a2a10MessageSendConfiguration struct {
	AcceptedOutputModes []string `json:"acceptedOutputModes"`
	// False requests synchronous completion and must not be omitted from JSON.
	ReturnImmediately bool `json:"returnImmediately"`
}

// buildA2A10TextRequest constructs the complete 1.0 body independently of 0.3.
// File input never passes through this builder.
func buildA2A10TextRequest(text, requestID, messageID string) a2aJSONRPCRequest {
	return a2aJSONRPCRequest{
		JSONRPC: a2aJSONRPCVersion,
		ID:      requestID,
		Method:  "SendMessage",
		Params: a2a10SendMessageRequest{
			Message: a2a10Message{
				MessageID: messageID,
				Role:      "ROLE_USER",
				Parts:     []a2a10TextPart{{Text: text}},
			},
			Configuration: a2a10MessageSendConfiguration{
				AcceptedOutputModes: []string{"text"},
				ReturnImmediately:   false,
			},
		},
	}
}
