// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzExtFsExtra } from '@microsoft/vscode-azext-utils';
import * as path from 'path';
import * as vscode from 'vscode';
import { getProjectRelativePath } from './azureYamlUtils';

export class AzureYamlDocumentDropEditProvider implements vscode.DocumentDropEditProvider {
    public async provideDocumentDropEdits(document: vscode.TextDocument, position: vscode.Position, dataTransfer: vscode.DataTransfer, token: vscode.CancellationToken): Promise<vscode.DocumentDropEdit | undefined> {
        const maybeFolder = dataTransfer.get('text/uri-list')?.value;
        const maybeFolderUri = vscode.Uri.parse(maybeFolder);

        if (await AzExtFsExtra.pathExists(maybeFolderUri) && await AzExtFsExtra.isDirectory(maybeFolderUri)) {
            const basename = path.basename(maybeFolderUri.fsPath);
            const newRelativePath = getProjectRelativePath(document.uri, maybeFolderUri);

            const initialWhitespace = position.character === 0 ? '\n\t' : '\n';

            const snippet = new vscode.SnippetString(initialWhitespace)
                .appendPlaceholder(basename).appendText(':\n')
                .appendText(`\t\tproject: ${newRelativePath}\n`)
                .appendText('\t\tlanguage: ')
                .appendChoice(['dotnet', 'csharp', 'fsharp', 'py', 'python', 'js', 'ts', 'java'])
                .appendText('\n')
                .appendText('\t\thost: ')
                .appendChoice(['appservice', 'containerapp', 'function', 'staticwebapp', 'aks'])
                .appendText('\n');
            return new vscode.DocumentDropEdit(snippet);
        }

        return undefined;
    }
}
