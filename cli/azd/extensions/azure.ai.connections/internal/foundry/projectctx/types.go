// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package projectctx encapsulates the Foundry project endpoint cascade and
// validation shared by every Foundry-extension command tree.
//
// This is the connections-extension copy of the agent_context.go /
// project_endpoint.go / project_context_store.go logic in azure.ai.agents and
// the matching projectctx package in azure.ai.toolboxes. Semantics match the
// agents original verbatim; identifiers are exported because they cross the
// package boundary in this layout. See AGENTS.md ("Package boundaries") for
// the one-way import contract that keeps the eventual lift mechanical.
package projectctx

const (
	// foundryEnvKey is the canonical project-endpoint key. It is read both from
	// the active azd environment (level 2) and as a host environment variable
	// (level 4).
	foundryEnvKey = "FOUNDRY_PROJECT_ENDPOINT"
	// azureAiEnvKey is the legacy/sibling project-endpoint key written by
	// `azd ai agent init` and `azd add` (Bicep output). It is read as a fallback
	// after foundryEnvKey at both the active-azd-env and host-env levels so the
	// hosted-agent + toolbox workflow resolves without an extra manual step.
	// See https://github.com/Azure/azure-dev/issues/8688.
	azureAiEnvKey = "AZURE_AI_PROJECT_ENDPOINT"
)

// EndpointSource identifies where a resolved project endpoint came from.
type EndpointSource string

const (
	// SourceFlag means the endpoint came from the --project-endpoint flag.
	SourceFlag EndpointSource = "flag"
	// SourceAzdEnv means the endpoint came from the active azd environment's
	// FOUNDRY_PROJECT_ENDPOINT (or, as a fallback, AZURE_AI_PROJECT_ENDPOINT) value.
	SourceAzdEnv EndpointSource = "azdEnv"
	// SourceGlobalConfig means the endpoint came from ~/.azd/config.json
	// (extensions.ai-agents.project.context.endpoint — owned by azure.ai.agents
	// and shared read-only with sibling extensions).
	SourceGlobalConfig EndpointSource = "globalConfig"
	// SourceFoundryEnv means the endpoint came from the FOUNDRY_PROJECT_ENDPOINT
	// (or, as a fallback, AZURE_AI_PROJECT_ENDPOINT) host environment variable.
	SourceFoundryEnv EndpointSource = "foundryEnv"
)

// ResolveOpts controls the 5-level endpoint resolution cascade.
type ResolveOpts struct {
	// FlagValue is the value of the --project-endpoint flag (level 1).
	// Empty means the flag was not provided.
	FlagValue string
}

// Resolved holds the result of Resolve.
type Resolved struct {
	Endpoint   string
	Source     EndpointSource
	AzdEnvName string
	SetAt      string // RFC3339 timestamp; only meaningful when Source == SourceGlobalConfig
}

// AzdHostedSources holds the values the resolver reads from azd-managed
// sources (active env + ~/.azd/config.json). Returned as a single struct so
// tests can stub the whole lookup via ReadAzdHostedSourcesFunc.
type AzdHostedSources struct {
	// EnvValue is the active-azd-env project endpoint: FOUNDRY_PROJECT_ENDPOINT
	// if set, otherwise AZURE_AI_PROJECT_ENDPOINT, otherwise "" (not set / no
	// active env / no azd client available).
	EnvValue string
	// EnvName is the active azd env name. Only meaningful when EnvValue != "".
	EnvName string
	// CfgState is the project context persisted in global config.
	CfgState State
	// CfgFound indicates whether a non-empty endpoint was found in global config.
	CfgFound bool
}

// State is the JSON shape stored at extensions.ai-agents.project.context in
// ~/.azd/config.json. This key is owned by azure.ai.agents; the connections
// extension reads it but never writes it.
type State struct {
	Endpoint string `json:"endpoint"`
	SetAt    string `json:"setAt"`
}
