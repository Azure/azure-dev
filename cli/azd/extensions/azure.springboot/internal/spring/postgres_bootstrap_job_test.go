// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/stretchr/testify/require"
)

type postgresBootstrapTestCredential struct{}

func (postgresBootstrapTestCredential) GetToken(
	context.Context,
	policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	return azcore.AccessToken{}, nil
}

type postgresBootstrapTestCloud struct {
	job              *armappcontainers.Job
	jobs             []*armappcontainers.Job
	outboundIPs      []*string
	executionStatus  armappcontainers.JobExecutionRunningState
	executionMissing int
	deleteAdminErr   error
	operations       []string
	administratorID  string
	administrator    postgresBootstrapAdministrator
}

func (c *postgresBootstrapTestCloud) createJob(
	_ context.Context,
	_ string,
	jobName string,
	job armappcontainers.Job,
) (*armappcontainers.Job, error) {
	c.operations = append(c.operations, "create-job")
	job.Name = new(jobName)
	job.ID = new("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/jobs/" + jobName)
	job.Identity.PrincipalID = new("00000000-0000-0000-0000-000000000003")
	job.Identity.TenantID = new("00000000-0000-0000-0000-000000000004")
	job.Properties.OutboundIPAddresses = c.outboundIPs
	c.job = &job
	return c.job, nil
}

func (c *postgresBootstrapTestCloud) getJob(
	context.Context,
	string,
	string,
) (*armappcontainers.Job, error) {
	c.operations = append(c.operations, "get-job")
	return c.job, nil
}

func (c *postgresBootstrapTestCloud) listJobs(
	context.Context,
	string,
) ([]*armappcontainers.Job, error) {
	c.operations = append(c.operations, "list-jobs")
	return c.jobs, nil
}

func (c *postgresBootstrapTestCloud) deleteJob(context.Context, string, string) error {
	c.operations = append(c.operations, "delete-job")
	return nil
}

func (c *postgresBootstrapTestCloud) startJob(context.Context, string, string) (string, error) {
	c.operations = append(c.operations, "start-job")
	return "execution-1", nil
}

func (c *postgresBootstrapTestCloud) getExecution(
	context.Context,
	string,
	string,
	string,
) (*armappcontainers.JobExecution, error) {
	c.operations = append(c.operations, "get-execution")
	if c.executionMissing > 0 {
		c.executionMissing--
		return nil, errPostgresBootstrapExecutionNotFound
	}
	return &armappcontainers.JobExecution{
		Name: new("execution-1"),
		Properties: &armappcontainers.JobExecutionProperties{
			Status: new(c.executionStatus),
		},
	}, nil
}

func (c *postgresBootstrapTestCloud) createAdministrator(
	_ context.Context,
	resourceID string,
	administrator postgresBootstrapAdministrator,
) error {
	c.operations = append(c.operations, "create-administrator")
	c.administratorID = resourceID
	c.administrator = administrator
	return nil
}

func (c *postgresBootstrapTestCloud) deleteAdministrator(
	context.Context,
	string,
) error {
	c.operations = append(c.operations, "delete-administrator")
	return c.deleteAdminErr
}

func TestPostgresRemoteAccessReconcilerSuccessCleansAdministratorBeforeJob(t *testing.T) {
	cloud := &postgresBootstrapTestCloud{
		outboundIPs:     []*string{new("1.2.3.4")},
		executionStatus: armappcontainers.JobExecutionRunningStateSucceeded,
	}
	reconciler := testPostgresRemoteAccessReconciler(cloud)

	err := reconciler.Reconcile(
		testPostgresRemoteContext(),
		"dev",
		testManagedIdentityDatabase(),
		map[string]string{},
	)
	require.NoError(t, err)
	require.Equal(t, []string{
		"list-jobs",
		"create-job",
		"get-job",
		"create-administrator",
		"start-job",
		"get-execution",
		"delete-administrator",
		"delete-job",
	}, cloud.operations)
	require.True(t, strings.HasSuffix(
		cloud.administratorID,
		"/administrators/00000000-0000-0000-0000-000000000003",
	))
	require.Equal(t, "azd-pg-0123456789abcdef", cloud.administrator.PrincipalName)
	require.Equal(t, "ServicePrincipal", cloud.administrator.PrincipalType)
}

