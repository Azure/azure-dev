// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	postgresBootstrapOwnerTag  = "azd-springboot-postgres-bootstrap"
	postgresBootstrapServerTag = "azd-springboot-postgres-server"
)

type postgresBootstrapAdministrator struct {
	PrincipalName string `json:"principalName"`
	PrincipalType string `json:"principalType"`
	TenantID      string `json:"tenantId"`
}

func postgresBootstrapServerTagValue(serverID string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(serverID)))
	return hex.EncodeToString(sum[:])
}

func postgresBootstrapRandomSuffix() (string, error) {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generating PostgreSQL bootstrap ownership suffix: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func newPostgresAccessDispatcher(client *azdext.AzdClient) func(
	context.Context, string, resolvedDatabase, map[string]string,
) error {
	local := newPostgresAccessReconciler(client)
	provision := newPostgresLocalAccessReconciler(local)
	return func(ctx context.Context, envName string, database resolvedDatabase, values map[string]string) error {
		binding, ok := ctx.Value(deploymentBindingKey{}).(*deploymentBinding)
		if ok && binding.automatic && database.Authentication == databaseAuthenticationManagedIdentity {
			return provision.Reconcile(ctx, envName, database, values)
		}
		return local.Reconcile(ctx, envName, database, values)
	}
}

func recoverLegacyPostgresBootstrap(
	ctx context.Context, binding *deploymentBinding, envName string, database resolvedDatabase,
	server *verifiedPostgresResources, credential azcore.TokenCredential,
) error {
	jobs, err := armappcontainers.NewJobsClient(server.SubscriptionID, credential, nil)
	if err != nil {
		return err
	}
	local, err := newAzurePostgresLocalCloud(server, credential)
	if err != nil {
		return err
	}
	pager := jobs.NewListByResourceGroupPager(server.ResourceGroup, nil)
	for page := 0; pager.More(); page++ {
		if page >= 128 {
			return configurationError("the legacy PostgreSQL bootstrap inventory exceeded its page limit")
		}
		response, err := pager.NextPage(ctx)
		if err != nil {
			return nonDisclosureError(err)
		}
		for _, job := range response.Value {
			if job == nil || !tagEqualsPointers(job.Tags, postgresBootstrapOwnerTag, "true") ||
				!tagEqualsPointers(job.Tags, azdEnvironmentTag, envName) ||
				!tagEqualsPointers(job.Tags, postgresBootstrapServerTag, postgresBootstrapServerTagValue(server.ServerID)) {
				continue
			}
			if err := verifyLegacyPostgresBootstrap(job, binding, envName, database, server, time.Now()); err != nil {
				return err
			}
			current, err := jobs.Get(ctx, server.ResourceGroup, *job.Name, nil)
			if isAzureNotFound(err) {
				return configurationError(
					"a legacy PostgreSQL bootstrap disappeared before its administrator could be verified")
			}
			if err != nil {
				return nonDisclosureError(err)
			}
			if err := verifyLegacyPostgresBootstrap(
				&current.Job, binding, envName, database, server, time.Now()); err != nil {
				return err
			}
			principalID := *current.Identity.PrincipalID
			if strings.EqualFold(principalID, binding.values[databaseAdministratorObjectIDEnv]) {
				return configurationError(
					"the legacy bootstrap identity conflicts with the selected persistent administrator")
			}
			adminCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), postgresCleanupTimeout)
			err = local.removeAdministrator(adminCtx, server.ServerID+"/administrators/"+principalID,
				postgresBootstrapAdministrator{
					PrincipalName: *current.Name, PrincipalType: "ServicePrincipal", TenantID: *current.Identity.TenantID,
				})
			cancel()
			if err != nil {
				return fmt.Errorf("removing legacy bootstrap administrator; Job retained for recovery: %w", err)
			}
			deleteCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), postgresCleanupTimeout)
			err = deleteLegacyPostgresJob(deleteCtx, jobs, server.ResourceGroup, *current.Name)
			cancel()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func deleteLegacyPostgresJob(ctx context.Context, jobs *armappcontainers.JobsClient, group, name string) error {
	poller, err := jobs.BeginDelete(ctx, group, name, nil)
	if err != nil && !isAzureNotFound(err) {
		return nonDisclosureError(err)
	}
	if err == nil {
		if _, err := poller.PollUntilDone(ctx, nil); err != nil && !isAzureNotFound(err) {
			return nonDisclosureError(err)
		}
	}
	_, err = jobs.Get(ctx, group, name, nil)
	if !isAzureNotFound(err) {
		return errors.Join(configurationError("legacy PostgreSQL bootstrap Job absence could not be verified"),
			nonDisclosureError(err))
	}
	return nil
}

