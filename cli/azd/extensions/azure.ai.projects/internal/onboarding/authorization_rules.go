// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import "strings"

type grantRequest struct {
	checkID, scope, principalID, roleName, roleID string
}

func grantRequests(p Plan, hosting, workload Identity) []grantRequest {
	return []grantRequest{
		{"hosting-aks", p.Cluster.ID, hosting.PrincipalID, "Reader", "acdd72a7-3385-48ef-bd42-f606fba81ae7"},
		{"hosting-aks", p.Cluster.ID, hosting.PrincipalID,
			"Kubernetes Extension Contributor", "85cb6faf-e071-4c9b-8136-154b5a04f717"},
		{"hosting-federation", p.Workload.ID, hosting.PrincipalID,
			"Managed Identity Federated Identity Credential Contributor", "7e559ce2-48d7-4b27-9128-fa1b247f1308"},
		{"workload-blob", p.Storage.ID, workload.PrincipalID,
			"Storage Blob Data Contributor", "ba92f5b4-2d11-453d-a403-e96b0029c9fe"},
	}
}

func authString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func roleGUID(id string) string {
	return strings.ToLower(id[strings.LastIndex(id, "/")+1:])
}