func TestPostgresRemoteAccessReconcilerFailureRetainsUnprivilegedJob(t *testing.T) {
	cloud := &postgresBootstrapTestCloud{
		outboundIPs:     []*string{new("1.2.3.4")},
		executionStatus: armappcontainers.JobExecutionRunningStateFailed,
	}
	reconciler := testPostgresRemoteAccessReconciler(cloud)

	err := reconciler.Reconcile(
		testPostgresRemoteContext(),
		"dev",
		testManagedIdentityDatabase(),
		map[string]string{},
	)
	require.ErrorContains(t, err, "retained for inspection")
	require.Contains(t, err.Error(), "az containerapp job logs show")
	require.Equal(t, "delete-administrator", cloud.operations[len(cloud.operations)-1])
	require.NotContains(t, cloud.operations, "delete-job")
}

func TestPostgresRemoteAccessReconcilerWaitsForExecutionVisibility(t *testing.T) {
	cloud := &postgresBootstrapTestCloud{
		outboundIPs:      []*string{new("1.2.3.4")},
		executionStatus:  armappcontainers.JobExecutionRunningStateSucceeded,
		executionMissing: 1,
	}
	reconciler := testPostgresRemoteAccessReconciler(cloud)

	err := reconciler.Reconcile(
		testPostgresRemoteContext(),
		"dev",
		testManagedIdentityDatabase(),
		map[string]string{},
	)
	require.NoError(t, err)
	executionReads := 0
	for _, operation := range cloud.operations {
		if operation == "get-execution" {
			executionReads++
		}
	}
	require.Equal(t, 2, executionReads)
}

func TestPostgresRemoteAccessReconcilerPreservesIdentityWhenAdministratorCleanupFails(t *testing.T) {
	cloud := &postgresBootstrapTestCloud{
		outboundIPs:     []*string{new("1.2.3.4")},
		executionStatus: armappcontainers.JobExecutionRunningStateSucceeded,
		deleteAdminErr:  errors.New("delete denied"),
	}
	reconciler := testPostgresRemoteAccessReconciler(cloud)

	err := reconciler.Reconcile(
		testPostgresRemoteContext(),
		"dev",
		testManagedIdentityDatabase(),
		map[string]string{},
	)
	require.ErrorContains(t, err, "job was retained for recovery")
	require.NotContains(t, cloud.operations, "delete-job")
}

func TestPostgresRemoteAccessReconcilerRejectsJobEgressBeforeAdministrator(t *testing.T) {
	cloud := &postgresBootstrapTestCloud{
		outboundIPs:     []*string{new("5.6.7.8")},
		executionStatus: armappcontainers.JobExecutionRunningStateSucceeded,
	}
	reconciler := testPostgresRemoteAccessReconciler(cloud)

	err := reconciler.Reconcile(
		testPostgresRemoteContext(),
		"dev",
		testManagedIdentityDatabase(),
		map[string]string{},
	)
	require.ErrorContains(t, err, "not covered by the verified runtime firewall rules")
	require.Equal(t, []string{"list-jobs", "create-job", "get-job", "delete-job"}, cloud.operations)
	require.NotContains(t, cloud.operations, "create-administrator")
}

func TestPostgresRemoteAccessReconcilerRecoversOnlyVerifiedStaleJob(t *testing.T) {
	now := time.Now().UTC()
	resources := testVerifiedPostgresResources()
	database := testManagedIdentityDatabase()
	input, err := encodePostgresBootstrapInput(postgresBootstrapInput{
		Version:                   postgresBootstrapProtocolVersion,
		ServerResourceID:          resources.ServerID,
		Host:                      resources.Host,
		DatabaseName:              database.Name,
		ApplicationUsername:       database.Username,
		ApplicationPrincipalID:    resources.IdentityPrincipalID,
		EnvironmentName:           "dev",
		ExpectedAdministratorName: resources.AdministratorName,
		SessionAdministratorName:  "azd-pg-stale",
	})
	require.NoError(t, err)
	image := "ghcr.io/example/bootstrap@sha256:" + strings.Repeat("a", 64)
	environmentID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/managedEnvironments/env"
	job := postgresBootstrapJob(
		"westus2",
		environmentID,
		"dev",
		resources.ServerID,
		image,
		input,
	)
	job.Name = new("azd-pg-stale")
	job.Identity.PrincipalID = new("00000000-0000-0000-0000-000000000003")
	job.Identity.TenantID = new("00000000-0000-0000-0000-000000000004")
	job.SystemData = &armappcontainers.SystemData{CreatedAt: new(now.Add(-2 * time.Hour))}

	cloud := &postgresBootstrapTestCloud{jobs: []*armappcontainers.Job{&job}}
	reconciler := testPostgresRemoteAccessReconciler(cloud)
	reconciler.image = image
	reconciler.now = func() time.Time { return now }
	binding := testPostgresRemoteContext().Value(deploymentBindingKey{}).(*deploymentBinding)

	err = reconciler.recoverStaleJobs(
		t.Context(),
		cloud,
		binding,
		"dev",
		database,
		resources,
		environmentID,
	)
	require.NoError(t, err)
	require.Equal(t, []string{"list-jobs", "delete-administrator", "delete-job"}, cloud.operations)
}

