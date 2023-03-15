// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';

/**
 * Sets a VSCode context to be used in when clauses
 * @param context The name of the context value to set
 * @param value The value to set the context to. This can be a boolean, string, or number.
 */
export async function setVsCodeContext(contextName: string, value: boolean | string | number): Promise<void> {
    await vscode.commands.executeCommand('setContext', contextName, value);
}