// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
)

const authoredBootstrapRule = "bootstrap-local-ip"

func (r *firewallReconciler) PrepareAutomatic(
	ctx context.Context, binding *deploymentBinding,
) (func(context.Context) error, error) {
	identity, err := firewallIdentityFromValues(binding.values)
	if err != nil {
		return nil, err
	}
	for _, resourceID := range []string{binding.values[postgresServerIDEnv], binding.values[containerAppIDEnv]} {
		if err := binding.requireResource(resourceID); err != nil {
			return nil, err
		}
	}
	credential, err := credentialForSubscription(ctx, r.client, identity.subscriptionID)
	if err != nil {
		return nil, err
	}
	client, err := armpostgresqlflexibleservers.NewFirewallRulesClient(identity.subscriptionID, credential, nil)
	if err != nil {
		return nil, err
	}
	return prepareAutomaticFirewall(ctx, client, identity, binding)
}

func prepareAutomaticFirewall(
	ctx context.Context,
	client *armpostgresqlflexibleservers.FirewallRulesClient,
	identity *firewallIdentity,
	binding *deploymentBinding,
) (func(context.Context) error, error) {
	serverID := binding.values[postgresServerIDEnv]
	bootstrapID := serverID + "/firewallRules/" + authoredBootstrapRule
	var cleanup func(context.Context) error
	if binding.resources[strings.ToLower(bootstrapID)] {
		cleanup = func(ctx context.Context) error {
			rule, err := client.Get(ctx, identity.resourceGroup, identity.postgresServerName, authoredBootstrapRule, nil)
			if isAzureNotFound(err) {
				return nil
			}
			if err != nil {
				return nonDisclosureError(err)
			}
			if rule.ID == nil || !strings.EqualFold(*rule.ID, bootstrapID) || !isAllPublicIPv4Rule(rule.Properties) {
				return configurationError("the owned bootstrap rule changed; its deletion cannot be authorized")
			}
			if err := deleteBootstrapFirewallRule(
				ctx, client, identity.resourceGroup, identity.postgresServerName, authoredBootstrapRule,
			); err != nil {
				return nonDisclosureError(err)
			}
			_, err = client.Get(ctx, identity.resourceGroup, identity.postgresServerName, authoredBootstrapRule, nil)
			if !isAzureNotFound(err) {
				return errors.Join(configurationError("the owned bootstrap firewall rule absence could not be verified"),
					nonDisclosureError(err))
			}
			return nil
		}
	}
	rules := map[string]*armpostgresqlflexibleservers.FirewallRule{}
	pager := client.NewListByServerPager(identity.resourceGroup, identity.postgresServerName, nil)
	pages := 0
	for pager.More() {
		pages++
		if pages > 128 {
			return cleanup, configurationError("the firewall inventory exceeded its bounded page limit")
		}
		page, err := pager.NextPage(ctx)
		if err != nil {
			return cleanup, nonDisclosureError(err)
		}
		for _, rule := range page.Value {
			if rule == nil || rule.Name == nil || rule.ID == nil {
				return cleanup, configurationError("the firewall inventory contains an unverifiable rule")
			}
			key := strings.ToLower(*rule.Name)
			if rules[key] != nil || len(rules) >= 5000 ||
				!strings.EqualFold(*rule.ID, serverID+"/firewallRules/"+*rule.Name) {
				return cleanup, configurationError("the firewall inventory contains ambiguous or excessive rule identities")
			}
			rules[key] = rule
			if isAllPublicIPv4Rule(rule.Properties) && (key != authoredBootstrapRule || cleanup == nil) {
				return cleanup, configurationError("unowned unrestricted PostgreSQL access must be resolved explicitly")
			}
		}
	}
	return cleanup, validateAutomaticRuntimeFirewall(binding, rules)
}

func validateAutomaticRuntimeFirewall(
	binding *deploymentBinding, rules map[string]*armpostgresqlflexibleservers.FirewallRule,
) error {
	if binding.app == nil {
		return configurationError("the current application is unavailable for runtime firewall verification")
	}
	actual, err := normalizeContainerAppOutboundIPs(binding.app.Properties)
	if err != nil {
		return err
	}
	output, present, err := findDeploymentOutput(binding.outputs, "DATABASE_OUTBOUND_IPS")
	if err != nil {
		return err
	}
	raw, valid := output.Value.(string)
	if !present || !valid || !strings.EqualFold(output.Type, "string") || len(raw) > 65536 ||
		raw != binding.values["DATABASE_OUTBOUND_IPS"] {
		return configurationError("the current deployment did not establish the runtime outbound address inventory")
	}
	var emitted []*string
	if err := json.Unmarshal([]byte(raw), &emitted); err != nil {
		return configurationError("the current outbound address inventory is not a JSON IPv4 array")
	}
	expected, err := normalizeOutboundIPs(emitted)
	if err != nil {
		return err
	}
	if !slices.Equal(actual, expected) {
		return configurationError("the Container App outbound addresses changed; reprovision its runtime firewall rules")
	}
	for _, ip := range actual {
		name := "aca-" + strings.ReplaceAll(ip, ".", "-")
		rule := rules[name]
		if rule == nil || rule.ID == nil || rule.Properties == nil || rule.Properties.StartIPAddress == nil ||
			rule.Properties.EndIPAddress == nil ||
			*rule.Properties.StartIPAddress != ip || *rule.Properties.EndIPAddress != ip {
			return configurationError(
				"a required exact runtime firewall rule is missing or changed; reprovision the environment",
			)
		}
		if err := binding.requireResource(*rule.ID); err != nil {
			return err
		}
	}
	binding.outboundIPs = append([]string(nil), actual...)
	return nil
}

func (r *Runtime) beginAutomaticNetwork(ctx context.Context) (func() error, error) {
	binding, present := ctx.Value(deploymentBindingKey{}).(*deploymentBinding)
	if !present || !binding.automatic || binding.values[postgresServerIDEnv] == "" {
		return func() error { return nil }, nil
	}
	if r.prepareNetwork == nil {
		return func() error { return nil }, errors.New("automatic database network verification is unavailable")
	}
	cleanup, err := r.prepareNetwork(ctx, binding)
	closeNetwork := sync.OnceValue(func() error {
		if cleanup == nil {
			return nil
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), postgresCleanupTimeout)
		defer cancel()
		return cleanup(cleanupCtx)
	})
	return closeNetwork, err
}
