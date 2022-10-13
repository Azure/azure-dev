import * as fs from 'fs/promises';
import { Subscription } from 'rxjs';
import * as vscode from 'vscode';
import * as yaml from 'yaml';
import { AzureDevApplication, AzureDevApplicationProvider } from '../../services/AzureDevApplicationProvider';
import { ProvideResourceOptions, WorkspaceResource, WorkspaceResourceProvider } from './ResourceGroupsApi';

interface AzureDevCliApplicationConfguration {
    name?: string;
}

async function getAzureDevCliApplicationConfiguration(path: string): Promise<AzureDevCliApplicationConfguration> {
    const configurationYaml = await fs.readFile(path, 'utf8');

    return yaml.parse(configurationYaml) as AzureDevCliApplicationConfguration;
}

export class AzureDevCliWorkspaceResourceProvider extends vscode.Disposable implements WorkspaceResourceProvider {
    private readonly onDidChangeResourceEmitter = new vscode.EventEmitter<WorkspaceResource | undefined>();
    private readonly applicationsSubscription: Subscription;

    private applications: AzureDevApplication[] = [];

    constructor(applicationProvider: AzureDevApplicationProvider) {
        super(
            () => {
                this.applicationsSubscription.unsubscribe();
                this.onDidChangeResourceEmitter.dispose();
            });

        this.applicationsSubscription =
            applicationProvider
                .applications
                .subscribe(
                    applications => {
                        this.applications = applications;
                        this.onDidChangeResourceEmitter.fire(undefined);
                    });
    }

    readonly onDidChangeResource = this.onDidChangeResourceEmitter.event;

    async getResources(source: vscode.WorkspaceFolder, options?: ProvideResourceOptions | undefined): Promise<WorkspaceResource[]> {
        const resources: WorkspaceResource[] = [];

        for (const application of this.applications) {
            const config = await getAzureDevCliApplicationConfiguration(application.configurationPath.fsPath);

            resources.push({
                folder: source,
                id: application.configurationPath.fsPath,
                name: config.name ?? application.configurationPath.fsPath,
                type: 'ms-azuretools.azure-dev.application'
            });
        }

        return resources;
    }
}