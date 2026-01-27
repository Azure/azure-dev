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
    public constructor(private readonly createAzureDevCliFunction = createAzureDevCli, private readonly execAsyncFunction = execAsync) {
    }

    public async getShowResults(context: IActionContext, configurationFile: vscode.Uri, environmentName?: string): Promise<AzDevShowResults> {
        const azureCli = await this.createAzureDevCliFunction(context);

        const configurationFileDirectory = path.dirname(configurationFile.fsPath);

        const args = composeArgs(
            withArg('show', '--no-prompt'),
            withNamedArg('--cwd', configurationFileDirectory, { shouldQuote: true }),
            withNamedArg('--environment', environmentName, { shouldQuote: true }),
            withNamedArg('--output', 'json'),
        )();

        try {
            const { stdout } = await this.execAsyncFunction(azureCli.invocation, args, azureCli.spawnOptions(configurationFileDirectory));
            return JSON.parse(stdout) as AzDevShowResults;
        } catch (error) {
            // Provide user-friendly error messages for common issues
            const errorMessage = error instanceof Error ? error.message : String(error);

            if (errorMessage.includes('File is empty') || errorMessage.includes('unable to parse azure.yaml')) {
                throw new Error(vscode.l10n.t('The azure.yaml file is invalid or empty. Please check the Problems panel for validation errors.'));
            }

            if (errorMessage.includes('parsing project file')) {
                throw new Error(vscode.l10n.t('Failed to parse azure.yaml. Please check the Problems panel for validation errors.'));
            }

            // Re-throw the original error for other cases
            throw error;
        }
    }
}
