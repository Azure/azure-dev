// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	postgresBootstrapAdministratorAPIVersion = "2024-08-01"
	postgresBootstrapOwnerTag                = "azure.springboot.postgres-bootstrap"
	postgresBootstrapServerTag               = "azure.springboot.postgres-server"
	postgresBootstrapReplicaTimeout          = int32(600)
	postgresBootstrapExecutionTimeout        = 12 * time.Minute
	postgresBootstrapPollInterval            = 5 * time.Second
	postgresBootstrapStaleThreshold          = time.Hour
	postgresBootstrapImage                   = "ghcr.io/xiaofeicao/azd-spring-postgres-bootstrap@" +
		"sha256:1600b5d5a0c5557e8f7e9ddcaedeccd7cb7020b24a89e28c4af1da591a17e0b3"
)

var errPostgresBootstrapExecutionNotFound = errors.New("PostgreSQL bootstrap execution not found")

type postgresBootstrapCloud interface {
	createJob(
		context.Context,
		string,
		string,
		armappcontainers.Job,
	) (*armappcontainers.Job, error)
	getJob(context.Context, string, string) (*armappcontainers.Job, error)
	listJobs(context.Context, string) ([]*armappcontainers.Job, error)
	deleteJob(context.Context, string, string) error
	startJob(context.Context, string, string) (string, error)
	getExecution(
		context.Context,
		string,
		string,
		string,
	) (*armappcontainers.JobExecution, error)
	createAdministrator(
		context.Context,
		string,
		postgresBootstrapAdministrator,
	) error
	deleteAdministrator(context.Context, string) error
}

type postgresBootstrapAdministrator struct {
	PrincipalName string `json:"principalName"`
	PrincipalType string `json:"principalType"`
	TenantID      string `json:"tenantId"`
}

type azurePostgresBootstrapCloud struct {
	jobs       *armappcontainers.JobsClient
	executions *armappcontainers.JobsExecutionsClient
	resources  *armresources.Client
}

func newAzurePostgresBootstrapCloud(
	subscriptionID string,
	credential azcore.TokenCredential,
) (postgresBootstrapCloud, error) {
	jobs, err := armappcontainers.NewJobsClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("creating Container Apps jobs client: %w", err)
	}
	executions, err := armappcontainers.NewJobsExecutionsClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("creating Container Apps job executions client: %w", err)
	}
	resources, err := armresources.NewClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("creating generic ARM resource client: %w", err)
	}
	return &azurePostgresBootstrapCloud{
		jobs:       jobs,
		executions: executions,
		resources:  resources,
	}, nil
}

func (c *azurePostgresBootstrapCloud) createJob(
	ctx context.Context,
	resourceGroup string,
	jobName string,
	job armappcontainers.Job,
) (*armappcontainers.Job, error) {
	poller, err := c.jobs.BeginCreateOrUpdate(ctx, resourceGroup, jobName, job, nil)
	if err != nil {
		return nil, err
	}
	response, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &response.Job, nil
}

func (c *azurePostgresBootstrapCloud) getJob(
	ctx context.Context,
	resourceGroup string,
	jobName string,
) (*armappcontainers.Job, error) {
	response, err := c.jobs.Get(ctx, resourceGroup, jobName, nil)
	if err != nil {
		return nil, err
	}
	return &response.Job, nil
}

func (c *azurePostgresBootstrapCloud) listJobs(
	ctx context.Context,
	resourceGroup string,
) ([]*armappcontainers.Job, error) {
	pager := c.jobs.NewListByResourceGroupPager(resourceGroup, nil)
	var jobs []*armappcontainers.Job
	for page := 0; pager.More() && page < 128; page++ {
		response, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, response.Value...)
	}
	if pager.More() {
		return nil, errors.New("listing retained PostgreSQL bootstrap jobs exceeded the page limit")
	}
	return jobs, nil
}

