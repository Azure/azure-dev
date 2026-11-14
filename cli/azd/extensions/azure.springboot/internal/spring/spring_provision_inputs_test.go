// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestAutomaticProvisionInputsValidateAuthoredBindings(t *testing.T) {
	for _, test := range []struct {
		name        string
		document    string
		h2          bool
		noDatabase  bool
		managed     bool
		wantManaged bool
		wantError   bool
	}{
		{name: "exact password", document: `{"parameters":{"databasePassword":{"value":"${SPRING_DATABASE_PASSWORD}"}}}`},
		{name: "empty fallback password",
			document: `{"parameters":{"databasePassword":{"value":"${SPRING_DATABASE_PASSWORD:-}"}}}`},
		{name: "exact managed identity", document: `{"parameters":{
			"databaseAdministratorName":{"value":"${DATABASE_ADMIN_NAME}"},
			"databaseAdministratorObjectId":{"value":"${DATABASE_ADMIN_OBJECT_ID}"}}}`, wantManaged: true},
		{name: "deferred managed identity", document: `{"parameters":{
			"databaseAdministratorName":{"value":"${DATABASE_ADMIN_NAME=azd-preprovision-pending}"},
			"databaseAdministratorObjectId":{
				"value":"${DATABASE_ADMIN_OBJECT_ID=00000000-0000-0000-0000-000000000000}"}}}`,
			wantManaged: true},
		{name: "mixed managed identity bindings", document: `{"parameters":{
			"databaseAdministratorName":{"value":"${DATABASE_ADMIN_NAME}"},
			"databaseAdministratorObjectId":{
				"value":"${DATABASE_ADMIN_OBJECT_ID=00000000-0000-0000-0000-000000000000}"}}}`,
			wantError: true},
		{name: "nonstring password", document: `{"parameters":{"databasePassword":{"value":123}}}`, wantError: true},
		{name: "flattened password", document: `{"parameters":{"databasePassword":"${SPRING_DATABASE_PASSWORD}"}}`,
			wantError: true},
		{name: "partial administrator", document: `{"parameters":{
			"databaseAdministratorName":{"value":"${DATABASE_ADMIN_NAME}"}}}`, wantError: true},
		{name: "conflicting bootstrap bindings", document: `{"parameters":{
			"databaseAdministratorName":{"value":"${DATABASE_ADMIN_NAME}"},
			"databaseAdministratorObjectId":{"value":"${DATABASE_ADMIN_OBJECT_ID}"},
			"databasePassword":{"value":"${SPRING_DATABASE_PASSWORD}"}}}`, wantError: true},
		{name: "MI app with password bootstrap",
			document: `{"parameters":{"databasePassword":{"value":"${SPRING_DATABASE_PASSWORD:-}"}}}`,
			managed:  true, wantError: true},
		{name: "malformed JSON", document: "{", wantError: true},
		{name: "PostgreSQL without parameters", wantError: true},
		{name: "positive H2 without parameters", h2: true},
		{name: "database-free without parameters", noDatabase: true},
		{name: "database-free generic parameters", noDatabase: true,
			document: `{"parameters":{"location":{"value":"${AZURE_LOCATION}"}}}`},
		{name: "database-free rejects bootstrap", noDatabase: true, wantError: true,
			document: `{"parameters":{"databasePassword":{"value":"${SPRING_DATABASE_PASSWORD}"}}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := &azdext.ProjectConfig{Path: t.TempDir()}
			if test.document != "" {
				require.NoError(t, os.MkdirAll(filepath.Join(project.Path, "infra"), 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(project.Path, "infra", "main.parameters.json"),
					[]byte(test.document), 0o600))
			}
			selected := resolvedDatabase{Type: databasePostgreSQL, Authentication: databaseAuthenticationPassword}
			if test.h2 {
				selected.Type = databaseH2
			}
			if test.noDatabase {
				selected.Type = databaseNone
			}
			if test.managed {
				selected.Authentication = databaseAuthenticationManagedIdentity
			}
			actual, err := automaticProvisionInputs(t.Context(), project, selected)
			if test.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, selected.Type, actual.Type)
			if test.wantManaged {
				require.Equal(t, databaseAuthenticationManagedIdentity, actual.Authentication)
			} else {
				require.Equal(t, selected.Authentication, actual.Authentication)
			}
		})
	}
}

func TestAutomaticProvisionInputsRejectTraversal(t *testing.T) {
	for _, infra := range []*azdext.InfraOptions{
		{Path: "..", Module: "main"},
		{Path: "infra", Module: "../../outside"},
		{Path: "file:infra", Module: "main"},
	} {
		project := &azdext.ProjectConfig{Path: t.TempDir(), Infra: infra}
		_, err := automaticProvisionInputs(t.Context(), project, resolvedDatabase{Type: databaseH2})
		require.Error(t, err)
	}
}
