// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/stretchr/testify/require"
)

func TestPlanFirewallRulesAddsBeforeRemovingOwnedRules(t *testing.T) {
	prefix := ownedFirewallPrefix("petclinic")
	plan := planFirewallRules(
		"petclinic",
		[]string{"10.0.0.2", "10.0.0.3"},
		[]firewallRule{
			{name: prefix + "10-0-0-1", ip: "10.0.0.1"},
			{name: prefix + "10-0-0-2", ip: "10.0.0.2-10.0.0.9"},
			{name: prefix + "stale-range", ip: "0.0.0.0-255.255.255.255"},
			{name: "unrelated", ip: "0.0.0.0"},
		},
	)
	require.Equal(t, []firewallRule{
		{name: prefix + "10-0-0-2", ip: "10.0.0.2"},
		{name: prefix + "10-0-0-3", ip: "10.0.0.3"},
	}, plan.add)
	require.Equal(t, []string{prefix + "10-0-0-1", prefix + "stale-range"}, plan.remove)
	require.NotContains(t, plan.remove, "unrelated")
}

func TestNormalizeOutboundIPs(t *testing.T) {
	one := "10.0.0.2"
	duplicate := "10.0.0.2"
	two := "10.0.0.1"
	ips, err := normalizeOutboundIPs([]*string{&one, &duplicate, &two})
	require.NoError(t, err)
	require.Equal(t, []string{"10.0.0.1", "10.0.0.2"}, ips)

	_, err = normalizeOutboundIPs(nil)
	require.ErrorContains(t, err, "no outbound IP")
	ipv6 := "::1"
	_, err = normalizeOutboundIPs([]*string{&ipv6})
	require.ErrorContains(t, err, "invalid or unsupported")
}

func TestNormalizeContainerAppOutboundIPsHandlesMissingProperties(t *testing.T) {
	_, err := normalizeContainerAppOutboundIPs(nil)
	require.ErrorContains(t, err, "no outbound IP")

	properties := &armappcontainers.ContainerAppProperties{}
	_, err = normalizeContainerAppOutboundIPs(properties)
	require.ErrorContains(t, err, "no outbound IP")
}

func TestFirewallIdentityUsesPersistedResourceIDs(t *testing.T) {
	values := map[string]string{
		containerAppIDEnv: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/containerApps/petclinic-custom",
		postgresServerIDEnv: "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.DBforPostgreSQL/flexibleServers/postgres-custom",
		postgresServerNameEnv: "postgres-custom",
	}
	identity, err := firewallIdentityFromValues(values)
	require.NoError(t, err)
	require.Equal(t, "sub", identity.subscriptionID)
	require.Equal(t, "rg", identity.resourceGroup)
	require.Equal(t, "petclinic-custom", identity.containerAppName)
	require.Equal(t, "postgres-custom", identity.postgresServerName)
}

func TestFirewallErrorsDoNotDiscloseResourceIDs(t *testing.T) {
	secretID := "https://user:password@example.invalid/resource?sig=secret"
	_, err := firewallIdentityFromValues(map[string]string{
		containerAppIDEnv:     secretID,
		postgresServerIDEnv:   secretID,
		postgresServerNameEnv: "server",
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "password")
	require.NotContains(t, err.Error(), "sig=secret")
}