func (c *azurePostgresBootstrapCloud) deleteJob(
	ctx context.Context,
	resourceGroup string,
	jobName string,
) error {
	poller, err := c.jobs.BeginDelete(ctx, resourceGroup, jobName, nil)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

func (c *azurePostgresBootstrapCloud) startJob(
	ctx context.Context,
	resourceGroup string,
	jobName string,
) (string, error) {
	poller, err := c.jobs.BeginStart(ctx, resourceGroup, jobName, nil)
	if err != nil {
		return "", err
	}
	response, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return "", err
	}
	if response.Name == nil || *response.Name == "" {
		return "", errors.New("Container Apps did not return a job execution name")
	}
	return *response.Name, nil
}

func (c *azurePostgresBootstrapCloud) getExecution(
	ctx context.Context,
	resourceGroup string,
	jobName string,
	executionName string,
) (*armappcontainers.JobExecution, error) {
	pager := c.executions.NewListPager(resourceGroup, jobName, nil)
	for page := 0; pager.More() && page < 128; page++ {
		response, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, execution := range response.Value {
			if execution != nil && execution.Name != nil &&
				strings.EqualFold(*execution.Name, executionName) {
				return execution, nil
			}
		}
	}
	if pager.More() {
		return nil, errors.New("listing PostgreSQL bootstrap executions exceeded the page limit")
	}
	return nil, fmt.Errorf("%w: %q", errPostgresBootstrapExecutionNotFound, executionName)
}

func (c *azurePostgresBootstrapCloud) createAdministrator(
	ctx context.Context,
	resourceID string,
	administrator postgresBootstrapAdministrator,
) error {
	poller, err := c.resources.BeginCreateOrUpdateByID(
		ctx,
		resourceID,
		postgresBootstrapAdministratorAPIVersion,
		armresources.GenericResource{Properties: administrator},
		nil,
	)
	if err != nil {
		return err
	}
	response, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return err
	}
	return verifyPostgresBootstrapAdministrator(response.Properties, administrator)
}

func (c *azurePostgresBootstrapCloud) deleteAdministrator(
	ctx context.Context,
	resourceID string,
) error {
	poller, err := c.resources.BeginDeleteByID(
		ctx,
		resourceID,
		postgresBootstrapAdministratorAPIVersion,
		nil,
	)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

func isNotFound(err error) bool {
	responseErr, ok := errors.AsType[*azcore.ResponseError](err)
	return ok && responseErr.StatusCode == http.StatusNotFound
}

func verifyPostgresBootstrapAdministrator(
	value any,
	expected postgresBootstrapAdministrator,
) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encoding the PostgreSQL administrator read-back: %w", err)
	}
	var actual postgresBootstrapAdministrator
	if err := json.Unmarshal(encoded, &actual); err != nil {
		return fmt.Errorf("decoding the PostgreSQL administrator read-back: %w", err)
	}
	if actual != expected {
		return validationError(
			"postgres_bootstrap_administrator_changed",
			"the temporary PostgreSQL administrator read-back did not match the requested managed identity",
			"remove the unexpected administrator assignment before retrying",
		)
	}
	return nil
}

type postgresRemoteAccessReconciler struct {
	verifyResources func(
		context.Context,
		string,
		resolvedDatabase,
		map[string]string,
	) (*verifiedPostgresResources, azcore.TokenCredential, error)
	newCloud         func(string, azcore.TokenCredential) (postgresBootstrapCloud, error)
	image            string
	now              func() time.Time
	randomSuffix     func() (string, error)
	pollInterval     time.Duration
	executionTimeout time.Duration
	staleThreshold   time.Duration
	sleep            func(context.Context, time.Duration) error
}

func newPostgresRemoteAccessReconciler(
	verify *postgresAccessReconciler,
) *postgresRemoteAccessReconciler {
	return &postgresRemoteAccessReconciler{
		verifyResources:  verify.verifyResources,
		newCloud:         newAzurePostgresBootstrapCloud,
		image:            postgresBootstrapImage,
		now:              time.Now,
		randomSuffix:     postgresBootstrapRandomSuffix,
		pollInterval:     postgresBootstrapPollInterval,
		executionTimeout: postgresBootstrapExecutionTimeout,
		staleThreshold:   postgresBootstrapStaleThreshold,
		sleep:            sleepWithContext,
	}
}

