// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"errors"
	"fmt"
)

// Pipeline collects read-only AKS, selected-network, resource and permission evidence.
type Pipeline struct {
	Resources ResourceChecker
	Access    Authorizer

	BeforeResourceCheck func()
	BeforeAccessCheck   func()
}

// Preflight collects resource and permission evidence before opening the write gate.
func (p Pipeline) Preflight(ctx context.Context, plan Plan) (Facts, Report, error) {
	if p.Resources == nil || p.Access == nil {
		return Facts{}, nil, fmt.Errorf("BYO-AKS preflight dependencies are unavailable")
	}
	if p.BeforeResourceCheck != nil {
		p.BeforeResourceCheck()
	}
	facts, report, err := p.Resources.Check(ctx, plan)
	if err != nil {
		if _, blocked := errors.AsType[*BlockedError](err); !blocked {
			return facts, report, err
		}
	}
	if p.BeforeAccessCheck != nil {
		p.BeforeAccessCheck()
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
