// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package emulators

import "github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulator"

func appConfig() emulator.Definition {
	return emulator.Definition{
		Name: "appconfig", DisplayName: "Azure App Configuration",
		Description: "Launch App Configuration with its web UI and anonymous local authentication.",
		Ports: []emulator.Port{
			{Flag: "port", Default: 8483, Description: "Host port for the API and web UI"},
		},
		Build: func(o emulator.Options) (emulator.Spec, error) {
			return emulator.Spec{
				Compose: emulator.Compose{Services: map[string]emulator.Service{
					"emulator": {
						Image: "mcr.microsoft.com/azure-app-configuration/app-configuration-emulator:1.2.0",
						Ports: []string{o.Binding("port", 8483)},
						Environment: map[string]string{
							"Tenant:AnonymousAuthEnabled": "true", "Authentication:Anonymous:AnonymousUserRole": "Owner",
						},
					},
				}},
				Connections: []emulator.Connection{
					{Name: "AZURE_APPCONFIG_ENDPOINT", Value: o.URL("port")},
				},
				Probes: []emulator.Probe{{URL: o.URL("port") + "/kv", Status: 200}},
				Notes: []string{
					"Use an SDK supporting anonymous authentication. No access key or Azure login is required.",
				},
			}, nil
		},
	}
}
