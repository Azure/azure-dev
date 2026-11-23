// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package emulators

import "github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulator"

// These public development keys are defined by the upstream emulators, not production credentials.
const (
	storageKey   = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw=="
	cosmosKey    = "C2y6yDjf5/R+ob0N8A7Cgv30VRDJIWEHLM+4QDU5DE2nQ9nDuVTqobD4b8mGGyPMbIZnqyMsEcaGQy67XIw/Jw=="
	realtimeKey  = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789ABCDEFGH"
	azuriteImage = "mcr.microsoft.com/azure-storage/azurite:3.35.0"
)

// All returns the available emulator commands, with a separate definition per service.
func All() []emulator.Definition {
	return []emulator.Definition{
		storage(), cosmosDB(), serviceBus(), eventHubs(), appConfig(), kusto(),
		durableTask(), signalR(), staticWebApps(), webPubSub(), sqlDB(),
	}
}
