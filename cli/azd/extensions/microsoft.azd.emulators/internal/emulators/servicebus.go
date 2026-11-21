// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package emulators

import (
	"crypto/rand"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulator"
)

func serviceBus() emulator.Definition {
	return emulator.Definition{
		Name: "servicebus", DisplayName: "Azure Service Bus",
		RequiresAMD64: true,
		Description:   "Launch Service Bus with a queue, topic, subscription and SQL Server dependency.",
		Ports: []emulator.Port{
			{Flag: "port", Default: 5672, Description: "Host port for AMQP"},
			{Flag: "management-port", Default: 5300, Description: "Host port for administration and readiness checks"},
		},
		LicenseURLs: []string{
			"https://github.com/Azure/azure-service-bus-emulator-installer/blob/main/EMULATOR_EULA.txt",
			"https://go.microsoft.com/fwlink/?LinkId=746388",
		},
		Build: func(o emulator.Options) (emulator.Spec, error) {
			password := "Aa1!" + rand.Text()
			connection := func(port int) string {
				return fmt.Sprintf("Endpoint=sb://localhost:%d;SharedAccessKeyName=RootManageSharedAccessKey;"+
					"SharedAccessKey=SAS_KEY_VALUE;UseDevelopmentEmulator=true;", port)
			}
			return emulator.Spec{
				Compose: emulator.Compose{
					Services: map[string]emulator.Service{
						"emulator": {
							Image: "mcr.microsoft.com/azure-messaging/servicebus-emulator:2.0.0",
							Ports: []string{o.Binding("port", 5672), o.Binding("management-port", 5300)},
							Environment: map[string]string{
								"ACCEPT_EULA": "Y", "SQL_SERVER": "mssql", "MSSQL_SA_PASSWORD": password,
							},
							DependsOn: []string{"mssql"},
							Configs: []emulator.ConfigMount{
								{Source: "entities", Target: "/ServiceBus_Emulator/ConfigFiles/Config.json"},
							},
						},
						"mssql": {
							Image: "mcr.microsoft.com/mssql/server:2022-latest", Platform: "linux/amd64",
							Environment: map[string]string{"ACCEPT_EULA": "Y", "MSSQL_SA_PASSWORD": password},
						},
					},
					Configs: map[string]emulator.Config{"entities": {Content: serviceBusConfig}},
				},
				Connections: []emulator.Connection{
					{Name: "SERVICEBUS_CONNECTION_STRING", Value: connection(o.Ports["port"])},
					{Name: "SERVICEBUS_ADMIN_CONNECTION_STRING", Value: connection(o.Ports["management-port"])},
					{Name: "SERVICEBUS_QUEUE", Value: "queue.1"},
					{Name: "SERVICEBUS_TOPIC", Value: "topic.1"},
					{Name: "SERVICEBUS_SUBSCRIPTION", Value: "subscription.1"},
				},
				Probes:  []emulator.Probe{{URL: o.URL("management-port") + "/health", Status: 200}},
				Secrets: []string{password},
				Notes: []string{
					"SQL Server requires x64 or an x64-capable container runtime.",
					"Data is not preserved across emulator restarts. Administration requires a compatible .NET SDK.",
				},
			}, nil
		},
	}
}

const serviceBusConfig = `{
  "UserConfig": {
    "Namespaces": [{
      "Name": "sbemulatorns",
      "Queues": [{
        "Name": "queue.1",
        "Properties": {
          "DeadLetteringOnMessageExpiration": false,
          "DefaultMessageTimeToLive": "PT1H",
          "DuplicateDetectionHistoryTimeWindow": "PT20S",
          "ForwardDeadLetteredMessagesTo": "",
          "ForwardTo": "",
          "LockDuration": "PT1M",
          "MaxDeliveryCount": 3,
          "RequiresDuplicateDetection": false,
          "RequiresSession": false
        }
      }],
      "Topics": [{
        "Name": "topic.1",
        "Properties": {
          "DefaultMessageTimeToLive": "PT1H",
          "DuplicateDetectionHistoryTimeWindow": "PT20S",
          "RequiresDuplicateDetection": false
        },
        "Subscriptions": [{
          "Name": "subscription.1",
          "Properties": {
            "DeadLetteringOnMessageExpiration": false,
            "DefaultMessageTimeToLive": "PT1H",
            "LockDuration": "PT1M",
            "MaxDeliveryCount": 3,
            "ForwardDeadLetteredMessagesTo": "",
            "ForwardTo": "",
            "RequiresSession": false
          }
        }]
      }]
    }],
    "Logging": {"Type": "Console"}
  }
}`
