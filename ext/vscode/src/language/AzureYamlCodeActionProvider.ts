// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzExtFsExtra } from '@microsoft/vscode-azext-utils';
import * as path from 'path';
import * as vscode from 'vscode';
import { isAzureYamlProjectPathDiagnostic } from './AzureYamlDiagnosticProvider';
import { getContainingFolderUri } from './getContainingFolderUri';

export class AzureYamlCodeActionProvider extends vscode.Disposable implements vscode.CodeActionProvider {
    private readonly knownFolderRenames: { oldFolder: vscode.Uri, newFolder: vscode.Uri }[] = [];

    public constructor() {
        const disposables: vscode.Disposable[] = [];

        disposables.push(vscode.workspace.onDidRenameFiles(e => this.onDidRenameFiles(e)));

        super(() => {
            vscode.Disposable.from(...disposables).dispose();
        });
    }

    public async provideCodeActions(document: vscode.TextDocument, range: vscode.Range | vscode.Selection, context: vscode.CodeActionContext, token: vscode.CancellationToken): Promise<vscode.CodeAction[]> {
        const diagnostics = vscode.languages
            .getDiagnostics(document.uri)
            .filter(isAzureYamlProjectPathDiagnostic);

        if (!diagnostics || diagnostics.length === 0) {
            // Nothing to do
            return [];
        }

        const results: vscode.CodeAction[] = [];

        for (const diagnostic of diagnostics) {
            const azureYamlFolder = getContainingFolderUri(document.uri);
            const missingFolder = vscode.Uri.joinPath(azureYamlFolder, diagnostic.sourceNode.value);

            const knownFolderRename = this.knownFolderRenames.find(r => r.oldFolder.fsPath === missingFolder.fsPath);
            if (knownFolderRename) {
                const newRelativeFolder = path.posix
                    .normalize(
                        path.relative(azureYamlFolder.fsPath, knownFolderRename.newFolder.fsPath)
                    )
                    .replace(/\\/g, '/') // Turn backslashes into forward slashes
                    .replace(/^\.?\/?/, './'); // Ensure it starts with ./
                

                const action = new vscode.CodeAction(vscode.l10n.t('Change path to "{0}"', newRelativeFolder), vscode.CodeActionKind.QuickFix);

                const edit = new vscode.WorkspaceEdit();
                edit.replace(document.uri, diagnostic.range, newRelativeFolder);
                action.edit = edit;

                results.push(action);
            }
        }

        return results;
    }

    private async onDidRenameFiles(e: vscode.FileRenameEvent): Promise<void> {
        if (await AzExtFsExtra.isDirectory(e.files[0].newUri)) {
            // If the new URI is a directory, then this is a folder rename event, and we should keep track
            this.knownFolderRenames.push({ oldFolder: e.files[0].oldUri, newFolder: e.files[0].newUri });
        }
    }
}
