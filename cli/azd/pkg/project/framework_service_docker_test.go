// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockarmresources"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
	"github.com/stretchr/testify/require"
)

func TestDefaultDockerOptions(t *testing.T) {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-test
services:
  web:
    project: src/web
    language: js
    host: containerapp
    resourceName: test-containerapp-web
`
	ran := false

	env := environment.EphemeralWithValues("test-env", nil)
	env.SetSubscriptionId("sub")

	mockContext := mocks.NewMockContext(context.Background())
	mockarmresources.AddAzResourceListMock(
		mockContext.HttpClient,
		convert.RefOf("rg-test"),
		[]*armresources.GenericResourceExpanded{
			{
				ID:       convert.RefOf("app-api-abc123"),
				Name:     convert.RefOf("test-containerapp-web"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeContainerApp)),
				Location: convert.RefOf("eastus2"),
			},
		})
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker build")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		ran = true

		require.Equal(t, []string{
			"build", "-q",
			"-f", "./Dockerfile",
			"--platform", "amd64",
			".",
		}, args.Args)

		return exec.RunResult{
			Stdout:   "imageId",
			Stderr:   "",
			ExitCode: 0,
		}, nil
	})

	projectConfig, err := ParseProjectConfig(testProj)
	require.NoError(t, err)
	prj, err := projectConfig.GetProject(*mockContext.Context, env, mockContext.Console, azCli, mockContext.CommandRunner)
	require.NoError(t, err)

	service := prj.Services[0]

	docker := docker.NewDocker(mockContext.CommandRunner)

	progress := make(chan string)
	done := make(chan bool)

	internalFramework := NewNpmProject(mockContext.CommandRunner, service.Config, env)
	progressMessages := []string{}

	go func() {
		for value := range progress {
			progressMessages = append(progressMessages, value)
		}
		done <- true
	}()

	framework := NewDockerProject(service.Config, env, docker, internalFramework)
	res, err := framework.Package(*mockContext.Context, progress)
	close(progress)
	<-done

	require.Equal(t, "imageId", res)
	require.Nil(t, err)
	require.Len(t, progressMessages, 1)
	require.Equal(t, "Building docker image", progressMessages[0])
	require.Equal(t, true, ran)
}

func TestCustomDockerOptions(t *testing.T) {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-test
services:
  web:
    project: src/web
    language: js
    host: containerapp
    resourceName: test-containerapp-web
    docker:
      path: ./Dockerfile.dev
      context: ../
`

	env := environment.EphemeralWithValues("test-env", nil)
	env.SetSubscriptionId("sub")
	mockContext := mocks.NewMockContext(context.Background())
	mockarmresources.AddAzResourceListMock(
		mockContext.HttpClient,
		convert.RefOf("rg-test"),
		[]*armresources.GenericResourceExpanded{
			{
				ID:       convert.RefOf("app-api-abc123"),
				Name:     convert.RefOf("test-containerapp-web"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeContainerApp)),
				Location: convert.RefOf("eastus2"),
			},
		})
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)

	ran := false

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker build")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		ran = true

		require.Equal(t, []string{
			"build", "-q",
			"-f", "./Dockerfile.dev",
			"--platform", "amd64",
			"../",
		}, args.Args)

		return exec.RunResult{
			Stdout:   "imageId",
			Stderr:   "",
			ExitCode: 0,
		}, nil
	})

	docker := docker.NewDocker(mockContext.CommandRunner)

	projectConfig, err := ParseProjectConfig(testProj)
	require.NoError(t, err)

	prj, err := projectConfig.GetProject(*mockContext.Context, env, mockContext.Console, azCli, mockContext.CommandRunner)
	require.NoError(t, err)

	service := prj.Services[0]

	progress := make(chan string)
	done := make(chan bool)

	internalFramework := NewNpmProject(mockContext.CommandRunner, service.Config, env)
	status := ""

	go func() {
		for value := range progress {
			status = value
		}
		done <- true
	}()

	framework := NewDockerProject(service.Config, env, docker, internalFramework)
	res, err := framework.Package(*mockContext.Context, progress)
	close(progress)
	<-done

	require.Equal(t, "imageId", res)
	require.Nil(t, err)
	require.Equal(t, "Building docker image", status)
	require.Equal(t, true, ran)
}