func postgresBootstrapRandomSuffix() (string, error) {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generating PostgreSQL bootstrap job suffix: %w", err)
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

func (r *postgresRemoteAccessReconciler) Reconcile(
	ctx context.Context,
	envName string,
	database resolvedDatabase,
	values map[string]string,
) error {
	binding, ok := ctx.Value(deploymentBindingKey{}).(*deploymentBinding)
	if !ok || !binding.automatic {
		return errors.New("automatic deployment ownership is unavailable for remote PostgreSQL bootstrap")
	}
	if database.Authentication != databaseAuthenticationManagedIdentity {
		return errors.New("remote PostgreSQL bootstrap requires managed identity authentication")
	}
	if err := validatePostgresBootstrapImage(r.image); err != nil {
		return err
	}
	if len(binding.outboundIPs) == 0 {
		return configurationError("the verified runtime outbound address inventory is unavailable")
	}
	environmentID, err := containerAppEnvironmentID(binding.app)
	if err != nil {
		return err
	}
	if binding.app.Location == nil || *binding.app.Location == "" {
		return configurationError("the current Container App location is unavailable")
	}

	resources, credential, err := r.verifyResources(ctx, envName, database, values)
	if err != nil {
		return err
	}
	cloud, err := r.newCloud(resources.SubscriptionID, credential)
	if err != nil {
		return err
	}
	if err := r.recoverStaleJobs(
		ctx,
		cloud,
		binding,
		envName,
		database,
		resources,
		environmentID,
	); err != nil {
		return err
	}
	suffix, err := r.randomSuffix()
	if err != nil {
		return err
	}
	jobName := "azd-pg-" + suffix
	sessionAdministrator := jobName
	input, err := encodePostgresBootstrapInput(postgresBootstrapInput{
		Version:                   postgresBootstrapProtocolVersion,
		ServerResourceID:          resources.ServerID,
		Host:                      resources.Host,
		DatabaseName:              database.Name,
		ApplicationUsername:       database.Username,
		ApplicationPrincipalID:    resources.IdentityPrincipalID,
		EnvironmentName:           envName,
		ExpectedAdministratorName: resources.AdministratorName,
		SessionAdministratorName:  sessionAdministrator,
	})
	if err != nil {
		return err
	}
	job := postgresBootstrapJob(
		*binding.app.Location,
		environmentID,
		envName,
		resources.ServerID,
		r.image,
		input,
	)
	if _, err := cloud.createJob(ctx, resources.ResourceGroup, jobName, job); err != nil {
		return fmt.Errorf("creating the PostgreSQL bootstrap job: %w", err)
	}
	created, err := cloud.getJob(ctx, resources.ResourceGroup, jobName)
	if err != nil {
		return errors.Join(
			fmt.Errorf("reading back the PostgreSQL bootstrap job: %w", err),
			r.deleteJob(ctx, cloud, resources.ResourceGroup, jobName),
		)
	}
	if err := verifyPostgresBootstrapJob(
		created,
		jobName,
		job,
		binding.outboundIPs,
	); err != nil {
		return errors.Join(err, r.deleteJob(ctx, cloud, resources.ResourceGroup, jobName))
	}

	principalID := strings.ToLower(*created.Identity.PrincipalID)
	tenantID := strings.ToLower(*created.Identity.TenantID)
	administratorID := resources.ServerID + "/administrators/" + principalID
	administrator := postgresBootstrapAdministrator{
		PrincipalName: sessionAdministrator,
		PrincipalType: "ServicePrincipal",
		TenantID:      tenantID,
	}
	if err := cloud.createAdministrator(ctx, administratorID, administrator); err != nil {
		return errors.Join(
			fmt.Errorf("creating the temporary PostgreSQL bootstrap administrator: %w", err),
			r.cleanupAdministratorAndJob(
				ctx,
				cloud,
				administratorID,
				resources.ResourceGroup,
				jobName,
			),
		)
	}

	executionName, err := cloud.startJob(ctx, resources.ResourceGroup, jobName)
	if err != nil {
		return errors.Join(
			fmt.Errorf("starting the PostgreSQL bootstrap job: %w", err),
			r.cleanupAdministratorAndJob(
				ctx,
				cloud,
				administratorID,
				resources.ResourceGroup,
				jobName,
			),
		)
	}
	executionErr := r.waitForExecution(
		ctx,
		cloud,
		resources.ResourceGroup,
		jobName,
		executionName,
	)
	if executionErr != nil {
		cleanupErr := r.deleteAdministrator(ctx, cloud, administratorID)
		if cleanupErr != nil {
			return errors.Join(
				executionErr,
				fmt.Errorf(
					"deleting the temporary PostgreSQL administrator; the job was retained for recovery: %w",
					cleanupErr,
				),
			)
		}
		return fmt.Errorf(
			"%w; the unprivileged job was retained for inspection: "+
				"az containerapp job logs show --name %s --resource-group \"%s\" --execution %s",
			executionErr,
			jobName,
			resources.ResourceGroup,
			executionName,
		)
	}
	if err := r.cleanupAdministratorAndJob(
		ctx,
		cloud,
		administratorID,
		resources.ResourceGroup,
		jobName,
	); err != nil {
		return err
	}
	return nil
}

func (r *postgresRemoteAccessReconciler) waitForExecution(
	ctx context.Context,
	cloud postgresBootstrapCloud,
	resourceGroup string,
	jobName string,
	executionName string,
) error {
	deadline := r.now().Add(r.executionTimeout)
	for {
		if !r.now().Before(deadline) {
			return fmt.Errorf(
				"timed out waiting for Container Apps job execution %q",
				executionName,
			)
		}
		execution, err := cloud.getExecution(ctx, resourceGroup, jobName, executionName)
		if errors.Is(err, errPostgresBootstrapExecutionNotFound) {
			if err := r.sleep(ctx, r.pollInterval); err != nil {
				return fmt.Errorf("waiting for the PostgreSQL bootstrap job execution: %w", err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("reading the PostgreSQL bootstrap job execution: %w", err)
		}
		if execution.Properties == nil || execution.Properties.Status == nil {
			return errors.New("the PostgreSQL bootstrap job execution did not report a status")
		}
		switch *execution.Properties.Status {
		case armappcontainers.JobExecutionRunningStateSucceeded:
			return nil
		case armappcontainers.JobExecutionRunningStateFailed,
			armappcontainers.JobExecutionRunningStateStopped,
			armappcontainers.JobExecutionRunningStateDegraded:
			return fmt.Errorf(
				"the PostgreSQL bootstrap job execution ended with status %q",
				*execution.Properties.Status,
			)
		case armappcontainers.JobExecutionRunningStateProcessing,
			armappcontainers.JobExecutionRunningStateRunning,
			armappcontainers.JobExecutionRunningStateUnknown:
		default:
			return fmt.Errorf(
				"the PostgreSQL bootstrap job execution reported unsupported status %q",
				*execution.Properties.Status,
			)
		}
		if err := r.sleep(ctx, r.pollInterval); err != nil {
			return fmt.Errorf("waiting for the PostgreSQL bootstrap job execution: %w", err)
		}
	}
}

func (r *postgresRemoteAccessReconciler) recoverStaleJobs(
	ctx context.Context,
	cloud postgresBootstrapCloud,
	binding *deploymentBinding,
	envName string,
	database resolvedDatabase,
	resources *verifiedPostgresResources,
	environmentID string,
) error {
	jobs, err := cloud.listJobs(ctx, resources.ResourceGroup)
	if err != nil {
		return fmt.Errorf("listing retained PostgreSQL bootstrap jobs: %w", err)
	}
	serverTag := postgresBootstrapServerTagValue(resources.ServerID)
	for _, job := range jobs {
		if job == nil || !tagEqualsPointers(job.Tags, postgresBootstrapOwnerTag, "true") ||
			!tagEqualsPointers(job.Tags, azdEnvironmentTag, envName) ||
			!tagEqualsPointers(job.Tags, postgresBootstrapServerTag, serverTag) {
			continue
		}
		if job.Name == nil || !strings.HasPrefix(*job.Name, "azd-pg-") {
			return configurationError("a retained PostgreSQL bootstrap job has invalid ownership metadata")
		}
		if job.SystemData == nil || job.SystemData.CreatedAt == nil {
			return configurationError("a retained PostgreSQL bootstrap job has no creation timestamp")
		}
		if r.now().Sub(*job.SystemData.CreatedAt) < r.staleThreshold {
			return validationError(
				"postgres_bootstrap_job_pending_recovery",
				fmt.Sprintf("PostgreSQL bootstrap job %q is still within its recovery window", *job.Name),
				"inspect the retained job logs and retry after the recovery window",
			)
		}
		input, err := encodePostgresBootstrapInput(postgresBootstrapInput{
			Version:                   postgresBootstrapProtocolVersion,
			ServerResourceID:          resources.ServerID,
			Host:                      resources.Host,
			DatabaseName:              database.Name,
			ApplicationUsername:       database.Username,
			ApplicationPrincipalID:    resources.IdentityPrincipalID,
			EnvironmentName:           envName,
			ExpectedAdministratorName: resources.AdministratorName,
			SessionAdministratorName:  *job.Name,
		})
		if err != nil {
			return err
		}
		expected := postgresBootstrapJob(
			*binding.app.Location,
			environmentID,
			envName,
			resources.ServerID,
			r.image,
			input,
		)
		if err := verifyPostgresBootstrapJobShape(job, *job.Name, expected); err != nil {
			return fmt.Errorf("verifying retained PostgreSQL bootstrap job %q: %w", *job.Name, err)
		}
		principalID := strings.ToLower(*job.Identity.PrincipalID)
		administratorID := resources.ServerID + "/administrators/" + principalID
		if err := r.cleanupAdministratorAndJob(
			ctx,
			cloud,
			administratorID,
			resources.ResourceGroup,
			*job.Name,
		); err != nil {
			return fmt.Errorf("recovering retained PostgreSQL bootstrap job %q: %w", *job.Name, err)
		}
	}
	return nil
}

func (r *postgresRemoteAccessReconciler) cleanupAdministratorAndJob(
	ctx context.Context,
	cloud postgresBootstrapCloud,
	administratorID string,
	resourceGroup string,
	jobName string,
) error {
	if err := r.deleteAdministrator(ctx, cloud, administratorID); err != nil {
		return fmt.Errorf(
			"deleting the temporary PostgreSQL administrator; the job was retained for recovery: %w",
			err,
		)
	}
	if err := r.deleteJob(ctx, cloud, resourceGroup, jobName); err != nil {
		return fmt.Errorf("deleting the PostgreSQL bootstrap job: %w", err)
	}
	return nil
}

func (r *postgresRemoteAccessReconciler) deleteAdministrator(
	ctx context.Context,
	cloud postgresBootstrapCloud,
	administratorID string,
) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), postgresCleanupTimeout)
	defer cancel()
	return cloud.deleteAdministrator(cleanupCtx, administratorID)
}

