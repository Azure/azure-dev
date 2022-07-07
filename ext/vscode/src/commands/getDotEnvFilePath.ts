// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { workspace } from "vscode";
import { IActionContext } from "@microsoft/vscode-azext-utils";
import { localize } from "../localize";
import { getEnvironments, EnvironmentInfo } from "./cmdUtil";

export async function getDotEnvFilePath(context: IActionContext, args: string[] | undefined): Promise<string> {
    const [environmentName, workingDir] = args ?? [];
    
    let cwd: string;
    if (workingDir) {
        cwd = workingDir;
    } else {
        if (workspace.workspaceFolders && workspace.workspaceFolders.length === 1) {
            cwd = workspace.workspaceFolders[0].uri.fsPath;
        } else {
            throw new Error(localize('azure-dev.commands.getDotEnvFilePath.noWorkingFolder', "Working directory could not be determined"));
        }
    }

    const envData = await getEnvironments(context, cwd);
    if (envData.length === 0) {
        throw new Error(localize('azure-dev.commands.getDotEnvFilePath.noEnvironments', 'No Azure developer environments found'));
    }

    const byName: (ei: EnvironmentInfo) => boolean = environmentName ? 
        ei => ei.Name === environmentName : ei => ei.IsDefault;
    const env = envData.find(byName);
    if (!env) {
        if (environmentName) {
            throw new Error(localize('azure-dev.commands.getDotEnvFilePath.environmentNotFound',"Azure developer environment '{0}' was not found", environmentName));
        } else {
            throw new Error(localize('azure-dev.commands.getDotEnvFilePath.noDefaultEnvironment', 'There is no default Azure developer environment, cannot determine environment settings file path'));
        }
    }

    return env.DotEnvPath;
}
