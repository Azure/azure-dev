// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package onboarding implements the read-only plan, evidence gates and owned
// preparation lifecycle for customer AKS hosting.
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
	Mandatory   bool
	Cause       error
}

// Report retains all checks, including real service errors.
type Report []Check

// Err returns all mandatory failures without discarding underlying errors.
func (r Report) Err() error {
	var blocked []Check
	for _, c := range r {
		if c.Mandatory && c.Status != Pass && c.Status != NotApplicable {
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
		message.WriteString(fmt.Sprintf("; %s %s: %s (%s)",
			c.ID, c.Status, RedactDiagnostic(c.Observed), RedactDiagnostic(c.Remediation)))
	}
	return message.String()
}

var diagnosticURLPattern = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)

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

// Resource describes a supplied resource or a planned owned resource.
type Resource struct {
	ID       string
	Parent   string // nearest existing ancestor scope
	Name     string
	Supplied bool
}

// Operation is an actual selected ARM action, not an unconditional role name.
type Operation struct {
	CheckID       string
	Action        string
	Scope         string
	RoleID        string
	PrincipalID   string // empty for a future managed identity
	PrincipalType string // defaults to ServicePrincipal for managed grants
}

// Plan is the single read-only source for checks and preparation.
type Plan struct {
	// NetworkChanges marks a selected topology whose mutation lifecycle is not implemented.
	NetworkChanges bool
	Network        *SubnetPreparation
	SubscriptionID string
	ResourceGroup  string
	Location       string
	Environment    string
	Owner          string
	Cluster        Resource
	Storage        Resource
	Registry       Resource
	AgentSubnet    Resource
	Hosting        Resource
	Workload       Resource
	Operations     []Operation
}

// SubnetPreparation is the bounded same-VNet companion topology, resolved without writes.
// It creates only a delegated subnet; it never changes AKS, existing subnets, routes, NSGs or peerings.
type SubnetPreparation struct {
	VNetID        string `json:"vnetId"`
	SubnetID      string `json:"subnetId"`
	AddressPrefix string `json:"addressPrefix"`
	Location      string `json:"location"`
}

// Facts contains verified existing-resource information needed by preparation.
type Facts struct {
	KubeletObjectID string
	RegistryABAC    bool
	TenantID        string
}

// Identity records distinct ARM, object, client and tenant identifiers.
type Identity struct {
	ID          string
	PrincipalID string
	ClientID    string
	TenantID    string
}

// Prepared is private deployment input; it must never be accepted from YAML.
type Prepared struct {
	ResourceGroupTags map[string]*string
	Hosting           Identity
	Workload          Identity
	StorageID         string
	RegistryID        string
	RegistryEndpoint  string
	RegistryABAC      bool
	AgentSubnetID     string
}

// ResourceChecker performs read-only existing resource and network checks.
type ResourceChecker interface {
	Check(context.Context, Plan) (Facts, Report, error)
}

// Authorizer checks scoped operations and grant conditions without writing.
type Authorizer interface {
	Check(context.Context, Plan, Facts) (Report, error)
}

// Preparer prepares only resources and assignments owned by this environment.
type Preparer interface {
	Prepare(context.Context, Plan, Facts) (Prepared, error)
}
