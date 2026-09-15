// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/state"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

const deployPreviewCommandProject = `
name: preview-project
services:
  app:
    project: .
    language: js
    host: custom
`

type deployPreviewCommandEnvManager struct {
	environment.Manager
	envs         map[string]*environment.Environment
	interactive  *environment.Environment
	getNames     []string
	mutableCalls []string
}

func (m *deployPreviewCommandEnvManager) Get(
	_ context.Context,
	name string,
) (*environment.Environment, error) {
	m.mutableCalls = append(m.mutableCalls, "Get")
	return m.environment(name)
}

func (m *deployPreviewCommandEnvManager) GetReadOnly(
	_ context.Context,
	name string,
) (*environment.Environment, error) {
	m.getNames = append(m.getNames, name)
	return m.environment(name)
}

func (m *deployPreviewCommandEnvManager) environment(name string) (*environment.Environment, error) {
	if name == "" {
		return nil, environment.ErrNameNotSpecified
	}
	env, ok := m.envs[name]
	if !ok {
		return nil, fmt.Errorf("'%s': %w", name, environment.ErrNotFound)
	}
	return env, nil
}

func (m *deployPreviewCommandEnvManager) Create(
	context.Context,
	environment.Spec,
) (*environment.Environment, error) {
	m.mutableCalls = append(m.mutableCalls, "Create")
	return nil, errors.New("unexpected Create")
}

func (m *deployPreviewCommandEnvManager) LoadOrInitInteractive(
	context.Context,
	string,
) (*environment.Environment, error) {
	m.mutableCalls = append(m.mutableCalls, "LoadOrInitInteractive")
	if m.interactive != nil {
		return m.interactive, nil
	}
	return nil, errors.New("unexpected LoadOrInitInteractive")
}

func (m *deployPreviewCommandEnvManager) Save(context.Context, *environment.Environment) error {
	m.mutableCalls = append(m.mutableCalls, "Save")
	return errors.New("unexpected Save")
}

func (m *deployPreviewCommandEnvManager) SaveWithOptions(
	context.Context,
	*environment.Environment,
	*environment.SaveOptions,
) error {
	m.mutableCalls = append(m.mutableCalls, "SaveWithOptions")
	return errors.New("unexpected SaveWithOptions")
}

func (m *deployPreviewCommandEnvManager) Reload(context.Context, *environment.Environment) error {
	m.mutableCalls = append(m.mutableCalls, "Reload")
	return errors.New("unexpected Reload")
}

func (m *deployPreviewCommandEnvManager) Delete(context.Context, string) error {
	m.mutableCalls = append(m.mutableCalls, "Delete")
	return errors.New("unexpected Delete")
}

func (m *deployPreviewCommandEnvManager) InvalidateEnvCache(context.Context, string) error {
	m.mutableCalls = append(m.mutableCalls, "InvalidateEnvCache")
	return errors.New("unexpected InvalidateEnvCache")
}

type deployPreviewCommandAuthManager struct {
	calls      []string
	cloud      *cloud.Cloud
	credential azcore.TokenCredential
}

func (m *deployPreviewCommandAuthManager) Cloud() *cloud.Cloud {
	m.calls = append(m.calls, "Cloud")
	return m.cloud
}

func (m *deployPreviewCommandAuthManager) Mode() (auth.AuthSource, error) {
	m.calls = append(m.calls, "Mode")
	return "", errors.New("preview must not inspect authentication mode")
}

func (m *deployPreviewCommandAuthManager) CredentialForCurrentUser(
	context.Context,
	*auth.CredentialForCurrentUserOptions,
) (azcore.TokenCredential, error) {
	m.calls = append(m.calls, "CredentialForCurrentUser")
	if m.credential != nil {
		return m.credential, nil
	}
	return nil, errors.New("preview must not request a credential")
}

type deployPreviewCommandTarget struct {
	project.ServiceTarget
	env       *environment.Environment
	err       error
	previewed bool
	supported bool
}

func (t *deployPreviewCommandTarget) SupportsPreview() bool {
	return t.supported
}

func (t *deployPreviewCommandTarget) Preview(
	context.Context,
	*project.ServiceConfig,
) (*project.ServiceDeployPreviewResult, error) {
	t.previewed = true
	if t.err != nil {
		return nil, t.err
	}
	return &project.ServiceDeployPreviewResult{
		Message: "preview " + t.env.Name(),
	}, nil
}

type deployPreviewUnsupportedCommandTarget struct {
	project.ServiceTarget
}

