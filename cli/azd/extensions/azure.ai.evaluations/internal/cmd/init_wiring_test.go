// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
)

func projectWith(names ...string) *azdext.ProjectConfig {
	proj := &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{}}
	for _, n := range names {
		proj.Services[n] = &azdext.ServiceConfig{Name: n}
	}
	return proj
}

// The eval service is added through azd's own AddService, so it has to pick a
// name azd will accept. Reusing one already in the project would overwrite
// somebody else's service.
func TestEvalServiceName_PrefersEvals(t *testing.T) {
	assert.Equal(t, "evals", evalServiceName(projectWith()))
	assert.Equal(t, "evals", evalServiceName(projectWith("api", "web")))
}

func TestEvalServiceName_StepsAsideForAnExistingName(t *testing.T) {
	assert.Equal(t, "evals2", evalServiceName(projectWith("evals")))
	assert.Equal(t, "evals3", evalServiceName(projectWith("evals", "evals2")))
}
