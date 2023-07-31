package templates

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/require"
)

func Test_NewFileTemplateSource_FileExists(t *testing.T) {
	name := "test"
	configDir, err := config.GetUserConfigDir()
	require.NoError(t, err)

	path := filepath.Join(configDir, "test-templates.json")
	err = os.WriteFile(path, []byte(jsonTemplates()), osutil.PermissionFile)
	require.Nil(t, err)

	source, err := NewFileTemplateSource(name, path)
	require.Nil(t, err)

	require.Equal(t, name, source.Name())

	err = os.Remove(path)
	require.Nil(t, err)
}

func Test_NewFileTemplateSource_InvalidJson(t *testing.T) {
	name := "test"
	configDir, err := config.GetUserConfigDir()
	require.NoError(t, err)

	path := filepath.Join(configDir, "test-templates.json")
	invalidJson := `invalid json`
	err = os.WriteFile(path, []byte(invalidJson), osutil.PermissionFile)
	require.Nil(t, err)

	_, err = NewFileTemplateSource(name, path)
	require.Error(t, err)

	err = os.Remove(path)
	require.Nil(t, err)
}

func Test_NewFileTemplateSource_FileDoesNotExist(t *testing.T) {
	name := "test"
	path := "testdata/nonexistent.json"
	_, err := NewFileTemplateSource(name, path)
	require.Error(t, err)
}
