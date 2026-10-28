// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package onboarding validates existing AKS hosting and plans its default supporting resources.
package onboarding

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
)

// Status describes the available resource evidence.
type Status string

const (
	Pass       Status = "PASS"
	Fail       Status = "FAIL"
	Unverified Status = "UNVERIFIED"
)

// Check records one prerequisite and its evidence.
type Check struct {
	ID, Scope, Expected, Observed string
	Status                        Status
	Cause                         error
}

// Report contains the AKS and network prerequisites.
type Report []Check

// Err preserves the service errors underlying failed or unverified prerequisites.
func (r Report) Err() error {
	var blocked Report
	for _, check := range r {
		if check.Status != Pass {
			blocked = append(blocked, check)
		}
	}
	if len(blocked) != 0 {
		return &BlockedError{Checks: blocked}
	}
	return nil
}

// Write renders prerequisite evidence on the caller's writer.
func (r Report) Write(w io.Writer) error {
	for _, check := range r {
		if _, err := fmt.Fprintf(w, "%s %s [%s]: %s\n", check.ID, check.Status,
			RedactDiagnostic(check.Scope), RedactDiagnostic(check.Observed)); err != nil {
			return err
		}
	}
	return nil
}

// BlockedError prevents deployment without verified prerequisites.
type BlockedError struct{ Checks Report }

func (e *BlockedError) Error() string {
	var text strings.Builder
	text.WriteString("BYO AKS prerequisites are not satisfied")
	for _, check := range e.Checks {
		fmt.Fprintf(&text, "; %s %s: %s (%s)", check.ID, check.Status,
			RedactDiagnostic(check.Observed), RedactDiagnostic(check.Expected))
	}
	return text.String()
}

// Unwrap retains every underlying Azure error.
func (e *BlockedError) Unwrap() []error {
	var causes []error
	for _, check := range e.Checks {
		if check.Cause != nil {
			causes = append(causes, check.Cause)
		}
	}
	return causes
}

var diagnosticURLPattern = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)

// RedactDiagnostic strips URL userinfo, queries and fragments from diagnostics.
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

// Facts contains verified bindings needed by the ARM graph.
type Facts struct {
	KubeletObjectID                   string `json:"kubeletObjectId"`
	FederatedIdentityRoleDefinitionID string `json:"federatedIdentityRoleDefinitionId"`
}

// ResourceChecker performs only read operations before the deployment intent is saved.
type ResourceChecker interface {
	Check(context.Context, Plan) (Facts, Report, error)
}
