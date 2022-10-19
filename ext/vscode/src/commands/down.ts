// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import * as path from 'path';
import { IActionContext } from "@microsoft/vscode-azext-utils";
import { localize } from '../localize';
import { quickPickWorkspaceFolder } from '../utils/quickPickWorkspaceFolder';
import { getAzDevTerminalTitle, pickAzureYamlFile, selectApplicationTemplate, showReadmeFile } from './cmdUtil';
import { createAzureDevCli } from '../utils/azureDevCli';
import { executeAsTask } from '../utils/executeAsTask';
import { TelemetryId } from '../telemetry/telemetryId';
import { AzureDevCliApplication } from '../views/workspace/AzureDevCliApplication';

interface TreeViewModel {
    unwrap<T>(): T;
}

function isTreeViewModel(selectedItem: vscode.Uri | TreeViewModel | undefined): selectedItem is TreeViewModel {
    return !!(selectedItem as TreeViewModel).unwrap;
}

export async function down(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel): Promise<void> {
    const selectedFile = isTreeViewModel(selectedItem) ? selectedItem.unwrap<AzureDevCliApplication>().context.configurationFile : selectedItem;

    let folder: vscode.WorkspaceFolder | undefined = (selectedFile ? vscode.workspace.getWorkspaceFolder(selectedFile) : undefined);
    if (!folder) {
        folder = await quickPickWorkspaceFolder(context, localize('azure-dev.commands.cli.init.needWorkspaceFolder', "To run '{0}' command you must first open a folder or workspace in VS Code", 'down'));
    }

    const azureCli = await createAzureDevCli(context);
    let command = azureCli.commandBuilder
        .withArg('down');
    let workingDir = folder.uri;

    const azureYamlFile = selectedFile ?? await pickAzureYamlFile(context);
    if (azureYamlFile) {
        // Workspace has already been initialized, no need to specify a template
        workingDir = vscode.Uri.file(path.dirname(azureYamlFile.fsPath));
    } else {
        const templateUrl = await selectApplicationTemplate(context);
        command = command.withNamedArg('-t', {value: templateUrl, quoting: vscode.ShellQuoting.Strong});
    }

    // TODO: Prompt user to confirm delection (as well as for Key Vaults?).
    //       Add `--force` flag to skip prompt.
    //       Separate option for `--purge` to skip prompt for soft-delete? (or just always use `--purge`?) (or --no-prompt?)

    // Don't wait
    void executeAsTask(command.build(), getAzDevTerminalTitle(), {
        alwaysRunNew: true,
        cwd: workingDir.fsPath,
        env: azureCli.env
    }, TelemetryId.DownCli).then(() => {
        // Only show README if we are initializing a new workspace/application
        if (!azureYamlFile) {
            void showReadmeFile(workingDir.fsPath);
        }
    });
}
