// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"errors"
	"fmt"
)

// Pipeline collects read-only planning and preflight evidence.
type Pipeline struct {
	Resources ResourceChecker
	Access    Authorizer
	// Resolve discovers existing ancestors without changing creation/reuse choices.
	Resolve func(context.Context, Plan) (Plan, error)
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
