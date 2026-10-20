// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"net/url"
	"slices"
	"strings"
)

func registryNetworkEndpoint(host string) string {
	if host == "" || strings.ContainsAny(host, "/\\:@?#% \t\r\n") || !strings.Contains(host, ".") {
		return ""
	}
	endpoint, err := url.Parse("https://" + host)
	if err != nil || endpoint.Hostname() != host {
		return ""
	}
	return endpoint.String()
}

func (c *sdkResourceChecker) registryNetworkChecks(ctx context.Context, id string, sources []string) Report {
	base := resourceCheck("R06", id, "AKS workload DNS/TCP 443/TLS to registry login and enabled dedicated data endpoints")
	base.Observed = "registry network reachability only; image-pull authorization and propagation are separate"
	if _, err := existingResourceID(id, "Microsoft.ContainerRegistry/registries"); err != nil {
		base.Cause, base.Missing = err, "complete prepared registry ARM ID"
		return Report{base}
	}
	client, err := newRegistryARMClient(c.credential, c.options)
	if err != nil {
		return Report{readFailure("R06", id, "Microsoft.ContainerRegistry/registries/read", err)}
	}
	registry, err := client.get(ctx, id)
	if err != nil {
		return Report{readFailure("R06", id, "Microsoft.ContainerRegistry/registries/read", err)}
	}
	p := registry.Properties
	if p == nil {
		base.Missing = "prepared registry properties"
		return Report{base}
	}
	readiness := resourceReadiness(id, resourceValue(registry.ID), resourceValue(p.ProvisioningState), "registries/read")
	readiness.ID = "R06"
	if readiness.Status != Pass {
		return Report{readiness}
	}
	hosts := []string{resourceValue(p.LoginServer)}
	if resourceBool(p.DataEndpointEnabled) {
		if len(p.DataEndpointHostNames) == 0 {
			base.Missing = "all documented dedicated data endpoint hostnames for the enabled registry"
			return Report{base}
		}
		for _, host := range p.DataEndpointHostNames {
			hosts = append(hosts, resourceValue(host))
		}
	}
	private := resourceValue(p.PublicNetworkAccess) == "Disabled" ||
		p.NetworkRuleSet != nil && resourceValue(p.NetworkRuleSet.DefaultAction) == "Deny"
	var report Report
	for _, host := range slices.Compact(hosts) {
		endpoint := registryNetworkEndpoint(host)
		check := base
		if endpoint == "" {
			check.Missing = "valid credential-free registry endpoint hostname"
		} else {
			check.Observed += "; endpoint=" + endpoint
			check = networkEvidenceCheck(ctx, c.evidence.Network, NetworkRequest{
				CheckID: "R06", Scope: id, SourceSubnetIDs: sources, Destination: endpoint,
			}, check, private)
		}
		report = append(report, check)
	}
	return report
}
