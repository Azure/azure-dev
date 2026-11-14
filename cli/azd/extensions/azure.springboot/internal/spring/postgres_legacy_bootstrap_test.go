// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/stretchr/testify/require"
)

func legacyPostgresJobFixture(t *testing.T) (
	*armappcontainers.Job, *deploymentBinding, resolvedDatabase, *verifiedPostgresResources,
) {
	t.Helper()
	input := testPostgresBootstrapInput()
	input.SessionAdministratorName = "azd-pg-0123456789abcdef"
	encoded, err := encodePostgresBootstrapInput(input)
	require.NoError(t, err)
	server := &verifiedPostgresResources{
		SubscriptionID: "00000000-0000-0000-0000-000000000001", ResourceGroup: "rg",
		ServerID: input.ServerResourceID, Host: input.Host, AdministratorName: input.ExpectedAdministratorName,
		IdentityPrincipalID: input.ApplicationPrincipalID,
	}
	database := resolvedDatabase{Name: input.DatabaseName, Username: input.ApplicationUsername}
	environmentID := "/subscriptions/" + server.SubscriptionID + "/resourceGroups/rg/" +
		"providers/Microsoft.App/managedEnvironments/environment"
	binding := &deploymentBinding{app: &armappcontainers.ContainerApp{
		Location: new("westus2"), Properties: &armappcontainers.ContainerAppProperties{
			EnvironmentID: new(environmentID),
		},
	}}
	job := &armappcontainers.Job{
		ID: new("/subscriptions/" + server.SubscriptionID + "/resourceGroups/rg/providers/" +
			"Microsoft.App/jobs/" + input.SessionAdministratorName),
		Name: new(input.SessionAdministratorName), Location: new("westus2"),
		SystemData: &armappcontainers.SystemData{CreatedAt: new(time.Now().Add(-2 * time.Hour))},
		Identity: &armappcontainers.ManagedServiceIdentity{
			Type:        new(armappcontainers.ManagedServiceIdentityTypeSystemAssigned),
			PrincipalID: new(localTestCallerID), TenantID: new(localTestTenantID),
		},
		Tags: map[string]*string{
			postgresBootstrapOwnerTag: new("true"), azdEnvironmentTag: new("dev"),
			postgresBootstrapServerTag: new(postgresBootstrapServerTagValue(server.ServerID)),
		},
		Properties: &armappcontainers.JobProperties{
			EnvironmentID: new(environmentID),
			Configuration: &armappcontainers.JobConfiguration{
				TriggerType: new(armappcontainers.TriggerTypeManual), ReplicaTimeout: new(int32(600)),
				ReplicaRetryLimit: new(int32(0)),
				ManualTriggerConfig: &armappcontainers.JobConfigurationManualTriggerConfig{
					Parallelism: new(int32(1)), ReplicaCompletionCount: new(int32(1)),
				},
			},
			Template: &armappcontainers.JobTemplate{
				Containers: []*armappcontainers.Container{{
					Name: new("postgres-bootstrap"),
					Image: new("mcr.microsoft.com/azure-dev/azd-spring-postgres-bootstrap@sha256:" +
						strings.Repeat("a", 64)),
					Env: []*armappcontainers.EnvironmentVar{{
						Name: new(postgresBootstrapInputEnv), Value: new(encoded),
					}},
					Resources: &armappcontainers.ContainerResources{CPU: new(0.25), Memory: new("0.5Gi")},
				}},
			},
		},
	}
	return job, binding, database, server
}

func TestLegacyPostgresRecoveryRequiresExactOwnedShape(t *testing.T) {
	for _, scenario := range []string{
		"owned-stale", "recent", "foreign-environment", "foreign-server", "foreign-identity",
		"changed-template", "changed-input", "changed-job-id", "changed-image",
	} {
		t.Run(scenario, func(t *testing.T) {
			job, binding, database, server := legacyPostgresJobFixture(t)
			switch scenario {
			case "recent":
				job.SystemData.CreatedAt = new(time.Now())
			case "foreign-environment":
				job.Properties.EnvironmentID = new("foreign")
			case "foreign-server":
				job.Tags[postgresBootstrapServerTag] = new("foreign")
			case "foreign-identity":
				job.Identity.Type = new(armappcontainers.ManagedServiceIdentityTypeUserAssigned)
			case "changed-template":
				job.Properties.Template.Containers[0].Command = []*string{new("changed")}
			case "changed-input":
				job.Properties.Template.Containers[0].Env[0].Value = new("changed")
			case "changed-job-id":
				job.ID = new(*job.ID + "-foreign")
			case "changed-image":
				job.Properties.Template.Containers[0].Image = new("unverified:latest")
			}
			err := verifyLegacyPostgresBootstrap(job, binding, "dev", database, server, time.Now())
			if scenario == "owned-stale" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
