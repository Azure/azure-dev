// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package lifecycle

import (
	"azurespringboot/internal/springmodel"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

const (
	databaseAdministratorNameBinding         = "${DATABASE_ADMIN_NAME}"
	databaseAdministratorIDBinding           = "${DATABASE_ADMIN_OBJECT_ID}"
	databaseAdministratorNameDeferredBinding = "${DATABASE_ADMIN_NAME=azd-preprovision-pending}"
	databaseAdministratorIDDeferredBinding   = "${DATABASE_ADMIN_OBJECT_ID=00000000-0000-0000-0000-000000000000}"
)

func automaticProvisionInputs(
	ctx context.Context, project *azdext.ProjectConfig, selected springmodel.ResolvedDatabase,
) (springmodel.ResolvedDatabase, error) {
	if err := ctx.Err(); err != nil {
		return springmodel.ResolvedDatabase{}, err
	}
	root, err := filepath.EvalSymlinks(project.GetPath())
	if err != nil {
		return springmodel.ResolvedDatabase{}, configurationError(
			"the project directory cannot be verified for infrastructure inputs",
		)
	}
	infra, module := project.GetInfra().GetPath(), project.GetInfra().GetModule()
	if infra == "" {
		infra = "infra"
	}
	if module == "" {
		module = "main"
	}
	if filepath.IsAbs(infra) || filepath.IsAbs(module) || strings.ContainsAny(infra+module, ":") {
		return springmodel.ResolvedDatabase{}, configurationError(
			"automatic bootstrap preparation requires contained Bicep parameter paths",
		)
	}
	name := filepath.Join(root, infra, module+".parameters.json")
	if !osutil.IsPathContained(root, name) {
		return springmodel.ResolvedDatabase{}, configurationError("the Bicep parameter path is outside the project")
	}
	if !selected.SelectionPending && (selected.Type == springmodel.DatabaseH2 || selected.Type == springmodel.DatabaseNone) {
		return selected, nil
	}
	resolved, err := filepath.EvalSymlinks(name)
	if err != nil || !osutil.IsPathContained(root, resolved) {
		return springmodel.ResolvedDatabase{}, configurationError(
			"the Bicep parameter file cannot be verified for database bootstrap",
		)
	}
	file, err := os.Open(resolved) //nolint:gosec // G304: path containment verified via osutil.IsPathContained above
	if err != nil {
		return springmodel.ResolvedDatabase{}, configurationError("the Bicep parameter file cannot be read")
	}
	data, readErr := springmodel.ReadConfigurationData(file)
	if err := errors.Join(readErr, file.Close()); err != nil {
		return springmodel.ResolvedDatabase{}, err
	}
	if err := ctx.Err(); err != nil {
		return springmodel.ResolvedDatabase{}, err
	}
	var document struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(data, &document); err != nil || document.Parameters == nil {
		return springmodel.ResolvedDatabase{}, configurationError(
			"automatic bootstrap preparation requires a valid Bicep parameter document",
		)
	}
	nameBinding, hasName := document.Parameters["databaseAdministratorName"]
	objectBinding, hasObject := document.Parameters["databaseAdministratorObjectId"]
	managed := hasName || hasObject
	directManaged := nameBinding.Value == databaseAdministratorNameBinding &&
		objectBinding.Value == databaseAdministratorIDBinding
	deferredManaged := nameBinding.Value == databaseAdministratorNameDeferredBinding &&
		objectBinding.Value == databaseAdministratorIDDeferredBinding
	if managed && (!hasName || !hasObject || (!directManaged && !deferredManaged)) {
		return springmodel.ResolvedDatabase{}, configurationError(
			"the Bicep developer administrator inputs do not match supported bindings",
		)
	}
	for _, field := range []string{"databasePassword", "databaseAdministratorPassword", "postgresAdministratorPassword"} {
		if _, present := document.Parameters[field]; present {
			return springmodel.ResolvedDatabase{}, configurationError(
				"password-authenticated PostgreSQL infrastructure bindings are unsupported; use Entra managed identity",
			)
		}
	}
	if managed {
		return springmodel.ResolvedDatabase{
			Type:           springmodel.DatabasePostgreSQL,
			Authentication: springmodel.DatabaseAuthenticationManagedIdentity,
		}, nil
	}
	if selected.Type == springmodel.DatabasePostgreSQL || selected.SelectionPending {
		return springmodel.ResolvedDatabase{}, configurationError(
			"the database bootstrap credentials have no supported infrastructure binding",
		)
	}
	return selected, nil
}
