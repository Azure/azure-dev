// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import { composeArgs, withArg, withNamedArg } from '@microsoft/vscode-processutils';
import * as path from 'path';
import * as vscode from 'vscode';
import { createAzureDevCli } from '../utils/azureDevCli';
import { execAsync } from '../utils/execAsync';

interface AzureDevService {
    readonly project: {
        readonly path: string;
        readonly language: string;
    };
    readonly target: {
        readonly resourceIds: string[];
    };
}

interface AzureDevResource {
    readonly id: string;
    readonly type: string;
}

export interface AzDevShowResults {
    readonly name: string;
    readonly services?: { readonly [name: string]: AzureDevService };
    readonly environmentName?: string; // Available in version 0.6.0+
    readonly resources?: AzureDevResource[];
}

export interface AzureDevShowProvider {
    getShowResults(context: IActionContext, configurationFile: vscode.Uri, environmentName?: string): Promise<AzDevShowResults>;
}

export class WorkspaceAzureDevShowProvider implements AzureDevShowProvider {
    public async getShowResults(context: IActionContext, configurationFile: vscode.Uri, environmentName?: string): Promise<AzDevShowResults> {
        const azureCli = await createAzureDevCli(context);

        const configurationFileDirectory = path.dirname(configurationFile.fsPath);

        const args = composeArgs(
            withArg('show', '--no-prompt'),
            withNamedArg('--cwd', configurationFileDirectory, { shouldQuote: true }),
            withNamedArg('--environment', environmentName, { shouldQuote: true }),
            withNamedArg('--output', 'json'),
        )();

        const { stdout } = await execAsync(azureCli.invocation, args, azureCli.spawnOptions(configurationFileDirectory));

        return JSON.parse(stdout) as AzDevShowResults;
    }
}
