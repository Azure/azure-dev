// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import * as path from 'path';
import * as vscode from 'vscode';
import { createAzureDevCli } from '../utils/azureDevCli';
import { execAsync } from '../utils/process';
import { withTimeout } from '../utils/withTimeout';

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

        const command = azureCli.commandBuilder
            .withArg('show')
            .withArg('--no-prompt')
            .withNamedArg('--cwd', configurationFileDirectory)
            .withNamedArg('--environment', environmentName)
            .withNamedArg('--output', 'json')
            .build();

        const options = azureCli.spawnOptions(configurationFileDirectory);

        const showResultsJson = await withTimeout(execAsync(command, options), 30000);

        return JSON.parse(showResultsJson.stdout) as AzDevShowResults;
    }
}
