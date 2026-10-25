// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"

func storageNetworkChecks(plan Plan, storage *armstorage.Account) Report {
	check := resourceCheck("S03", plan.Storage.ID, "supplied Storage matches the public static network baseline")
	if !plan.Storage.Supplied {
		return Report{resourceResult(check, NotApplicable,
			"S03 applies only to supplied Storage; new Storage checks are skipped before and after deployment")}
	}
	if storage == nil || storage.Properties == nil || storage.Properties.PrimaryEndpoints == nil {
		return Report{check}
	}
	p := storage.Properties
	endpoint := safeResourceEndpoint(resourceValue(p.PrimaryEndpoints.Blob))
	access := resourceValue(p.PublicNetworkAccess)
	action := ""
	if p.NetworkRuleSet != nil {
		action = resourceValue(p.NetworkRuleSet.DefaultAction)
	}
	check.Observed = "Blob endpoint=" + endpoint + "; publicNetworkAccess=" + access + "; defaultAction=" + action
	if endpoint == "" || access != "Enabled" && access != "Disabled" || action != "Allow" && action != "Deny" {
		check.Missing = "valid HTTPS Blob endpoint and explicit public/firewall access settings"
		return Report{check}
	}
	if access == "Enabled" && action == "Allow" {
		return Report{resourceResult(check, Pass, check.Observed+"; public static baseline, not actual connectivity")}
	}
	check = resourceResult(check, NotApplicable, check.Observed+"; restricted/private network path verification skipped")
	check.Mandatory = false
	check.Warning = "Restricted/private Storage network connectivity was not verified; " +
		"S03 is skipped and does not block provisioning."
	return Report{check}
}
