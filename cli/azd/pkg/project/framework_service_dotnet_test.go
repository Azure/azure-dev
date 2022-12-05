// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestBicepOutputsWithDoubleUnderscoresAreConverted(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	keys := []string{}

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "dotnet user-secrets set")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		t.Logf("dotnet user-secrets set was called with: %+v", args)
		keys = append(keys, args.Args[2])
		return exec.NewRunResult(0, "", ""), nil
	})

	dp := NewDotNetProject(mockContext.CommandRunner, &ServiceConfig{
		Project: &ProjectConfig{
			Path: "/sample/path/for/test",
		},
		RelativePath: "",
	}, environment.Ephemeral()).(*dotnetProject)

	err := dp.setUserSecretsFromOutputs(*mockContext.Context, ServiceLifecycleEventArgs{
		Args: map[string]any{
			"bicepOutput": map[string]provisioning.OutputParameter{
				"EXAMPLE_OUTPUT":          {Type: "string", Value: "foo"},
				"EXAMPLE__NESTED__OUTPUT": {Type: "string", Value: "bar"},
			},
		},
	})

	require.NoError(t, err)
	require.Len(t, keys, 2)

	sort.Strings(keys)
	require.Equal(t, "EXAMPLE:NESTED:OUTPUT", keys[0])
	require.Equal(t, "EXAMPLE_OUTPUT", keys[1])

}
