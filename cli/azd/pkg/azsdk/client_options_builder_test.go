package azsdk

import (
	"context"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
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
		testPolicy := &testPolicy{}
		transport := mockContext.HttpClient

		builder := NewClientOptionsBuilder().
			WithTransport(transport).
			WithPolicy(userAgentPolicy).
			WithPolicy(testPolicy)

		armOptions := builder.BuildArmClientOptions()

		require.Same(t, transport, armOptions.Transport)
		require.Same(t, userAgentPolicy, armOptions.PerCallPolicies[0])
		require.Same(t, testPolicy, armOptions.PerCallPolicies[1])
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
		testPolicy := &testPolicy{}
		transport := mockContext.HttpClient

		builder := NewClientOptionsBuilder().
			WithTransport(transport).
			WithPolicy(userAgentPolicy).
			WithPolicy(testPolicy)

		coreOptions := builder.BuildCoreClientOptions()

		require.Same(t, transport, coreOptions.Transport)
		require.Same(t, userAgentPolicy, coreOptions.PerCallPolicies[0])
		require.Same(t, testPolicy, coreOptions.PerCallPolicies[1])
	})
}

type testPolicy struct {
}

func (p *testPolicy) Do(req *policy.Request) (*http.Response, error) {
	return req.Next()
}
