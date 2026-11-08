// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore kubelet

// Package onboarding checks BYO AKS prerequisites, supplied-identity permissions and supporting resources
// without mutating Azure or persisting deployment state.
package onboarding

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
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
	ID          string
	Scope       string
	Status      Status
	Expected    string
	Observed    string
	Missing     string
	Remediation string
	Warning     string
	Mandatory   bool
	Cause       error
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

// Write renders evidence to the caller's writer.
func (r Report) Write(w io.Writer) error {
	for _, c := range r {
		if _, err := fmt.Fprintf(w, "%s %s [%s]\n  Expected: %s\n  Observed: %s\n"+
			"  Missing evidence/permission: %s\n  Remediation: %s\n",
			c.ID, c.Status, RedactDiagnostic(c.Scope), RedactDiagnostic(c.Expected), RedactDiagnostic(c.Observed),
			RedactDiagnostic(c.Missing), RedactDiagnostic(c.Remediation)); err != nil {
			return err
		}
	}
	for _, c := range r {
		if c.Warning != "" {
			if _, err := fmt.Fprintf(w, "WARNING: %s [%s]: %s\n",
				c.ID, RedactDiagnostic(c.Scope), RedactDiagnostic(c.Warning)); err != nil {
				return err
			}
		}
	}
	return nil
}

// BlockedError identifies the exact checks that prevent Azure writes.
type BlockedError struct {
	Checks []Check
}

func (e *BlockedError) Error() string {
	var message strings.Builder
	message.WriteString("BYO-AKS preflight blocked")
	for _, c := range e.Checks {
		message.WriteString(fmt.Sprintf("; %s %s [%s]: %s (%s)",
			c.ID, c.Status, RedactDiagnostic(c.Scope), RedactDiagnostic(c.Observed), RedactDiagnostic(c.Remediation)))
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

var diagnosticURLPattern = regexp.MustCompile(`(?i)https?://[^\s<>"]+`)

// RedactDiagnostic removes URL credentials without discarding service error codes or safe endpoint paths.
func RedactDiagnostic(message string) string {
	return diagnosticURLPattern.ReplaceAllStringFunc(message, func(raw string) string {
		endpoint, err := url.Parse(raw)
		if err != nil {
			return "[redacted URL]"
		}
		endpoint.User, endpoint.RawQuery, endpoint.Fragment = nil, "", ""
		return endpoint.String()
	})
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

// SkippedCheck reports an unresolved template-plan dependency, not successful evidence.
func SkippedCheck(id, scope, requirement, reason string) Check {
	return Check{
		ID: id, Scope: scope, Status: Skipped, Expected: requirement,
		Observed: "check skipped: " + reason, Missing: reason,
		Warning: "skipped " + requirement + ": " + reason,
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
