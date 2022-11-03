package auth

import (
	"context"
	"os"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestReadUserProperties(t *testing.T) {
	t.Run("homeID", func(t *testing.T) {
		cfg := config.NewConfig(nil)
		require.NoError(t, cfg.Set("auth.account.currentUser.homeAccountId", "testAccountId"))

		props, err := readUserProperties(cfg)

		require.NoError(t, err)
		require.Nil(t, props.ClientID)
		require.Nil(t, props.TenantID)
		require.Equal(t, "testAccountId", *props.HomeAccountID)
	})

	t.Run("clientID", func(t *testing.T) {
		cfg := config.NewConfig(nil)
		require.NoError(t, cfg.Set("auth.account.currentUser.clientId", "testClientId"))
		require.NoError(t, cfg.Set("auth.account.currentUser.tenantId", "testTenantId"))

		props, err := readUserProperties(cfg)

		require.NoError(t, err)
		require.Nil(t, props.HomeAccountID)
		require.Equal(t, "testClientId", *props.ClientID)
		require.Equal(t, "testTenantId", *props.TenantID)
	})
}

func TestServicePrincipalLoginClientSecret(t *testing.T) {
	credentialCache := &memoryCache{
		cache: make(map[string][]byte),
	}

	m := Manager{
		configManager:   &memoryConfigManager{},
		credentialCache: credentialCache,
	}

	cred, err := m.LoginWithServicePrincipalSecret(
		context.Background(), "testClientId", "testTenantId", "testClientSecret",
	)

	require.NoError(t, err)
	require.IsType(t, cred, new(azidentity.ClientSecretCredential))

	cred, err = m.CredentialForCurrentUser(context.Background())

	require.NoError(t, err)
	require.IsType(t, cred, new(azidentity.ClientSecretCredential))
}

type memoryConfigManager struct {
	configs map[string]config.Config
}

func (m *memoryConfigManager) Load(filePath string) (config.Config, error) {
	if cfg, has := m.configs[filePath]; has {
		return cfg, nil
	}

	return nil, os.ErrNotExist
}

func (m *memoryConfigManager) Save(cfg config.Config, filePath string) error {
	if m.configs == nil {
		m.configs = make(map[string]config.Config)
	}

	m.configs[filePath] = cfg
	return nil
}
