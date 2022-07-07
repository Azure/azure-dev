// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';

export async function executeInTerminal(command: string, options: vscode.TerminalOptions): Promise<void> {
    const term = vscode.window.createTerminal(options);
    await term.processId;
    term.show();
    term.sendText(command, true /* add new line */);
}
