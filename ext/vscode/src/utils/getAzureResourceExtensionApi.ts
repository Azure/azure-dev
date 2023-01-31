// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzureResourcesExtensionApi, getAzureResourcesExtensionApi } from '@microsoft/vscode-azureresources-api';
import * as vscode from 'vscode';
import ext from '../ext';

const AzureResourcesExtensionId = 'ms-azuretools.vscode-azureresourcegroups';

export async function getAzureResourceExtensionApi(): Promise<AzureResourcesExtensionApi> {
    return await getAzureResourcesExtensionApi(
        ext.context,
        '2.0.0', // API version 2.0.0
        {
            extensionId: ext.azureDevExtensionId
        }
    );
}

export function isResourcesExtensionInstalled(): boolean {
    return !!vscode.extensions.getExtension(AzureResourcesExtensionId);
}