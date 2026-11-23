// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package emulators

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulator"
)

func webPubSub() emulator.Definition {
	return emulator.Definition{
		Name: "webpubsub", DisplayName: "Azure Web PubSub (preview)",
		Description: "Launch the preview Web PubSub emulator for local WebSocket messaging.",
		Ports: []emulator.Port{
			{Flag: "port", Default: 8084, Description: "Host port for Web PubSub"},
		},
		Build: func(o emulator.Options) (emulator.Spec, error) {
			return emulator.Spec{
				Compose: emulator.Compose{Services: map[string]emulator.Service{
					"emulator": {
						Build: &emulator.Build{Context: "build/webpubsub"},
						Ports: []string{o.Binding("port", 8080)},
					},
				}},
				Dockerfile: webPubSubDockerfile,
				Connections: []emulator.Connection{
					{Name: "WEBPUBSUB_CONNECTION_STRING", Value: fmt.Sprintf(
						"Endpoint=%s;AccessKey=%s;Version=1.0;", o.URL("port"), realtimeKey)},
				},
				Probes: []emulator.Probe{{URL: o.URL("port") + "/api/health", Method: "HEAD", Status: 200}},
				Notes: []string{
					"Preview. State is held in memory and message TTL expiration is not enforced.",
					"Enable your SDK's local HTTP option (JavaScript: allowInsecureConnection: true).",
				},
			}, nil
		},
	}
}

const webPubSubDockerfile = `FROM mcr.microsoft.com/dotnet/sdk:10.0
ENV DOTNET_CLI_TELEMETRY_OPTOUT=1
RUN dotnet tool install --tool-path /tools Microsoft.Azure.WebPubSub.Emulator --version 1.0.0-beta.1
WORKDIR /app
ENTRYPOINT ["/tools/awps-emulator", "--urls", "http://0.0.0.0:8080"]
`
