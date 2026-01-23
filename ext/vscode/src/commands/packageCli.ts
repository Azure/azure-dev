// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import { composeArgs, withArg } from '@microsoft/vscode-processutils';
import * as vscode from 'vscode';
import { TelemetryId } from '../telemetry/telemetryId';
import { createAzureDevCli } from '../utils/azureDevCli';
import { executeAsTask } from '../utils/executeAsTask';
import { isAzureDevCliModel, isTreeViewModel, TreeViewModel } from '../utils/isTreeViewModel';
import { AzureDevCliModel } from '../views/workspace/AzureDevCliModel';
import { AzureDevCliService } from '../views/workspace/AzureDevCliService';
import { getAzDevTerminalTitle, getWorkingFolder } from './cmdUtil';

// `package` is a reserved identifier so `packageCli` had to be used instead
export async function packageCli(context: IActionContext, selectedItem?: vscode.Uri | TreeViewModel): Promise<void> {
    let selectedModel: AzureDevCliModel | undefined;
    let selectedFile: vscode.Uri | undefined;

    if (isTreeViewModel(selectedItem)) {
        selectedModel = selectedItem.unwrap<AzureDevCliModel>();
        selectedFile = selectedModel.context.configurationFile;
    } else if (isAzureDevCliModel(selectedItem)) {
        selectedModel = selectedItem;
        selectedFile = selectedModel.context.configurationFile;
    } else {
        selectedFile = selectedItem as vscode.Uri;
    }

    // Validate that selectedFile is valid for file system operations
    // Virtual file systems or certain VS Code contexts may not provide a valid fsPath
    if (selectedFile && !selectedFile.fsPath) {
        context.errorHandling.suppressReportIssue = true;
        const itemType = isTreeViewModel(selectedItem) ? 'TreeViewModel' : 
                        isAzureDevCliModel(selectedItem) ? 'AzureDevCliModel' : 
                        selectedItem ? 'vscode.Uri' : 'undefined';
        throw new Error(vscode.l10n.t(
            "Unable to determine working folder for package command. The selected file has an unsupported URI scheme '{0}' (selectedItem type: {1}). " +
            "Azure Developer CLI commands are not supported in virtual file systems. Please open a local folder or clone the repository locally.",
            selectedFile.scheme,
            itemType
        ));
    }

    const workingFolder = await getWorkingFolder(context, selectedFile);

    const azureCli = await createAzureDevCli(context);
    const args = composeArgs(
        withArg('package'),
        withArg(selectedModel instanceof AzureDevCliService ? selectedModel.name : '--all'),
    )();

    void executeAsTask(azureCli.invocation, args, getAzDevTerminalTitle(), azureCli.spawnOptions(workingFolder), {
        alwaysRunNew: true,
    }, TelemetryId.PackageCli);
}
