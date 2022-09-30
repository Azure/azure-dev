import * as fs from 'fs/promises';
import * as vscode from 'vscode';
import * as yaml from 'yaml';
import { ProvideResourceOptions, WorkspaceResource, WorkspaceResourceProvider } from './ResourceGroupsApi';

interface AzureDevCliApplicationConfguration {
    name?: string;
}

async function getAzureDevCliApplicationConfiguration(path: string): Promise<AzureDevCliApplicationConfguration> {
    const configurationYaml = await fs.readFile(path, 'utf8');

    return yaml.parse(configurationYaml) as AzureDevCliApplicationConfguration;
}

export class AzureDevCliWorkspaceResourceProvider implements WorkspaceResourceProvider {
    // TODO: Identify and report changes.
    // onDidChangeResource?: Event<WorkspaceResource | undefined> | undefined;

    // TODO: What if no workspace folder is open?
    async getResources(source: vscode.WorkspaceFolder, options?: ProvideResourceOptions | undefined): Promise<WorkspaceResource[]> {
        const files = await vscode.workspace.findFiles('**/azure.{yml,yaml}', '**/node_modules/**');
        
        const resources: WorkspaceResource[] = [];

        for (const file of files) {
            const config = await getAzureDevCliApplicationConfiguration(file.fsPath);

            resources.push({
                folder: source,
                id: file.fsPath,
                name: config.name ?? file.fsPath,
                type: 'ms-azuretools.azure-dev.application'
            });
        }

        return resources;
    }
}