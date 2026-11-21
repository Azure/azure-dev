// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package emulators

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulator"
)

func signalR() emulator.Definition {
	return emulator.Definition{
		Name: "signalr", DisplayName: "Azure SignalR",
		Description: "Launch the SignalR serverless emulator with a configurable upstream.",
		Ports: []emulator.Port{
			{Flag: "port", Default: 8888, Description: "Host port for SignalR"},
		},
		HasUpstream: true,
		Build: func(o emulator.Options) (emulator.Spec, error) {
			upstream, err := url.Parse(o.UpstreamURL)
			if err != nil || upstream.Host == "" || (upstream.Scheme != "http" && upstream.Scheme != "https") {
				return emulator.Spec{}, fmt.Errorf("--upstream-url must be an absolute HTTP or HTTPS URL")
			}
			settings, err := json.Marshal(map[string]any{
				"UpstreamSettings": map[string]any{
					"Templates": []map[string]string{{
						"UrlTemplate": o.UpstreamURL, "EventPattern": "*", "HubPattern": "*", "CategoryPattern": "*",
					}},
				},
			})
			if err != nil {
				return emulator.Spec{}, fmt.Errorf("encode SignalR settings: %w", err)
			}
			return emulator.Spec{
				Compose: emulator.Compose{
					Services: map[string]emulator.Service{
						"emulator": {
							Build: &emulator.Build{Context: "build/signalr"},
							Ports: []string{o.Binding("port", 8888)},
							Configs: []emulator.ConfigMount{
								{Source: "upstream", Target: "/app/settings.json"},
							},
							ExtraHosts: []string{"host.docker.internal:host-gateway"},
						},
					},
					Configs: map[string]emulator.Config{"upstream": {Content: string(settings)}},
				},
				Dockerfile: signalRDockerfile,
				Connections: []emulator.Connection{
					{Name: "AzureSignalRConnectionString", Value: fmt.Sprintf(
						"Endpoint=http://localhost;Port=%d;AccessKey=%s;Version=1.0;", o.Ports["port"], realtimeKey)},
				},
				Probes:  []emulator.Probe{{URL: o.URL("port") + "/api/v1/health", Status: 200}},
				Secrets: []string{o.UpstreamURL},
				Notes: []string{
					"Only serverless scenarios with transient transport are supported.",
					"The upstream URL is resolved inside the container. Use host.docker.internal to reach the host.",
				},
			}, nil
		},
	}
}

const signalRDockerfile = `FROM mcr.microsoft.com/dotnet/sdk:8.0
ENV DOTNET_CLI_TELEMETRY_OPTOUT=1
RUN dotnet tool install --tool-path /tools Microsoft.Azure.SignalR.Emulator --version 1.7.1
WORKDIR /app
ENTRYPOINT ["/tools/asrs-emulator", "start", "--ip", "0.0.0.0", "--port", "8888", "--config", "/app/settings.json"]
`
