// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
)

const roleAssignmentWrite = "Microsoft.Authorization/roleAssignments/write"

type grantRequest struct {
	checkID, scope, principalID, roleName, roleID string
}

func grantRequests(p Plan, hosting, workload Identity) []grantRequest {
	return []grantRequest{
		{"G01", p.Cluster.ID, hosting.PrincipalID, "Reader", "acdd72a7-3385-48ef-bd42-f606fba81ae7"},
		{"G01", p.Cluster.ID, hosting.PrincipalID,
			"Kubernetes Extension Contributor", "85cb6faf-e071-4c9b-8136-154b5a04f717"},
		{"G02", p.Workload.ID, hosting.PrincipalID,
			"Managed Identity Federated Identity Credential Contributor", ""},
		{"G03", p.Storage.ID, workload.PrincipalID,
			"Storage Blob Data Contributor", "ba92f5b4-2d11-453d-a403-e96b0029c9fe"},
	}
}

// GrantCheckIDForRole classifies authored assignments using the existing required grant-role definitions.
func GrantCheckIDForRole(roleID string) string {
	for _, request := range grantRequests(Plan{}, Identity{}, Identity{}) {
		if request.roleID != "" && strings.EqualFold(request.roleID, roleID) {
			return request.checkID
		}
	}
	return ""
}

func createsGrant(plan Plan, id string) bool {
	if plan.Grants != nil {
		for _, grant := range plan.Grants {
			if grant.CheckID == id {
				return true
			}
		}
		return false
	}
	switch id {
	case "G01":
		return !plan.Hosting.Supplied
	case "G02":
		return !plan.Hosting.Supplied || !plan.Workload.Supplied
	case "G03":
		return !plan.Workload.Supplied || !plan.Storage.Supplied
	default:
		return false
	}
}

func authString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func scopeContains(parent, child string) bool {
	parent = strings.ToLower(strings.TrimRight(parent, "/"))
	child = strings.ToLower(strings.TrimRight(child, "/"))
	return parent != "" && (parent == child || strings.HasPrefix(child, parent+"/"))
}

// ARM operation wildcards are not filesystem globs: '*' also matches separators.
func actionMatches(pattern, action string) bool {
	pattern = regexp.QuoteMeta(strings.ToLower(pattern))
	pattern = strings.ReplaceAll(pattern, `\*`, ".*")
	matched, _ := regexp.MatchString("^"+pattern+"$", strings.ToLower(action))
	return matched
}

func anyAction(patterns []*string, action string) bool {
	for _, p := range patterns {
		if p != nil && actionMatches(*p, action) {
			return true
		}
	}
	return false
}

func permissionAllows(permissions []*armauthorization.Permission, action string) bool {
	for _, p := range permissions {
		if p != nil && anyAction(p.Actions, action) && !anyAction(p.NotActions, action) {
			return true
		}
	}
	return false
}

type callerIdentity struct {
	objectID     string
	groups       map[string]bool
	groupOverage bool
}

func identityFromToken(token string) (callerIdentity, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return callerIdentity{}, fmt.Errorf("operator token has no supported object-ID evidence")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return callerIdentity{}, fmt.Errorf("decode operator identity evidence: %w", err)
	}
	var claims struct {
		ObjectID   string            `json:"oid"`
		Groups     []string          `json:"groups"`
		HasGroups  bool              `json:"hasgroups"`
		ClaimNames map[string]string `json:"_claim_names"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil || claims.ObjectID == "" {
		return callerIdentity{}, fmt.Errorf("operator token has no supported object-ID evidence")
	}
	caller := callerIdentity{
		objectID: claims.ObjectID, groups: map[string]bool{},
		groupOverage: claims.HasGroups || claims.ClaimNames["groups"] != "",
	}
	for _, g := range claims.Groups {
		caller.groups[strings.ToLower(g)] = true
	}
	return caller, nil
}

func (c callerIdentity) includes(id string) bool {
	return strings.EqualFold(c.objectID, id) || c.groups[strings.ToLower(id)]
}
