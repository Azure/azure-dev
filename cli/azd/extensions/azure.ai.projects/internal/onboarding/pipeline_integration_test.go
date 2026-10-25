// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type contractResources func(context.Context, Plan) (Facts, Report, error)

func (f contractResources) Check(ctx context.Context, plan Plan) (Facts, Report, error) {
	return f(ctx, plan)
}

type contractAccess func(context.Context, Plan, Facts) (Report, error)

func (f contractAccess) Check(ctx context.Context, plan Plan, facts Facts) (Report, error) {
	return f(ctx, plan, facts)
}

func TestPipelineRetainsEveryMandatoryEvidenceGate(t *testing.T) {
	for _, id := range []string{
		"I01", "I02", "I03", "A01", "A02", "A03", "A04", "A05", "A06", "A07", "A08", "A09", "A10", "A11",
		"S01", "S02", "S03", "C01", "N02", "P02", "P03", "P04", "P05", "P07", "P09",
	} {
		for _, status := range []Status{Fail, Unverified} {
			t.Run(id+"/"+string(status), func(t *testing.T) {
				cause := &azcore.ResponseError{StatusCode: 403, ErrorCode: "AuthorizationFailed"}
				failure := Report{{ID: id, Status: status, Mandatory: true, Cause: cause}}
				pipeline := Pipeline{
					Resources: contractResources(func(context.Context, Plan) (Facts, Report, error) {
						return Facts{TenantID: "verified"}, failure, failure.Err()
					}),
					Access: contractAccess(func(_ context.Context, _ Plan, facts Facts) (Report, error) {
						assert.Equal(t, "verified", facts.TenantID)
						return Report{{ID: "P02", Status: Pass, Mandatory: true}}, nil
					}),
				}
				_, report, err := pipeline.Preflight(t.Context(), Plan{})
				require.Error(t, err)
				assert.ErrorIs(t, err, cause)
				_, blocked := errors.AsType[*BlockedError](err)
				assert.True(t, blocked)
				require.Len(t, report, 2, "resource failure must not hide independent permission evidence")
			})
		}
	}
}

func TestPipelineHasNoMutationOrDeploymentExecutor(t *testing.T) {
	plan, err := BuildPlan(contractPlanInput())
	require.NoError(t, err)
	var stages []string
	pipeline := Pipeline{
		Resolve: func(_ context.Context, p Plan) (Plan, error) {
			stages = append(stages, "resolve")
			return p, nil
		},
		Resources: contractResources(func(context.Context, Plan) (Facts, Report, error) {
			stages = append(stages, "resources")
			return Facts{TenantID: "tenant", KubeletObjectID: "kubelet"}, nil, nil
		}),
		Access: contractAccess(func(context.Context, Plan, Facts) (Report, error) {
			stages = append(stages, "access")
			return nil, nil
		}),
	}
	plan, err = pipeline.Resolve(t.Context(), plan)
	require.NoError(t, err)
	facts, _, err := pipeline.Preflight(t.Context(), plan)
	require.NoError(t, err)
	data, err := PlannedDeployment(plan, facts)
	require.NoError(t, err)
	assert.Equal(t, plan.Hosting, data.Hosting)
	assert.Equal(t, plan.Workload, data.Workload)
	assert.Equal(t, []string{"resolve", "resources", "access"}, stages)
}
