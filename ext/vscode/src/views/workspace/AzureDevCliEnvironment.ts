// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AzureDevCliModel } from './AzureDevCliModel';
import { AzureDevCliEnvironmentsModelContext } from './AzureDevCliEnvironments';
import { AzureDevEnvValuesProvider } from '../../services/AzureDevEnvValuesProvider';
import { AzureDevCliEnvironmentVariables } from './AzureDevCliEnvironmentVariables';

export class AzureDevCliEnvironment implements AzureDevCliModel {
    constructor(
        public readonly context: AzureDevCliEnvironmentsModelContext,
        public readonly name: string,
        private readonly isDefault: boolean,
        public readonly environmentFile: vscode.Uri | undefined,
        private readonly envValuesProvider: AzureDevEnvValuesProvider,
        private readonly visibleEnvVars: Set<string>,
        private readonly onToggleVisibility: (key: string) => void) {
    }

    getChildren(): AzureDevCliModel[] {
        return [
            new AzureDevCliEnvironmentVariables(
                this.context,
                this.envValuesProvider,
                this.name,
                this.visibleEnvVars,
                this.onToggleVisibility
            )
        ];
    }

    getTreeItem(): vscode.TreeItem {
        const treeItem = new vscode.TreeItem(this.name, vscode.TreeItemCollapsibleState.Collapsed);

        treeItem.contextValue = 'ms-azuretools.azure-dev.views.workspace.environment';

        if (this.isDefault) {
            treeItem.contextValue += ';default';
            treeItem.description = vscode.l10n.t('(Current)');
            treeItem.iconPath = new vscode.ThemeIcon('pass', new vscode.ThemeColor('testing.iconPassed'));
        } else {
            treeItem.iconPath = new vscode.ThemeIcon('circle-large-outline');
        }

        return treeItem;
    }
}
