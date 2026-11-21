// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package emulators

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulator"
)

func storage() emulator.Definition {
	return emulator.Definition{
		Name: "storage", DisplayName: "Azure Storage (Azurite)",
		Description: "Launch Azurite for local Blob, Queue and Table Storage.",
		Ports: []emulator.Port{
			{Flag: "port", Default: 10000, Description: "Host port for Blob Storage"},
			{Flag: "queue-port", Default: 10001, Description: "Host port for Queue Storage"},
			{Flag: "table-port", Default: 10002, Description: "Host port for Table Storage"},
		},
		Build: func(o emulator.Options) (emulator.Spec, error) {
			blob := o.URL("port") + "/devstoreaccount1"
			queue := o.URL("queue-port") + "/devstoreaccount1"
			table := o.URL("table-port") + "/devstoreaccount1"
			return emulator.Spec{
				Compose: emulator.Compose{Services: map[string]emulator.Service{
					"emulator": {
						Image: azuriteImage,
						Ports: []string{
							o.Binding("port", 10000), o.Binding("queue-port", 10001), o.Binding("table-port", 10002),
						},
					},
				}},
				Connections: []emulator.Connection{
					{Name: "AZURE_STORAGE_CONNECTION_STRING", Value: fmt.Sprintf(
						"DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;AccountKey=%s;"+
							"BlobEndpoint=%s;QueueEndpoint=%s;TableEndpoint=%s;", storageKey, blob, queue, table)},
					{Name: "BLOB_ENDPOINT", Value: blob},
					{Name: "QUEUE_ENDPOINT", Value: queue},
					{Name: "TABLE_ENDPOINT", Value: table},
				},
				Probes: []emulator.Probe{{URL: blob + "?comp=list", Status: 403}},
				Notes: []string{
					"Table Storage support is preview. Azure Files and Data Lake Storage Gen2 are not emulated.",
				},
			}, nil
		},
	}
}
