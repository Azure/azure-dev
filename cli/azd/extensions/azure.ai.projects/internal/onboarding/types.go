// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore kubelet termenv

// Package onboarding checks BYO AKS prerequisites, supplied-identity permissions and supporting resources
// without mutating Azure or persisting deployment state.
package onboarding

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/muesli/termenv"
)

// Status describes evidence, not whether an API request happened to succeed.
type Status string

const (
	Pass          Status = "PASS"
	Fail          Status = "FAIL"
	Unverified    Status = "UNVERIFIED"
	NotApplicable Status = "NOT_APPLICABLE"
	Skipped       Status = "SKIPPED"
)

// Check records a single requirement and its actionable evidence.
type Check struct {
	ID             string
	Scope          string
	ResourceType   string
	Status         Status
	FailureReasons []string
	Mandatory      bool
	Cause          error
}

func (c Check) blocks() bool {
	return c.Mandatory && c.Status != Pass && c.Status != NotApplicable
}

// Report retains all checks, including real service errors.
type Report []Check

// Err returns all mandatory failures without discarding underlying errors.
func (r Report) Err() error {
	var blocked []Check
	for _, c := range r {
		if c.blocks() {
			blocked = append(blocked, c)
		}
	}
	if len(blocked) > 0 {
		return &BlockedError{Checks: blocked}
	}
	return nil
}

// BlockedError identifies the exact checks that prevent Azure writes.
type BlockedError struct {
	Checks []Check
}

func (e *BlockedError) Error() string {
	groups := map[string][]string{}
	var resourceTypes []string
	for _, c := range e.Checks {
		if !c.blocks() {
			continue
		}
		resourceType := strings.TrimSpace(c.ResourceType)
		if resourceType == "" {
			resourceType = "Resource configuration"
		}
		if _, exists := groups[resourceType]; !exists {
			resourceTypes = append(resourceTypes, resourceType)
		}
		reasons := c.FailureReasons
		if !slices.ContainsFunc(reasons, func(reason string) bool { return strings.TrimSpace(reason) != "" }) {
			reasons = []string{"A mandatory prerequisite could not be verified"}
		}
		for _, reason := range reasons {
			reason = strings.TrimSpace(reason)
			if reason == "" {
				continue
			}
			if !slices.Contains(groups[resourceType], reason) {
				groups[resourceType] = append(groups[resourceType], reason)
			}
		}
	}
	var message strings.Builder
	for i, resourceType := range resourceTypes {
		if i > 0 {
			message.WriteString("\n\n")
		}
		fmt.Fprintf(&message, "%s does not meet requirements. Review the %s and edit your configuration before proceeding.",
			resourceType, prerequisitesLink())
		for _, reason := range groups[resourceType] {
			message.WriteString("\n- " + reason)
		}
	}
	return message.String()
}

// Unwrap preserves independent service error chains.
func (e *BlockedError) Unwrap() []error {
	var causes []error
	for _, c := range e.Checks {
		if c.Cause != nil {
			causes = append(causes, c.Cause)
		}
	}
	return causes
}

const prerequisitesURL = "https://aka.ms/byo-aks-onboarding-prerequisites"

func prerequisitesLink() string {
	// The extension's stdout is a host pipe, not the user's terminal. Color intent alone does not establish OSC8 support.
	term := os.Getenv("TERM")
	knownTerminal := os.Getenv("WT_SESSION") != ""
	switch os.Getenv("TERM_PROGRAM") {
	case "vscode", "iTerm.app", "WezTerm", "ghostty":
		knownTerminal = true
	}
	if os.Getenv("FORCE_COLOR") == "1" && os.Getenv("NO_COLOR") == "" && os.Getenv("CI") == "" &&
		term != "dumb" && !strings.HasPrefix(term, "screen") && !strings.HasPrefix(term, "tmux") && knownTerminal {
		return termenv.Hyperlink(prerequisitesURL, "requirements")
	}
	return "requirements (" + prerequisitesURL + ")"
}

// Resource records a selected ARM resource's lifecycle and metadata requirements.
type Resource struct {
	ID               string
	Parent           string
	Supplied         bool
	Origin           string
	Missing          string
	LifecycleMissing string
	// PrincipalRequired requests binding facts without changing the ARM create/update lifecycle.
	PrincipalRequired bool
}

// SkippedCheck records an unresolved template-plan dependency, not successful evidence.
func SkippedCheck(id, scope, resourceType string) Check {
	return Check{
		ID: id, Scope: scope, ResourceType: resourceType, Status: Skipped,
	}
}

func (r Resource) diagnosticScope() string {
	if r.ID != "" {
		return r.ID
	}
	return r.Origin
}

// Plan contains this deployment's resolved resource and standard-network selections.
type Plan struct {
	SubscriptionID      string
	AgentSubnet         Resource
	SkippedChecks       Report
	OwningSubscriptions []string
	Cluster             Resource
	Storage             Resource
	Hosting             Resource
	Workload            Resource
}

// Facts contains current read-only identity evidence for authorization and deployment bindings.
type Facts struct {
	KubeletObjectID  string
	HostingIdentity  Identity
	WorkloadIdentity Identity
}

// Identity records a managed identity's ARM resource ID and principal object ID.
type Identity struct {
	ID          string
	PrincipalID string
}

// ResourceChecker performs read-only AKS, selected-network and supporting-resource checks.
type ResourceChecker interface {
	Check(context.Context, Plan) (Facts, Report, error)
}

// Authorizer checks supplied-identity grants without writing.
type Authorizer interface {
	Check(context.Context, Plan, Facts) (Report, error)
}
