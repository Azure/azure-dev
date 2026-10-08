// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/require"
)

func TestServiceStableDeclaredDoesNotImport(t *testing.T) {
	t.Parallel()
	project := &ProjectConfig{
		Services: map[string]*ServiceConfig{
			"app": {Name: "app", Language: ServiceLanguageDotNet, Host: ContainerAppTarget},
		},
	}
	// A nil .NET importer fails immediately if preview attempts any discovery or build.
	services, err := NewImportManager(nil).ServiceStableDeclared(project)
	require.NoError(t, err)
	require.Equal(t, []*ServiceConfig{project.Services["app"]}, services)
	require.Nil(t, project.Services["app"].DotNetContainerApp)
}

func TestServiceStableDeclaredDependencyOrdering(t *testing.T) {
	t.Parallel()
	project := &ProjectConfig{
		Services: map[string]*ServiceConfig{
			"delta":   {Name: "delta", Uses: []string{"beta"}},
			"charlie": {Name: "charlie"},
			"beta":    {Name: "beta", Uses: []string{"alpha"}},
			"alpha":   {Name: "alpha"},
		},
	}
	manager := NewImportManager(nil)
	for range 20 {
		declared, err := manager.ServiceStableDeclared(project)
		require.NoError(t, err)
		imported, err := manager.ServiceStable(t.Context(), project)
		require.NoError(t, err)
		expected := []*ServiceConfig{
			project.Services["alpha"], project.Services["beta"], project.Services["charlie"], project.Services["delta"],
		}
		require.Equal(t, expected, declared)
		require.Equal(t, declared, imported, "normal deployment and preview share deterministic dependency ordering")
	}
}

func TestServiceStableDeclaredDependencyErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		uses []string
		want string
	}{
		{name: "Missing", uses: []string{"missing"}, want: "does not exist"},
		{name: "Circular", uses: []string{"api"}, want: "circular dependency"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			project := &ProjectConfig{Services: map[string]*ServiceConfig{
				"api": {Name: "api", Uses: tc.uses},
			}}
			_, err := NewImportManager(nil).ServiceStableDeclared(project)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestFilterServicesByConditionPreservesSelectionOrder(t *testing.T) {
	t.Parallel()
	services := []*ServiceConfig{
		{Name: "dependency"},
		{Name: "disabled", Condition: osutil.NewExpandableString("false")},
		{Name: "api", Uses: []string{"dependency"}, Condition: osutil.NewExpandableString("${ENABLE_API}")},
	}
	result, err := FilterServicesByCondition(services, "", func(string) string { return "true" })
	require.NoError(t, err)
	require.Equal(t, []*ServiceConfig{services[0], services[2]}, result)
	require.Len(t, services, 3, "filtering must not mutate the input slice")
}