func TestDeployPreviewCommandDoesNotCreateEnvironment(t *testing.T) {
	projectDir := t.TempDir()
	t.Chdir(projectDir)
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	t.Setenv("NO_COLOR", "1")
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://example.services.ai.azure.com/api/projects/preview")
	require.NoError(t, os.WriteFile(
		filepath.Join(projectDir, azdcontext.ProjectFileName),
		[]byte(deployPreviewCommandProject),
		0o600,
	))

	authManager := &deployPreviewCommandAuthManager{}
	container := ioc.NewNestedContainer(nil)
	ioc.RegisterInstance(container, &internal.GlobalCommandOptions{NoPrompt: true})
	root := NewRootCmd(false, nil, container)
	container.MustRegisterScoped(func() middleware.CurrentUserAuthManager {
		return authManager
	})
	target := &deployPreviewCommandTarget{supported: true}
	container.MustRegisterNamedScoped("custom", func(env *environment.Environment) project.ServiceTarget {
		target.env = env
		return target
	})

	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"deploy", "--preview", "app"})
	err := root.ExecuteContext(t.Context())

	require.NoError(t, err)
	require.True(t, target.previewed)
	require.Empty(t, target.env.Name())
	require.Equal(
		t,
		"https://example.services.ai.azure.com/api/projects/preview",
		target.env.Getenv("FOUNDRY_PROJECT_ENDPOINT"),
	)
	require.NoDirExists(t, filepath.Join(projectDir, azdcontext.EnvironmentDirectoryName))
	require.Empty(t, authManager.calls)
}

func TestDeployCommandStillResolvesInteractiveEnvironment(t *testing.T) {
	projectDir := t.TempDir()
	t.Chdir(projectDir)
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	t.Setenv("NO_COLOR", "1")
	require.NoError(t, os.WriteFile(
		filepath.Join(projectDir, azdcontext.ProjectFileName),
		[]byte(deployPreviewCommandProject),
		0o600,
	))

	envManager := &deployPreviewCommandEnvManager{
		interactive: environment.New("existing"),
	}
	azureCloud, err := cloud.NewCloud(&cloud.Config{Name: cloud.AzurePublicName})
	require.NoError(t, err)
	authManager := &deployPreviewCommandAuthManager{
		cloud:      azureCloud,
		credential: &mocks.MockCredentials{},
	}
	container := ioc.NewNestedContainer(nil)
	ioc.RegisterInstance(container, &internal.GlobalCommandOptions{NoPrompt: true})
	root := NewRootCmd(false, nil, container)
	container.MustRegisterScoped(func() environment.Manager {
		return envManager
	})
	container.MustRegisterScoped(func() middleware.CurrentUserAuthManager {
		return authManager
	})

	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"deploy", "-e", "existing", "app"})
	err = root.ExecuteContext(t.Context())

	require.ErrorIs(t, err, internal.ErrInfraNotProvisioned)
	require.Equal(t, []string{"LoadOrInitInteractive"}, envManager.mutableCalls)
	require.Empty(t, envManager.getNames)
	require.Contains(t, authManager.calls, "CredentialForCurrentUser")
}