func (r *postgresRemoteAccessReconciler) deleteJob(
	ctx context.Context,
	cloud postgresBootstrapCloud,
	resourceGroup string,
	jobName string,
) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), postgresCleanupTimeout)
	defer cancel()
	return cloud.deleteJob(cleanupCtx, resourceGroup, jobName)
}

func postgresBootstrapJob(
	location string,
	environmentID string,
	envName string,
	serverID string,
	image string,
	input string,
) armappcontainers.Job {
	identityType := armappcontainers.ManagedServiceIdentityTypeSystemAssigned
	triggerType := armappcontainers.TriggerTypeManual
	zero := int32(0)
	one := int32(1)
	return armappcontainers.Job{
		Location: new(location),
		Identity: &armappcontainers.ManagedServiceIdentity{
			Type: &identityType,
		},
		Tags: map[string]*string{
			azdEnvironmentTag:          new(envName),
			postgresBootstrapOwnerTag:  new("true"),
			postgresBootstrapServerTag: new(postgresBootstrapServerTagValue(serverID)),
		},
		Properties: &armappcontainers.JobProperties{
			EnvironmentID: new(environmentID),
			Configuration: &armappcontainers.JobConfiguration{
				TriggerType:       &triggerType,
				ReplicaTimeout:    new(postgresBootstrapReplicaTimeout),
				ReplicaRetryLimit: &zero,
				ManualTriggerConfig: &armappcontainers.JobConfigurationManualTriggerConfig{
					Parallelism:            &one,
					ReplicaCompletionCount: &one,
				},
			},
			Template: &armappcontainers.JobTemplate{
				Containers: []*armappcontainers.Container{{
					Name:  new("postgres-bootstrap"),
					Image: new(image),
					Env: []*armappcontainers.EnvironmentVar{{
						Name:  new(postgresBootstrapInputEnv),
						Value: new(input),
					}},
					Resources: &armappcontainers.ContainerResources{
						CPU:    new(0.25),
						Memory: new("0.5Gi"),
					},
				}},
			},
		},
	}
}

