// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package emulators

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulator"
)

func eventHubs() emulator.Definition {
	return emulator.Definition{
		Name: "eventhubs", DisplayName: "Azure Event Hubs",
		Description: "Launch Event Hubs with an event hub, consumer group and Azurite dependency.",
		Ports: []emulator.Port{
			{Flag: "port", Default: 5673, Description: "Host port for AMQP"},
			{Flag: "kafka-port", Default: 9092, Description: "Host port for Kafka"},
			{Flag: "health-port", Default: 5301, Description: "Host port for readiness checks"},
		},
		LicenseURLs: []string{
			"https://github.com/Azure/azure-event-hubs-emulator-installer/blob/main/EMULATOR_EULA.md",
		},
		Build: func(o emulator.Options) (emulator.Spec, error) {
			connection := fmt.Sprintf(eventHubsConnectionFormat, o.Ports["port"])
			// The emulator advertises the Kafka broker using the endpoint supplied during SASL authentication.
			kafkaConnection := fmt.Sprintf(eventHubsConnectionFormat, o.Ports["kafka-port"])
			return emulator.Spec{
				Compose: emulator.Compose{
					Services: map[string]emulator.Service{
						"emulator": {
							Image: "mcr.microsoft.com/azure-messaging/eventhubs-emulator:latest",
							Ports: []string{
								o.Binding("port", 5672), o.Binding("kafka-port", 9092), o.Binding("health-port", 5300),
							},
							Environment: map[string]string{
								"ACCEPT_EULA": "Y", "BLOB_SERVER": "azurite", "METADATA_SERVER": "azurite",
							},
							DependsOn: []string{"azurite"},
							Configs: []emulator.ConfigMount{
								{Source: "entities", Target: "/Eventhubs_Emulator/ConfigFiles/Config.json"},
							},
						},
						"azurite": {Image: azuriteImage},
					},
					Configs: map[string]emulator.Config{"entities": {Content: eventHubsConfig}},
				},
				Connections: []emulator.Connection{
					{Name: "EVENTHUBS_CONNECTION_STRING", Value: connection},
					{Name: "EVENTHUB_NAME", Value: "eh1"},
					{Name: "EVENTHUB_CONSUMER_GROUP", Value: "cg1"},
					{Name: "KAFKA_BOOTSTRAP_SERVERS", Value: fmt.Sprintf("localhost:%d", o.Ports["kafka-port"])},
					{Name: "KAFKA_SECURITY_PROTOCOL", Value: "SASL_PLAINTEXT"},
					{Name: "KAFKA_SASL_MECHANISM", Value: "PLAIN"},
					{Name: "KAFKA_SASL_USERNAME", Value: "$ConnectionString"},
					{Name: "KAFKA_SASL_PASSWORD", Value: kafkaConnection},
				},
				Probes: []emulator.Probe{{URL: o.URL("health-port") + "/health", Status: 200}},
				Notes: []string{
					"Data is not preserved across emulator restarts. Kafka supports producer and consumer APIs only.",
				},
			}, nil
		},
	}
}

const eventHubsConnectionFormat = "Endpoint=sb://localhost:%d;SharedAccessKeyName=RootManageSharedAccessKey;" +
	"SharedAccessKey=SAS_KEY_VALUE;UseDevelopmentEmulator=true;"

const eventHubsConfig = `{
  "UserConfig": {
    "NamespaceConfig": [{
      "Type": "EventHub",
      "Name": "emulatorNs1",
      "Entities": [{
        "Name": "eh1",
        "PartitionCount": 2,
        "ConsumerGroups": [{"Name": "cg1"}]
      }]
    }],
    "LoggingConfig": {"Type": "Console"}
  }
}`
