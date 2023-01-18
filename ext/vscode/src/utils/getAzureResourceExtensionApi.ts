// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { getAzureExtensionApi } from '@microsoft/vscode-azext-utils';
import { AzureResourcesApi } from '@microsoft/vscode-azext-utils/hostapi.v2';
import * as vscode from 'vscode';
import ext from '../ext';

const AzureResourcesExtensionId = 'ms-azuretools.vscode-azureresourcegroups';

export async function getAzureResourceExtensionApi(): Promise<AzureResourcesApi> {
    return await getAzureExtensionApi<AzureResourcesApi>(
        AzureResourcesExtensionId,
        '2',
        {
            extensionId: ext.azureDevExtensionId
        }
    );
}

export function isResourcesExtensionInstalled(): boolean {
    return !!vscode.extensions.getExtension(AzureResourcesExtensionId);
}