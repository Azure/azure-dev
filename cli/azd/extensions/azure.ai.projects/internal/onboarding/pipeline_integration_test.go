// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"bytes"
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

type contractPreparer func(context.Context, Plan, Facts) (Prepared, error)

func (f contractPreparer) Prepare(ctx context.Context, plan Plan, facts Facts) (Prepared, error) {
	return f(ctx, plan, facts)
}

func contractPrepared(plan Plan) Prepared {
	return Prepared{
		Hosting: Identity{
			ID: plan.Hosting.ID, PrincipalID: "hosting-object", ClientID: "hosting-client", TenantID: "tenant",
		},
		Workload: Identity{
			ID: plan.Workload.ID, PrincipalID: "workload-object", ClientID: "workload-client", TenantID: "tenant",
		},
		StorageID: plan.Storage.ID, RegistryID: plan.Registry.ID,
		RegistryEndpoint: "registry.azurecr.io", AgentSubnetID: plan.AgentSubnet.ID,
	}
}

func TestPipelineContractEveryMandatoryCheckClosesWriteGate(t *testing.T) {
	groups := []struct {
		ids    []string
		access bool
	}{
		{ids: []string{
			"I01", "I02", "I03", "A01", "A02", "A03", "A04", "A05", "A06", "A07", "A08", "A09", "A10", "A11",
			"S01", "S02", "S03", "C01", "N02",
		}},
		{ids: []string{"P01", "P02", "P03", "P04", "P05", "P06", "P07", "P08", "P09"}, access: true},
	}
	for _, group := range groups {
		for _, id := range group.ids {
			for _, status := range []Status{Fail, Unverified} {
				t.Run(id+"/"+string(status), func(t *testing.T) {
					plan, err := BuildPlan(contractPlanInput())
					require.NoError(t, err)
					cause := &azcore.ResponseError{StatusCode: 403, ErrorCode: "AuthorizationFailed"}
					failed := Report{{
						ID: id, Scope: plan.Cluster.ID, Status: status, Mandatory: true, Cause: cause,
						Expected: "verified", Observed: "missing", Missing: "evidence", Remediation: "ask administrator",
					}}
					pipeline := Pipeline{
						Resources: contractResources(func(context.Context, Plan) (Facts, Report, error) {
							if !group.access {
								return Facts{}, failed, nil
							}
							return Facts{}, nil, nil
						}),
						Access: contractAccess(func(context.Context, Plan, Facts) (Report, error) {
							if group.access {
								return failed, nil
							}
							return nil, nil
						}),
						Prepare: contractPreparer(func(context.Context, Plan, Facts) (Prepared, error) {
							t.Error("mandatory failure reached Azure preparation")
							return contractPrepared(plan), nil
						}),
					}
					report, err := pipeline.Run(t.Context(), plan, false, func(context.Context, Prepared) error {
						t.Error("mandatory failure reached Foundry deployment")
						return nil
					})
					require.Error(t, err)
					assert.ErrorIs(t, err, cause)
					blocked, ok := errors.AsType[*BlockedError](err)
					require.True(t, ok)
					assert.Equal(t, failed, Report(blocked.Checks))
					assert.Equal(t, failed, report)
					var output bytes.Buffer
					require.NoError(t, report.Write(&output))
					for _, text := range []string{id, string(status), plan.Cluster.ID, "evidence", "ask administrator"} {
						assert.Contains(t, output.String(), text)
					}
				})
			}
		}
	}
}

