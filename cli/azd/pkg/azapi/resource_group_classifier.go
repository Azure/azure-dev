// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// ClassifyResult holds the outcome of resource group classification.
type ClassifyResult struct {
	Owned   []string         // RGs classified as created by azd — safe to delete
	Skipped []ClassifiedSkip // RGs classified as external/unknown/vetoed — not deleted
}

// ClassifiedSkip represents a resource group that will NOT be deleted, with the reason.
type ClassifiedSkip struct {
	Name   string
	Reason string // Human-readable, e.g. "external (Tier 1: Read operation found)"
}

// ResourceWithTags is a resource with its ARM tags, used for extra-resource checks.
type ResourceWithTags struct {
	Name string
	Tags map[string]*string
}

// ManagementLock represents an ARM management lock on a resource.
type ManagementLock struct {
	Name     string
	LockType string // "CanNotDelete" or "ReadOnly"
}

// ClassifyOptions configures the classification pipeline.
type ClassifyOptions struct {
	Interactive bool   // Whether to prompt for unknown RGs
	EnvName     string // Current azd environment name for tag matching

	// GetResourceGroupTags returns the tags on a resource group (nil map if 404).
	GetResourceGroupTags func(ctx context.Context, rgName string) (map[string]*string, error)
	// ListResourceGroupResources returns all resources in a resource group.
	ListResourceGroupResources func(ctx context.Context, rgName string) ([]*ResourceWithTags, error)
	// ListResourceGroupLocks returns management locks on a resource group.
	ListResourceGroupLocks func(ctx context.Context, rgName string) ([]*ManagementLock, error)
	// Prompter asks the user whether to delete an unknown RG. Returns true to delete.
	Prompter func(rgName, reason string) (bool, error)
}

const (
	cAzdEnvNameTag       = "azd-env-name"
	cAzdProvisionHashTag = "azd-provision-param-hash"
	cRGResourceType      = "Microsoft.Resources/resourceGroups"
	cProvisionOpCreate   = "Create"
	cProvisionOpRead     = "Read"
	cProvisionOpEvalOut  = "EvaluateDeploymentOutput"
	cLockCanNotDelete    = "CanNotDelete"
	cLockReadOnly        = "ReadOnly"
	cTier4Parallelism    = 5
)

// tier1Result is the outcome of Tier 1 classification for a single RG.
type tier1Result int

const (
	tier1Unknown  tier1Result = iota
	tier1Owned                // Create operation found
	tier1External             // Read / EvaluateDeploymentOutput operation found
)

