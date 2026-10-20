// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreparationGrantRecoveryRequiresMatchingOwnershipOnEveryReadPath(t *testing.T) {
	for _, listed := range []bool{false, true} {
		for _, test := range []struct {
			name        string
			description string
			mutate      func(*OwnedAssignment)
			untracked   bool
			failSave    bool
			wantError   string
		}{
			{name: "recover interrupted owned grant", description: "owner"},
			{name: "reject changed ownership", description: "another-owner", wantError: "ownership changed"},
			{name: "reject missing ownership", wantError: "ownership changed"},
			{name: "reject changed recorded ID", description: "owner",
				mutate: func(r *OwnedAssignment) { r.ID += "-changed" }, wantError: "ownership changed"},
			{name: "reject changed recorded scope", description: "owner",
				mutate: func(r *OwnedAssignment) { r.Scope += "-changed" }, wantError: "ownership changed"},
			{name: "reject changed recorded principal", description: "owner",
				mutate:    func(r *OwnedAssignment) { r.PrincipalID = "00000000-0000-0000-0000-000000000099" },
				wantError: "ownership changed"},
			{name: "reject changed recorded role", description: "owner",
				mutate: func(r *OwnedAssignment) { r.RoleID += "-changed" }, wantError: "ownership changed"},
			{name: "reuse customer grant without claiming ownership", description: "customer", untracked: true},
			{name: "surface recovery persistence failure", description: "owner", failSave: true,
				wantError: "journal unavailable"},
		} {
			path := "get-only"
			if listed {
				path = "list-visible"
			}
			t.Run(path+"/"+test.name, func(t *testing.T) {
				plan, err := BuildPlan(contractPlanInput())
				require.NoError(t, err)
				grant := grantRequest{
					checkID: "G03", scope: plan.Storage.ID,
					principalID: "00000000-0000-0000-0000-000000000001",
					roleID: "/providers/Microsoft.Authorization/roleDefinitions/" +
						"ba92f5b4-2d11-453d-a403-e96b0029c9fe",
				}
				name := stableAssignmentName(grant.scope, grant.principalID, grant.roleID)
				assignmentID := grant.scope + "/providers/Microsoft.Authorization/roleAssignments/" + name
				record := OwnedAssignment{
					ID: assignmentID, Scope: grant.scope, PrincipalID: grant.principalID, RoleID: grant.roleID,
				}
				if test.mutate != nil {
					test.mutate(&record)
				}
				state := PreparationState{Owner: "owner", Assignments: map[string]OwnedAssignment{}}
				if !test.untracked {
					state.Assignments[name] = record
				}
				journal := &memoryPreparationJournal{}
				require.NoError(t, journal.Save(t.Context(), state))
				journal.fail = test.failSave
				observed := map[string]any{
					"id": assignmentID,
					"properties": map[string]any{
						"scope": grant.scope, "principalId": grant.principalID,
						"principalType": "ServicePrincipal", "roleDefinitionId": grant.roleID,
						"description": test.description,
					},
				}
				var gets int
				options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
					require.Equal(t, http.MethodGet, request.Method, "recovering a grant must not recreate or retag it")
					gets++
					var body any = observed
					if strings.HasSuffix(strings.ToLower(request.URL.Path), "/roleassignments") {
						values := []any{}
						if listed {
							values = append(values, observed)
						}
						body = map[string]any{"value": values}
					} else {
						require.Equal(t, strings.ToLower(assignmentID), strings.ToLower(request.URL.Path))
					}
					data, err := json.Marshal(body)
					require.NoError(t, err)
					return contractARMResponse(request, http.StatusOK, string(data)), nil
				})
				preparer := ownedPreparer{credential: contractCredential{}, options: options, journal: journal}
				err = preparer.prepareGrant(t.Context(), &state, grant)
				if test.wantError != "" {
					require.ErrorContains(t, err, test.wantError)
				} else {
					require.NoError(t, err)
				}
				if listed {
					assert.Equal(t, 1, gets)
				} else {
					assert.Equal(t, 2, gets)
				}
				persisted, err := journal.Load(t.Context())
				require.NoError(t, err)
				if test.untracked {
					assert.Empty(t, persisted.Assignments)
				} else {
					assert.Equal(t, test.wantError == "", persisted.Assignments[name].Complete)
				}
			})
		}
	}
}
