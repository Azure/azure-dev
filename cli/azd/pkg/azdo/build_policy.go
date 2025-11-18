// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/policy"
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
	buildDefinition *build.BuildDefinition,
	env *environment.Environment) error {
	client, err := policy.NewClient(ctx, connection)
	if err != nil {
		return err
	}

	buildPolicyType, err := getBuildType(ctx, &projectId, client)
	if err != nil {
		return err
	}

	localClient, err := newLocalClient(ctx, connection)
	if err != nil {
		return err
	}

	// check if the policy already exists
	existingPoliciesResponse, err := localClient.getPolicyConfigurations(ctx, getPolicyConfigurationsArgs{
		Project:    &projectId,
		PolicyType: buildPolicyType.Id,
	})
	if err != nil {
		return err
	}
	existingPolicies := []policy.PolicyConfiguration{}
	for existingPoliciesResponse != nil {
		existingPolicies = append(existingPolicies, existingPoliciesResponse.Value...)
		if existingPoliciesResponse.ContinuationToken == "" {
			break
		}
		existingPoliciesResponse, err = localClient.getPolicyConfigurations(ctx, getPolicyConfigurationsArgs{
			Project:           &projectId,
			PolicyType:        buildPolicyType.Id,
			ContinuationToken: existingPoliciesResponse.ContinuationToken,
		})
		if err != nil {
			return err
		}
	}

	for _, policy := range existingPolicies {
		pSettings := policy.Settings.(map[string]interface{})
		if def, exists := pSettings["buildDefinitionId"]; exists {
			defId, castOk := def.(float64)
			if !castOk {
				return fmt.Errorf("could not cast buildDefinitionId to int")
			}
			if defId == float64(*buildDefinition.Id) {
				// policy already exists
				return nil
			}
		}
	}

	policyTypeRef := &policy.PolicyTypeRef{
		Id: buildPolicyType.Id,
	}
	policyRevision := 1
	policyIsDeleted := false
	policyIsBlocking := true
	policyIsEnabled := true

	policySettingsScope := map[string]interface{}{
		"repositoryId": repoId,
		"refName":      fmt.Sprintf("refs/heads/%s", DefaultBranch),
		"matchKind":    "Exact",
	}

	policySettingsScopes := []map[string]interface{}{
		policySettingsScope,
	}

	policySettings := map[string]interface{}{
		"buildDefinitionId":       buildDefinition.Id,
		"displayName":             fmt.Sprintf("Azure Dev Deploy PR - %s", env.Name()),
		"manualQueueOnly":         false,
		"queueOnSourceUpdateOnly": true,
		"validDuration":           720,
		"scope":                   policySettingsScopes,
	}

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

func newLocalClient(ctx context.Context, connection *azuredevops.Connection) (*clientImpl, error) {
	client, err := connection.GetClientByResourceAreaId(ctx, uuid.MustParse("fb13a388-40dd-4a04-b530-013a739c72ef"))
	if err != nil {
		return nil, err
	}
	return &clientImpl{
		Client: *client,
	}, nil
}

type clientImpl struct {
	Client azuredevops.Client
}

// local implementation for GetPolicyConfigurations
// The implementation from the policy client is broken because it does not support taking a continuation token
// see: https://github.com/microsoft/azure-devops-go-api/issues/156
func (client *clientImpl) getPolicyConfigurations(
	ctx context.Context, args getPolicyConfigurationsArgs) (*getPolicyConfigurationsResponseValue, error) {
	routeValues := make(map[string]string)
	if args.Project == nil || *args.Project == "" {
		return nil, &azuredevops.ArgumentNilOrEmptyError{ArgumentName: "args.Project"}
	}
	routeValues["project"] = *args.Project

	queryParams := url.Values{}
	if args.Scope != nil {
		queryParams.Add("scope", *args.Scope)
	}
	if args.PolicyType != nil {
		queryParams.Add("policyType", (*args.PolicyType).String())
	}
	if args.ContinuationToken != "" {
		queryParams.Add("continuationToken", args.ContinuationToken)
	}
	locationId, _ := uuid.Parse("dad91cbe-d183-45f8-9c6e-9c1164472121")
	resp, err := client.Client.Send(
		ctx, http.MethodGet, locationId, "7.1-preview.1", routeValues, queryParams, nil, "", "application/json", nil)
	if err != nil {
		return nil, err
	}

	var responseValue getPolicyConfigurationsResponseValue
	responseValue.ContinuationToken = resp.Header.Get(azuredevops.HeaderKeyContinuationToken)
	err = client.Client.UnmarshalCollectionBody(resp, &responseValue.Value)
	return &responseValue, err
}

// Arguments for the GetPolicyConfigurations function
type getPolicyConfigurationsArgs struct {
	// (required) Project ID or project name
	Project *string
	// (optional) [Provided for legacy reasons] The scope on which a subset of policies is defined.
	Scope *string
	// (optional) Filter returned policies to only this type
	PolicyType        *uuid.UUID
	ContinuationToken string
}

// Return type for the GetPolicyConfigurations function
type getPolicyConfigurationsResponseValue struct {
	Value             []policy.PolicyConfiguration
	ContinuationToken string
}
