package azsdk

import (
	"context"
	"testing"

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
		apiVersionPolicy := NewApiVersionPolicy("5.0.0")
		transport := mockContext.HttpClient

		builder := NewClientOptionsBuilder().
			WithTransport(transport).
			WithPolicy(userAgentPolicy).
			WithPolicy(apiVersionPolicy)

		armOptions := builder.BuildArmClientOptions()

		require.Same(t, transport, armOptions.Transport)
		require.Same(t, userAgentPolicy, armOptions.PerCallPolicies[0])
		require.Same(t, apiVersionPolicy, armOptions.PerCallPolicies[1])
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
		apiVersionPolicy := NewApiVersionPolicy("5.0.0")
		transport := mockContext.HttpClient

		builder := NewClientOptionsBuilder().
			WithTransport(transport).
			WithPolicy(userAgentPolicy).
			WithPolicy(apiVersionPolicy)

		coreOptions := builder.BuildCoreClientOptions()

		require.Same(t, transport, coreOptions.Transport)
		require.Same(t, userAgentPolicy, coreOptions.PerCallPolicies[0])
		require.Same(t, apiVersionPolicy, coreOptions.PerCallPolicies[1])
	})
}
