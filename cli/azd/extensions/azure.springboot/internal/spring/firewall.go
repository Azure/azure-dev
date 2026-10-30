// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type firewallRule struct {
	name string
	ip   string
}

type firewallPlan struct {
	add    []firewallRule
	remove []string
}

type firewallReconciler struct {
	client *azdext.AzdClient
}

func newFirewallReconciler(client *azdext.AzdClient) *firewallReconciler {
	return &firewallReconciler{client: client}
}

func (r *firewallReconciler) Reconcile(ctx context.Context, values map[string]string) error {
	identity, err := firewallIdentityFromValues(values)
	if err != nil {
		return err
	}
	tenant, err := r.client.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: identity.subscriptionID,
	})
	if err != nil {
		return fmt.Errorf("looking up the user access tenant for the selected subscription: %w", err)
	}
	if tenant.GetTenantId() == "" {
		return errorsNoTenant()
	}
	credential, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   tenant.GetTenantId(),
			AdditionallyAllowedTenants: []string{"*"},
		},
	)
	if err != nil {
		return fmt.Errorf("creating the azd Azure credential: %w", err)
	}

	apps, err := armappcontainers.NewContainerAppsClient(identity.subscriptionID, credential, nil)
	if err != nil {
		return fmt.Errorf("creating Container Apps client: %w", err)
	}
	app, err := apps.Get(ctx, identity.resourceGroup, identity.containerAppName, nil)
	if err != nil {
		return fmt.Errorf("reading the provisioned Container App: %w", err)
	}
	ips, err := normalizeContainerAppOutboundIPs(app.Properties)
	if err != nil {
		return err
	}

	firewalls, err := armpostgresqlflexibleservers.NewFirewallRulesClient(
		identity.subscriptionID,
		credential,
		nil,
	)
	if err != nil {
		return fmt.Errorf("creating PostgreSQL firewall client: %w", err)
	}
	existing := make([]firewallRule, 0)
	pager := firewalls.NewListByServerPager(identity.resourceGroup, identity.postgresServerName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("listing PostgreSQL firewall rules: %w", err)
		}
		for _, rule := range page.Value {
			if rule == nil || rule.Name == nil {
				continue
			}
			ip := ""
			if rule.Properties != nil &&
				rule.Properties.StartIPAddress != nil &&
				rule.Properties.EndIPAddress != nil {
				ip = *rule.Properties.StartIPAddress
				if *rule.Properties.StartIPAddress != *rule.Properties.EndIPAddress {
					ip += "-" + *rule.Properties.EndIPAddress
				}
			}
			existing = append(existing, firewallRule{name: *rule.Name, ip: ip})
		}
	}

	plan := planFirewallRules(identity.containerAppName, ips, existing)
	for _, rule := range plan.add {
		poller, err := firewalls.BeginCreateOrUpdate(
			ctx,
			identity.resourceGroup,
			identity.postgresServerName,
			rule.name,
			armpostgresqlflexibleservers.FirewallRule{
				Properties: &armpostgresqlflexibleservers.FirewallRuleProperties{
					StartIPAddress: new(rule.ip),
					EndIPAddress:   new(rule.ip),
				},
			},
			nil,
		)
		if err != nil {
			return fmt.Errorf("adding an owned PostgreSQL firewall rule: %w", err)
		}
		if _, err := poller.PollUntilDone(ctx, nil); err != nil {
			return fmt.Errorf("waiting for an owned PostgreSQL firewall rule: %w", err)
		}
	}
	for _, name := range plan.remove {
		poller, err := firewalls.BeginDelete(
			ctx,
			identity.resourceGroup,
			identity.postgresServerName,
			name,
			nil,
		)
		if err != nil {
			return fmt.Errorf("deleting a stale owned PostgreSQL firewall rule: %w", err)
		}
		if _, err := poller.PollUntilDone(ctx, nil); err != nil {
			return fmt.Errorf("waiting to delete a stale owned PostgreSQL firewall rule: %w", err)
		}
	}
	return nil
}

type firewallIdentity struct {
	subscriptionID     string
	resourceGroup      string
	containerAppName   string
	postgresServerName string
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

func ownedFirewallPrefix(appName string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(appName)))
	return "azd-springboot-" + hex.EncodeToString(sum[:4]) + "-"
}

func planFirewallRules(appName string, ips []string, existing []firewallRule) firewallPlan {
	prefix := ownedFirewallPrefix(appName)
	desired := make(map[string]string, len(ips))
	for _, ip := range ips {
		desired[prefix+strings.ReplaceAll(ip, ".", "-")] = ip
	}
	current := make(map[string]string, len(existing))
	for _, rule := range existing {
		current[rule.name] = rule.ip
	}

	plan := firewallPlan{}
	for name, ip := range desired {
		if current[name] != ip {
			plan.add = append(plan.add, firewallRule{name: name, ip: ip})
		}
	}
	for name := range current {
		if strings.HasPrefix(name, prefix) {
			if _, wanted := desired[name]; !wanted {
				plan.remove = append(plan.remove, name)
			}
		}
	}
	slices.SortFunc(plan.add, func(left, right firewallRule) int {
		return strings.Compare(left.name, right.name)
	})
	slices.Sort(plan.remove)
	return plan
}

func errorsNoTenant() error {
	return validationError(
		"missing_user_access_tenant",
		"azd did not return a user access tenant for the selected subscription",
		"run azd auth login for an account that can access the subscription",
	)
}
