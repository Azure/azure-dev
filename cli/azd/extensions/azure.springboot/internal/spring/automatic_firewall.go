// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const authoredBootstrapRule = "bootstrap-local-ip"

type firewallReconciler struct {
	client *azdext.AzdClient
}

func newFirewallReconciler(client *azdext.AzdClient) *firewallReconciler {
	return &firewallReconciler{client: client}
}

type firewallIdentity struct {
	subscriptionID     string
	resourceGroup      string
	containerAppName   string
	postgresServerName string
}

func isAllPublicIPv4Rule(properties *armpostgresqlflexibleservers.FirewallRuleProperties) bool {
	return properties != nil && properties.StartIPAddress != nil && properties.EndIPAddress != nil &&
		*properties.StartIPAddress == "0.0.0.0" && *properties.EndIPAddress == "255.255.255.255"
}

func firewallIdentityFromValues(values map[string]string) (*firewallIdentity, error) {
	appID := strings.TrimSpace(values[containerAppIDEnv])
	postgresID := strings.TrimSpace(values[postgresServerIDEnv])
	if appID == "" || postgresID == "" {
		return nil, validationError(
			"missing_provisioned_resource_identity",
			"the provisioned Container App or PostgreSQL resource ID is unavailable",
			"run azd provision successfully before deploying the PostgreSQL application",
		)
	}
	app, err := arm.ParseResourceID(appID)
	if err != nil {
		return nil, validationError(
			"invalid_container_app_resource_id",
			"the provisioned Container App resource ID is invalid",
			"reprovision the environment to restore its deployment outputs",
		)
	}
	postgres, err := arm.ParseResourceID(postgresID)
	if err != nil {
		return nil, validationError(
			"invalid_postgres_resource_id",
			"the provisioned PostgreSQL resource ID is invalid",
			"reprovision the environment to restore its deployment outputs",
		)
	}
	if !strings.EqualFold(app.ResourceType.Namespace, "Microsoft.App") ||
		!strings.EqualFold(app.ResourceType.Type, "containerApps") {
		return nil, validationError(
			"invalid_container_app_resource_id",
			"the provisioned application resource is not a Container App",
			"reprovision the environment to restore its deployment outputs",
		)
	}
	if !strings.EqualFold(postgres.ResourceType.Namespace, "Microsoft.DBforPostgreSQL") ||
		!strings.EqualFold(postgres.ResourceType.Type, "flexibleServers") {
		return nil, validationError(
			"invalid_postgres_resource_id",
			"the provisioned database resource is not a PostgreSQL Flexible Server",
			"reprovision the environment to restore its deployment outputs",
		)
	}
	if !strings.EqualFold(app.SubscriptionID, postgres.SubscriptionID) ||
		!strings.EqualFold(app.ResourceGroupName, postgres.ResourceGroupName) {
		return nil, validationError(
			"mismatched_provisioned_resources",
			"the provisioned Container App and PostgreSQL server are not in the same subscription and resource group",
			"reprovision the environment using the authored Spring infrastructure",
		)
	}
	if outputName := values[postgresServerNameEnv]; outputName != "" &&
		!strings.EqualFold(outputName, postgres.Name) {
		return nil, validationError(
			"mismatched_postgres_identity",
			"the persisted PostgreSQL server name does not match its resource ID",
			"reprovision the environment to restore consistent deployment outputs",
		)
	}
	return &firewallIdentity{
		subscriptionID:     app.SubscriptionID,
		resourceGroup:      app.ResourceGroupName,
		containerAppName:   app.Name,
		postgresServerName: postgres.Name,
	}, nil
}

func normalizeContainerAppOutboundIPs(properties *armappcontainers.ContainerAppProperties) ([]string, error) {
	if properties == nil {
		return normalizeOutboundIPs(nil)
	}
	return normalizeOutboundIPs(properties.OutboundIPAddresses)
}

func normalizeOutboundIPs(values []*string) ([]string, error) {
	unique := map[string]struct{}{}
	for _, value := range values {
		if value == nil {
			continue
		}
		ip := strings.TrimSpace(*value)
		parsed := net.ParseIP(ip)
		if parsed == nil || parsed.To4() == nil || strings.Contains(ip, ":") {
			return nil, validationError(
				"invalid_container_app_outbound_ip",
				"Container Apps returned an invalid or unsupported outbound IP address",
				"retry after the Container App reports its IPv4 outbound addresses",
			)
		}
		unique[parsed.To4().String()] = struct{}{}
	}
	ips := make([]string, 0, len(unique))
	for ip := range unique {
		ips = append(ips, ip)
	}
	slices.Sort(ips)
	if len(ips) == 0 {
		return nil, validationError(
			"missing_container_app_outbound_ips",
			"the provisioned Container App reports no outbound IP addresses",
			"retry provision or deploy after Container Apps publishes its outbound addresses",
		)
	}
	return ips, nil
}

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
