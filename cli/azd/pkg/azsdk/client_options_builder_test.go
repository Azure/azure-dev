package azsdk

import (
	"context"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestCreateArmOptions(t *testing.T) {
	t.Run("WithDefaults", func(t *testing.T) {
		builder := NewClientOptionsBuilder()
		armOptions := builder.BuildArmClientOptions()

		require.Nil(t, armOptions.Transport)
		require.Nil(t, armOptions.PerCallPolicies)
	})

	t.Run("WithOverrides", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		userAgentPolicy := NewUserAgentPolicy("custom-user-agent")
		perCallPolicy := &testPerCallPolicy{}
		preRetryPolicy := &testPerRetryPolicy{}
		transport := mockContext.HttpClient

		builder := NewClientOptionsBuilder().
			WithTransport(transport).
			WithPerCallPolicy(userAgentPolicy).
			WithPerCallPolicy(perCallPolicy).
			WithPerRetryPolicy(preRetryPolicy)

		armOptions := builder.BuildArmClientOptions()

		require.Same(t, transport, armOptions.Transport)
		require.Same(t, userAgentPolicy, armOptions.PerCallPolicies[0])
		require.Same(t, perCallPolicy, armOptions.PerCallPolicies[1])
		require.Same(t, preRetryPolicy, armOptions.PerRetryPolicies[0])
	})
}

func TestCreateCoreOptions(t *testing.T) {
	t.Run("WithDefaults", func(t *testing.T) {
		builder := NewClientOptionsBuilder()
		armOptions := builder.BuildArmClientOptions()

		require.Nil(t, armOptions.Transport)
		require.Nil(t, armOptions.PerCallPolicies)
	})

	t.Run("WithOverrides", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		userAgentPolicy := NewUserAgentPolicy("custom-user-agent")
		perCallPolicy := &testPerCallPolicy{}
		preRetryPolicy := &testPerRetryPolicy{}
		transport := mockContext.HttpClient

		builder := NewClientOptionsBuilder().
			WithTransport(transport).
			WithPerCallPolicy(userAgentPolicy).
			WithPerCallPolicy(perCallPolicy).
			WithPerRetryPolicy(preRetryPolicy)

		coreOptions := builder.BuildCoreClientOptions()

		require.Same(t, transport, coreOptions.Transport)
		require.Same(t, userAgentPolicy, coreOptions.PerCallPolicies[0])
		require.Same(t, perCallPolicy, coreOptions.PerCallPolicies[1])
		require.Same(t, preRetryPolicy, coreOptions.PerRetryPolicies[0])
	})

	t.Run("WithCloud", func(t *testing.T) {
		builder := NewClientOptionsBuilder()
		cloud := cloud.AzurePublic()
		coreOptions := builder.WithCloud(cloud.Configuration).BuildCoreClientOptions()

		require.Equal(t, cloud.Configuration, coreOptions.Cloud)
	})

}

type testPerCallPolicy struct {
}

func (p *testPerCallPolicy) Do(req *policy.Request) (*http.Response, error) {
	return req.Next()
}

type testPerRetryPolicy struct {
}

func (p *testPerRetryPolicy) Do(req *policy.Request) (*http.Response, error) {
	return req.Next()
}