// ClassifyResourceGroups determines which resource groups from a deployment are
// safe to delete (owned by azd) vs which should be skipped (external/unknown/vetoed).
//
// The operations parameter should be the result of deployment.Operations() — a single
// API call that returns all operations for the deployment.
func ClassifyResourceGroups(
	ctx context.Context,
	operations []*armresources.DeploymentOperation,
	rgNames []string,
	opts ClassifyOptions,
) (*ClassifyResult, error) {
	if len(rgNames) == 0 {
		return &ClassifyResult{}, nil
	}

	result := &ClassifyResult{}

	// --- Tier 1: classify all RGs from deployment operations (zero extra API calls) ---
	owned, unknown := classifyTier1(operations, rgNames, result)

	// --- Tier 2: dual-tag check for unknowns ---
	var tier2Owned, tier3Candidates []string
	for _, rg := range unknown {
		skip, isOwned, err := classifyTier2(ctx, rg, opts)
		if err != nil {
			return nil, err
		}
		if skip != nil {
			result.Skipped = append(result.Skipped, *skip)
			continue
		}
		if isOwned {
			tier2Owned = append(tier2Owned, rg)
		} else {
			tier3Candidates = append(tier3Candidates, rg)
		}
	}

	// Merge tier-2-owned into owned list for Tier 4 processing.
	owned = append(owned, tier2Owned...)

	// --- Tier 3: prompt or skip remaining unknowns ---
	// Tier 3 runs BEFORE Tier 4 so that user-accepted RGs also receive veto checks
	// (lock check, foreign-resource check). This prevents a user from accidentally
	// deleting a locked or shared RG they accepted as "unknown."
	for _, rg := range tier3Candidates {
		reason := "unknown ownership"
		if opts.Interactive && opts.Prompter != nil {
			accept, err := opts.Prompter(rg, reason)
			if err != nil {
				return nil, fmt.Errorf("classify rg=%s tier=3 prompt: %w", rg, err)
			}
			if accept {
				owned = append(owned, rg)
				continue
			}
		}
		result.Skipped = append(result.Skipped, ClassifiedSkip{
			Name:   rg,
			Reason: fmt.Sprintf("skipped (Tier 3: %s)", reason),
		})
	}

	// --- Tier 4: veto checks on all deletion candidates (parallel, capacity 5) ---
	// This includes Tier 1 owned, Tier 2 owned, AND Tier 3 user-accepted RGs.
	// Tier 4 foreign-resource prompts are collected and executed sequentially below
	// to avoid concurrent terminal output from parallel goroutines.
	type veto struct {
		rg     string
		reason string
	}
	type pendingPrompt struct {
		rg     string
		reason string
	}
	vetoCh := make(chan veto, len(owned))
	promptCh := make(chan pendingPrompt, len(owned))
	sem := make(chan struct{}, cTier4Parallelism)
	var wg sync.WaitGroup
	for _, rg := range owned {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			reason, vetoed, needsPrompt, err := classifyTier4(ctx, rg, opts)
			if err != nil {
				// Fail safe: treat errors as vetoes to avoid accidental deletion.
				log.Printf("ERROR: classify rg=%s tier=4: safety check failed: %v (treating as veto)", rg, err)
				vetoCh <- veto{rg: rg, reason: fmt.Sprintf("error during safety check: %s", err.Error())}
				return
			}
			if needsPrompt {
				promptCh <- pendingPrompt{rg: rg, reason: reason}
				return
			}
			if vetoed {
				vetoCh <- veto{rg: rg, reason: reason}
			}
		}()
	}
	wg.Wait()
	close(vetoCh)
	close(promptCh)

	vetoedSet := make(map[string]string)
	for v := range vetoCh {
		vetoedSet[v.rg] = v.reason
	}

	// Process foreign-resource prompts sequentially on the main goroutine
	// to avoid concurrent terminal output.
	for p := range promptCh {
		if opts.Interactive && opts.Prompter != nil {
			accept, err := opts.Prompter(p.rg, p.reason)
			if err != nil {
				return nil, fmt.Errorf("classify rg=%s tier=4 prompt: %w", p.rg, err)
			}
			if !accept {
				vetoedSet[p.rg] = p.reason
			}
		} else {
			// Non-interactive: foreign resources are a hard veto.
			vetoedSet[p.rg] = p.reason
		}
	}

	for _, rg := range owned {
		if reason, vetoed := vetoedSet[rg]; vetoed {
			result.Skipped = append(result.Skipped, ClassifiedSkip{Name: rg, Reason: reason})
		} else {
			result.Owned = append(result.Owned, rg)
		}
	}

	return result, nil
}

// classifyTier1 uses deployment operations to classify RGs with zero extra API calls.
// Returns (owned, unknown) slices. External RGs are appended directly to result.Skipped.
func classifyTier1(
	operations []*armresources.DeploymentOperation,
	rgNames []string,
	result *ClassifyResult,
) (owned, unknown []string) {
	tier1 := make(map[string]tier1Result, len(rgNames))
	for _, rg := range rgNames {
		tier1[rg] = tier1Unknown
	}
	for _, op := range operations {
		if name, ok := operationTargetsRG(op, cProvisionOpCreate); ok {
			if _, tracked := tier1[name]; tracked {
				tier1[name] = tier1Owned
				continue
			}
			// normalize case for map lookup
			for _, rg := range rgNames {
				if strings.EqualFold(rg, name) {
					tier1[rg] = tier1Owned
					break
				}
			}
			continue
		}
		if name, ok := operationTargetsRG(op, cProvisionOpRead); ok {
			for _, rg := range rgNames {
				if strings.EqualFold(rg, name) && tier1[rg] != tier1Owned {
					tier1[rg] = tier1External
					break
				}
			}
			continue
		}
		if name, ok := operationTargetsRG(op, cProvisionOpEvalOut); ok {
			for _, rg := range rgNames {
				if strings.EqualFold(rg, name) && tier1[rg] != tier1Owned {
					tier1[rg] = tier1External
					break
				}
			}
		}
	}

	for _, rg := range rgNames {
		switch tier1[rg] {
		case tier1Owned:
			owned = append(owned, rg)
		case tier1External:
			result.Skipped = append(result.Skipped, ClassifiedSkip{
				Name:   rg,
				Reason: "external (Tier 1: Read operation found)",
			})
		default:
			unknown = append(unknown, rg)
		}
	}
	return owned, unknown
}