func TestPostgresRemoteAccessReconcilerBlocksRecentOwnedJob(t *testing.T) {
	now := time.Now().UTC()
	resources := testVerifiedPostgresResources()
	database := testManagedIdentityDatabase()
	input, err := encodePostgresBootstrapInput(postgresBootstrapInput{
		Version:                   postgresBootstrapProtocolVersion,
		ServerResourceID:          resources.ServerID,
		Host:                      resources.Host,
		DatabaseName:              database.Name,
		ApplicationUsername:       database.Username,
		ApplicationPrincipalID:    resources.IdentityPrincipalID,
		EnvironmentName:           "dev",
		ExpectedAdministratorName: resources.AdministratorName,
		SessionAdministratorName:  "azd-pg-recent",
	})
	require.NoError(t, err)
	image := "ghcr.io/example/bootstrap@sha256:" + strings.Repeat("a", 64)
	environmentID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/managedEnvironments/env"
	job := postgresBootstrapJob(
		"westus2",
		environmentID,
		"dev",
		resources.ServerID,
		image,
		input,
	)
	job.Name = new("azd-pg-recent")
	job.SystemData = &armappcontainers.SystemData{CreatedAt: new(now.Add(-time.Minute))}

	cloud := &postgresBootstrapTestCloud{jobs: []*armappcontainers.Job{&job}}
	reconciler := testPostgresRemoteAccessReconciler(cloud)
	reconciler.image = image
	reconciler.now = func() time.Time { return now }
	binding := testPostgresRemoteContext().Value(deploymentBindingKey{}).(*deploymentBinding)

	err = reconciler.recoverStaleJobs(
		t.Context(),
		cloud,
		binding,
		"dev",
		database,
		resources,
		environmentID,
	)
	require.ErrorContains(t, err, "recovery window")
	require.Equal(t, []string{"list-jobs"}, cloud.operations)
}

func testPostgresRemoteAccessReconciler(
	cloud *postgresBootstrapTestCloud,
) *postgresRemoteAccessReconciler {
	return &postgresRemoteAccessReconciler{
		verifyResources: func(
			context.Context,
			string,
			resolvedDatabase,
			map[string]string,
		) (*verifiedPostgresResources, azcore.TokenCredential, error) {
			return testVerifiedPostgresResources(), postgresBootstrapTestCredential{}, nil
		},
		newCloud: func(string, azcore.TokenCredential) (postgresBootstrapCloud, error) {
			return cloud, nil
		},
		image:            "ghcr.io/example/bootstrap@sha256:" + strings.Repeat("a", 64),
		now:              time.Now,
		randomSuffix:     func() (string, error) { return "0123456789abcdef", nil },
		pollInterval:     time.Millisecond,
		executionTimeout: time.Minute,
		staleThreshold:   time.Hour,
		sleep:            func(context.Context, time.Duration) error { return nil },
	}
}

func testVerifiedPostgresResources() *verifiedPostgresResources {
	return &verifiedPostgresResources{
		SubscriptionID:      "sub",
		ResourceGroup:       "rg",
		ServerName:          "server",
		ServerID:            testManagedIdentityDatabase().ServerResourceID,
		Host:                "server.postgres.database.azure.com",
		AdministratorName:   "developer",
		IdentityPrincipalID: "00000000-0000-0000-0000-000000000002",
	}
}

func testPostgresRemoteContext() context.Context {
	environmentID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/managedEnvironments/env"
	binding := &deploymentBinding{
		automatic:   true,
		outboundIPs: []string{"1.2.3.4"},
		app: &armappcontainers.ContainerApp{
			Location: new("westus2"),
			Properties: &armappcontainers.ContainerAppProperties{
				EnvironmentID: new(environmentID),
			},
		},
	}
	return context.WithValue(context.Background(), deploymentBindingKey{}, binding)
}

func testManagedIdentityDatabase() resolvedDatabase {
	return resolvedDatabase{
		Type: databasePostgreSQL,
		ServerResourceID: "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.DBforPostgreSQL/flexibleServers/server",
		Name:           "petclinic",
		Authentication: databaseAuthenticationManagedIdentity,
		Username:       "petclinic_app",
	}
}
