// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import { composeArgs, withArg, withNamedArg } from '@microsoft/vscode-processutils';
import { createAzureDevCli } from '../utils/azureDevCli';
import { execAsync } from '../utils/execAsync';

export interface AzureDevExtension {
    readonly id: string;
    readonly name: string;
    readonly version: string;
}

export type AzDevExtensionListResults = AzureDevExtension[];

export interface AzureDevExtensionProvider {
    getExtensionListResults(context: IActionContext): Promise<AzDevExtensionListResults>;
}

export class WorkspaceAzureDevExtensionProvider implements AzureDevExtensionProvider {
    public async getExtensionListResults(context: IActionContext): Promise<AzDevExtensionListResults> {
        const azureCli = await createAzureDevCli(context);

        const args = composeArgs(
            withArg('extension', 'list'),
            withNamedArg('--output', 'json'),
        )();

        try {
            const { stdout } = await execAsync(azureCli.invocation, args, azureCli.spawnOptions());
            return JSON.parse(stdout) as AzDevExtensionListResults;
        } catch (err) {
            // If command fails (e.g. not supported or no extensions), return empty list
            return [];
        }
    }
}
