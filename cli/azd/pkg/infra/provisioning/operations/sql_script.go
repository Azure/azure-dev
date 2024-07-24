// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/sqlcmd"
)

type SqlScript struct {
	Description string
	Server      string
	Database    string
	Path        string
	Env         map[string]string
}

var ErrSqlScriptOperationDisabled = fmt.Errorf(
	"%sYour project has sql server scripts.\n  - %w\n%s\n",
	output.WithWarningFormat("*Note: "),
	ErrAzdOperationsNotEnabled,
	output.WithWarningFormat("Ignoring scripts."),
)

func SqlScripts(model AzdOperationsModel, infraPath string) ([]SqlScript, error) {
	var sqlServerOperations []SqlScript
	for _, operation := range model.Operations {
		if operation.Type == sqlServerOperation {
			var sqlServerScript SqlScript
			bytes, err := json.Marshal(operation.Config)
			if err != nil {
				return nil, err
			}
			err = json.Unmarshal(bytes, &sqlServerScript)
			if err != nil {
				return nil, err
			}
			sqlServerScript.Description = operation.Description
			if !filepath.IsAbs(sqlServerScript.Path) {
				sqlServerScript.Path = filepath.Join(infraPath, sqlServerScript.Path)
			}
			sqlServerOperations = append(sqlServerOperations, sqlServerScript)
		}
	}
	return sqlServerOperations, nil
}

func DoSqlScript(
	ctx context.Context,
	SqlScriptsOperations []SqlScript,
	console input.Console,
	env environment.Environment,
	sqlCmdCli *sqlcmd.SqlCmdCli,
) error {
	if len(SqlScriptsOperations) > 0 {
		console.ShowSpinner(ctx, "execute sql scripts", input.StepFailed)
	}
	for _, op := range SqlScriptsOperations {
		filePath := op.Path
		if op.Env != nil {
			fileEnv := environment.NewWithValues("fileEnv", op.Env)
			tmpDir, err := os.MkdirTemp("", "azd-sql-scripts")
			if err != nil {
				return err
			}
			defer os.RemoveAll(tmpDir)
			data, err := os.ReadFile(filePath)
			if err != nil {
				return err
			}
			expString := osutil.NewExpandableString(string(data))
			evaluated, err := expString.Envsubst(fileEnv.Getenv)
			if err != nil {
				return err
			}
			filePath = filepath.Join(tmpDir, filepath.Base(filePath))
			err = os.WriteFile(filePath, []byte(evaluated), osutil.PermissionDirectory)
			if err != nil {
				return err
			}
		}

		if _, err := sqlCmdCli.ExecuteScript(
			ctx,
			op.Server,
			op.Database,
			filePath,
			// sqlCmd cli uses DAC to connect to the server, but it doesn't know how to handle multi-tenant accounts.
			// sqlCmd cli asks az or azd for a token w/o passing a tenant-id arg.
			// sqlCmd cli runs from ~/.azd/bin:
			//  - azd doesn't know the tenant-id to use and defaults to get a token for home tenant.
			// By setting the AZURE_SUBSCRIPTION_ID as env var to run sqlCmd cli, azd will use it to get tenant-id.
			[]string{
				fmt.Sprintf("%s=%s", environment.SubscriptionIdEnvVarName, env.GetSubscriptionId()),
			},
		); err != nil {
			return fmt.Errorf("error run sqlcmd: %w", err)
		}
		console.MessageUxItem(ctx, &ux.DisplayedResource{
			Type:  sqlServerOperation,
			Name:  op.Description,
			State: ux.SucceededState,
		})
	}
	return nil
}
