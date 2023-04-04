// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from "@microsoft/vscode-azext-utils";
import * as vscode from 'vscode';
import { getEnvironments, EnvironmentInfo } from "./cmdUtil";

export async function getDotEnvFilePath(context: IActionContext, args: string[] | undefined): Promise<string> {
    const [environmentName, workingDir] = args ?? [];
    
    let cwd: string;
    if (workingDir) {
        cwd = workingDir;
    } else {
        if (vscode.workspace.workspaceFolders && vscode.workspace.workspaceFolders.length === 1) {
            cwd = vscode.workspace.workspaceFolders[0].uri.fsPath;
        } else {
            throw new Error(vscode.l10n.t("Working directory could not be determined"));
        }
    }

    let envData: EnvironmentInfo[] = [];
    try {
        envData = await getEnvironments(context, cwd);
    } catch { }
    if (envData.length === 0) {
        context.errorHandling.suppressReportIssue = true;
        throw new Error(vscode.l10n.t("No Azure developer environments found. You can create one by running 'azd env new' in the terminal."));
    }

    const byName: (ei: EnvironmentInfo) => boolean = environmentName ? 
        ei => ei.Name === environmentName : ei => ei.IsDefault;
    const env = envData.find(byName);
    if (!env) {
        if (environmentName) {
            throw new Error(vscode.l10n.t("Azure developer environment '{0}' was not found", environmentName));
        } else {
            throw new Error(vscode.l10n.t('There is no default Azure developer environment, cannot determine environment settings file path'));
        }
    }

    return env.DotEnvPath;
}
