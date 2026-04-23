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

func Test_GetResource_JsonOutput(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	deployment := Deployment{
		Resource: Resource{
			ApiVersion: "apps/v1",
			Kind:       "Deployment",
			Metadata: ResourceMetadata{
				Name:      "my-deploy",
				Namespace: "default",
			},
		},
		Spec:   DeploymentSpec{Replicas: 3},
		Status: DeploymentStatus{ReadyReplicas: 3},
	}
	depJSON, err := json.Marshal(deployment)
	require.NoError(t, err)

	mockContext.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl get deployment")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, string(depJSON), ""), nil
		})

	cli := NewCli(mockContext.CommandRunner)
	result, err := GetResource[Deployment](
		*mockContext.Context, cli,
		ResourceTypeDeployment, "my-deploy", nil,
	)
	require.NoError(t, err)
	require.Equal(t, "my-deploy", result.Metadata.Name)
	require.Equal(t, 3, result.Spec.Replicas)
	require.Equal(t, 3, result.Status.ReadyReplicas)
}

func Test_GetResource_ExplicitJsonFlag(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	svcJSON := `{
		"apiVersion":"v1","kind":"Service",
		"metadata":{"name":"my-svc","namespace":"ns"},
		"spec":{"type":"ClusterIP","clusterIP":"10.0.0.1",
			"ports":[{"port":80,"targetPort":8080,
			"protocol":"TCP"}]},
		"status":{"loadBalancer":{}}
	}`
	mockContext.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl get svc")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, svcJSON, ""), nil
		})

	cli := NewCli(mockContext.CommandRunner)
	flags := &KubeCliFlags{Output: OutputTypeJson}
	result, err := GetResource[Service](
		*mockContext.Context, cli,
		ResourceTypeService, "my-svc", flags,
	)
	require.NoError(t, err)
	require.Equal(t, ServiceTypeClusterIp, result.Spec.Type)
	require.Equal(t, "10.0.0.1", result.Spec.ClusterIp)
}

func Test_GetResource_UnsupportedOutputFormat(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl get")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "some output", ""), nil
		})

	cli := NewCli(mockContext.CommandRunner)
	flags := &KubeCliFlags{Output: OutputType("xml")}
	_, err := GetResource[Deployment](
		*mockContext.Context, cli,
		ResourceTypeDeployment, "my-deploy", flags,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not supported")
}

func Test_GetResource_ExecError(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl get")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.RunResult{},
				errors.New("connection refused")
		})

	cli := NewCli(mockContext.CommandRunner)
	_, err := GetResource[Deployment](
		*mockContext.Context, cli,
		ResourceTypeDeployment, "my-deploy", nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed getting resources")
}

func Test_GetResource_InvalidJson(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl get")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "not-json{", ""), nil
		})

	cli := NewCli(mockContext.CommandRunner)
	_, err := GetResource[Deployment](
		*mockContext.Context, cli,
		ResourceTypeDeployment, "my-deploy", nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed unmarshalling")
}

func Test_GetResources_JsonOutput(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	listJSON := `{
		"apiVersion":"v1","kind":"DeploymentList",
		"items":[
			{"apiVersion":"apps/v1","kind":"Deployment",
			 "metadata":{"name":"deploy-a","namespace":"ns"},
			 "spec":{"replicas":2},
			 "status":{"readyReplicas":2}},
			{"apiVersion":"apps/v1","kind":"Deployment",
			 "metadata":{"name":"deploy-b","namespace":"ns"},
			 "spec":{"replicas":1},
			 "status":{"readyReplicas":0}}
		]
	}`
	mockContext.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl get deployment")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, listJSON, ""), nil
		})

	cli := NewCli(mockContext.CommandRunner)
	list, err := GetResources[Deployment](
		*mockContext.Context, cli, ResourceTypeDeployment, nil,
	)
	require.NoError(t, err)
	require.Len(t, list.Items, 2)
	require.Equal(t, "deploy-a", list.Items[0].Metadata.Name)
	require.Equal(t, "deploy-b", list.Items[1].Metadata.Name)
}