func postgresBootstrapServerTagValue(serverID string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(serverID)))
	return hex.EncodeToString(sum[:])
}

func validatePostgresBootstrapImage(image string) error {
	const separator = "@sha256:"
	index := strings.LastIndex(image, separator)
	if index <= 0 || len(image[index+len(separator):]) != 64 {
		return errors.New("the PostgreSQL bootstrap image is not configured with a SHA-256 digest")
	}
	if _, err := hex.DecodeString(image[index+len(separator):]); err != nil {
		return errors.New("the PostgreSQL bootstrap image has an invalid SHA-256 digest")
	}
	return nil
}

func containerAppEnvironmentID(app *armappcontainers.ContainerApp) (string, error) {
	if app == nil || app.Properties == nil {
		return "", configurationError("the current Container App environment is unavailable")
	}
	environmentID := app.Properties.EnvironmentID
	if environmentID == nil {
		environmentID = app.Properties.ManagedEnvironmentID
	}
	if environmentID == nil || *environmentID == "" {
		return "", configurationError("the current Container App environment is unavailable")
	}
	return *environmentID, nil
}

func verifyPostgresBootstrapJob(
	actual *armappcontainers.Job,
	jobName string,
	expected armappcontainers.Job,
	expectedOutboundIPs []string,
) error {
	if err := verifyPostgresBootstrapJobShape(actual, jobName, expected); err != nil {
		return err
	}
	actualOutboundIPs, err := normalizeOutboundIPs(actual.Properties.OutboundIPAddresses)
	if err != nil {
		return err
	}
	if !slices.Equal(actualOutboundIPs, expectedOutboundIPs) {
		return configurationError(
			"the PostgreSQL bootstrap job outbound addresses are not covered by the verified runtime firewall rules",
		)
	}
	return nil
}

