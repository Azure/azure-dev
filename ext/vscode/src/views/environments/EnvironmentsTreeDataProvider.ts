// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { callWithTelemetryAndErrorHandling, IActionContext } from '@microsoft/vscode-azext-utils';
import { TelemetryId } from '../../telemetry/telemetryId';
import { WorkspaceAzureDevApplicationProvider } from '../../services/AzureDevApplicationProvider';
import { WorkspaceAzureDevEnvListProvider } from '../../services/AzureDevEnvListProvider';
import { WorkspaceAzureDevEnvValuesProvider } from '../../services/AzureDevEnvValuesProvider';
import { FileSystemWatcherService } from '../../services/FileSystemWatcherService';

export interface EnvironmentItem {
    name: string;
    isDefault: boolean;
    dotEnvPath?: string;
    configurationFile: vscode.Uri;
}

export interface EnvironmentVariableItem extends EnvironmentItem {
    key: string;
    value: string;
}

type TreeItemType = 'Environment' | 'Group' | 'Detail' | 'Variable';

export class EnvironmentTreeItem extends vscode.TreeItem {
    constructor(
        public readonly type: TreeItemType,
        label: string,
        collapsibleState: vscode.TreeItemCollapsibleState,
        public readonly data?: EnvironmentItem | EnvironmentVariableItem
    ) {
        super(label, collapsibleState);
    }
}

export class EnvironmentsTreeDataProvider implements vscode.TreeDataProvider<EnvironmentTreeItem> {
    private _onDidChangeTreeData: vscode.EventEmitter<EnvironmentTreeItem | undefined | null | void> = new vscode.EventEmitter<EnvironmentTreeItem | undefined | null | void>();
    readonly onDidChangeTreeData: vscode.Event<EnvironmentTreeItem | undefined | null | void> = this._onDidChangeTreeData.event;

    private readonly applicationProvider = new WorkspaceAzureDevApplicationProvider();
    private readonly envListProvider = new WorkspaceAzureDevEnvListProvider();
    private readonly envValuesProvider = new WorkspaceAzureDevEnvValuesProvider();
    private readonly configFileWatcherDisposable: vscode.Disposable;
    private readonly envDirWatcherDisposable: vscode.Disposable;
    private readonly visibleEnvVars = new Set<string>();

    constructor(private fileSystemWatcherService: FileSystemWatcherService) {
        const onFileChange = () => {
            this.refresh();
        };

        this.configFileWatcherDisposable = this.fileSystemWatcherService.watch(
            '**/azure.{yml,yaml}',
            onFileChange
        );

        this.envDirWatcherDisposable = this.fileSystemWatcherService.watch(
            '**/.azure/**',
            onFileChange
        );
    }

    refresh(): void {
        this._onDidChangeTreeData.fire();
    }

    toggleVisibility(item: EnvironmentTreeItem): void {
        if (item.type === 'Variable' && item.data) {
            const data = item.data as EnvironmentVariableItem;
            const id = `${data.name}/${data.key}`;
            if (this.visibleEnvVars.has(id)) {
                this.visibleEnvVars.delete(id);
            } else {
                this.visibleEnvVars.add(id);
            }

            // Signal that this item's representation has changed; getTreeItem will
            // recreate the TreeItem with the appropriate label and tooltip.
            this._onDidChangeTreeData.fire(item);
        }
    }

    getTreeItem(element: EnvironmentTreeItem): vscode.TreeItem {
        if (element.type === 'Variable' && element.data) {
            const data = element.data as EnvironmentVariableItem;
            const id = `${data.name}/${data.key}`;
            const isVisible = this.visibleEnvVars.has(id);

            const label = isVisible
                ? `${data.key}=${data.value}`
                : `${data.key}=Hidden value. Click to view.`;
            const tooltip = isVisible
                ? `${data.key}=${data.value}`
                : 'Click to view value';

            const treeItem = new vscode.TreeItem(label, vscode.TreeItemCollapsibleState.None);
            treeItem.tooltip = tooltip;
            treeItem.contextValue = element.contextValue;
            treeItem.command = element.command;
            treeItem.iconPath = element.iconPath;

            return treeItem;
        }
        return element;
    }

