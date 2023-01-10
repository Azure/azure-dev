// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { /*getAzureExtensionApi,*/ IActionContext } from '@microsoft/vscode-azext-utils';
// import { AzureResourcesApi } from '@microsoft/vscode-azext-utils/hostapi.v2';
import { AzureDevCliApplication } from '../../views/workspace/AzureDevCliApplication';
import { AzureDevCliEnvironment } from '../../views/workspace/AzureDevCliEnvironment';
import { AzureDevCliService } from '../../views/workspace/AzureDevCliService';

export async function revealAzureResource(context: IActionContext, selectedItem: AzureDevCliService): Promise<void> {
    context.telemetry.properties.revealSource = selectedItem.constructor.name;

    // TODO
    //const azureResourceId: string = 'TODO';

    //await revealAzureResourceInternal(azureResourceId);
    throw new Error('Not implemented');
}

export async function revealAzureResourceGroup(context: IActionContext, selectedItem: AzureDevCliApplication | AzureDevCliEnvironment): Promise<void> {
    context.telemetry.properties.revealSource = selectedItem.constructor.name;

    // TODO
    //const azureResourceGroupId: string = 'TODO';

    //await revealAzureResourceInternal(azureResourceGroupId);
    throw new Error('Not implemented');
}

// async function revealAzureResourceInternal(azureResourceId: string): Promise<void> {
//     const api = await getAzureExtensionApi<AzureResourcesApi>('ms-azuretools.vscode-azureresourcegroups', '2');
//     await api.resources.revealAzureResource(azureResourceId, { select: true, focus: true, expand: true });
// }