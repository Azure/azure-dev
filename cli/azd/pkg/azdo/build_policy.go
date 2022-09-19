package azdo

import (
	"context"
	"fmt"

	"github.com/microsoft/azure-devops-go-api/azuredevops"
	"github.com/microsoft/azure-devops-go-api/azuredevops/build"
	"github.com/microsoft/azure-devops-go-api/azuredevops/policy"
)

// returns a build policy type named "Build." Used to created the PR build policy on the default branch
func getBuildType(ctx context.Context, projectId *string, policyClient policy.Client) (*policy.PolicyType, error) {
	getPolicyTypesArgs := policy.GetPolicyTypesArgs{
		Project: projectId,
	}
	policyTypes, err := policyClient.GetPolicyTypes(ctx, getPolicyTypesArgs)
	if err != nil {
		return nil, err
	}
	for _, policy := range *policyTypes {
		if *policy.DisplayName == "Build" {
			return &policy, nil
		}
	}
	return nil, fmt.Errorf("could not find 'Build' policy type in project")
}

// create the PR build policy to ensure that the pipeline runs on a new pull request
// this also disables direct pushes to the default branch and requires changes to go through a PR.
func CreateBuildPolicy(
	ctx context.Context,
	connection *azuredevops.Connection,
	projectId string,
	repoId string,
	buildDefinition *build.BuildDefinition) error {
	client, err := policy.NewClient(ctx, connection)
	if err != nil {
		return err
	}

	buildPolicyType, err := getBuildType(ctx, &projectId, client)
	if err != nil {
		return err
	}

	policyTypeRef := &policy.PolicyTypeRef{
		Id: buildPolicyType.Id,
	}
	policyRevision := 1
	policyIsDeleted := false
	policyIsBlocking := true
	policyIsEnabled := true

	policySettingsScope := make(map[string]interface{})
	policySettingsScope["repositoryId"] = repoId
	policySettingsScope["refName"] = fmt.Sprintf("refs/heads/%s", DefaultBranch)
	policySettingsScope["matchKind"] = "Exact"

	policySettingsScopes := make([]map[string]interface{}, 1)
	policySettingsScopes[0] = policySettingsScope

	policySettings := make(map[string]interface{})
	policySettings["buildDefinitionId"] = buildDefinition.Id
	policySettings["displayName"] = "Azure Dev Deploy PR"
	policySettings["manualQueueOnly"] = false
	policySettings["queueOnSourceUpdateOnly"] = true
	policySettings["validDuration"] = 720
	policySettings["scope"] = policySettingsScopes

	policyConfiguration := &policy.PolicyConfiguration{
		Type:       policyTypeRef,
		Revision:   &policyRevision,
		IsDeleted:  &policyIsDeleted,
		IsBlocking: &policyIsBlocking,
		IsEnabled:  &policyIsEnabled,
		Settings:   policySettings,
	}

	createPolicyConfigurationArgs := policy.CreatePolicyConfigurationArgs{
		Project:       &projectId,
		Configuration: policyConfiguration,
	}

	_, err = client.CreatePolicyConfiguration(ctx, createPolicyConfigurationArgs)
	if err != nil {
		return err
	}

	return nil
}