    async getChildren(element?: EnvironmentTreeItem): Promise<EnvironmentTreeItem[]> {
        return await callWithTelemetryAndErrorHandling(TelemetryId.WorkspaceViewEnvironmentResolve, async (context) => {
            if (!element) {
                return this.getEnvironments(context);
            }

            if (element.type === 'Environment') {
                return this.getEnvironmentDetails(context, element.data as EnvironmentItem);
            }

            if (element.type === 'Group' && element.label === 'Environment Variables') {
                return this.getEnvironmentVariables(context, element.data as EnvironmentItem);
            }

            return [];
        }) ?? [];
    }

    private async getEnvironments(context: IActionContext): Promise<EnvironmentTreeItem[]> {
        const applications = await this.applicationProvider.getApplications();
        if (applications.length === 0) {
            return [];
        }

        // Assuming single project for now as per requirement
        const app = applications[0];
        const envs = await this.envListProvider.getEnvListResults(context, app.configurationPath);

        return envs.map(env => {
            const item = new EnvironmentTreeItem(
                'Environment',
                env.Name,
                vscode.TreeItemCollapsibleState.Collapsed,
                {
                    name: env.Name,
                    isDefault: env.IsDefault,
                    dotEnvPath: env.DotEnvPath,
                    configurationFile: app.configurationPath
                } as EnvironmentItem
            );
            item.contextValue = 'ms-azuretools.azure-dev.views.environments.environment';

            if (env.IsDefault) {
                item.description = vscode.l10n.t('(Current)');
                item.contextValue += ';default';
                item.iconPath = new vscode.ThemeIcon('pass', new vscode.ThemeColor('testing.iconPassed'));
            } else {
                item.iconPath = new vscode.ThemeIcon('circle-large-outline');
            }

            return item;
        });
    }

    private async getEnvironmentDetails(context: IActionContext, env: EnvironmentItem): Promise<EnvironmentTreeItem[]> {
        const items: EnvironmentTreeItem[] = [];

        // Properties Group
        // For now, just listing properties directly or we could group them
        // Let's add a group for Variables
        const variablesGroup = new EnvironmentTreeItem(
            'Group',
            vscode.l10n.t('Environment Variables'),
            vscode.TreeItemCollapsibleState.Collapsed,
            env
        );
        variablesGroup.iconPath = new vscode.ThemeIcon('symbol-variable');
        items.push(variablesGroup);

        return items;
    }

    private async getEnvironmentVariables(context: IActionContext, env: EnvironmentItem): Promise<EnvironmentTreeItem[]> {
        const values = await this.envValuesProvider.getEnvValues(context, env.configurationFile, env.name);

        return Object.entries(values).map(([key, value]) => {
            const id = `${env.name}/${key}`;
            const isVisible = this.visibleEnvVars.has(id);
            const label = isVisible ? `${key}=${value}` : vscode.l10n.t('{0}=Hidden value. Click to view.', key);

            const item = new EnvironmentTreeItem(
                'Variable',
                label,
                vscode.TreeItemCollapsibleState.None,
                { ...env, key, value } as EnvironmentVariableItem
            );

            item.tooltip = isVisible ? `${key}=${value}` : vscode.l10n.t('Click to view value');
            item.iconPath = new vscode.ThemeIcon('key');
            item.command = {
                command: 'azure-dev.views.environments.toggleEnvVarVisibility',
                title: vscode.l10n.t('Toggle Environment Variable Visibility'),
                arguments: [item]
            };

            return item;
        });
    }

    dispose(): void {
        this.configFileWatcherDisposable.dispose();
        this.envDirWatcherDisposable.dispose();
        this._onDidChangeTreeData.dispose();
    }
}
