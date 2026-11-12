// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package schemas embeds the extension's JSON schemas.
package schemas

import (
	"bytes"
	_ "embed"
)

//go:embed azure.ai.agent.json
var agentDefinitionSchema []byte

// AgentDefinitionSchema returns the embedded direct agent schema.
func AgentDefinitionSchema() []byte {
	return bytes.Clone(agentDefinitionSchema)
}
