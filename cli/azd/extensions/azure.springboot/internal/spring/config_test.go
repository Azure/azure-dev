// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func serviceWithConfig(t *testing.T, value map[string]any) *azdext.ServiceConfig {
	t.Helper()
	config, err := structpb.NewStruct(value)
	require.NoError(t, err)
	return &azdext.ServiceConfig{Name: "petclinic", Host: SpringHost, AdditionalProperties: config}
}

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
