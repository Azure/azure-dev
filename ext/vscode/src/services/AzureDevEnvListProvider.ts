// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import { composeArgs, withArg, withNamedArg } from '@microsoft/vscode-processutils';
import * as path from 'path';
import * as vscode from 'vscode';
import { createAzureDevCli } from '../utils/azureDevCli';
import { execAsync } from '../utils/execAsync';

interface AzureDevEnvironment {
    readonly Name: string;
    readonly IsDefault: boolean;
    readonly DotEnvPath?: string;
}

export type AzDevEnvListResults = AzureDevEnvironment[];

export interface AzureDevEnvListProvider {
    getEnvListResults(context: IActionContext, configurationFile: vscode.Uri): Promise<AzDevEnvListResults>;
}

export class WorkspaceAzureDevEnvListProvider implements AzureDevEnvListProvider {
    public async getEnvListResults(context: IActionContext, configurationFile: vscode.Uri): Promise<AzDevEnvListResults> {
        const azureCli = await createAzureDevCli(context);

        const configurationFileDirectory = path.dirname(configurationFile.fsPath);

        const args = composeArgs(
            withArg('env', 'list', '--no-prompt'),
            withNamedArg('--cwd', configurationFileDirectory, { shouldQuote: true }),
            withNamedArg('--output', 'json'),
        )();

        const { stdout } = await execAsync(azureCli.invocation, args, azureCli.spawnOptions(configurationFileDirectory));
        return JSON.parse(stdout) as AzDevEnvListResults;
    }
}
