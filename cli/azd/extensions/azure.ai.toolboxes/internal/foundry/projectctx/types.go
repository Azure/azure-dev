// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package projectctx encapsulates the Foundry project endpoint cascade and
// validation shared by every Foundry-extension command tree.
//
// This is the toolboxes-extension copy of the agent_context.go / project_endpoint.go /
// project_context_store.go logic in azure.ai.agents (see § 3.2 of the toolbox
// design spec). Semantics match the agents original verbatim; identifiers are
// exported because they cross the package boundary in this layout.
package projectctx

// EndpointSource identifies where a resolved project endpoint came from.
type EndpointSource string

const (
	// SourceFlag means the endpoint came from the --project-endpoint flag.
	SourceFlag EndpointSource = "flag"
	// SourceAzdEnv means the endpoint came from the active azd environment's
	// AZURE_AI_PROJECT_ENDPOINT value.
	SourceAzdEnv EndpointSource = "azdEnv"
	// SourceGlobalConfig means the endpoint came from ~/.azd/config.json
	// (extensions.ai-agents.project.context.endpoint — owned by azure.ai.agents
	// and shared read-only with sibling extensions).
	SourceGlobalConfig EndpointSource = "globalConfig"
	// SourceFoundryEnv means the endpoint came from the FOUNDRY_PROJECT_ENDPOINT
	// host environment variable.
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
	// EnvValue is the AZURE_AI_PROJECT_ENDPOINT value from the active azd env,
	// or "" if not set / no active env / no azd client available.
	EnvValue string
	// EnvName is the active azd env name. Only meaningful when EnvValue != "".
	EnvName string
	// CfgState is the project context persisted in global config.
	CfgState State
	// CfgFound indicates whether a non-empty endpoint was found in global config.
	CfgFound bool
}

// State is the JSON shape stored at extensions.ai-agents.project.context in
// ~/.azd/config.json. This key is owned by azure.ai.agents; the toolboxes
// extension reads it but never writes it.
type State struct {
	Endpoint string `json:"endpoint"`
	SetAt    string `json:"setAt"`
}
