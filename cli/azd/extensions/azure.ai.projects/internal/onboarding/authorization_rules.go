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

func grantRequests(p Plan, f Facts, hosting, workload Identity) []grantRequest {
	registryRole := "AcrPull"
	if f.RegistryABAC {
		registryRole = "Container Registry Repository Reader"
	}
	return []grantRequest{
		{"G01", p.Cluster.ID, hosting.PrincipalID, "Reader", "acdd72a7-3385-48ef-bd42-f606fba81ae7"},
		{"G01", p.Cluster.ID, hosting.PrincipalID,
			"Kubernetes Extension Contributor", "85cb6faf-e071-4c9b-8136-154b5a04f717"},
		{"G02", p.Workload.ID, hosting.PrincipalID,
			"Managed Identity Federated Identity Credential Contributor", ""},
		{"G03", p.Storage.ID, workload.PrincipalID,
			"Storage Blob Data Contributor", "ba92f5b4-2d11-453d-a403-e96b0029c9fe"},
		{"G04", p.Registry.ID, f.KubeletObjectID, registryRole, registryRoleID(f.RegistryABAC)},
	}
}

func registryRoleID(abac bool) string {
	if abac {
		return "b93aa761-3e63-49ed-ac28-beffa264f7ac"
	}
	return "7f951dda-4ed3-4680-a7ca-43fe172d538d"
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
	objectID string
	groups   map[string]bool
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
		ObjectID string   `json:"oid"`
		Groups   []string `json:"groups"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil || claims.ObjectID == "" {
		return callerIdentity{}, fmt.Errorf("operator token has no supported object-ID evidence")
	}
	caller := callerIdentity{objectID: claims.ObjectID, groups: map[string]bool{}}
	for _, g := range claims.Groups {
		caller.groups[strings.ToLower(g)] = true
	}
	return caller, nil
}

func (c callerIdentity) includes(id string) bool {
	return strings.EqualFold(c.objectID, id) || c.groups[strings.ToLower(id)]
}

func evaluateDeny(
	caller callerIdentity, op Operation, denies []*armauthorization.DenyAssignment,
) (Status, string) {
	for _, d := range denies {
		if d == nil || d.Properties == nil {
			return Unverified, "deny assignment evidence is incomplete"
		}
		p := d.Properties
		if p.Scope == nil {
			return Unverified, "deny assignment scope is unavailable"
		}
		if !scopeContains(*p.Scope, op.Scope) {
			continue
		}
		if p.DoNotApplyToChildScopes != nil && *p.DoNotApplyToChildScopes &&
			!strings.EqualFold(*p.Scope, op.Scope) {
			continue
		}
		possible := false
		for _, permission := range p.Permissions {
			if permission == nil {
				return Unverified, "deny permissions are unavailable"
			}
			if anyAction(permission.Actions, op.Action) && !anyAction(permission.NotActions, op.Action) {
				possible = true
			}
		}
		if !possible {
			continue
		}
		excluded, uncertain := false, false
		for _, principal := range p.ExcludePrincipals {
			if principal == nil || principal.ID == nil {
				uncertain = true
			} else if caller.includes(*principal.ID) {
				excluded = true
			} else if strings.EqualFold(authString(principal.Type), "Group") || authString(principal.Type) == "" {
				uncertain = true
			}
		}
		if excluded {
			continue
		}
		applies := false
		for _, principal := range p.Principals {
			if principal == nil || principal.ID == nil {
				uncertain = true
			} else if caller.includes(*principal.ID) || *principal.ID == "00000000-0000-0000-0000-000000000000" {
				applies = true
			} else if strings.EqualFold(authString(principal.Type), "Group") || authString(principal.Type) == "" {
				uncertain = true
			}
		}
		if uncertain || len(p.Principals) == 0 {
			return Unverified, "deny group membership or exclusion evidence is incomplete"
		}
		if applies {
			for _, permission := range p.Permissions {
				if anyAction(permission.Actions, op.Action) && !anyAction(permission.NotActions, op.Action) {
					if authString(permission.Condition) != "" {
						return Unverified, "conditional deny evaluation is unsupported"
					}
					return Fail, "an applicable deny assignment blocks " + op.Action
				}
			}
		}
	}
	return Pass, ""
}

func delegationCondition(condition string, op Operation) (Status, string) {
	if strings.TrimSpace(condition) == "" {
		return Pass, ""
	}
	status, err := evaluateDelegation(condition, op)
	if err != nil {
		return Unverified, "unsupported delegation expression; administrator must supply supported role/recipient conditions"
	}
	switch status {
	case Pass:
		return Pass, "supported role/recipient delegation condition permits the planned grant"
	case Fail:
		return Fail, "delegation condition does not permit the planned role/recipient"
	default:
		return Unverified, "delegation requires unavailable recipient or unsupported attribute evidence"
	}
}
