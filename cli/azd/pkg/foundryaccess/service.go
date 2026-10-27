// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundryaccess

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Service checks access before remote operations. It keeps no access cache:
// every call revalidates the target and probes the operation, so grants revoked
// outside azd or different principals/projects cannot reuse a stale success.
// Concurrent callers reconcile grants through the backend's deterministic assignment.
type Service struct {
	backend Backend
	options Options
}

// NewService constructs a service without performing network calls.
func NewService(backend Backend, options *Options) (*Service, error) {
	if backend == nil {
		return nil, fmt.Errorf("a Foundry access backend is required")
	}
	opts := Options{PropagationTimeout: 2 * time.Minute, InitialDelay: time.Second}
	if options != nil {
		if options.PropagationTimeout < 0 || options.InitialDelay < 0 {
			return nil, fmt.Errorf("Foundry access timeouts must not be negative")
		}
		if options.PropagationTimeout != 0 {
			opts.PropagationTimeout = options.PropagationTimeout
		}
		if options.InitialDelay != 0 {
			opts.InitialDelay = options.InitialDelay
		}
	}
	return &Service{backend: backend, options: opts}, nil
}

// Ensure probes access and optionally ensures Foundry User after a confirmed RBAC
// denial. Unknown, probe errors, and CheckOnly never cause role writes. After a
// grant, only Denied is retried; Unknown/errors stop immediately. An assignment
// never counts as success until the operation-specific probe returns Allowed.
// Callers must check Result.State, not just the error, before proceeding.
func (s *Service) Ensure(ctx context.Context, request Request) (Result, error) {
	result := Result{State: Unknown, Operation: request.Operation}
	if request.Probe == nil || strings.TrimSpace(request.Operation) == "" {
		return result, fmt.Errorf("an operation and a non-mutating access probe are required")
	}
	if request.Policy != "" && request.Policy != CheckOnly && request.Policy != GrantFoundryUser {
		return result, fmt.Errorf("unsupported Foundry access grant policy")
	}
	if _, err := validateTarget(request.Target); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	project, err := s.backend.Resolve(ctx, request.Target)
	if err != nil {
		return result, fmt.Errorf("resolving Foundry project access: %w", err)
	}
	result.Project = project
	check := func(ctx context.Context) error {
		result.State = Unknown
		if err := ctx.Err(); err != nil {
			return err
		}
		result.ProbeAttempts++
		state, err := request.Probe(ctx, project)
		if err != nil {
			return fmt.Errorf("probing Foundry project access: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		switch state {
		case Allowed, Denied, Unknown:
			result.State = state
			return nil
		default:
			return fmt.Errorf("access probe returned an invalid state")
		}
	}
	if err := check(ctx); err != nil {
		return result, err
	}
	if result.State != Denied || request.Policy != GrantFoundryUser {
		return result, nil
	}
	grantCtx, cancel := context.WithTimeout(ctx, s.options.PropagationTimeout)
	defer cancel()
	if err := grantCtx.Err(); err != nil {
		return result, err
	}
	created, err := s.backend.EnsureGrant(grantCtx, project)
	if err != nil {
		return result, fmt.Errorf("ensuring Foundry User access: %w", err)
	}
	result.AssignmentCreated, result.GrantEnsured = created, true
	delay := min(s.options.InitialDelay, 10*time.Second)
	for {
		if err := grantCtx.Err(); err != nil {
			return result, fmt.Errorf("waiting for Foundry access propagation: %w", err)
		}
		if err := check(grantCtx); err != nil {
			return result, err
		}
		if result.State != Denied {
			return result, nil
		}
		timer := time.NewTimer(delay)
		select {
		case <-grantCtx.Done():
			timer.Stop()
			return result, fmt.Errorf("waiting for Foundry access propagation: %w", grantCtx.Err())
		case <-timer.C:
		}
		delay = min(delay*2, 10*time.Second)
	}
}
