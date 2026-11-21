// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package emulators

import "github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulator"

func durableTask() emulator.Definition {
	return emulator.Definition{
		Name: "durabletask", DisplayName: "Durable Task Scheduler",
		Description: "Launch Durable Task Scheduler with the default task hub and dashboard.",
		Ports: []emulator.Port{
			{Flag: "port", Default: 8080, Description: "Host port for the scheduler"},
			{Flag: "dashboard-port", Default: 8082, Description: "Host port for the dashboard"},
		},
		Build: func(o emulator.Options) (emulator.Spec, error) {
			return emulator.Spec{
				Compose: emulator.Compose{Services: map[string]emulator.Service{
					"emulator": {
						Image: "mcr.microsoft.com/dts/dts-emulator:latest",
						Ports: []string{o.Binding("port", 8080), o.Binding("dashboard-port", 8082)},
					},
				}},
				Connections: []emulator.Connection{
					{Name: "DURABLE_TASK_SERVICE_CONNECTION_STRING",
						Value: "Endpoint=" + o.URL("port") + ";TaskHub=default;Authentication=None"},
					{Name: "TASKHUB_NAME", Value: "default"},
					{Name: "DASHBOARD_URL", Value: o.URL("dashboard-port")},
				},
				Probes: []emulator.Probe{{URL: o.URL("dashboard-port"), Status: 200}},
			}, nil
		},
	}
}
