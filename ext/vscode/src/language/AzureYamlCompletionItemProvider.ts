// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';

export class AzureYamlCompletionItemProvider implements vscode.CompletionItemProvider {
    public async provideCompletionItems(document: vscode.TextDocument, position: vscode.Position, token: vscode.CancellationToken, context: vscode.CompletionContext): Promise<vscode.CompletionItem[]> {
        // if (context.triggerKind !== vscode.CompletionTriggerKind.TriggerCharacter ||
        //     context.triggerCharacter !== '/') {
        //     // Shouldn't have been triggered, return an empty set
        //     return [];
        // }

        const pathPrefix = this.getPathPrefix(document, position);
        const matchingPaths = await this.getMatchingPaths(document, pathPrefix, token);
    
        return matchingPaths.map((path) => {
            const completionItem = new vscode.CompletionItem(path);
            completionItem.insertText = path;
            completionItem.kind = vscode.CompletionItemKind.Folder;
            return completionItem;
        });
    }

    private getPathPrefix(document: vscode.TextDocument, position: vscode.Position): string | undefined {
        const prefixRange = document.getWordRangeAtPosition(position);

        if (!prefixRange) {
            return undefined;
        }

        return document.getText(prefixRange);
    }

    private async getMatchingPaths(document: vscode.TextDocument, pathPrefix: string | undefined, token: vscode.CancellationToken): Promise<string[]> {
        if (!pathPrefix || pathPrefix[0] !== '.') {
            return [];
        }

        const currentFolder = vscode.Uri.joinPath(document.uri, '.' + pathPrefix);
        const results: string[] = [];
        
        for (const [file, type] of await vscode.workspace.fs.readDirectory(currentFolder)) {
            if (type !== vscode.FileType.Directory) {
                continue;
            }

            results.push(file);
        }

        return results;
    }
}