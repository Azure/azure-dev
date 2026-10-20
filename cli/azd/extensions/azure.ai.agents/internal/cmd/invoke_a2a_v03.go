// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

// a2a03MessageSendParams is the public A2A 0.3 message/send parameter shape.
// See https://a2a-protocol.org/v0.3.0/specification/#711-messagesendparams-object.
type a2a03MessageSendParams struct {
	Message       a2a03Message                  `json:"message"`
	Configuration a2a03MessageSendConfiguration `json:"configuration"`
}

type a2a03Message struct {
	Kind      string          `json:"kind"`
	MessageID string          `json:"messageId"`
	Role      string          `json:"role"`
	Parts     []a2a03TextPart `json:"parts"`
}

type a2a03TextPart struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type a2a03MessageSendConfiguration struct {
	AcceptedOutputModes []string `json:"acceptedOutputModes"`
	Blocking            bool     `json:"blocking"`
}

// buildA2A03TextRequest constructs the complete 0.3 body without inheriting or
// patching another dialect's defaults. Required message/part discriminators and
// blocking output are explicit. File input never passes through this builder.
func buildA2A03TextRequest(text, requestID, messageID string) a2aJSONRPCRequest {
	return a2aJSONRPCRequest{
		JSONRPC: a2aJSONRPCVersion,
		ID:      requestID,
		Method:  "message/send",
		Params: a2a03MessageSendParams{
			Message: a2a03Message{
				Kind:      "message",
				MessageID: messageID,
				Role:      "user",
				Parts:     []a2a03TextPart{{Kind: "text", Text: text}},
			},
			Configuration: a2a03MessageSendConfiguration{
				AcceptedOutputModes: []string{"text"},
				Blocking:            true,
			},
		},
	}
}
