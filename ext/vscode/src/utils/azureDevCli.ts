// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { CommonOptions } from "child_process";
import { CommandLineBuilder } from "./commandLineBuilder";
import ext from "../ext";
import { execAsync } from './process';
import { AsyncLazy } from './lazy';
import { localize } from "../localize";
import { AzExtErrorButton, IActionContext } from '@microsoft/vscode-azext-utils';

// Twenty seconds: generous, but not infinite
export const DefaultAzCliInvocationTimeout: number = 20 * 1000;
const AzdInstallationUrl: string = 'https://aka.ms/azd-install';
const AzdVersionCacheLifetime = 15 * 60 * 1000; // 15 minutes

enum AzdVersionCheckFailure {
    NotInstalled = 1,
    CannotDetermineVersion = 2 
}
let userWarnedAzdMissing: boolean = false;
const azdVersionChecker = new AsyncLazy<string | AzdVersionCheckFailure>(getAzdVersion, AzdVersionCacheLifetime);

export type Environment = { [key: string]: string };
export type AzureDevCli = {
    commandBuilder: CommandLineBuilder,
    env: Environment
    spawnOptions: (cwd?: string) => CommonOptions
};

export async function createAzureDevCli(context: IActionContext): Promise<AzureDevCli> {
    const azdVersion = await azdVersionChecker.getValue();
    if (azdVersion === AzdVersionCheckFailure.NotInstalled) {
        context.errorHandling.suppressReportIssue = true;
        context.errorHandling.buttons = azdNotInstalledUserChoices();
        userWarnedAzdMissing = true;
        throw new Error(azdNotInstalledMsg());
    }
    if (typeof azdVersion === 'string') {
        context.telemetry.properties.azdVersion = azdVersion;
    }

    return createCli();
}

export function scheduleAzdInstalledCheck(): void {
    const fiveSeconds = 5 * 1000;

    setTimeout(async () => {
        const ver = await azdVersionChecker.getValue();

        if (ver === AzdVersionCheckFailure.NotInstalled && !userWarnedAzdMissing) {
            userWarnedAzdMissing = true;
            const response = await vscode.window.showWarningMessage(azdNotInstalledMsg(), {}, ...azdNotInstalledUserChoices());
            await response?.callback();
        }
    }, fiveSeconds);
}

function createCli(): AzureDevCli {
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

async function getAzdVersion(): Promise<string | AzdVersionCheckFailure> {
    const cli = createCli();
    const command = cli.commandBuilder.withArgs(['version', '--output', 'json']).build();
    let stdout: string;
    try {
        stdout = (await execAsync(command, cli.spawnOptions())).stdout;
    } catch {
        return AzdVersionCheckFailure.NotInstalled;
    }

    try {
        const versionSpec = JSON.parse(stdout) as { azd: { version: string } };
        return versionSpec?.azd?.version || AzdVersionCheckFailure.CannotDetermineVersion;
    } catch {
        return AzdVersionCheckFailure.CannotDetermineVersion;
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

function azdNotInstalledMsg(): string {
    return localize("azure-dev.utils.azd.notInstalled", "Azure Developer CLI is not installed. Visit {0} to get it.", AzdInstallationUrl);
}

function azdNotInstalledUserChoices(): AzExtErrorButton[] {
    const choices: AzExtErrorButton[] = [
        {
            "title": localize("azure-dev.utils.azd.goToInstallUrl", "Go to {0}", AzdInstallationUrl),
            "callback": async () => {
                await vscode.env.openExternal(vscode.Uri.parse(AzdInstallationUrl));
            }
        },
        {
            "title": localize("azure-dev.utils.azd.later", "Later"),
            "callback": () => { return Promise.resolve(); /* no-op */ }
        }
    ];
    return choices;
}
