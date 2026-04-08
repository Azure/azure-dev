// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package kubectl

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_Cli_Name(t *testing.T) {
	cli := NewCli(nil)
	require.Equal(t, "kubectl", cli.Name())
}

func Test_Cli_InstallUrl(t *testing.T) {
	cli := NewCli(nil)
	require.Contains(t, cli.InstallUrl(), "kubectl-install")
}

func Test_Cli_SetEnv(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	var capturedArgs exec.RunArgs
	mockCtx.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			capturedArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	cli := NewCli(mockCtx.CommandRunner)
	cli.SetEnv(map[string]string{
		"MY_VAR": "my_value",
	})

	_, err := cli.Exec(*mockCtx.Context, nil, "get", "pods")
	require.NoError(t, err)
	require.Contains(t, capturedArgs.Env, "MY_VAR=my_value")
}

func Test_Cli_SetEnv_MergesValues(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	var capturedArgs exec.RunArgs
	mockCtx.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			capturedArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	cli := NewCli(mockCtx.CommandRunner)
	cli.SetEnv(map[string]string{"A": "1"})
	cli.SetEnv(map[string]string{"B": "2"})

	_, err := cli.Exec(*mockCtx.Context, nil, "version")
	require.NoError(t, err)
	require.Contains(t, capturedArgs.Env, "A=1")
	require.Contains(t, capturedArgs.Env, "B=2")
}

func Test_Cli_SetKubeConfig(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	var capturedArgs exec.RunArgs
	mockCtx.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			capturedArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	cli := NewCli(mockCtx.CommandRunner)
	cli.SetKubeConfig("/path/to/config")

	_, err := cli.Exec(*mockCtx.Context, nil, "get", "pods")
	require.NoError(t, err)
	require.Contains(t,
		capturedArgs.Env, "KUBECONFIG=/path/to/config",
	)
}

func Test_Cli_Cwd(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	var capturedArgs exec.RunArgs
	mockCtx.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			capturedArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	cli := NewCli(mockCtx.CommandRunner)
	cli.Cwd("/my/workdir")

	_, err := cli.Exec(*mockCtx.Context, nil, "get", "pods")
	require.NoError(t, err)
	require.Equal(t, "/my/workdir", capturedArgs.Cwd)
}

func Test_Cli_Exec_NilFlags(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	var capturedArgs exec.RunArgs
	mockCtx.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			capturedArgs = args
			return exec.NewRunResult(0, "ok", ""), nil
		})

	cli := NewCli(mockCtx.CommandRunner)
	res, err := cli.Exec(*mockCtx.Context, nil, "version")
	require.NoError(t, err)
	require.Equal(t, "ok", res.Stdout)
	require.Equal(t, []string{"version"}, capturedArgs.Args)
}

func Test_Cli_Exec_AllFlags(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	var capturedArgs exec.RunArgs
	mockCtx.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			capturedArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	cli := NewCli(mockCtx.CommandRunner)
	flags := &KubeCliFlags{
		Namespace: "prod",
		DryRun:    DryRunTypeServer,
		Output:    OutputTypeYaml,
	}
	_, err := cli.Exec(
		*mockCtx.Context, flags, "apply", "-f", "file.yaml",
	)
	require.NoError(t, err)
	require.Equal(t, "kubectl", capturedArgs.Cmd)
	require.Equal(t, []string{
		"apply", "-f", "file.yaml",
		"--dry-run=server", "-n", "prod", "-o", "yaml",
	}, capturedArgs.Args)
}

func Test_Cli_ApplyWithKustomize(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	var capturedArgs exec.RunArgs
	mockCtx.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl apply -k")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			capturedArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	cli := NewCli(mockCtx.CommandRunner)
	err := cli.ApplyWithKustomize(
		*mockCtx.Context, "./overlays/prod",
		&KubeCliFlags{Namespace: "prod"},
	)
	require.NoError(t, err)
	require.Equal(t, "kubectl", capturedArgs.Cmd)
	require.Equal(t, []string{
		"apply", "-k", "./overlays/prod", "-n", "prod",
	}, capturedArgs.Args)
}

func Test_Cli_ApplyWithKustomize_Error(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	mockCtx.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl apply -k")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.RunResult{}, errors.New("not found")
		})

	cli := NewCli(mockCtx.CommandRunner)
	err := cli.ApplyWithKustomize(
		*mockCtx.Context, "./bad-path", nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "kubectl apply -k")
}

func Test_Cli_CheckInstalled_Success(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	mockCtx.CommandRunner.MockToolInPath("kubectl", nil)
	mockCtx.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl version")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			ver := `{"clientVersion":{"gitVersion":"v1.28.0"}}`
			return exec.NewRunResult(0, ver, ""), nil
		})

	cli := NewCli(mockCtx.CommandRunner)
	err := cli.CheckInstalled(*mockCtx.Context)
	require.NoError(t, err)
}

func Test_Cli_CheckInstalled_NotInPath(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	mockCtx.CommandRunner.MockToolInPath(
		"kubectl", errors.New("not found"),
	)

	cli := NewCli(mockCtx.CommandRunner)
	err := cli.CheckInstalled(*mockCtx.Context)
	require.Error(t, err)
}