func TestPipelineContractStageOrderingAndErrors(t *testing.T) {
	for _, stage := range []string{"success", "preview", "resource", "access", "prepare", "deploy"} {
		t.Run(stage, func(t *testing.T) {
			plan, err := BuildPlan(contractPlanInput())
			require.NoError(t, err)
			prepared := contractPrepared(plan)
			facts := Facts{KubeletObjectID: "kubelet-object", RegistryABAC: true, TenantID: "tenant"}
			cause := &azcore.ResponseError{StatusCode: 409, ErrorCode: "Conflict"}
			var events []string
			stageError := func(name string) error {
				if stage == name {
					return cause
				}
				return nil
			}
			resources := Report{{ID: "I02", Status: Pass, Mandatory: true}}
			access := Report{{ID: "P07", Status: Pass, Mandatory: true}}
			pipeline := Pipeline{
				Resources: contractResources(func(_ context.Context, got Plan) (Facts, Report, error) {
					events = append(events, "resource")
					assert.Equal(t, plan, got)
					return facts, resources, stageError("resource")
				}),
				Access: contractAccess(func(_ context.Context, got Plan, gotFacts Facts) (Report, error) {
					events = append(events, "access")
					assert.Equal(t, plan, got)
					assert.Equal(t, facts, gotFacts)
					return access, stageError("access")
				}),
				Prepare: contractPreparer(func(_ context.Context, got Plan, gotFacts Facts) (Prepared, error) {
					events = append(events, "prepare")
					assert.Equal(t, plan, got)
					assert.Equal(t, facts, gotFacts)
					return prepared, stageError("prepare")
				}),
			}
			report, err := pipeline.Run(t.Context(), plan, stage == "preview",
				func(_ context.Context, got Prepared) error {
					events = append(events, "deploy")
					assert.Equal(t, prepared, got)
					return stageError("deploy")
				})
			wantEvents := map[string][]string{
				"success":  {"resource", "access", "prepare", "deploy"},
				"preview":  {"resource", "access"},
				"resource": {"resource"},
				"access":   {"resource", "access"},
				"prepare":  {"resource", "access", "prepare"},
				"deploy":   {"resource", "access", "prepare", "deploy"},
			}
			assert.Equal(t, wantEvents[stage], events)
			if stage == "success" || stage == "preview" {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, cause)
			}
			if stage == "resource" {
				assert.Equal(t, resources, report)
			} else {
				assert.Equal(t, append(resources, access...), report)
			}
		})
	}
}

func TestPipelineContractCancellation(t *testing.T) {
	for _, stage := range []string{"before preflight", "during access", "during prepare"} {
		t.Run(stage, func(t *testing.T) {
			plan, err := BuildPlan(contractPlanInput())
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if stage == "before preflight" {
				cancel()
			}
			pipeline := Pipeline{
				Resources: contractResources(func(context.Context, Plan) (Facts, Report, error) {
					return Facts{}, nil, nil
				}),
				Access: contractAccess(func(context.Context, Plan, Facts) (Report, error) {
					if stage == "during access" {
						cancel()
					}
					return nil, nil
				}),
				Prepare: contractPreparer(func(context.Context, Plan, Facts) (Prepared, error) {
					assert.Equal(t, "during prepare", stage, "canceled preflight reached preparation")
					cancel()
					return contractPrepared(plan), nil
				}),
			}
			_, err = pipeline.Run(ctx, plan, false, func(context.Context, Prepared) error {
				t.Error("canceled pipeline reached deployment")
				return nil
			})
			assert.ErrorIs(t, err, context.Canceled)
		})
	}
}

func TestPipelineContractFinalValidationAfterPreparation(t *testing.T) {
	cases := map[string]func(*Prepared){
		"same identity ID":    func(p *Prepared) { p.Workload.ID = p.Hosting.ID },
		"same object ID":      func(p *Prepared) { p.Workload.PrincipalID = p.Hosting.PrincipalID },
		"missing hosting ID":  func(p *Prepared) { p.Hosting.ID = "" },
		"missing object ID":   func(p *Prepared) { p.Workload.PrincipalID = "" },
		"missing client ID":   func(p *Prepared) { p.Hosting.ClientID = "" },
		"missing tenant ID":   func(p *Prepared) { p.Workload.TenantID = "" },
		"missing storage ID":  func(p *Prepared) { p.StorageID = "" },
		"missing registry ID": func(p *Prepared) { p.RegistryID = "" },
		"missing subnet ID":   func(p *Prepared) { p.AgentSubnetID = "" },
		"changed storage ID":  func(p *Prepared) { p.StorageID += "-different" },
		"changed registry ID": func(p *Prepared) { p.RegistryID += "-different" },
		"changed subnet ID":   func(p *Prepared) { p.AgentSubnetID += "-different" },
	}
	for name, invalidate := range cases {
		t.Run(name, func(t *testing.T) {
			plan, err := BuildPlan(contractPlanInput())
			require.NoError(t, err)
			var events []string
			pipeline := Pipeline{
				Resources: contractResources(func(context.Context, Plan) (Facts, Report, error) {
					return Facts{}, nil, nil
				}),
				Access: contractAccess(func(context.Context, Plan, Facts) (Report, error) { return nil, nil }),
				Prepare: contractPreparer(func(context.Context, Plan, Facts) (Prepared, error) {
					events = append(events, "prepare")
					prepared := contractPrepared(plan)
					invalidate(&prepared)
					return prepared, nil
				}),
			}
			_, err = pipeline.Run(t.Context(), plan, false, func(context.Context, Prepared) error {
				t.Error("incomplete internal deployment parameters reached Foundry")
				return nil
			})
			require.Error(t, err)
			assert.Equal(t, []string{"prepare"}, events)
		})
	}
}