func verifyLegacyPostgresBootstrap(
	job *armappcontainers.Job, binding *deploymentBinding, envName string, database resolvedDatabase,
	server *verifiedPostgresResources, now time.Time,
) error {
	if job == nil || job.Name == nil || !strings.HasPrefix(*job.Name, "azd-pg-") ||
		job.ID == nil || !strings.EqualFold(*job.ID, "/subscriptions/"+server.SubscriptionID+
		"/resourceGroups/"+server.ResourceGroup+"/providers/Microsoft.App/jobs/"+*job.Name) ||
		job.SystemData == nil || job.SystemData.CreatedAt == nil || job.Identity == nil ||
		job.Identity.Type == nil || *job.Identity.Type != armappcontainers.ManagedServiceIdentityTypeSystemAssigned ||
		len(job.Identity.UserAssignedIdentities) != 0 || job.Identity.PrincipalID == nil ||
		!validAdministratorObjectID(*job.Identity.PrincipalID) || job.Identity.TenantID == nil ||
		!validAdministratorObjectID(*job.Identity.TenantID) ||
		!tagEqualsPointers(job.Tags, postgresBootstrapOwnerTag, "true") ||
		!tagEqualsPointers(job.Tags, azdEnvironmentTag, envName) ||
		!tagEqualsPointers(job.Tags, postgresBootstrapServerTag, postgresBootstrapServerTagValue(server.ServerID)) {
		return configurationError("the legacy PostgreSQL bootstrap ownership could not be verified")
	}
	if now.Sub(*job.SystemData.CreatedAt) < time.Hour {
		return validationError("postgres_bootstrap_job_pending_recovery",
			"a legacy PostgreSQL bootstrap Job is still within its recovery window",
			"inspect the retained Job and retry azd provision after the one-hour recovery window")
	}
	if binding == nil || binding.app == nil || binding.app.Properties == nil || binding.app.Location == nil ||
		job.Location == nil || !strings.EqualFold(*job.Location, *binding.app.Location) || job.Properties == nil ||
		job.Properties.EnvironmentID == nil {
		return configurationError("the legacy PostgreSQL bootstrap environment could not be verified")
	}
	environmentID := binding.app.Properties.EnvironmentID
	if environmentID == nil {
		environmentID = binding.app.Properties.ManagedEnvironmentID
	}
	if environmentID == nil || !strings.EqualFold(*environmentID, *job.Properties.EnvironmentID) {
		return configurationError("the legacy PostgreSQL bootstrap belongs to another application environment")
	}
	configuration := job.Properties.Configuration
	if configuration == nil || configuration.TriggerType == nil ||
		*configuration.TriggerType != armappcontainers.TriggerTypeManual ||
		configuration.ReplicaTimeout == nil || *configuration.ReplicaTimeout != 600 ||
		configuration.ReplicaRetryLimit == nil || *configuration.ReplicaRetryLimit != 0 ||
		len(configuration.Secrets) != 0 || len(configuration.Registries) != 0 ||
		len(configuration.IdentitySettings) != 0 || configuration.EventTriggerConfig != nil ||
		configuration.ScheduleTriggerConfig != nil || configuration.ManualTriggerConfig == nil ||
		configuration.ManualTriggerConfig.Parallelism == nil || *configuration.ManualTriggerConfig.Parallelism != 1 ||
		configuration.ManualTriggerConfig.ReplicaCompletionCount == nil ||
		*configuration.ManualTriggerConfig.ReplicaCompletionCount != 1 {
		return configurationError("the legacy PostgreSQL bootstrap Job configuration changed")
	}
	template := job.Properties.Template
	if template == nil || len(template.Containers) != 1 || template.Containers[0] == nil ||
		len(template.InitContainers) != 0 || len(template.Volumes) != 0 {
		return configurationError("the legacy PostgreSQL bootstrap Job template changed")
	}
	container := template.Containers[0]
	if container.Name == nil || *container.Name != "postgres-bootstrap" ||
		container.Image == nil || len(container.Command) != 0 || len(container.Args) != 0 ||
		len(container.Probes) != 0 || len(container.VolumeMounts) != 0 || len(container.Env) != 1 ||
		container.Env[0] == nil || container.Env[0].Name == nil ||
		*container.Env[0].Name != postgresBootstrapInputEnv || container.Env[0].Value == nil ||
		container.Env[0].SecretRef != nil || container.Resources == nil || container.Resources.CPU == nil ||
		*container.Resources.CPU != 0.25 || container.Resources.Memory == nil || *container.Resources.Memory != "0.5Gi" {
		return configurationError("the legacy PostgreSQL bootstrap container changed")
	}
	imagePrefix := "mcr.microsoft.com/azure-dev/azd-spring-postgres-bootstrap@sha256:"
	if !strings.HasPrefix(*container.Image, imagePrefix) || len(*container.Image) != len(imagePrefix)+64 {
		return configurationError("the legacy PostgreSQL bootstrap image ownership could not be verified")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(*container.Image, imagePrefix)); err != nil {
		return configurationError("the legacy PostgreSQL bootstrap image digest changed")
	}
	actual, err := decodePostgresBootstrapInput(*container.Env[0].Value)
	if err != nil {
		return configurationError("the legacy PostgreSQL bootstrap input could not be verified")
	}
	expected := postgresBootstrapInput{
		Version: postgresBootstrapProtocolVersion, ServerResourceID: server.ServerID, Host: server.Host,
		DatabaseName: database.Name, ApplicationUsername: database.Username,
		ApplicationPrincipalID: server.IdentityPrincipalID, EnvironmentName: envName,
		ExpectedAdministratorName: server.AdministratorName, SessionAdministratorName: *job.Name,
	}
	if actual != expected {
		return configurationError("the legacy PostgreSQL bootstrap input ownership changed")
	}
	return nil
}
