// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package emulators

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulator"
)

func TestLaunchersPublishLoopbackPortsAndUseConfiguredValues(t *testing.T) {
	defaults := map[int]string{}
	for _, definition := range All() {
		t.Run(definition.Name, func(t *testing.T) {
			options := emulator.Options{
				Ports: map[string]int{}, Directory: t.TempDir(),
				UpstreamURL: "http://host.docker.internal:7071/runtime/webhooks/signalr",
			}
			for _, port := range definition.Ports {
				require.NotContains(t, defaults, port.Default, "default ports must allow different emulators to coexist")
				defaults[port.Default] = definition.Name
				options.Ports[port.Flag] = port.Default + 10000
			}
			spec, err := definition.Build(options)
			require.NoError(t, err)
			require.NotEmpty(t, spec.Connections)
			require.True(t, len(spec.Probes) > 0 || len(spec.ExecProbe) > 0)
			published := map[int]bool{}
			for _, service := range spec.Compose.Services {
				if service.Build != nil {
					require.Equal(t, "build/"+definition.Name, service.Build.Context)
					require.NotEmpty(t, spec.Dockerfile)
				}
				for _, port := range service.Ports {
					require.True(t, strings.HasPrefix(port, "127.0.0.1:"), port)
				}
				for _, hostPort := range options.Ports {
					for _, binding := range service.Ports {
						if strings.Contains(binding, ":"+strconv.Itoa(hostPort)+":") {
							published[hostPort] = true
						}
					}
				}
				for _, dependency := range service.DependsOn {
					require.Contains(t, spec.Compose.Services, dependency)
				}
				for _, mount := range service.Configs {
					require.Contains(t, spec.Compose.Configs, mount.Source)
				}
			}
			for _, port := range options.Ports {
				require.True(t, published[port], "configured port must be published")
			}
			for _, config := range spec.Compose.Configs {
				require.True(t, json.Valid([]byte(config.Content)))
			}
		})
	}
}

func TestStorageConnectionStringUsesAzuriteDevelopmentKey(t *testing.T) {
	key, err := base64.StdEncoding.DecodeString(storageKey)
	require.NoError(t, err)
	require.Len(t, key, 64)
	spec, err := storage().Build(emulator.Options{Ports: map[string]int{
		"port": 11000, "queue-port": 11001, "table-port": 11002,
	}})
	require.NoError(t, err)
	connection := spec.Connections[0].Value
	require.Contains(t, connection, "BlobEndpoint=http://127.0.0.1:11000/devstoreaccount1")
	require.Contains(t, connection, "QueueEndpoint=http://127.0.0.1:11001/devstoreaccount1")
	require.Contains(t, connection, "TableEndpoint=http://127.0.0.1:11002/devstoreaccount1")
}

func TestServiceBusUsesSameGeneratedPasswordForDependency(t *testing.T) {
	spec, err := serviceBus().Build(emulator.Options{Ports: map[string]int{"port": 5674, "management-port": 5400}})
	require.NoError(t, err)
	main := spec.Compose.Services["emulator"]
	sql := spec.Compose.Services["mssql"]
	require.NotEmpty(t, main.Environment["MSSQL_SA_PASSWORD"])
	require.Equal(t, main.Environment["MSSQL_SA_PASSWORD"], sql.Environment["MSSQL_SA_PASSWORD"])
	require.Empty(t, sql.Ports)
	require.Contains(t, spec.Connections[0].Value, "localhost:5674")
	require.Contains(t, spec.Connections[1].Value, "localhost:5400")
}

func TestEventHubsUsesKafkaPortInSASLConnectionString(t *testing.T) {
	spec, err := eventHubs().Build(emulator.Options{Ports: map[string]int{
		"port": 15673, "kafka-port": 19092, "health-port": 15301,
	}})
	require.NoError(t, err)
	settings := map[string]string{}
	for _, connection := range spec.Connections {
		settings[connection.Name] = connection.Value
	}
	require.Contains(t, settings["EVENTHUBS_CONNECTION_STRING"], "Endpoint=sb://localhost:15673;")
	require.Contains(t, settings["KAFKA_SASL_PASSWORD"], "Endpoint=sb://localhost:19092;")
	require.Equal(t, "localhost:19092", settings["KAFKA_BOOTSTRAP_SERVERS"])
}

func TestSignalRRejectsInvalidUpstreamWithoutDisclosingIt(t *testing.T) {
	// #nosec G101 -- Fake credentials verify validation errors do not disclose input URLs.
	_, err := signalR().Build(emulator.Options{UpstreamURL: "ftp://user:secret@example.com/?sig=private"})
	require.ErrorContains(t, err, "--upstream-url")
	require.NotContains(t, err.Error(), "secret")
	require.NotContains(t, err.Error(), "private")
}

func TestStaticWebAppsRequiresExistingDirectory(t *testing.T) {
	_, err := staticWebApps().Build(emulator.Options{Directory: "/path/that/does/not/exist"})
	require.ErrorContains(t, err, "static-content directory")
}
