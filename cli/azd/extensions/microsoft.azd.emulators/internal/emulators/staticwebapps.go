// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package emulators

import (
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulator"
)

func staticWebApps() emulator.Definition {
	return emulator.Definition{
		Name: "staticwebapps", DisplayName: "Azure Static Web Apps",
		Description: "Launch the Static Web Apps emulator for a local static-content directory.",
		Ports: []emulator.Port{
			{Flag: "port", Default: 4280, Description: "Host port for the website"},
		},
		HasDirectory: true,
		Build: func(o emulator.Options) (emulator.Spec, error) {
			info, err := os.Stat(o.Directory)
			if err != nil {
				return emulator.Spec{}, fmt.Errorf("read static-content directory: %w", err)
			}
			if !info.IsDir() {
				return emulator.Spec{}, fmt.Errorf("--directory must refer to a directory")
			}
			return emulator.Spec{
				Compose: emulator.Compose{Services: map[string]emulator.Service{
					"emulator": {
						Build: &emulator.Build{Context: "build/staticwebapps"},
						Ports: []string{o.Binding("port", 4280)},
						Volumes: []emulator.Volume{
							{Type: "bind", Source: o.Directory, Target: "/app", ReadOnly: true},
						},
					},
				}},
				Dockerfile: staticWebAppsDockerfile,
				Connections: []emulator.Connection{
					{Name: "APP_URL", Value: o.URL("port")},
					{Name: "CONTENT_DIRECTORY", Value: o.Directory},
				},
				Probes: []emulator.Probe{{URL: o.URL("port") + "/.auth/me", Status: 200}},
				Notes: []string{
					"Serves static files with local routing and mock authentication. API hosting is not included.",
				},
			}, nil
		},
	}
}

const staticWebAppsDockerfile = `FROM node:22-bookworm-slim
RUN npm install --global @azure/static-web-apps-cli@2.1.0
WORKDIR /app
ENTRYPOINT ["swa", "start", "/app", "--host", "0.0.0.0", "--port", "4280"]
`
