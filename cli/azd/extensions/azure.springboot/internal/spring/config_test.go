// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentLookupPrefersPersistedValue(t *testing.T) {
	t.Setenv("VALUE", "process")
	value, present := environmentLookup(map[string]string{"VALUE": "persisted"})("VALUE")
	require.True(t, present)
	require.Equal(t, "persisted", value)

	require.NoError(t, os.Unsetenv("VALUE"))
}

func TestSpringServicesSelectCanonicalHostOnly(t *testing.T) {
	canonical := &azdext.ServiceConfig{Name: "canonical", Host: SpringHost}
	project := &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
		"canonical": canonical,
		"legacy":    {Name: "legacy", Host: "azure.springboot"},
		"native":    {Name: "native", Host: "containerapp"},
	}}

	require.Equal(t, []*azdext.ServiceConfig{canonical}, springServices(project))
}
