// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as path from 'path';
import * as vscode from 'vscode';
import * as yaml from 'yaml';
import { AzureYamlSelector } from './languageFeatures';

interface AzureYamlProjectInformation {
    azureYamlUri: vscode.Uri;
    serviceName: string;

    projectValue: string;
    projectUri: vscode.Uri;

    projectValueNodeRange: vscode.Range;
}

export async function getAzureYamlProjectInformation(document: vscode.TextDocument): Promise<AzureYamlProjectInformation[]> {
    if (!vscode.languages.match(AzureYamlSelector, document)) {
        throw new Error('Document is not an Azure YAML file');
    }

    // Parse the document
    const yamlDocument = yaml.parseDocument(document.getText()) as yaml.Document;
    if (!yamlDocument || yamlDocument.errors.length > 0) {
        throw new Error(vscode.l10n.t('Unable to parse {0}', document.uri.toString()));
    }

    const results: AzureYamlProjectInformation[] = [];

    const services = yamlDocument.get('services') as yaml.YAMLMap<yaml.Scalar<string>, yaml.YAMLMap>;

    // For each service, ensure that a directory exists matching the relative path specified for the service
    for (const service of services?.items || []) {
        const projectNode = service.value?.get('project', true) as yaml.Scalar<string>;
        const projectPath = projectNode?.value;

        if (!projectNode || !projectPath || !service.key || !projectNode.range?.[0] || !projectNode.range?.[1]) {
            continue;
        }

        results.push({
            azureYamlUri: document.uri,
            serviceName: service.key.value,
            projectValue: projectPath,
            projectUri: vscode.Uri.joinPath(getContainingFolderUri(document.uri), projectPath),
            projectValueNodeRange: new vscode.Range(
                document.positionAt(projectNode.range[0]),
                document.positionAt(projectNode.range[1])
            ),
        });
    }

    return results;
}

export function getContainingFolderUri(targetUri: vscode.Uri): vscode.Uri {
    return vscode.Uri.joinPath(targetUri, '..');
}

export function getProjectRelativePath(azureYamlUri: vscode.Uri, projectUri: vscode.Uri): string {
    const relativePath = path.relative(path.dirname(azureYamlUri.fsPath), projectUri.fsPath);
    const normalizedPosixRelativePath = path.posix.normalize(relativePath)
        .replace(/\\/g, '/') // Replace backslashes with forward slashes
        .replace(/^\.?\/?/, './'); // Make sure it starts with `./`

    return normalizedPosixRelativePath;
}