func verifyPostgresBootstrapJobShape(
	actual *armappcontainers.Job,
	jobName string,
	expected armappcontainers.Job,
) error {
	if actual == nil || actual.Name == nil || !strings.EqualFold(*actual.Name, jobName) ||
		actual.Location == nil || expected.Location == nil ||
		!strings.EqualFold(*actual.Location, *expected.Location) ||
		actual.Identity == nil || actual.Identity.Type == nil ||
		*actual.Identity.Type != armappcontainers.ManagedServiceIdentityTypeSystemAssigned ||
		len(actual.Identity.UserAssignedIdentities) != 0 ||
		actual.Identity.PrincipalID == nil ||
		!databasePrincipalIDPattern.MatchString(*actual.Identity.PrincipalID) ||
		actual.Identity.TenantID == nil ||
		!databasePrincipalIDPattern.MatchString(*actual.Identity.TenantID) {
		return configurationError("the PostgreSQL bootstrap job identity or resource metadata changed")
	}
	for key, expectedValue := range expected.Tags {
		actualValue := actual.Tags[key]
		if actualValue == nil || expectedValue == nil || *actualValue != *expectedValue {
			return configurationError("the PostgreSQL bootstrap job ownership tags changed")
		}
	}
	if actual.Properties == nil || expected.Properties == nil ||
		actual.Properties.EnvironmentID == nil || expected.Properties.EnvironmentID == nil ||
		!strings.EqualFold(*actual.Properties.EnvironmentID, *expected.Properties.EnvironmentID) ||
		actual.Properties.Configuration == nil ||
		actual.Properties.Configuration.TriggerType == nil ||
		*actual.Properties.Configuration.TriggerType != armappcontainers.TriggerTypeManual ||
		actual.Properties.Configuration.ReplicaTimeout == nil ||
		*actual.Properties.Configuration.ReplicaTimeout != postgresBootstrapReplicaTimeout ||
		actual.Properties.Configuration.ReplicaRetryLimit == nil ||
		*actual.Properties.Configuration.ReplicaRetryLimit != 0 ||
		len(actual.Properties.Configuration.Secrets) != 0 ||
		len(actual.Properties.Configuration.Registries) != 0 ||
		len(actual.Properties.Configuration.IdentitySettings) != 0 ||
		actual.Properties.Configuration.EventTriggerConfig != nil ||
		actual.Properties.Configuration.ScheduleTriggerConfig != nil ||
		actual.Properties.Configuration.ManualTriggerConfig == nil ||
		actual.Properties.Configuration.ManualTriggerConfig.Parallelism == nil ||
		*actual.Properties.Configuration.ManualTriggerConfig.Parallelism != 1 ||
		actual.Properties.Configuration.ManualTriggerConfig.ReplicaCompletionCount == nil ||
		*actual.Properties.Configuration.ManualTriggerConfig.ReplicaCompletionCount != 1 {
		return configurationError("the PostgreSQL bootstrap job configuration changed")
	}
	if !samePostgresBootstrapTemplate(actual.Properties.Template, expected.Properties.Template) {
		return configurationError("the PostgreSQL bootstrap job template changed")
	}
	return nil
}

