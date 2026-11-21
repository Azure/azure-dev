// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package emulators

import (
	"crypto/rand"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulator"
)

func sqlDB() emulator.Definition {
	return emulator.Definition{
		Name: "sqldb", DisplayName: "Azure SQL Database (preview)",
		Description: "Launch the preview local Azure SQL Database engine.",
		Ports: []emulator.Port{
			{Flag: "port", Default: 1433, Description: "Host port for SQL connections"},
		},
		LicenseURLs: []string{"https://go.microsoft.com/fwlink/?LinkId=746388"},
		Build: func(o emulator.Options) (emulator.Spec, error) {
			password := "Aa1!" + rand.Text()
			return emulator.Spec{
				Compose: emulator.Compose{Services: map[string]emulator.Service{
					"emulator": {
						Image: "mcr.microsoft.com/mssql/azure-sql-db-server:latest",
						Ports: []string{o.Binding("port", 1433)},
						Environment: map[string]string{
							"ACCEPT_EULA": "Y", "MSSQL_SA_PASSWORD": password, "SQLCMDPASSWORD": password,
						},
					},
				}},
				Connections: []emulator.Connection{
					{Name: "SQL_CONNECTION_STRING", Value: fmt.Sprintf(
						"Server=localhost,%d;Database=master;User Id=sa;Password=%s;"+
							"Encrypt=True;TrustServerCertificate=True;",
						o.Ports["port"], password)},
					{Name: "SQL_USER", Value: "sa"},
					{Name: "SQL_PASSWORD", Value: password},
				},
				ExecProbe: []string{
					"/opt/mssql-tools18/bin/sqlcmd", "-S", "localhost", "-U", "sa", "-C", "-b", "-Q", "SELECT 1",
				},
				Secrets: []string{password},
				Notes: []string{
					"Preview of the real Azure SQL Database engine, not a complete managed-service replica.",
					"The sa account and self-signed certificate trust are for local development only.",
				},
			}, nil
		},
	}
}
