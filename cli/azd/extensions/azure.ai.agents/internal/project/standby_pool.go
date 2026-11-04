// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore BYOAKS

package project

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/featureflags"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// RequireStandbyPoolHosting permits explicit standby targets only for opted-in
// agents bound through uses to a single existing-AKS project declaration or an
// existing project endpoint, whose live hosting must be verified before writes.
func RequireStandbyPoolHosting(
	endpoint *agent_yaml.AgentEndpoint,
	service *azdext.ServiceConfig,
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
) error {
	if endpoint == nil || endpoint.StandbyPool == nil || endpoint.StandbyPool.Size == nil {
		return nil
	}
	if err := featureflags.Require(featureflags.BYOAKS); err != nil {
		return err
	}
	projectService, err := standbyPoolProject(service, services)
	if err != nil {
		return err
	}
	props, err := ResolveServiceConfigProps(projectService, projectRoot)
	if err != nil {
		return err
	}
	fields := props.AsMap()
	endpointURL, _ := fields["endpoint"].(string)
	if fields["agentHosting"] == nil && strings.TrimSpace(endpointURL) != "" {
		return nil
	}
	hosting, _ := fields["agentHosting"].(map[string]any)
	clusterID, _ := hosting["clusterResourceId"].(string)
	if strings.TrimSpace(clusterID) == "" || fields["endpoint"] != nil {
		return standbyPoolHostingError(service.GetName())
	}
	return nil
}

func standbyPoolProject(
	service *azdext.ServiceConfig, services map[string]*azdext.ServiceConfig,
) (*azdext.ServiceConfig, error) {
	var projectService *azdext.ServiceConfig
	for _, name := range service.GetUses() {
		dependency := services[name]
		if dependency.GetHost() != foundryProjectHost {
			continue
		}
		if projectService != nil && projectService != dependency {
			return nil, standbyPoolHostingError(service.GetName())
		}
		projectService = dependency
	}
	if projectService == nil {
		return nil, standbyPoolHostingError(service.GetName())
	}
	return projectService, nil
}

// VerifyStandbyPoolExistingHosting verifies adopted project identity and its
// account's managed-cluster hosting through read-only ARM calls before agent writes.
func VerifyStandbyPoolExistingHosting(
	ctx context.Context, client *azdext.AzdClient, endpoint *agent_yaml.AgentEndpoint,
	service *azdext.ServiceConfig, services map[string]*azdext.ServiceConfig,
	projectRoot, runtimeEndpoint, envName string,
) error {
	if endpoint == nil || endpoint.StandbyPool == nil || endpoint.StandbyPool.Size == nil {
		return nil
	}
	if err := RequireStandbyPoolHosting(endpoint, service, services, projectRoot); err != nil {
		return err
	}
	projectService, err := standbyPoolProject(service, services)
	if err != nil {
		return err
	}
	props, err := ResolveServiceConfigProps(projectService, projectRoot)
	if err != nil {
		return err
	}
	declaredEndpoint, _ := props.AsMap()["endpoint"].(string)
	if declaredEndpoint == "" {
		return nil
	}
	values, err := client.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{Name: envName})
	if err != nil {
		return exterrors.FromHost(err, "get environment", "validating standby pool hosting")
	}
	env := map[string]string{}
	for _, entry := range values.GetKeyValues() {
		env[entry.Key] = entry.Value
	}
	declaredEndpoint, err = ExpandEnv(declaredEndpoint, func(key string) string { return env[key] })
	if err != nil {
		return err
	}
	if !sameStandbyProjectEndpoint(declaredEndpoint, runtimeEndpoint) {
		return standbyPoolHostingError(service.GetName())
	}
	info, err := storageProjectInfo(env["AZURE_AI_PROJECT_ID"])
	if err != nil {
		return exterrors.Validation(exterrors.CodeInvalidServiceConfig,
			"standby pool verification requires a valid AZURE_AI_PROJECT_ID",
			"set AZURE_AI_PROJECT_ID to the ARM resource ID of the configured project")
	}
	tenant, err := client.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{SubscriptionId: info.SubscriptionID})
	if err != nil {
		return exterrors.FromHost(err, "lookup tenant", "validating standby pool hosting")
	}
	if tenant.GetTenantId() == "" {
		return errors.New("project user access tenant is unavailable for standby pool verification")
	}
	credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID: tenant.GetTenantId(),
	})
	if err != nil {
		return err
	}
	if err := verifyStandbyPoolARMHosting(ctx, credential, azure.NewArmClientOptions(),
		info.ProjectScope, runtimeEndpoint); err != nil {
		return fmt.Errorf("verify existing project BYO AKS hosting for standby pool: %w", err)
	}
	return nil
}

func sameStandbyProjectEndpoint(left, right string) bool {
	return strings.EqualFold(strings.TrimRight(left, "/"), strings.TrimRight(right, "/"))
}

func verifyStandbyPoolARMHosting(
	ctx context.Context, credential azcore.TokenCredential, options *arm.ClientOptions,
	projectID, endpoint string,
) error {
	info, err := storageProjectInfo(projectID)
	if err != nil {
		return err
	}
	resources, err := armresources.NewClient(info.SubscriptionID, credential, options)
	if err != nil {
		return err
	}
	projectResponse, err := resources.GetByID(ctx, info.ProjectScope, "2025-06-01", nil)
	if err != nil {
		return fmt.Errorf("read project: %w", err)
	}
	properties, _ := projectResponse.Properties.(map[string]any)
	endpoints, _ := properties["endpoints"].(map[string]any)
	matches := false
	for _, value := range endpoints {
		if value, ok := value.(string); ok && sameStandbyProjectEndpoint(value, endpoint) {
			matches = true
		}
	}
	if !matches {
		return errors.New("AZURE_AI_PROJECT_ID does not identify the actual deployment project endpoint")
	}
	accountResponse, err := resources.GetByID(ctx, info.AccountScope, "2026-07-15-preview", nil)
	if err != nil {
		return fmt.Errorf("read account hosting configuration: %w", err)
	}
	properties, _ = accountResponse.Properties.(map[string]any)
	configurations, _ := properties["agentHostingConfigurations"].([]any)
	for _, configuration := range configurations {
		config, _ := configuration.(map[string]any)
		hostingType, _ := config["hostingType"].(string)
		clusterID, _ := config["clusterResourceId"].(string)
		cluster, parseErr := arm.ParseResourceID(clusterID)
		if strings.EqualFold(hostingType, "ManagedCluster") && parseErr == nil &&
			cluster.SubscriptionID != "" && cluster.ResourceGroupName != "" &&
			strings.EqualFold(cluster.ResourceType.String(), "Microsoft.ContainerService/managedClusters") {
			return nil
		}
	}
	return errors.New("existing project account has no verified ManagedCluster hosting; standby requires BYO AKS")
}

func standbyPoolHostingError(service string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidServiceConfig,
		fmt.Sprintf("agentEndpoint.standbyPool.size for service %q requires BYO AKS hosting", service),
		"declare exactly one azure.ai.project dependency in uses with agentHosting.clusterResourceId "+
			"or an existing endpoint with ARM-verified BYO AKS hosting, or remove agentEndpoint.standbyPool.size",
	)
}
