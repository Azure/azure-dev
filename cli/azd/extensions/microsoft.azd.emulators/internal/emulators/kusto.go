// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package emulators

import "github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulator"

func kusto() emulator.Definition {
	return emulator.Definition{
		Name: "kusto", DisplayName: "Azure Data Explorer (Kusto)",
		RequiresAMD64: true,
		Description:   "Launch the local Kusto query engine (requires x64).",
		Ports: []emulator.Port{
			{Flag: "port", Default: 8085, Description: "Host port for the query and management APIs"},
		},
		LicenseURLs: []string{"https://aka.ms/adx.emulator.license"},
		Build: func(o emulator.Options) (emulator.Spec, error) {
			return emulator.Spec{
				Compose: emulator.Compose{Services: map[string]emulator.Service{
					"emulator": {
						Image: "mcr.microsoft.com/azuredataexplorer/kustainer-linux:latest", Platform: "linux/amd64",
						Ports:       []string{o.Binding("port", 8080)},
						Environment: map[string]string{"ACCEPT_EULA": "Y"},
					},
				}},
				Connections: []emulator.Connection{
					{Name: "KUSTO_ENDPOINT", Value: o.URL("port")},
					{Name: "KUSTO_CONNECTION_STRING",
						Value: "Data Source=" + o.URL("port") + ";AAD Federated Security=False;"},
				},
				Probes: []emulator.Probe{{
					URL: o.URL("port") + "/v1/rest/mgmt", Method: "POST", Body: `{"csl":".show cluster"}`, Status: 200,
				}},
				Notes: []string{
					"Requires an x64 CPU with SSE4.2 and AVX2. Managed ingestion and authentication are not available.",
					`Create a database with: .create database emulator persist (@"/kustodata/db/md", @"/kustodata/db/data")`,
				},
			}, nil
		},
	}
}
