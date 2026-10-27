// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package foundryaccess checks operation-specific Foundry project access and,
// only with explicit authorization, ensures a project-scoped Foundry User grant.
// It does not register hooks, change provisioning, print output, or replay writes.
package foundryaccess

import (
	"context"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// State describes the result of a non-mutating, operation-specific access probe.
type State string

const (
	// Unknown means access could not be established. It never authorizes a grant.
	Unknown State = "unknown"
	// Allowed means the probe confirmed the requested access.
	Allowed State = "allowed"
	// Denied means the probe confirmed an RBAC denial for the requested operation.
	// A generic HTTP 403, network restriction, or missing known role is not sufficient.
	Denied State = "denied"
)

// GrantPolicy controls whether a confirmed denial may cause a role assignment.
type GrantPolicy string

const (
	// CheckOnly never changes role assignments. The empty policy also means CheckOnly.
	CheckOnly GrantPolicy = "checkOnly"
	// GrantFoundryUser permits granting Foundry User to the current principal at
	// the verified project scope. Callers must obtain consent or explicit configured
	// authorization first; --no-prompt alone is not consent.
	GrantFoundryUser GrantPolicy = "grantFoundryUser"
)

// Target identifies the exact Foundry project the operation will use.
// Both fields are required; an ARM account name must not be inferred from DNS.
type Target struct {
	ProjectID string
	Endpoint  string
}

// Project is a verified ARM/data-plane binding and the current authenticated identity.
type Project struct {
	Target
	TenantID      string
	PrincipalID   string
	PrincipalType string
	Credential    azcore.TokenCredential
}

// Probe performs only a non-mutating access check for the caller's operation,
// using the supplied project and credential. Return Unknown for ambiguous failures,
// including network/policy 403s. Do not retry the intended write as a probe.
type Probe func(context.Context, Project) (State, error)

// Backend resolves verified project context and ensures an idempotent grant.
// Implementations must support concurrent calls without changing identity or scope.
type Backend interface {
	Resolve(context.Context, Target) (Project, error)
	// EnsureGrant returns true only when this call created the assignment.
	// Existing assignments and compatible concurrent creation return false.
	EnsureGrant(context.Context, Project) (bool, error)
}

// Request describes an operation-specific access prerequisite.
type Request struct {
	Target Target
	// Operation identifies the capability being checked, for example "toolboxes.write".
	Operation string
	Policy    GrantPolicy
	Probe     Probe
}

// Result describes readiness without conflating an assignment with effective access.
// Unknown and Denied are not ready, even when Ensure returns no error.
type Result struct {
	State             State
	Project           Project
	Operation         string
	AssignmentCreated bool
	// GrantEnsured means an assignment was created or a compatible one already existed.
	GrantEnsured  bool
	ProbeAttempts int
}

// Options bounds propagation checks after an explicitly authorized grant.
type Options struct {
	// PropagationTimeout defaults to two minutes. It includes assignment and re-probing.
	PropagationTimeout time.Duration
	// InitialDelay defaults to one second; subsequent delays double up to ten seconds.
	InitialDelay time.Duration
}
