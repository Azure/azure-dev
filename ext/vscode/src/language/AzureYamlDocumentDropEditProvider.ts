// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzExtFsExtra } from '@microsoft/vscode-azext-utils';
import * as path from 'path';
import * as vscode from 'vscode';

export class AzureYamlDocumentDropEditProvider implements vscode.DocumentDropEditProvider {
    public async provideDocumentDropEdits(document: vscode.TextDocument, position: vscode.Position, dataTransfer: vscode.DataTransfer, token: vscode.CancellationToken): Promise<vscode.DocumentDropEdit | undefined> {
        const maybeFolder = dataTransfer.get('text/uri-list')?.value;
        const folder = vscode.Uri.parse(maybeFolder);

        const basename = path.basename(folder.fsPath);
        const newRelativePath = path.posix.normalize(path.relative(path.dirname(document.uri.fsPath), folder.fsPath)).replace(/\\/g, '/').replace(/^\.?\/?/, './');

        if (await AzExtFsExtra.pathExists(folder) && await AzExtFsExtra.isDirectory(folder)) {
            const snippet = new vscode.SnippetString('\t')
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