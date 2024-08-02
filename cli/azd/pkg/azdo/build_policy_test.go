// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"context"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/policy"
	"github.com/stretchr/testify/require"
)

func Test_getBuildType(t *testing.T) {
	ctx := context.Background()

	t.Run("getPolicyTypesArgs contains projectId", func(t *testing.T) {
		//arrange
		mockClient := MockPolicyClient{}
		projectId := "111222"
		//act
		policyType, err := getBuildType(ctx, &projectId, &mockClient)
		//assert
		require.NoError(t, err)
		require.NotNil(t, policyType)
		require.EqualValues(t, *mockClient.getPolicyTypesArgs.Project, projectId)
	})

	t.Run("returns only 'Build' policy", func(t *testing.T) {
		//arrange
		mockClient := MockPolicyClient{}
		projectId := "111222"
		//act
		policyType, err := getBuildType(ctx, &projectId, &mockClient)
		//assert
		require.NoError(t, err)
		require.NotNil(t, policyType)
		require.EqualValues(t, *policyType.DisplayName, "Build")

	})

}

type MockPolicyClient struct {
	getPolicyTypesArgs policy.GetPolicyTypesArgs
}

func (c *MockPolicyClient) CreatePolicyConfiguration(
	context.Context,
	policy.CreatePolicyConfigurationArgs) (*policy.PolicyConfiguration, error) {
	return nil, nil
}

func (c *MockPolicyClient) DeletePolicyConfiguration(
	context.Context,
	policy.DeletePolicyConfigurationArgs) error {
	return nil
}

func (c *MockPolicyClient) GetPolicyConfiguration(
	context.Context,
	policy.GetPolicyConfigurationArgs) (*policy.PolicyConfiguration, error) {
	return nil, nil
}

func (c *MockPolicyClient) GetPolicyConfigurationRevision(
	context.Context,
	policy.GetPolicyConfigurationRevisionArgs) (*policy.PolicyConfiguration, error) {
	return nil, nil
}

func (c *MockPolicyClient) GetPolicyConfigurationRevisions(context.Context,
	policy.GetPolicyConfigurationRevisionsArgs) (*[]policy.PolicyConfiguration, error) {
	return nil, nil
}

func (c *MockPolicyClient) GetPolicyConfigurations(context.Context,
	policy.GetPolicyConfigurationsArgs) (*policy.GetPolicyConfigurationsResponseValue, error) {
	return nil, nil
}

func (c *MockPolicyClient) GetPolicyEvaluation(
	context.Context,
	policy.GetPolicyEvaluationArgs) (*policy.PolicyEvaluationRecord, error) {
	return nil, nil
}

func (c *MockPolicyClient) GetPolicyEvaluations(
	context.Context,
	policy.GetPolicyEvaluationsArgs) (*[]policy.PolicyEvaluationRecord, error) {
	return nil, nil
}

func (c *MockPolicyClient) GetPolicyType(
	context.Context,
	policy.GetPolicyTypeArgs) (*policy.PolicyType, error) {
	return nil, nil
}

func (c *MockPolicyClient) GetPolicyTypes(
	ctx context.Context,
	args policy.GetPolicyTypesArgs,
) (*[]policy.PolicyType, error) {
	c.getPolicyTypesArgs = args
	policyTypes := make([]policy.PolicyType, 2)
	buildPolicyType := "Build"
	testPolicyType := "Test"
	policyTypes[0] = policy.PolicyType{
		DisplayName: &buildPolicyType,
	}
	policyTypes[1] = policy.PolicyType{
		DisplayName: &testPolicyType,
	}
	return &policyTypes, nil
}

func (c *MockPolicyClient) RequeuePolicyEvaluation(
	context.Context,
	policy.RequeuePolicyEvaluationArgs) (*policy.PolicyEvaluationRecord, error) {
	return nil, nil
}

func (c *MockPolicyClient) UpdatePolicyConfiguration(
	context.Context,
	policy.UpdatePolicyConfigurationArgs) (*policy.PolicyConfiguration, error) {
	return nil, nil
}
