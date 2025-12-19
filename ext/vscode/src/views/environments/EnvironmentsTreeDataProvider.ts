import * as vscode from 'vscode';
import * as path from 'path';
import { callWithTelemetryAndErrorHandling, IActionContext } from '@microsoft/vscode-azext-utils';
import { TelemetryId } from '../../telemetry/telemetryId';
import { WorkspaceAzureDevApplicationProvider } from '../../services/AzureDevApplicationProvider';
import { WorkspaceAzureDevEnvListProvider } from '../../services/AzureDevEnvListProvider';
import { WorkspaceAzureDevEnvValuesProvider } from '../../services/AzureDevEnvValuesProvider';

interface EnvironmentItem {
    name: string;
    isDefault: boolean;
    dotEnvPath?: string;
    configurationFile: vscode.Uri;
}

type TreeItemType = 'Environment' | 'Group' | 'Detail';

class EnvironmentTreeItem extends vscode.TreeItem {
    constructor(
        public readonly type: TreeItemType,
        label: string,
        collapsibleState: vscode.TreeItemCollapsibleState,
        public readonly data?: EnvironmentItem
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
    private readonly configFileWatcher: vscode.FileSystemWatcher;

    constructor() {
        this.configFileWatcher = vscode.workspace.createFileSystemWatcher(
            '**/azure.{yml,yaml}',
            false, false, false
        );

        const onFileChange = () => {
            this.refresh();
        };

        this.configFileWatcher.onDidCreate(onFileChange);
        this.configFileWatcher.onDidChange(onFileChange);
        this.configFileWatcher.onDidDelete(onFileChange);
    }

    refresh(): void {
        this._onDidChangeTreeData.fire();
    }

    getTreeItem(element: EnvironmentTreeItem): vscode.TreeItem {
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
            item.iconPath = new vscode.ThemeIcon('cloud');
            if (env.IsDefault) {
                item.description = '(default)';
                item.contextValue += ';default';
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
            'Environment Variables',
            vscode.TreeItemCollapsibleState.Collapsed,
            env
        );
        variablesGroup.iconPath = new vscode.ThemeIcon('symbol-variable');
        items.push(variablesGroup);

        // Add other details if needed, e.g. location, subscription if available from other commands
        // For now, maybe just show the .env path if it exists
        if (env.dotEnvPath) {
            const dotEnvItem = new EnvironmentTreeItem(
                'Detail',
                `.env: ${path.basename(env.dotEnvPath)}`,
                vscode.TreeItemCollapsibleState.None
            );
            dotEnvItem.tooltip = env.dotEnvPath;
            dotEnvItem.iconPath = new vscode.ThemeIcon('file');
            dotEnvItem.command = {
                command: 'vscode.open',
                title: 'Open .env file',
                arguments: [vscode.Uri.file(env.dotEnvPath)]
            };
            items.push(dotEnvItem);
        }

        return items;
    }

    private async getEnvironmentVariables(context: IActionContext, env: EnvironmentItem): Promise<EnvironmentTreeItem[]> {
        const values = await this.envValuesProvider.getEnvValues(context, env.configurationFile, env.name);

        return Object.entries(values).map(([key, value]) => {
            const item = new EnvironmentTreeItem(
                'Detail',
                `${key}=${value}`, // Be careful with secrets, maybe mask them?
                vscode.TreeItemCollapsibleState.None
            );
            item.tooltip = `${key}=${value}`;
            item.iconPath = new vscode.ThemeIcon('key');
            return item;
        });
    }

    dispose(): void {
        this.configFileWatcher.dispose();
        this._onDidChangeTreeData.dispose();
    }
}
