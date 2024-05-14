package vsrpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
)

func Test_azdContext(t *testing.T) {
	root := t.TempDir()
	nearestRel := filepath.Join("nearest", "apphost.csproj")
	nearest := filepath.Join(root, nearestRel)
	inAppHost := filepath.Join(root, filepath.Join("in-apphost", "apphost.csproj"))
	nearestUnmatched := filepath.Join(root, filepath.Join("nearest-unmatched", "apphost.csproj"))

	// Create app host directories and files
	require.NoError(t, createAppHost(nearest))
	require.NoError(t, createAppHost(inAppHost))
	require.NoError(t, createAppHost(nearestUnmatched))

	// By default, no azure.yaml is present. All projects would choose their app host directory as the context directory.
	ctxDir, err := azdContext(nearest)
	require.NoError(t, err)
	require.Equal(t, filepath.Dir(nearest), ctxDir.ProjectDirectory())

	ctxDir, err = azdContext(inAppHost)
	require.NoError(t, err)
	require.Equal(t, filepath.Dir(inAppHost), ctxDir.ProjectDirectory())

	ctxDir, err = azdContext(nearestUnmatched)
	require.NoError(t, err)
	require.Equal(t, filepath.Dir(nearestUnmatched), ctxDir.ProjectDirectory())

	// Create azure.yaml files.
	require.NoError(t, createProject(root, nearestRel))
	require.NoError(t, createProject(filepath.Dir(inAppHost), "apphost.csproj"))

	// nearest uses 'root'
	ctxDir, err = azdContext(nearest)
	require.NoError(t, err)
	require.Equal(t, root, ctxDir.ProjectDirectory())

	// inAppHost uses 'in-apphost'
	ctxDir, err = azdContext(inAppHost)
	require.NoError(t, err)
	require.Equal(t, filepath.Dir(inAppHost), ctxDir.ProjectDirectory())

	// nearestUnmatched uses its own directory
	ctxDir, err = azdContext(nearestUnmatched)
	require.NoError(t, err)
	require.Equal(t, filepath.Dir(nearestUnmatched), ctxDir.ProjectDirectory())
}

func createProject(prjDir string, appHostPath string) error {
	err := os.MkdirAll(prjDir, 0755)
	if err != nil {
		return err
	}
	prjPath := filepath.Join(prjDir, azdcontext.ProjectFileName)

	prjConfig := &project.ProjectConfig{
		Name: "app",
		Services: map[string]*project.ServiceConfig{
			"app": {
				Host:         project.ContainerAppTarget,
				Language:     project.ServiceLanguageDotNet,
				RelativePath: appHostPath,
			},
		},
	}

	return project.Save(context.Background(), prjConfig, prjPath)
}

func createAppHost(path string) error {
	dir := filepath.Dir(path)
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return f.Close()
}
