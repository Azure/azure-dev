// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package emulators

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulator"
)

func cosmosDB() emulator.Definition {
	return emulator.Definition{
		Name: "cosmosdb", DisplayName: "Azure Cosmos DB",
		Description: "Launch the Cosmos DB vNext emulator for the NoSQL gateway API.",
		Ports: []emulator.Port{
			{Flag: "port", Default: 8081, Description: "Host port for the HTTPS gateway"},
			{Flag: "explorer-port", Default: 1234, Description: "Host port for Data Explorer"},
			{Flag: "health-port", Default: 18081, Description: "Host port for readiness checks"},
		},
		Build: func(o emulator.Options) (emulator.Spec, error) {
			endpoint := fmt.Sprintf("https://localhost:%d/", o.Ports["port"])
			return emulator.Spec{
				Compose: emulator.Compose{Services: map[string]emulator.Service{
					"emulator": {
						Image:   "mcr.microsoft.com/cosmosdb/linux/azure-cosmos-emulator:vnext-latest",
						Command: []string{"--protocol", "https", "--explorer-protocol", "http"},
						Ports: []string{
							o.Binding("port", 8081), o.Binding("explorer-port", 1234), o.Binding("health-port", 8080),
						},
						Environment: map[string]string{"ENABLE_TELEMETRY": "false"},
					},
				}},
				Connections: []emulator.Connection{
					{Name: "COSMOS_ENDPOINT", Value: endpoint},
					{Name: "COSMOS_KEY", Value: cosmosKey},
					{Name: "COSMOS_CONNECTION_STRING",
						Value: "AccountEndpoint=" + endpoint + ";AccountKey=" + cosmosKey + ";"},
					{Name: "DATA_EXPLORER_URL", Value: o.URL("explorer-port")},
				},
				Probes: []emulator.Probe{{URL: o.URL("health-port") + "/ready", Status: 200}},
				Notes: []string{
					"Supports the NoSQL gateway API only. The vNext emulator does not enforce authentication.",
					"The gateway uses a self-signed development certificate. Configure your SDK to trust it locally.",
				},
			}, nil
		},
	}
}
