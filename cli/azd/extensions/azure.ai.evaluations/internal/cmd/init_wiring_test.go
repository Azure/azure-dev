// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func projectWith(names ...string) *azdext.ProjectConfig {
	proj := &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{}}
	for _, n := range names {
		proj.Services[n] = &azdext.ServiceConfig{Name: n}
	}
	return proj
}

// The eval service is ordered after everything it reads, but only names
// services the project actually declares. Naming one it does not have is a
// broken reference, and an eval config can sit in a repo that reaches an
// existing Foundry project by endpoint and an agent deployed elsewhere.
func TestEvalServiceUses_OnlyWhatTheProjectDeclares(t *testing.T) {
	assert.Nil(t, evalServiceUses(projectWith("api", "web"), "support-agent"),
		"neither the project service nor the agent is declared, so there is nothing to order after")

	withProject := projectWith("api", "support-agent")
	withProject.Services["ai-project"] = &azdext.ServiceConfig{
		Name: "ai-project", Host: aiProjectHost,
	}
	assert.Equal(t, []string{"ai-project", "support-agent"},
		evalServiceUses(withProject, "support-agent"),
		"the eval runs after the project it evaluates against and the agent it evaluates")

	assert.Equal(t, []string{"support-agent"},
		evalServiceUses(projectWith("support-agent"), "support-agent"),
		"an agent alone is still worth ordering after")
}

// `init` detects the judge deployment from the project, because it makes no
// service calls and this is the only place it can read one.
func TestDetectModelDeployment(t *testing.T) {
	assert.Empty(t, detectModelDeployment(projectWith("api", "web")))

	proj := projectWith("api")
	proj.Services["chat"] = &azdext.ServiceConfig{Name: "chat", Host: aiModelHost}
	assert.Equal(t, "chat", detectModelDeployment(proj),
		"the service name is the deployment name when nothing more specific is declared")

	named := projectWith()
	named.Services["chat"] = &azdext.ServiceConfig{
		Name: "chat",
		Host: aiModelHost,
		AdditionalProperties: mustStruct(t, map[string]any{
			"deployment": "gpt-5.6-luna",
		}),
	}
	assert.Equal(t, "gpt-5.6-luna", detectModelDeployment(named))
}

func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	require.NoError(t, err)
	return s
}
