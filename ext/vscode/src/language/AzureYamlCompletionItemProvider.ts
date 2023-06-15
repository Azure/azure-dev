// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { getContainingFolderUri } from './azureYamlUtils';

export class AzureYamlCompletionItemProvider implements vscode.CompletionItemProvider {
    public async provideCompletionItems(document: vscode.TextDocument, position: vscode.Position, token: vscode.CancellationToken, context: vscode.CompletionContext): Promise<vscode.CompletionItem[]> {
        // if (context.triggerKind !== vscode.CompletionTriggerKind.TriggerCharacter ||
        //     context.triggerCharacter !== '/') {
        //     // Shouldn't have been triggered, return an empty set
        //     return [];
        // }

        const pathPrefix = this.getPathPrefix(document, position);
        const matchingPaths = await this.getMatchingWorkspacePaths(document, pathPrefix, token);

        return matchingPaths.map((path) => {
            const completionItem = new vscode.CompletionItem(path);
            completionItem.insertText = path;
            completionItem.kind = vscode.CompletionItemKind.Folder;
            return completionItem;
        });
    }

    private getPathPrefix(document: vscode.TextDocument, position: vscode.Position): string | undefined {
        const line = document.lineAt(position.line);
        const lineRegex = /\s+project:\s*(?<project>\S*)/i;
        const match = lineRegex.exec(line.text);

        return match?.groups?.['project'];
    }

    private async getMatchingWorkspacePaths(document: vscode.TextDocument, pathPrefix: string | undefined, token: vscode.CancellationToken): Promise<string[]> {
        if (!pathPrefix || pathPrefix[0] !== '.') {
            return [];
        }

        const currentFolder = vscode.Uri.joinPath(getContainingFolderUri(document.uri), pathPrefix);
        const results: string[] = [];

        for (const [file, type] of await vscode.workspace.fs.readDirectory(currentFolder)) {
            if (type !== vscode.FileType.Directory) {
                continue;
            } else if (file[0] === '.') {
                // Ignore folders that start with '.'
                continue;
            }

            results.push(file);
        }

        return results;
    }
}