func Test_Cli_CheckInstalled_VersionError(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	mockCtx.CommandRunner.MockToolInPath("kubectl", nil)
	mockCtx.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl version")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.RunResult{},
				errors.New("version fetch failed")
		})

	cli := NewCli(mockCtx.CommandRunner)
	// CheckInstalled logs the error but does not fail
	err := cli.CheckInstalled(*mockCtx.Context)
	require.NoError(t, err)
}

func Test_Cli_GetClientVersion(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	mockCtx.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl version")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			v := `{"clientVersion":{"gitVersion":"v1.30.1"}}`
			return exec.NewRunResult(0, v, ""), nil
		})

	cli := NewCli(mockCtx.CommandRunner)
	ver, err := cli.getClientVersion(*mockCtx.Context)
	require.NoError(t, err)
	require.Equal(t, "v1.30.1", ver)
}

func Test_Cli_GetClientVersion_BadJSON(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	mockCtx.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl version")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "not-json", ""), nil
		})

	cli := NewCli(mockCtx.CommandRunner)
	_, err := cli.getClientVersion(*mockCtx.Context)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parsing kubectl version")
}

func Test_Cli_ConfigUseContext_Error(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	mockCtx.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl config")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.RunResult{},
				errors.New("context not found")
		})

	cli := NewCli(mockCtx.CommandRunner)
	_, err := cli.ConfigUseContext(
		*mockCtx.Context, "missing-ctx", nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed setting kubectl")
}

func Test_Cli_CreateNamespace_Error(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	mockCtx.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl create namespace")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.RunResult{},
				errors.New("already exists")
		})

	cli := NewCli(mockCtx.CommandRunner)
	_, err := cli.CreateNamespace(
		*mockCtx.Context, "existing-ns", nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "kubectl create namespace")
}

func Test_Cli_RolloutStatus_Error(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	mockCtx.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl rollout")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.RunResult{},
				errors.New("deadline exceeded")
		})

	cli := NewCli(mockCtx.CommandRunner)
	_, err := cli.RolloutStatus(
		*mockCtx.Context, "my-deploy", nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "rollout failed")
}

func Test_Cli_ApplyWithStdIn_Error(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	mockCtx.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl apply -f -")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.RunResult{},
				errors.New("invalid yaml")
		})

	cli := NewCli(mockCtx.CommandRunner)
	_, err := cli.ApplyWithStdIn(
		*mockCtx.Context, "bad-yaml", nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "kubectl apply")
}

func Test_Cli_ApplyWithFile_Error(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	mockCtx.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl apply -f")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.RunResult{},
				errors.New("file not found")
		})

	cli := NewCli(mockCtx.CommandRunner)
	_, err := cli.ApplyWithFile(
		*mockCtx.Context, "/bad/path.yaml", nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "kubectl apply")
}

func Test_ParseKubeConfig_Valid(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: Config
current-context: my-cluster
clusters:
  - name: my-cluster
    cluster:
      server: https://my-cluster.example.com:6443
      certificate-authority-data: Y2VydA==
contexts:
  - name: my-cluster
    context:
      cluster: my-cluster
      namespace: default
      user: my-user
users:
  - name: my-user
    user:
      token: my-token
preferences: {}`)

	cfg, err := ParseKubeConfig(context.Background(), raw)
	require.NoError(t, err)
	require.Equal(t, "v1", cfg.ApiVersion)
	require.Equal(t, "Config", cfg.Kind)
	require.Equal(t, "my-cluster", cfg.CurrentContext)
	require.Len(t, cfg.Clusters, 1)
	require.Equal(t, "my-cluster", cfg.Clusters[0].Name)
	require.Equal(t,
		"https://my-cluster.example.com:6443",
		cfg.Clusters[0].Cluster.Server,
	)
	require.Len(t, cfg.Contexts, 1)
	require.Equal(t, "default",
		cfg.Contexts[0].Context.Namespace,
	)
	require.Len(t, cfg.Users, 1)
	require.Equal(t, "my-user", cfg.Users[0].Name)
}

func Test_ParseKubeConfig_InvalidYaml(t *testing.T) {
	raw := []byte(":\tbad yaml\n\t:")
	_, err := ParseKubeConfig(context.Background(), raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed unmarshalling")
}

func Test_ParseKubeConfig_Empty(t *testing.T) {
	cfg, err := ParseKubeConfig(context.Background(), []byte(""))
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Empty(t, cfg.Clusters)
}

func Test_KubeConfig_RoundTrip(t *testing.T) {
	original := &KubeConfig{
		ApiVersion:     "v1",
		Kind:           "Config",
		CurrentContext: "ctx",
		Preferences:    KubePreferences{},
		Clusters: []*KubeCluster{{
			Name: "c1",
			Cluster: KubeClusterData{
				Server: "https://c1:443",
			},
		}},
		Contexts: []*KubeContext{{
			Name: "ctx",
			Context: KubeContextData{
				Cluster: "c1", User: "u1",
			},
		}},
		Users: []*KubeUser{{
			Name:         "u1",
			KubeUserData: KubeUserData{"token": "t"},
		}},
	}

	// Marshal to JSON and back
	data, err := json.Marshal(original)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	var restored KubeConfig
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)
	require.Equal(t, original.CurrentContext, restored.CurrentContext)
}
