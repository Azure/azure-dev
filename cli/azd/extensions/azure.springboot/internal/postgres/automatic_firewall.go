// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package postgres

import (
	"azurespringboot/internal/deploy"
	"azurespringboot/internal/springmodel"
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"

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
	appID := strings.TrimSpace(values[springmodel.ContainerAppIDEnv])
	postgresID := strings.TrimSpace(values[springmodel.PostgresServerIDEnv])
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
	if outputName := values[springmodel.PostgresServerNameEnv]; outputName != "" &&
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
	ctx context.Context, binding *deploy.DeploymentBinding,
) (func(context.Context) error, error) {
	identity, err := firewallIdentityFromValues(binding.Values)
	if err != nil {
		return nil, err
	}
	required := []string{
		binding.Values[springmodel.PostgresServerIDEnv],
		binding.Values[springmodel.ContainerAppIDEnv],
	}
	for _, resourceID := range required {
		if err := binding.RequireResource(resourceID); err != nil {
			return nil, err
		}
	}
	credential, err := deploy.CredentialForSubscription(ctx, r.client, identity.subscriptionID)
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
	binding *deploy.DeploymentBinding,
) (func(context.Context) error, error) {
	serverID := binding.Values[springmodel.PostgresServerIDEnv]
	bootstrapID := serverID + "/firewallRules/" + authoredBootstrapRule
	var cleanup func(context.Context) error
	if binding.Resources[strings.ToLower(bootstrapID)] {
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
	binding *deploy.DeploymentBinding, rules map[string]*armpostgresqlflexibleservers.FirewallRule,
) error {
	if binding.App == nil {
		return configurationError("the current application is unavailable for runtime firewall verification")
	}
	actual, err := normalizeContainerAppOutboundIPs(binding.App.Properties)
	if err != nil {
		return err
	}
	output, present, err := deploy.FindDeploymentOutput(binding.Outputs, "DATABASE_OUTBOUND_IPS")
	if err != nil {
		return err
	}
	raw, valid := output.Value.(string)
	if !present || !valid || !strings.EqualFold(output.Type, "string") || len(raw) > 65536 ||
		raw != binding.Values["DATABASE_OUTBOUND_IPS"] {
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
		if err := binding.RequireResource(*rule.ID); err != nil {
			return err
		}
	}
	binding.OutboundIPs = append([]string(nil), actual...)
	return nil
}
