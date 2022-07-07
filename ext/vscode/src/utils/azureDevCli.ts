// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { CommonOptions } from "child_process";
import { CommandLineBuilder } from "./commandLineBuilder";
import ext from "../ext";

// Twenty seconds: generous, but not infinite
export const DefaultAzCliInvocationTimeout: number = 20 * 1000;

export type Environment = { [key: string]: string };
export type AzureDevCli = {
    commandBuilder: CommandLineBuilder,
    env: Environment
    spawnOptions: (cwd?: string) => CommonOptions
};

export function createAzureDevCli(): AzureDevCli {
    const invocation = getAzDevInvocation();
    const builder = CommandLineBuilder.create(invocation[0], ...invocation.slice(1));
    const azDevCliEnv: NodeJS.ProcessEnv = {
        // eslint-disable-next-line @typescript-eslint/naming-convention
        'AZURE_DEV_USER_AGENT': ext.userAgent
    };
    if (!vscode.env.isTelemetryEnabled) {
        azDevCliEnv['AZURE_DEV_COLLECT_TELEMETRY'] = "no";
    }
    const combinedEnv = { 
        ...process.env,
        ...azDevCliEnv
    };

    return {
        commandBuilder: builder,
        env: normalize(combinedEnv),
        spawnOptions: (cwd?: string) => {
            return { 
                timeout: DefaultAzCliInvocationTimeout, 
                cwd: cwd,
                env: combinedEnv,
                windowsHide: true
            };
        }
    };
}

function getAzDevInvocation(): string[] {
    if (process.env.AZURE_DEV_CLI_PATH) {
        return [process.env.AZURE_DEV_CLI_PATH]; 
    } else {
        return ['azd'];
    }
}

// This is only necessary because Node defines the environment slightly differently from VS Code... %-/
function normalize(env: NodeJS.ProcessEnv): Environment {
    const retval: Environment = {};
    for (const prop of Object.getOwnPropertyNames(env)) {
        if (env[prop]) {
            // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
            retval[prop] = env[prop]!;
        }
    }
    return retval;
}