func TestDeployPreviewCommandResolvesReadOnlyPrerequisites(t *testing.T) {
	providerErr := errors.New("provider preview failed")
	tests := []struct {
		name            string
		defaultEnv      string
		envs            map[string]*environment.Environment
		args            []string
		target          project.ServiceTarget
		wantGet         []string
		wantOutput      string
		wantError       string
		wantProviderErr bool
		wantPreviewed   bool
	}{
		{
			name:          "NoDefaultEnvironment",
			args:          []string{"deploy", "--preview", "app"},
			target:        &deployPreviewCommandTarget{supported: true},
			wantGet:       []string{""},
			wantOutput:    "preview \n",
			wantPreviewed: true,
		},
		{
			name:       "ExplicitExistingEnvironmentOverridesDefault",
			defaultEnv: "default",
			envs: map[string]*environment.Environment{
				"default":  environment.NewWithValues("default", map[string]string{"MARKER": "default"}),
				"selected": environment.NewWithValues("selected", map[string]string{"MARKER": "selected"}),
			},
			args:          []string{"deploy", "--preview", "-e", "selected", "app"},
			target:        &deployPreviewCommandTarget{supported: true},
			wantGet:       []string{"selected"},
			wantOutput:    "preview selected\n",
			wantPreviewed: true,
		},
		{
			name:       "MissingExplicitEnvironmentDoesNotUseDefault",
			defaultEnv: "default",
			envs: map[string]*environment.Environment{
				"default": environment.New("default"),
			},
			args:      []string{"deploy", "--preview", "-e", "missing", "app"},
			wantGet:   []string{"missing"},
			wantError: "environment not found",
		},
		{
			name:       "ProviderErrorPrecedesAuthentication",
			defaultEnv: "default",
			envs: map[string]*environment.Environment{
				"default": environment.New("default"),
			},
			args:            []string{"deploy", "--preview", "app"},
			target:          &deployPreviewCommandTarget{supported: true, err: providerErr},
			wantGet:         []string{"default"},
			wantError:       providerErr.Error(),
			wantProviderErr: true,
			wantPreviewed:   true,
		},
		{
			name:       "UnsupportedTargetPrecedesAuthentication",
			defaultEnv: "default",
			envs: map[string]*environment.Environment{
				"default": environment.New("default"),
			},
			args:      []string{"deploy", "--preview", "app"},
			target:    &deployPreviewUnsupportedCommandTarget{},
			wantGet:   []string{"default"},
			wantError: "no selected service could be previewed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectDir := t.TempDir()
			t.Chdir(projectDir)
			t.Setenv("AZD_CONFIG_DIR", t.TempDir())
			t.Setenv("NO_COLOR", "1")
			require.NoError(t, os.WriteFile(
				filepath.Join(projectDir, azdcontext.ProjectFileName),
				[]byte(deployPreviewCommandProject),
				0o600,
			))

			if tt.defaultEnv != "" {
				azdCtx := azdcontext.NewAzdContextWithDirectory(projectDir)
				require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{
					DefaultEnvironment: tt.defaultEnv,
				}))
				for name := range tt.envs {
					envDir := azdCtx.EnvironmentRoot(name)
					require.NoError(t, os.MkdirAll(envDir, 0o700))
					require.NoError(t, os.WriteFile(
						filepath.Join(envDir, environment.DotEnvFileName),
						[]byte("AZURE_ENV_NAME="+name+"\nMARKER="+name+"\n"),
						0o600,
					))
					require.NoError(t, os.WriteFile(
						filepath.Join(envDir, environment.ConfigFileName),
						[]byte(`{"previewTest":"`+name+`"}`),
						0o600,
					))
				}
			}

			before := snapshotDeployPreviewState(t, projectDir)
			envManager := &deployPreviewCommandEnvManager{envs: tt.envs}
			authManager := &deployPreviewCommandAuthManager{}
			container := ioc.NewNestedContainer(nil)
			ioc.RegisterInstance(container, &internal.GlobalCommandOptions{NoPrompt: true})
			root := NewRootCmd(false, nil, container)
			container.MustRegisterScoped(func() environment.Manager {
				return envManager
			})
			container.MustRegisterScoped(func() middleware.CurrentUserAuthManager {
				return authManager
			})
			if tt.target != nil {
				container.MustRegisterNamedScoped("custom", func(env *environment.Environment) project.ServiceTarget {
					if target, ok := tt.target.(*deployPreviewCommandTarget); ok {
						target.env = env
					}
					return tt.target
				})
			}

			var output strings.Builder
			root.SetOut(&output)
			root.SetErr(io.Discard)
			root.SetArgs(tt.args)
			err := root.ExecuteContext(t.Context())

			if tt.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantError)
			}
			if tt.wantProviderErr {
				require.ErrorIs(t, err, providerErr)
			}
			if tt.wantError == "" {
				require.Equal(t, tt.wantOutput, output.String())
			} else {
				require.Contains(t, output.String(), tt.wantError)
			}
			require.Equal(t, tt.wantGet, envManager.getNames)
			require.Empty(t, envManager.mutableCalls)
			require.Empty(t, authManager.calls)
			require.Equal(t, before, snapshotDeployPreviewState(t, projectDir))

			if target, ok := tt.target.(*deployPreviewCommandTarget); ok {
				require.Equal(t, tt.wantPreviewed, target.previewed)
			}
		})
	}
}

func snapshotDeployPreviewState(t *testing.T, projectDir string) map[string]string {
	t.Helper()
	root := filepath.Join(projectDir, azdcontext.EnvironmentDirectoryName)
	stateFiles := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			stateFiles[relative+string(filepath.Separator)] = "directory"
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		stateFiles[relative] = string(contents)
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return stateFiles
	}
	require.NoError(t, err)

	return stateFiles
}

func (*deployPreviewCommandEnvManager) List(context.Context) ([]*environment.Description, error) {
	return nil, errors.New("unexpected List")
}

func (*deployPreviewCommandEnvManager) EnvPath(*environment.Environment) string {
	panic("unexpected EnvPath")
}

func (*deployPreviewCommandEnvManager) ConfigPath(*environment.Environment) string {
	panic("unexpected ConfigPath")
}

func (*deployPreviewCommandEnvManager) GetStateCacheManager() *state.StateCacheManager {
	panic("unexpected GetStateCacheManager")
}