func samePostgresBootstrapTemplate(
	actual *armappcontainers.JobTemplate,
	expected *armappcontainers.JobTemplate,
) bool {
	if actual == nil || expected == nil ||
		len(actual.Containers) != 1 || len(expected.Containers) != 1 ||
		len(actual.InitContainers) != 0 || len(actual.Volumes) != 0 {
		return false
	}
	actualContainer := actual.Containers[0]
	expectedContainer := expected.Containers[0]
	if actualContainer == nil || expectedContainer == nil ||
		actualContainer.Name == nil || expectedContainer.Name == nil ||
		*actualContainer.Name != *expectedContainer.Name ||
		actualContainer.Image == nil || expectedContainer.Image == nil ||
		*actualContainer.Image != *expectedContainer.Image ||
		len(actualContainer.Command) != 0 || len(actualContainer.Args) != 0 ||
		len(actualContainer.Probes) != 0 || len(actualContainer.VolumeMounts) != 0 ||
		len(actualContainer.Env) != 1 || len(expectedContainer.Env) != 1 ||
		actualContainer.Resources == nil || expectedContainer.Resources == nil ||
		actualContainer.Resources.CPU == nil || expectedContainer.Resources.CPU == nil ||
		*actualContainer.Resources.CPU != *expectedContainer.Resources.CPU ||
		actualContainer.Resources.Memory == nil || expectedContainer.Resources.Memory == nil ||
		*actualContainer.Resources.Memory != *expectedContainer.Resources.Memory {
		return false
	}
	actualEnv := actualContainer.Env[0]
	expectedEnv := expectedContainer.Env[0]
	return actualEnv != nil && expectedEnv != nil &&
		actualEnv.Name != nil && expectedEnv.Name != nil &&
		*actualEnv.Name == *expectedEnv.Name &&
		actualEnv.Value != nil && expectedEnv.Value != nil &&
		*actualEnv.Value == *expectedEnv.Value &&
		actualEnv.SecretRef == nil
}

func newPostgresAccessDispatcher(client *azdext.AzdClient) func(
	context.Context,
	string,
	resolvedDatabase,
	map[string]string,
) error {
	local := newPostgresAccessReconciler(client)
	remote := newPostgresRemoteAccessReconciler(local)
	return func(
		ctx context.Context,
		envName string,
		database resolvedDatabase,
		values map[string]string,
	) error {
		binding, ok := ctx.Value(deploymentBindingKey{}).(*deploymentBinding)
		if ok && binding.automatic &&
			database.Authentication == databaseAuthenticationManagedIdentity {
			return remote.Reconcile(ctx, envName, database, values)
		}
		return local.Reconcile(ctx, envName, database, values)
	}
}
