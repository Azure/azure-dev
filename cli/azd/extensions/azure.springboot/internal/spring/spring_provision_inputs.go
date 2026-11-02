// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

func automaticProvisionInputs(
	ctx context.Context, project *azdext.ProjectConfig, selected resolvedDatabase,
) (resolvedDatabase, error) {
	if err := ctx.Err(); err != nil {
		return resolvedDatabase{}, err
	}
	root, err := filepath.EvalSymlinks(project.GetPath())
	if err != nil {
		return resolvedDatabase{}, configurationError("the project directory cannot be verified for infrastructure inputs")
	}
	infra, module := project.GetInfra().GetPath(), project.GetInfra().GetModule()
	if infra == "" {
		infra = "infra"
	}
	if module == "" {
		module = "main"
	}
	if filepath.IsAbs(infra) || filepath.IsAbs(module) || strings.ContainsAny(infra+module, ":") {
		return resolvedDatabase{}, configurationError(
			"automatic bootstrap preparation requires contained Bicep parameter paths",
		)
	}
	name := filepath.Join(root, infra, module+".parameters.json")
	if !osutil.IsPathContained(root, name) {
		return resolvedDatabase{}, configurationError("the Bicep parameter path is outside the project")
	}
	resolved, err := filepath.EvalSymlinks(name)
	if errors.Is(err, os.ErrNotExist) && selected.Type == databaseH2 {
		return selected, nil
	}
	if err != nil || !osutil.IsPathContained(root, resolved) {
		return resolvedDatabase{}, configurationError("the Bicep parameter file cannot be verified for database bootstrap")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return resolvedDatabase{}, configurationError("the Bicep parameter file cannot be read")
	}
	data, readErr := readSpringConfigurationData(file)
	if err := errors.Join(readErr, file.Close()); err != nil {
		return resolvedDatabase{}, err
	}
	if err := ctx.Err(); err != nil {
		return resolvedDatabase{}, err
	}
	var document struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(data, &document); err != nil || document.Parameters == nil {
		return resolvedDatabase{}, configurationError(
			"automatic bootstrap preparation requires a valid Bicep parameter document",
		)
	}
	nameBinding, hasName := document.Parameters["databaseAdministratorName"]
	objectBinding, hasObject := document.Parameters["databaseAdministratorObjectId"]
	managed := hasName || hasObject
	if managed && (!hasName || !hasObject ||
		nameBinding.Value != "${DATABASE_ADMIN_NAME}" || objectBinding.Value != "${DATABASE_ADMIN_OBJECT_ID}") {
		return resolvedDatabase{}, configurationError(
			"the Bicep developer administrator inputs do not match supported bindings",
		)
	}
	password := false
	for _, field := range []string{"databasePassword", "databaseAdministratorPassword", "postgresAdministratorPassword"} {
		binding, present := document.Parameters[field]
		if !present {
			continue
		}
		if binding.Value != "${SPRING_DATABASE_PASSWORD}" && binding.Value != "${SPRING_DATABASE_PASSWORD:-}" {
			return resolvedDatabase{}, configurationError(
				"the Bicep administrator password does not match the supported binding",
			)
		}
		password = true
	}
	if managed && password {
		return resolvedDatabase{}, configurationError("the infrastructure mixes incompatible database bootstrap credentials")
	}
	if managed {
		if selected.Authentication == databaseAuthenticationPassword {
			return resolvedDatabase{}, configurationError(
				"the application and infrastructure database authentication modes conflict",
			)
		}
		return resolvedDatabase{Type: databasePostgreSQL, Authentication: databaseAuthenticationManagedIdentity}, nil
	}
	if password {
		if selected.Authentication == databaseAuthenticationManagedIdentity {
			return resolvedDatabase{}, configurationError(
				"the application and infrastructure database authentication modes conflict",
			)
		}
		return resolvedDatabase{Type: databasePostgreSQL, Authentication: databaseAuthenticationPassword}, nil
	}
	if selected.Type == databasePostgreSQL || selected.selectionPending {
		return resolvedDatabase{}, configurationError(
			"the database bootstrap credentials have no supported infrastructure binding",
		)
	}
	return selected, nil
}
