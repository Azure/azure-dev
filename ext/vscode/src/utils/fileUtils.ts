// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';

export async function fileExists(path: vscode.Uri): Promise<boolean> {
    try {
        return (await vscode.workspace.fs.stat(path)).type === vscode.FileType.File;
    } catch {
        return false;
    }
}
