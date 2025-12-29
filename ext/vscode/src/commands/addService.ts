// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import * as yaml from 'yaml';
import { AzureDevCliModel } from '../views/workspace/AzureDevCliModel';

/**
 * Adds a new service to the azure.yaml file associated with the given tree item.
 * This command is invoked from the Services tree item inline action.
 */
export async function addService(context: IActionContext, node?: AzureDevCliModel): Promise<void> {
    let documentUri: vscode.Uri | undefined;

    // Get the azure.yaml file URI from the tree node context
    if (node && 'context' in node && node.context.configurationFile) {
        documentUri = node.context.configurationFile;
    }

    // If no URI was provided via tree node, try to find an azure.yaml in the workspace
    if (!documentUri) {
        const workspaceFolders = vscode.workspace.workspaceFolders;
        if (!workspaceFolders || workspaceFolders.length === 0) {
            void vscode.window.showErrorMessage('No workspace folder is open.');
            return;
        }

        // Search for azure.yaml or azure.yml files in workspace
        const azureYamlFiles = await vscode.workspace.findFiles('**/azure.{yml,yaml}', '**/node_modules/**', 1);
        if (azureYamlFiles.length === 0) {
            void vscode.window.showErrorMessage('No azure.yaml file found in workspace.');
            return;
        }

        documentUri = azureYamlFiles[0];
    }

    // Prompt for service name
    const serviceName = await vscode.window.showInputBox({
        prompt: 'Enter service name',
        placeHolder: 'api',
        validateInput: (value) => {
            if (!value || !/^[a-zA-Z0-9-_]+$/.test(value)) {
                return 'Service name must contain only letters, numbers, hyphens, and underscores';
            }
            return undefined;
        }
    });

    if (!serviceName) {
        return;
    }

    // Prompt for programming language
    const language = await vscode.window.showQuickPick(
        ['python', 'js', 'ts', 'csharp', 'java', 'go'],
        { placeHolder: 'Select programming language' }
    );

    if (!language) {
        return;
    }

    // Prompt for Azure host
    const host = await vscode.window.showQuickPick(
        [
            { label: 'containerapp', description: 'Azure Container Apps' },
            { label: 'appservice', description: 'Azure App Service' },
            { label: 'function', description: 'Azure Functions' }
        ],
        { placeHolder: 'Select Azure host' }
    );

    if (!host) {
        return;
    }

    try {
        const document = await vscode.workspace.openTextDocument(documentUri);
        const text = document.getText();
        const doc = yaml.parseDocument(text);

        const services = doc.get('services') as yaml.YAMLMap;
        if (!services) {
            void vscode.window.showErrorMessage('No services section found in azure.yaml');
            return;
        }

        const serviceSnippet = `\n  ${serviceName}:\n    project: ./${serviceName}\n    language: ${language}\n    host: ${host.label}`;

        // Find the end of the services section
        if (doc.contents && yaml.isMap(doc.contents)) {
            const servicesNode = doc.contents.items.find((item) => yaml.isScalar(item.key) && item.key.value === 'services');
            if (servicesNode && servicesNode.value && 'range' in servicesNode.value && servicesNode.value.range) {
                const insertPosition = document.positionAt(servicesNode.value.range[1]);
                const edit = new vscode.WorkspaceEdit();
                edit.insert(documentUri, insertPosition, serviceSnippet);
                const success = await vscode.workspace.applyEdit(edit);

                if (success) {
                    void vscode.window.showInformationMessage(`Service '${serviceName}' added to azure.yaml`);
                }
            }
        }
    } catch (error) {
        void vscode.window.showErrorMessage(`Failed to add service: ${error instanceof Error ? error.message : String(error)}`);
    }
}
