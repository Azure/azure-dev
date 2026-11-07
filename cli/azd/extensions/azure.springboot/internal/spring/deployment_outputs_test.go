// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateCurrentDeploymentOutputsRequiresCurrentRootEvidence(t *testing.T) {
	database, values := managedIdentityDeploymentOutputFixture()
	outputs := deploymentOutputsFromValues(values)

	require.NoError(t, validateCurrentDeploymentOutputs(database, values, outputs))

	delete(outputs, postgresDatabaseEnv)
	err := validateCurrentDeploymentOutputs(database, values, outputs)
	require.ErrorContains(t, err, "was not emitted consistently")

	outputs[postgresDatabaseEnv] = currentDeploymentOutput{Type: "String", Value: "petclinic"}
	err = validateCurrentDeploymentOutputs(database, values, outputs)
	require.ErrorContains(t, err, "was not emitted consistently")
}

func TestDeploymentOutputsAcceptNativeARMShape(t *testing.T) {
	database, values := managedIdentityDeploymentOutputFixture()
	rawOutputs := map[string]any{}
	for name, value := range values {
		rawOutputs[nativeARMOutputName(name)] = map[string]any{
			"type":  "String",
			"value": value,
		}
	}
	rawOutputs["UNRELATED_NUMBER"] = map[string]any{
		"type":  "Int",
		"value": 42,
	}
	rawOutputs["UNRELATED_OBJECT"] = map[string]any{
		"type":  "Object",
		"value": map[string]any{"enabled": true},
	}

	outputs, err := decodeDeploymentOutputs(rawOutputs)
	require.NoError(t, err)
	require.NoError(t, validateCurrentDeploymentOutputs(database, values, outputs))
}

func TestDeploymentOutputsAcceptSystemAssignedPrincipalWithoutIdentityResource(t *testing.T) {
	database, values := managedIdentityDeploymentOutputFixture()
	database.AppIdentity = &databaseAppIdentity{
		Type:       databaseAppIdentityTypeSystemAssigned,
		ResourceID: values[containerAppIDEnv],
	}
	delete(values, serviceIdentityIDEnv)
	delete(values, serviceIdentityClientIDEnv)

	require.NoError(t, validateCurrentDeploymentOutputs(
		database,
		values,
		deploymentOutputsFromValues(values),
	))

	delete(values, serviceIdentityPrincipalIDEnv)
	err := validateCurrentDeploymentOutputs(database, values, deploymentOutputsFromValues(values))
	require.ErrorContains(t, err, serviceIdentityPrincipalIDEnv+" was not emitted consistently")
}

func TestDeploymentOutputsRejectAmbiguousNameCasing(t *testing.T) {
	database, values := managedIdentityDeploymentOutputFixture()
	outputs := deploymentOutputsFromValues(values)
	outputs[nativeARMOutputName(postgresServerIDEnv)] = currentDeploymentOutput{
		Type:  "String",
		Value: values[postgresServerIDEnv] + "-conflict",
	}

	err := validateCurrentDeploymentOutputs(database, values, outputs)
	require.ErrorContains(t, err, "multiple outputs matching AZURE_POSTGRES_SERVER_ID")
}

func TestDeploymentOutputsRejectUnexpectedRequiredTypesAndValues(t *testing.T) {
	tests := []struct {
		name   string
		output currentDeploymentOutput
	}{
		{
			name:   "secure string",
			output: currentDeploymentOutput{Type: "SecureString", Value: "PetClinic"},
		},
		{
			name:   "object",
			output: currentDeploymentOutput{Type: "Object", Value: map[string]any{"name": "PetClinic"}},
		},
		{
			name:   "non string value",
			output: currentDeploymentOutput{Type: "String", Value: 42},
		},
		{
			name:   "missing type",
			output: currentDeploymentOutput{Value: "PetClinic"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, values := managedIdentityDeploymentOutputFixture()
			outputs := deploymentOutputsFromValues(values)
			outputs[postgresDatabaseEnv] = test.output

			err := validateCurrentDeploymentOutputs(database, values, outputs)
			require.ErrorContains(t, err, "is not a plain string")
		})
	}
}

func TestDeploymentOutputsRejectMismatchedResourceBindings(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*resolvedDatabase)
		message string
	}{
		{
			name: "postgres server",
			mutate: func(database *resolvedDatabase) {
				database.ServerResourceID += "-different"
			},
			message: "serverResourceId does not match",
		},
		{
			name: "application identity",
			mutate: func(database *resolvedDatabase) {
				database.AppIdentity.ResourceID += "-different"
			},
			message: "application identity does not match",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, values := managedIdentityDeploymentOutputFixture()
			test.mutate(&database)

			err := validateCurrentDeploymentOutputs(database, values, deploymentOutputsFromValues(values))
			require.ErrorContains(t, err, test.message)
		})
	}
}

func managedIdentityDeploymentOutputFixture() (resolvedDatabase, map[string]string) {
	serverID := "/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.DBforPostgreSQL/flexibleServers/pg"
	identityID := "/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.ManagedIdentity/userAssignedIdentities/app"
	return resolvedDatabase{
			Type:             databasePostgreSQL,
			ServerResourceID: serverID,
			Name:             "PetClinic",
			Authentication:   databaseAuthenticationManagedIdentity,
			Username:         "PetClinic_App",
			AppIdentity: &databaseAppIdentity{
				Type:       databaseAppIdentityTypeUserAssigned,
				ResourceID: identityID,
			},
		}, map[string]string{
			postgresServerIDEnv:           serverID,
			postgresServerNameEnv:         "pg",
			postgresHostEnv:               "pg.postgres.database.azure.com",
			postgresDatabaseEnv:           "PetClinic",
			postgresAdministratorEnv:      "Developer@contoso.com",
			containerAppIDEnv:             "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/containerApps/app",
			serviceIdentityIDEnv:          identityID,
			serviceIdentityClientIDEnv:    "11111111-1111-1111-1111-111111111111",
			serviceIdentityPrincipalIDEnv: "22222222-2222-2222-2222-222222222222",
		}
}

func deploymentOutputsFromValues(values map[string]string) currentDeploymentOutputs {
	outputs := make(currentDeploymentOutputs, len(values))
	for name, value := range values {
		outputs[name] = currentDeploymentOutput{Type: "string", Value: value}
	}
	return outputs
}

func nativeARMOutputName(name string) string {
	prefix, suffix, found := strings.Cut(name, "_")
	if !found || len(prefix) < 2 {
		return name
	}
	return strings.ToLower(prefix[:len(prefix)-1]) + prefix[len(prefix)-1:] + "_" + suffix
}
