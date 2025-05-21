// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from "@microsoft/vscode-azext-utils";
import * as vscode from 'vscode';
import { getEnvironments, EnvironmentInfo } from "./cmdUtil";

/**
 * Get the path to the .env file for an Azure developer environment.
 * 
 * This can be used in launch.json to provide environment variables to VS Code tasks:
 * ```json
 * {
 *   "configurations": [
 *     {
 *       "envFile": "${input:azdDotenv}"
 *     }
 *   ],
 *   "inputs": [
 *     {
 *       "id": "azdDotenv",
 *       "type": "command",
 *       "command": "azure-dev.commands.getDotEnvFilePath"
 *     }
 *   ]
 * }
 * ```
 * 
 * @param context - The VS Code extension action context
 * @param args - Optional array containing [environmentName, workingDir]
 *               - environmentName: Name of the environment to use (uses default if not provided)
 *               - workingDir: Working directory to find environments (uses first workspace folder if not provided)
 * @returns Path to the .env file for the specified environment
 */
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