func Test_GetResources_UnsupportedFormat(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl get")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "out", ""), nil
		})

	cli := NewCli(mockContext.CommandRunner)
	flags := &KubeCliFlags{Output: OutputType("table")}
	_, err := GetResources[Deployment](
		*mockContext.Context, cli, ResourceTypeDeployment, flags,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not supported")
}

func Test_GetResources_ExecError(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl get")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.RunResult{}, errors.New("timeout")
		})

	cli := NewCli(mockContext.CommandRunner)
	_, err := GetResources[Deployment](
		*mockContext.Context, cli, ResourceTypeDeployment, nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed getting resources")
}

func Test_GetResources_InvalidJson(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl get")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "{bad", ""), nil
		})

	cli := NewCli(mockContext.CommandRunner)
	_, err := GetResources[Deployment](
		*mockContext.Context, cli, ResourceTypeDeployment, nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed unmarshalling")
}

func Test_Environ(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		result := environ(map[string]string{})
		require.Empty(t, result)
	})

	t.Run("SingleEntry", func(t *testing.T) {
		result := environ(map[string]string{"FOO": "bar"})
		require.Len(t, result, 1)
		require.Equal(t, "FOO=bar", result[0])
	})

	t.Run("MultipleEntries", func(t *testing.T) {
		input := map[string]string{
			"A": "1",
			"B": "2",
		}
		result := environ(input)
		require.Len(t, result, 2)
		// Map iteration order is non-deterministic
		require.ElementsMatch(t,
			[]string{"A=1", "B=2"}, result,
		)
	})
}

func Test_GetResource_YamlOutput(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	yamlOutput := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: yaml-deploy
  namespace: default
spec:
  replicas: 5
status:
  readyReplicas: 5
  availableReplicas: 5
  replicas: 5
  updatedReplicas: 5`
	mockContext.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl get")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, yamlOutput, ""), nil
		})

	cli := NewCli(mockContext.CommandRunner)
	flags := &KubeCliFlags{Output: OutputTypeYaml}
	result, err := GetResource[Deployment](
		*mockContext.Context, cli,
		ResourceTypeDeployment, "yaml-deploy", flags,
	)
	require.NoError(t, err)
	require.Equal(t, 5, result.Spec.Replicas)
}

func Test_GetResources_YamlOutput(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	yamlOutput := `apiVersion: v1
kind: DeploymentList
items:
  - apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: d1
      namespace: ns
    spec:
      replicas: 1
    status:
      readyReplicas: 1`
	mockContext.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl get")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, yamlOutput, ""), nil
		})

	cli := NewCli(mockContext.CommandRunner)
	flags := &KubeCliFlags{Output: OutputTypeYaml}
	list, err := GetResources[Deployment](
		*mockContext.Context, cli, ResourceTypeDeployment, flags,
	)
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	require.Equal(t, 1, list.Items[0].Spec.Replicas)
}

func Test_GetResource_YamlInvalidOutput(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl get")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, ":\tbad yaml\n\t:", ""), nil
		})

	cli := NewCli(mockContext.CommandRunner)
	flags := &KubeCliFlags{Output: OutputTypeYaml}
	_, err := GetResource[Deployment](
		*mockContext.Context, cli,
		ResourceTypeDeployment, "x", flags,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed unmarshalling")
}

func Test_GetResources_YamlInvalidOutput(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.When(
		func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "kubectl get")
		}).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, ":\tbad\n:", ""), nil
		})

	cli := NewCli(mockContext.CommandRunner)
	flags := &KubeCliFlags{Output: OutputTypeYaml}
	_, err := GetResources[Deployment](
		*mockContext.Context, cli, ResourceTypeDeployment, flags,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed unmarshalling")
}
