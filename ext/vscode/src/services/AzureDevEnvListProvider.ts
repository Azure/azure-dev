// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import * as path from 'path';
import * as vscode from 'vscode';
import { createAzureDevCli } from '../utils/azureDevCli';
import { execAsync } from '../utils/process';
import { withTimeout } from '../utils/withTimeout';

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

        const command = azureCli.commandBuilder
            .withArg('env')
            .withArg('list')
            .withArg('--no-prompt')
            .withNamedArg('--cwd', configurationFileDirectory)
            .withNamedArg('--output', 'json')
            .build();

        const options = azureCli.spawnOptions(configurationFileDirectory);

        const envListResultsJson = await withTimeout(execAsync(command, options), 30000);

        return JSON.parse(envListResultsJson.stdout) as AzDevEnvListResults;
    }
}
