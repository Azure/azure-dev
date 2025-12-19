// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import { composeArgs, withArg, withNamedArg } from '@microsoft/vscode-processutils';
import * as path from 'path';
import * as vscode from 'vscode';
import { createAzureDevCli } from '../utils/azureDevCli';
import { execAsync } from '../utils/execAsync';

export type AzDevEnvValuesResults = Record<string, string>;

export interface AzureDevEnvValuesProvider {
    getEnvValues(context: IActionContext, configurationFile: vscode.Uri, environmentName: string): Promise<AzDevEnvValuesResults>;
}

export class WorkspaceAzureDevEnvValuesProvider implements AzureDevEnvValuesProvider {
    public async getEnvValues(context: IActionContext, configurationFile: vscode.Uri, environmentName: string): Promise<AzDevEnvValuesResults> {
        const azureCli = await createAzureDevCli(context);
        const configurationFileDirectory = path.dirname(configurationFile.fsPath);

        const args = composeArgs(
            withArg('env', 'get-values'),
            withNamedArg('--environment', environmentName, { shouldQuote: true }),
            withNamedArg('--cwd', configurationFileDirectory, { shouldQuote: true }),
            withNamedArg('--output', 'json'),
        )();

        try {
            const { stdout } = await execAsync(azureCli.invocation, args, azureCli.spawnOptions(configurationFileDirectory));
            return JSON.parse(stdout) as AzDevEnvValuesResults;
        } catch (error) {
            // Fallback or handle error if json output is not supported or command fails
            // For now, assuming JSON output is supported in recent azd versions
            console.error('Failed to get env values', error);
            return {};
        }
    }
}
