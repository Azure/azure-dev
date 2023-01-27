package environment

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestConfigManagerRoundTrips(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	envConfigMgr := NewConfigManagerFromRoot(root, config.NewManager())
	config, err := envConfigMgr.Load()
	require.NoError(t, err)

	// Set a config value.
	err = config.Set("is.this.a.test", true)
	require.NoError(t, err)

	// Save the environment
	err = envConfigMgr.Save(config)
	require.NoError(t, err)

	// Load the environment back up, we expect no error and for the config value we wrote to still exist.
	reloadedConfig, err := envConfigMgr.Load()
	require.NoError(t, err)
	v, has := reloadedConfig.Get("is.this.a.test")
	require.True(t, has)
	require.Equal(t, true, v)
}
