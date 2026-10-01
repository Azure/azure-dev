// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProviderScopedEnvIndependentAliases(t *testing.T) {
	raw := NewWithValues("test", map[string]string{"INPUT": "orders", "OUTPUT": "old", "OTHER": "keep"})
	inputs := map[string]string{"SERVICE_BUS_NAME": "INPUT"}
	outputs := map[string]string{"SERVICE_BUS_NAME": "OUTPUT"}
	env := NewProviderScopedEnv(raw, inputs, outputs)
	require.Same(t, raw, raw.PersistableEnv())
	require.Same(t, raw, env.PersistableEnv())
	require.Same(t, raw, NewProviderScopedEnv(env, nil, nil).PersistableEnv())
	inputs["SERVICE_BUS_NAME"] = "OTHER"
	outputs["SERVICE_BUS_NAME"] = "OTHER"

	require.Equal(t, "test", env.Name())
	require.Same(t, raw.GetConfig(), env.GetConfig())
	require.Equal(t, "orders", env.Getenv("SERVICE_BUS_NAME"))
	value, found := env.LookupEnv("SERVICE_BUS_NAME")
	require.True(t, found)
	require.Equal(t, "orders", value)
	require.Equal(t, map[string]string{
		"SERVICE_BUS_NAME": "orders", "OUTPUT": "old", "OTHER": "keep",
	}, env.Dotenv())

	env.DotenvSet("SERVICE_BUS_NAME", "billing")
	require.Equal(t, "billing", raw.Getenv("OUTPUT"))
	require.Equal(t, "orders", env.Getenv("SERVICE_BUS_NAME"))
	env.DotenvDelete("SERVICE_BUS_NAME")
	require.Equal(t, map[string]string{"INPUT": "orders", "OTHER": "keep"}, raw.Dotenv())

	detached := env.Dotenv()
	detached["SERVICE_BUS_NAME"] = "changed"
	require.Equal(t, "orders", env.Getenv("SERVICE_BUS_NAME"))
	require.Equal(t, "orders", raw.Getenv("INPUT"))
}

func TestProviderScopedEnvOneWayAliases(t *testing.T) {
	raw := NewWithValues("test", map[string]string{"INPUT": "orders", "SERVICE_BUS_NAME": "original"})
	input := NewProviderScopedEnv(raw, map[string]string{"SERVICE_BUS_NAME": "INPUT"}, nil)
	input.DotenvSet("SERVICE_BUS_NAME", "billing")
	require.Equal(t, "billing", raw.Getenv("SERVICE_BUS_NAME"))
	require.Equal(t, "orders", input.Getenv("SERVICE_BUS_NAME"))
	input.DotenvDelete("SERVICE_BUS_NAME")
	require.Equal(t, "orders", raw.Getenv("INPUT"))

	output := NewProviderScopedEnv(raw, nil, map[string]string{"SERVICE_BUS_NAME": "OUTPUT"})
	output.DotenvSet("SERVICE_BUS_NAME", "new")
	require.Equal(t, "", output.Getenv("SERVICE_BUS_NAME"))
	require.Equal(t, "new", raw.Getenv("OUTPUT"))
	require.Equal(t, raw.Dotenv(), output.Dotenv())
}

func TestProviderScopedEnvLookupPrecedence(t *testing.T) {
	t.Setenv("INPUT", "from-process")
	t.Setenv("SERVICE_BUS_NAME", "target-process")
	raw := NewWithValues("test", map[string]string{"SERVICE_BUS_NAME": "target-dotenv"})
	env := NewProviderScopedEnv(raw, map[string]string{"SERVICE_BUS_NAME": "INPUT"}, nil)
	require.Equal(t, "from-process", env.Getenv("SERVICE_BUS_NAME"))
	require.Empty(t, env.Dotenv())
	require.Empty(t, env.Environ())

	raw.DotenvSet("INPUT", "")
	value, found := env.LookupEnv("SERVICE_BUS_NAME")
	require.True(t, found)
	require.Empty(t, value)
	require.Equal(t, map[string]string{"SERVICE_BUS_NAME": ""}, env.Dotenv())
	require.Equal(t, []string{"SERVICE_BUS_NAME="}, env.Environ())
}

func TestProviderScopedEnvOverlappingAliases(t *testing.T) {
	raw := NewWithValues("test", map[string]string{"A": "first", "B": "second"})
	env := NewProviderScopedEnv(raw, map[string]string{"A": "B", "B": "A"}, nil)
	require.Equal(t, map[string]string{"A": "second", "B": "first"}, env.Dotenv())
	require.Equal(t, map[string]string{"A": "first", "B": "second"}, raw.Dotenv())

	env = NewProviderScopedEnv(raw, nil, map[string]string{"A": "B", "B": "C"})
	env.DotenvSet("A", "updated")
	require.Equal(t, "updated", raw.Getenv("B"))
	require.Empty(t, raw.Getenv("C"))
	env.DotenvDelete("A")
	require.Empty(t, raw.Getenv("B"))
}

func TestProviderScopedEnvUnmappedVariables(t *testing.T) {
	raw := NewWithValues("test", map[string]string{"KEY": "original"})
	env := NewProviderScopedEnv(raw, nil, nil)
	require.Equal(t, "original", env.Getenv("KEY"))
	env.DotenvSet("KEY", "updated")
	require.Equal(t, "updated", raw.Getenv("KEY"))
	require.Equal(t, map[string]string{"KEY": "updated"}, env.Dotenv())
	require.Equal(t, []string{"KEY=updated"}, env.Environ())
	env.DotenvDelete("KEY")
	require.Empty(t, raw.Dotenv())
}

func TestProviderScopedEnvAccessors(t *testing.T) {
	raw := NewWithValues("test", map[string]string{
		"SUB": "sub-id", "LOC": "westus", "TENANT": "tenant-id", "BUS": "orders",
	})
	env := NewProviderScopedEnv(raw,
		map[string]string{
			SubscriptionIdEnvVarName: "SUB", LocationEnvVarName: "LOC",
			TenantIdEnvVarName: "TENANT", "SERVICE_ORDER_PROCESSOR_BUS_NAME": "BUS",
		},
		map[string]string{
			SubscriptionIdEnvVarName: "SUB_OUT", LocationEnvVarName: "LOC_OUT",
			"SERVICE_ORDER_PROCESSOR_BUS_NAME": "BUS_OUT",
		},
	)
	require.Equal(t, "sub-id", env.GetSubscriptionId())
	require.Equal(t, "westus", env.GetLocation())
	require.Equal(t, "tenant-id", env.GetTenantId())
	require.Equal(t, "orders", env.GetServiceProperty("order-processor", "BUS_NAME"))
	env.SetSubscriptionId("other-sub")
	env.SetLocation("eastus")
	env.SetServiceProperty("order-processor", "BUS_NAME", "billing")
	require.Equal(t, "other-sub", raw.Getenv("SUB_OUT"))
	require.Equal(t, "eastus", raw.Getenv("LOC_OUT"))
	require.Equal(t, "billing", raw.Getenv("BUS_OUT"))

	list := env.Environ()
	slices.Sort(list)
	require.Equal(t, []string{
		"AZURE_LOCATION=westus", "AZURE_SUBSCRIPTION_ID=sub-id", "AZURE_TENANT_ID=tenant-id",
		"BUS_OUT=billing", "LOC_OUT=eastus", "SERVICE_ORDER_PROCESSOR_BUS_NAME=orders", "SUB_OUT=other-sub",
	}, list)
}
