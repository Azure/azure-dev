// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Pipeline sequences checks, writes and final deployment without preview writes.
type Pipeline struct {
	Resources ResourceChecker
	Access    Authorizer
	Prepare   Preparer
	// Resolve discovers existing ancestors without changing creation/reuse choices.
	Resolve func(context.Context, Plan) (Plan, error)
	// Observe publishes the complete preflight report before the write gate opens.
	Observe func(Report)
}

// Preflight collects resource and permission evidence before opening the write gate.
func (p Pipeline) Preflight(ctx context.Context, plan Plan) (Facts, Report, error) {
	if p.Resources == nil || p.Access == nil {
		return Facts{}, nil, fmt.Errorf("BYO-AKS preflight dependencies are unavailable")
	}
	facts, report, err := p.Resources.Check(ctx, plan)
	if err != nil {
		if _, blocked := errors.AsType[*BlockedError](err); !blocked {
			return facts, report, err
		}
	}
	access, err := p.Access.Check(ctx, plan, facts)
	report = append(report, access...)
	if err != nil {
		if _, blocked := errors.AsType[*BlockedError](err); !blocked {
			return facts, report, err
		}
	}
	return facts, report, report.Err()
}

// Run never invokes preparation or deployment when evidence is missing.
func (p Pipeline) Run(
	ctx context.Context, plan Plan, preview bool,
	deploy func(context.Context, Prepared) error,
) (Report, error) {
	if p.Resolve != nil {
		resolved, err := p.Resolve(ctx, plan)
		if err != nil {
			report := Report{{
				ID: "I03", Scope: plan.Cluster.ID, Status: Unverified, Mandatory: true,
				Expected: "read-only, fully resolved creation/reuse and topology plan",
				Observed: err.Error(), Missing: "supported topology and existing-ancestor evidence",
				Remediation: "resolve the stated planning evidence or supply an existing supported agent subnet",
				Cause:       err,
			}}
			if p.Observe != nil {
				p.Observe(report)
			}
			return report, report.Err()
		}
		plan = resolved
	}
	facts, report, err := p.Preflight(ctx, plan)
	if p.Observe != nil {
		p.Observe(report)
	}
	if err != nil || preview {
		return report, err
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if p.Prepare == nil || deploy == nil {
		return report, fmt.Errorf("BYO-AKS preparation or deployment dependency is unavailable")
	}
	prepared, err := p.Prepare.Prepare(ctx, plan, facts)
	if err != nil {
		return report, err
	}
	if err := ValidatePrepared(plan, prepared); err != nil {
		return report, err
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	return report, deploy(ctx, prepared)
}

// ValidatePrepared checks complete internal parameters only after preparation.
func ValidatePrepared(plan Plan, prepared Prepared) error {
	if strings.EqualFold(prepared.Hosting.ID, prepared.Workload.ID) ||
		strings.EqualFold(prepared.Hosting.PrincipalID, prepared.Workload.PrincipalID) ||
		strings.EqualFold(prepared.Hosting.ClientID, prepared.Workload.ClientID) {
		return fmt.Errorf("R04/R05: hosting and workload identities must be distinct")
	}
	if !strings.EqualFold(prepared.Hosting.ID, plan.Hosting.ID) ||
		!strings.EqualFold(prepared.Workload.ID, plan.Workload.ID) {
		return fmt.Errorf("R04/R05: prepared identities do not match the owned resource plan")
	}
	for _, identity := range []Identity{prepared.Hosting, prepared.Workload} {
		if _, err := ParseID(identity.ID, "Microsoft.ManagedIdentity/userAssignedIdentities"); err != nil {
			return err
		}
		if identity.PrincipalID == "" || identity.ClientID == "" || identity.TenantID == "" {
			return fmt.Errorf("R04/R05: prepared identity lacks object, client or tenant ID")
		}
	}
	for _, resource := range []struct{ id, kind string }{
		{prepared.StorageID, "Microsoft.Storage/storageAccounts"},
		{prepared.RegistryID, "Microsoft.ContainerRegistry/registries"},
		{prepared.AgentSubnetID, "Microsoft.Network/virtualNetworks/subnets"},
	} {
		if _, err := ParseID(resource.id, resource.kind); err != nil {
			return err
		}
	}
	if !strings.EqualFold(prepared.StorageID, plan.Storage.ID) ||
		!strings.EqualFold(prepared.RegistryID, plan.Registry.ID) ||
		plan.AgentSubnet.ID != "" && !strings.EqualFold(prepared.AgentSubnetID, plan.AgentSubnet.ID) {
		return fmt.Errorf("prepared resources do not preserve planned resource choices")
	}
	return nil
}
