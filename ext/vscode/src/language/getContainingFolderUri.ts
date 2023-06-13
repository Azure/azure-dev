// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';

export function getContainingFolderUri(targetUri: vscode.Uri): vscode.Uri {
    return vscode.Uri.joinPath(targetUri, '..');
}