// classifyTier2 performs the dual-tag check on a single RG.
// Returns (skip, isOwned, error):
//   - skip != nil  → already decided (404 = already deleted, etc.)
//   - isOwned      → both tags matched
//   - neither      → fall through to Tier 3
func classifyTier2(ctx context.Context, rgName string, opts ClassifyOptions) (*ClassifiedSkip, bool, error) {
	if opts.GetResourceGroupTags == nil {
		return nil, false, nil
	}
	tags, err := opts.GetResourceGroupTags(ctx, rgName)
	if err != nil {
		if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
			switch respErr.StatusCode {
			case 404:
				return &ClassifiedSkip{Name: rgName, Reason: "already deleted (Tier 2: 404)"}, false, nil
			case 403:
				// Cannot read tags — fall through to Tier 3.
				return nil, false, nil
			}
		}
		return nil, false, fmt.Errorf("classify rg=%s tier=2: %w", rgName, err)
	}

	envTag := tagValue(tags, cAzdEnvNameTag)
	hashTag := tagValue(tags, cAzdProvisionHashTag)
	if envTag != "" && hashTag != "" && strings.EqualFold(envTag, opts.EnvName) {
		return nil, true, nil
	}
	return nil, false, nil
}

// classifyTier4 runs lock and extra-resource veto checks on an owned RG.
// Returns (reason, vetoed, needsPrompt, error).
// When needsPrompt is true, the caller should prompt the user sequentially (not from a goroutine)
// and veto if the user declines.
func classifyTier4(ctx context.Context, rgName string, opts ClassifyOptions) (string, bool, bool, error) {
	// Lock check.
	if opts.ListResourceGroupLocks != nil {
		lockVetoed, lockReason, lockErr := checkTier4Locks(ctx, rgName, opts)
		if lockErr != nil {
			return "", false, false, lockErr
		}
		if lockVetoed {
			return lockReason, true, false, nil
		}
	}

	// Extra-resource check.
	if opts.ListResourceGroupResources != nil {
		resources, err := opts.ListResourceGroupResources(ctx, rgName)
		if err != nil {
			if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
				if respErr.StatusCode == 403 || respErr.StatusCode == 404 {
					return "", false, false, nil
				}
			}
			return "", false, false, fmt.Errorf("classify rg=%s tier=4 resources: %w", rgName, err)
		}
		var foreign []string
		for _, res := range resources {
			tv := tagValue(res.Tags, cAzdEnvNameTag)
			if !strings.EqualFold(tv, opts.EnvName) {
				foreign = append(foreign, res.Name)
			}
		}
		if len(foreign) > 0 {
			reason := fmt.Sprintf(
				"vetoed (Tier 4: %d foreign resource(s) without azd-env-name=%q)", len(foreign), opts.EnvName,
			)
			return reason, true, true, nil
		}
	}

	return "", false, false, nil
}

// checkTier4Locks checks management locks on an RG.
// Returns (vetoed, reason, error). On 403/404, logs and returns no veto (best-effort).
func checkTier4Locks(
	ctx context.Context, rgName string, opts ClassifyOptions,
) (bool, string, error) {
	locks, err := opts.ListResourceGroupLocks(ctx, rgName)
	if err != nil {
		if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
			if respErr.StatusCode == 403 || respErr.StatusCode == 404 {
				log.Printf("classify rg=%s tier=4: lock check skipped (HTTP %d)", rgName, respErr.StatusCode)
				return false, "", nil
			}
		}
		return false, "", fmt.Errorf("classify rg=%s tier=4 locks: %w", rgName, err)
	}
	for _, lock := range locks {
		if strings.EqualFold(lock.LockType, cLockCanNotDelete) ||
			strings.EqualFold(lock.LockType, cLockReadOnly) {
			reason := fmt.Sprintf(
				"vetoed (Tier 4: management lock %q of type %q)", lock.Name, lock.LockType,
			)
			return true, reason, nil
		}
	}
	return false, "", nil
}

// operationTargetsRG checks if a deployment operation targets a resource group
// with the given provisioning operation type. All fields are nil-checked.
func operationTargetsRG(
	op *armresources.DeploymentOperation, provisioningOp string,
) (rgName string, matches bool) {
	if op == nil || op.Properties == nil {
		return "", false
	}
	props := op.Properties
	if props.ProvisioningOperation == nil || props.TargetResource == nil {
		return "", false
	}
	if props.TargetResource.ResourceType == nil || props.TargetResource.ResourceName == nil {
		return "", false
	}
	if !strings.EqualFold(string(*props.ProvisioningOperation), provisioningOp) {
		return "", false
	}
	if !strings.EqualFold(*props.TargetResource.ResourceType, cRGResourceType) {
		return "", false
	}
	return *props.TargetResource.ResourceName, true
}

// tagValue returns the dereferenced value of a tag, or "" if the key is absent or nil.
func tagValue(tags map[string]*string, key string) string {
	if tags == nil {
		return ""
	}
	for k, v := range tags {
		if strings.EqualFold(k, key) {
			if v != nil {
				return *v
			}
			return ""
		}
	}
	return ""
}
