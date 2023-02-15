// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { localize } from "../localize";

export function parseAzureResourceId(azureResourceId: string): { subscription: string; resourceGroup: string; provider?: string; resourceName?: string } {
    const azureResourceIdRegex = /^\/subscriptions\/(?<subscription>[^/]+)\/resourceGroups\/(?<resourceGroup>[^/]+)(\/providers\/(?<provider>[^/]+\/[^/]+)\/(?<resourceName>[^/]+))?$/i;
    const match = azureResourceIdRegex.exec(azureResourceId);

    if (!match || !match.groups) {
        throw new Error(localize('azure-dev.commands.invalidResourceId', 'Invalid Azure resource ID: "{0}"', azureResourceId));
    }

    return {
        subscription: match.groups.subscription,
        resourceGroup: match.groups.resourceGroup,
        provider: match.groups.provider,
        resourceName: match.groups.resourceName,
    };
}