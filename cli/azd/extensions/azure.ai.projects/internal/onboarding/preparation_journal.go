// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

const preparationOwnerTag = "azd-byoaks-owner"

const preparationJournalVersion = 2

// Journal durably stores each intent before its Azure write and each result before the next step.
// Load returns a zero state when absent. Save must atomically replace the state and surface write failures.
// Callers must serialize preparation for an environment; this interface is not a distributed lock.
type Journal interface {
	Load(context.Context) (PreparationState, error)
	Save(context.Context, PreparationState) error
}

// PreparationState is the recoverable ownership ledger, not evidence of live workload access.
type PreparationState struct {
	Version         int                        `json:"version"`
	SubscriptionID  string                     `json:"subscriptionId"`
	ResourceGroup   string                     `json:"resourceGroup"`
	Location        string                     `json:"location"`
	Owner           string                     `json:"owner"`
	Network         *SubnetPreparation         `json:"network,omitempty"`
	Resources       map[string]OwnedResource   `json:"resources"`
	Assignments     map[string]OwnedAssignment `json:"assignments"`
	Foundry         *FoundryState              `json:"foundry,omitempty"`
	CleanupStarted  bool                       `json:"cleanupStarted,omitempty"`
	CleanupComplete bool                       `json:"cleanupComplete,omitempty"`
}

// OwnedResource records an exact intended resource ID and its confirmed creation result.
type OwnedResource struct {
	ID       string   `json:"id"`
	Complete bool     `json:"complete"`
	Deleted  bool     `json:"deleted,omitempty"`
	Identity Identity `json:"identity,omitzero"`
}

// OwnedAssignment records one exact scoped grant; it never tracks a reused customer grant as owned.
type OwnedAssignment struct {
	ID          string `json:"id"`
	Scope       string `json:"scope"`
	PrincipalID string `json:"principalId"`
	RoleID      string `json:"roleId"`
	Complete    bool   `json:"complete"`
	Deleted     bool   `json:"deleted,omitempty"`
}

type ownedPreparer struct {
	credential azcore.TokenCredential
	options    *arm.ClientOptions
	journal    Journal
	network    PreparedNetworkVerifier
}

// PreparedNetworkVerifier verifies newly created paths before Foundry deployment.
type PreparedNetworkVerifier interface {
	VerifyPreparedNetwork(context.Context, Plan, Prepared) (Report, error)
}

// NewPreparer creates only journaled, owner-tagged dependencies and scoped grants.
// The caller must pass the complete mandatory preflight gate before invoking Prepare.
func NewPreparer(credential azcore.TokenCredential, options *arm.ClientOptions, journal Journal) Preparer {
	checker := &sdkResourceChecker{credential: credential, options: onboardingARMOptions(options)}
	return NewPreparerWithVerifier(credential, options, journal, checker)
}

// NewPreparerWithVerifier injects supported read-only post-creation path evidence.
func NewPreparerWithVerifier(
	credential azcore.TokenCredential, options *arm.ClientOptions, journal Journal, network PreparedNetworkVerifier,
) Preparer {
	return &ownedPreparer{credential: credential, options: onboardingARMOptions(options), journal: journal, network: network}
}

func (p *ownedPreparer) beginState(ctx context.Context, plan Plan) (PreparationState, error) {
	if p.journal == nil || plan.Owner == "" {
		return PreparationState{}, fmt.Errorf("preparation requires a durable journal and nonempty environment owner")
	}
	state, err := p.journal.Load(ctx)
	if err != nil {
		return state, fmt.Errorf("load preparation ownership: %w", err)
	}
	if state.Version > preparationJournalVersion || (state.Version < preparationJournalVersion && state.Owner != "") {
		return state, fmt.Errorf("legacy/unknown journal has no authoritative Foundry lifecycle; resolve it before preparation")
	}
	state.Version = preparationJournalVersion
	if state.Owner != "" && state.Owner != plan.Owner {
		return state, fmt.Errorf("preparation journal belongs to another environment owner")
	}
	if state.Owner == "" && (len(state.Resources) > 0 || len(state.Assignments) > 0) {
		return state, fmt.Errorf("preparation journal has resources without an owner")
	}
	if state.CleanupStarted && !state.CleanupComplete {
		return state, fmt.Errorf("owned-resource cleanup is incomplete; resume azd down before provisioning again")
	}
	if state.CleanupComplete {
		for step := range state.Resources {
			if step != "R01" {
				delete(state.Resources, step)
			}
		}
		state.Assignments = map[string]OwnedAssignment{}
		state.Network = nil
	}
	state.SubscriptionID, state.ResourceGroup, state.Location = plan.SubscriptionID, plan.ResourceGroup, plan.Location
	state.CleanupStarted, state.CleanupComplete = false, false
	state.Owner = plan.Owner
	if plan.Network != nil {
		if state.Network != nil && !sameSubnetPreparation(*state.Network, *plan.Network) {
			return state, fmt.Errorf("R06: resolved network differs from the recorded preparation plan")
		}
		network := *plan.Network
		state.Network = &network
	}
	if state.Resources == nil {
		state.Resources = map[string]OwnedResource{}
	}
	if state.Assignments == nil {
		state.Assignments = map[string]OwnedAssignment{}
	}
	if err := p.journal.Save(ctx, state); err != nil {
		return state, fmt.Errorf("persist preparation owner before writes: %w", err)
	}
	return state, nil
}

func (p *ownedPreparer) resourceIntent(
	ctx context.Context, state *PreparationState, step, id string,
) error {
	if previous, ok := state.Resources[step]; ok && !strings.EqualFold(previous.ID, id) {
		return fmt.Errorf("%s: planned resource differs from the recorded ownership intent", step)
	} else if ok && previous.Complete {
		return fmt.Errorf("%s: a previously completed resource disappeared; refusing silent replacement", step)
	}
	state.Resources[step] = OwnedResource{ID: id}
	if err := p.journal.Save(ctx, *state); err != nil {
		return fmt.Errorf("%s: persist creation intent: %w", step, err)
	}
	return nil
}

func (p *ownedPreparer) resourceComplete(
	ctx context.Context, state *PreparationState, step, id string,
) error {
	record := state.Resources[step]
	record.ID, record.Complete = id, true
	state.Resources[step] = record
	if err := p.journal.Save(ctx, *state); err != nil {
		return fmt.Errorf("%s: persist creation result: %w", step, err)
	}
	return nil
}

func confirmOwned(state PreparationState, step, id string, tags map[string]*string) error {
	record, ok := state.Resources[step]
	if !ok || !strings.EqualFold(record.ID, id) || authString(tags[preparationOwnerTag]) != state.Owner {
		return fmt.Errorf("%s: existing resource is not the exact journaled, owner-tagged resource; refusing adoption", step)
	}
	return nil
}

func ownerTags(owner string) map[string]*string {
	return map[string]*string{preparationOwnerTag: new(owner)}
}
