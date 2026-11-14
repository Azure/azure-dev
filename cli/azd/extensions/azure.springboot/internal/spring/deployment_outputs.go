// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type deploymentOutputVerifier interface {
	Read(context.Context, map[string]string) (*deploymentBinding, error)
	Verify(context.Context, resolvedDatabase, map[string]string) (*deploymentBinding, error)
}

type azureDeploymentOutputVerifier struct {
	client *azdext.AzdClient
}

type currentDeploymentOutput struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

type currentDeploymentOutputs map[string]currentDeploymentOutput

func newDeploymentOutputVerifier(client *azdext.AzdClient) deploymentOutputVerifier {
	return &azureDeploymentOutputVerifier{client: client}
}

func (v *azureDeploymentOutputVerifier) Read(
	ctx context.Context, values map[string]string,
) (*deploymentBinding, error) {
	return readDeploymentBinding(ctx, v.client, values["AZURE_ENV_NAME"], values)
}

func (v *azureDeploymentOutputVerifier) Verify(
	ctx context.Context,
	database resolvedDatabase,
	values map[string]string,
) (*deploymentBinding, error) {
	binding, present := ctx.Value(deploymentBindingKey{}).(*deploymentBinding)
	if !present {
		var err error
		binding, err = v.Read(ctx, values)
		if err != nil {
			return nil, err
		}
	}
	if !binding.original {
		if err := validateCurrentDeploymentOutputs(database, values, binding.outputs); err != nil {
			return nil, err
		}
	}
	if err := validateProvisionedBindings(database, binding.values); err != nil {
		return nil, err
	}
	required := []string{
		database.ServerResourceID,
		database.ServerResourceID + "/databases/" + database.Name,
		binding.values[containerAppIDEnv],
	}
	if database.AppIdentity != nil {
		if database.AppIdentity.Type == databaseAppIdentityTypeUserAssigned {
			required = append(required, database.AppIdentity.ResourceID)
		}
	}
	if binding.original {
		required = append(required, binding.values["CONTAINER_APP_ENVIRONMENT_ID"])
	}
	for _, resourceID := range required {
		if err := binding.requireResource(resourceID); err != nil {
			return nil, err
		}
	}
	return binding, nil
}

func decodeDeploymentOutputs(raw any) (currentDeploymentOutputs, error) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encoding current root deployment outputs: %w", err)
	}
	var document currentDeploymentOutputs
	if err := json.Unmarshal(encoded, &document); err != nil {
		return nil, fmt.Errorf("decoding current root deployment outputs: %w", err)
	}
	return document, nil
}

func validateCurrentDeploymentOutputs(
	database resolvedDatabase,
	values map[string]string,
	outputs currentDeploymentOutputs,
) error {
	bindings := []struct {
		name          string
		caseSensitive bool
	}{
		{postgresServerIDEnv, false},
		{postgresServerNameEnv, false},
		{postgresHostEnv, false},
		{postgresDatabaseEnv, true},
		{postgresAdministratorEnv, true},
		{containerAppIDEnv, false},
	}
	if database.Authentication == databaseAuthenticationManagedIdentity {
		if database.AppIdentity == nil {
			return validationError(
				"missing_database_app_identity",
				"the discovered Spring application identity is unavailable",
				"restore the current application identity configuration and deployment outputs",
			)
		}
		if database.AppIdentity.Type == databaseAppIdentityTypeSystemAssigned {
			bindings = append(bindings, struct {
				name          string
				caseSensitive bool
			}{serviceIdentityPrincipalIDEnv, false})
		} else {
			bindings = append(
				bindings,
				struct {
					name          string
					caseSensitive bool
				}{serviceIdentityIDEnv, false},
				struct {
					name          string
					caseSensitive bool
				}{serviceIdentityClientIDEnv, false},
				struct {
					name          string
					caseSensitive bool
				}{serviceIdentityPrincipalIDEnv, false},
			)
		}
	}
	currentValues := make(map[string]string, len(bindings))
	for _, binding := range bindings {
		output, emitted, err := findDeploymentOutput(outputs, binding.name)
		if err != nil {
			return err
		}
		current, isString := output.Value.(string)
		if emitted && (!strings.EqualFold(output.Type, "string") || !isString) {
			return validationError(
				"invalid_current_deployment_output",
				fmt.Sprintf("current root deployment output %s is not a plain string", binding.name),
				"restore the authored root Bicep output contract and rerun provision",
			)
		}
		persisted := values[binding.name]
		matches := current == persisted
		if !binding.caseSensitive {
			matches = strings.EqualFold(current, persisted)
		}
		if !emitted || strings.TrimSpace(current) == "" || strings.TrimSpace(persisted) == "" || !matches {
			return validationError(
				"stale_or_missing_current_deployment_output",
				fmt.Sprintf("%s was not emitted consistently by the current root deployment", binding.name),
				"restore the authored root Bicep output contract and rerun azd provision",
			)
		}
		currentValues[binding.name] = current
	}
	serverID := currentValues[postgresServerIDEnv]
	if !strings.EqualFold(serverID, database.ServerResourceID) {
		return validationError(
			"mismatched_postgres_binding",
			"database.serverResourceId does not match the current PostgreSQL root deployment output",
			"restore AZURE_POSTGRES_SERVER_ID and use the selected serverResourceId expression",
		)
	}
	databaseName := currentValues[postgresDatabaseEnv]
	if databaseName != database.Name {
		return validationError(
			"mismatched_database_name",
			"database.databaseName does not match the current PostgreSQL root deployment output",
			"restore AZURE_POSTGRES_DATABASE and use the authored database name",
		)
	}
	if database.Authentication == databaseAuthenticationManagedIdentity {
		switch database.AppIdentity.Type {
		case databaseAppIdentityTypeSystemAssigned:
			if !strings.EqualFold(currentValues[containerAppIDEnv], database.AppIdentity.ResourceID) {
				return validationError(
					"mismatched_database_app_identity",
					"the discovered system-assigned identity does not belong to the current service application",
					"restore the current Container App and application principal outputs",
				)
			}
		case databaseAppIdentityTypeUserAssigned:
			if !strings.EqualFold(currentValues[serviceIdentityIDEnv], database.AppIdentity.ResourceID) {
				return validationError(
					"mismatched_database_app_identity",
					"the discovered Spring application identity does not match the current service identity output",
					"restore the current service identity output and use the selected UAMI resourceId expression",
				)
			}
		default:
			return validationError(
				"unsupported_database_app_identity",
				"the discovered Spring application identity type is unsupported",
				"select the Container App system identity or one attached user-assigned identity",
			)
		}
	}
	return nil
}

func findDeploymentOutput(
	outputs currentDeploymentOutputs,
	expectedName string,
) (currentDeploymentOutput, bool, error) {
	var matched currentDeploymentOutput
	found := false
	for name, output := range outputs {
		if !strings.EqualFold(name, expectedName) {
			continue
		}
		if found {
			return currentDeploymentOutput{}, false, validationError(
				"ambiguous_current_deployment_output",
				fmt.Sprintf("current root deployment emitted multiple outputs matching %s", expectedName),
				"restore the authored root Bicep output contract and rerun azd provision",
			)
		}
		matched = output
		found = true
	}
	return matched, found, nil
}
