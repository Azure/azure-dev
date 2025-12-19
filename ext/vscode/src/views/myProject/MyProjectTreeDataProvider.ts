import * as vscode from 'vscode';
import * as path from 'path';
import { AzureDevCliModel } from '../workspace/AzureDevCliModel';
import { AzureDevApplicationProvider, WorkspaceAzureDevApplicationProvider } from '../../services/AzureDevApplicationProvider';
import { AzureDevCliApplication } from '../workspace/AzureDevCliApplication';
import { WorkspaceAzureDevShowProvider } from '../../services/AzureDevShowProvider';
import { WorkspaceAzureDevEnvListProvider } from '../../services/AzureDevEnvListProvider';
import { WorkspaceResource } from '@microsoft/vscode-azureresources-api';

export class MyProjectTreeDataProvider implements vscode.TreeDataProvider<AzureDevCliModel> {
    private _onDidChangeTreeData: vscode.EventEmitter<AzureDevCliModel | undefined | null | void> = new vscode.EventEmitter<AzureDevCliModel | undefined | null | void>();
    readonly onDidChangeTreeData: vscode.Event<AzureDevCliModel | undefined | null | void> = this._onDidChangeTreeData.event;

    private readonly applicationProvider: AzureDevApplicationProvider;
    private readonly showProvider = new WorkspaceAzureDevShowProvider();
    private readonly envListProvider = new WorkspaceAzureDevEnvListProvider();
    private readonly configFileWatcher: vscode.FileSystemWatcher;

    constructor() {
        this.applicationProvider = new WorkspaceAzureDevApplicationProvider();

        // Listen to azure.yaml file changes globally
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

    getTreeItem(element: AzureDevCliModel): vscode.TreeItem | Thenable<vscode.TreeItem> {
        return element.getTreeItem();
    }

    async getChildren(element?: AzureDevCliModel): Promise<AzureDevCliModel[]> {
        if (element) {
            return element.getChildren();
        }

        const applications = await this.applicationProvider.getApplications();
        const children: AzureDevCliModel[] = [];

        for (const application of applications) {
            const configurationFilePath = application.configurationPath.fsPath;
            const configurationFolder = application.configurationFolder;
            const configurationFolderName = path.basename(configurationFolder);

            const workspaceResource: WorkspaceResource = {
                folder: application.workspaceFolder,
                id: configurationFilePath,
                name: configurationFolderName,
                resourceType: 'ms-azuretools.azure-dev.application'
            };

            const appModel = new AzureDevCliApplication(
                workspaceResource,
                (model) => this._onDidChangeTreeData.fire(model),
                this.showProvider,
                this.envListProvider,
                false // Do not include environments
            );

            children.push(appModel);
        }

        return children;
    }

    dispose(): void {
        this.configFileWatcher.dispose();
        this._onDidChangeTreeData.dispose();
    }
}
