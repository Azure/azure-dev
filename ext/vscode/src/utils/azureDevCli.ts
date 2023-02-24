// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as path from 'path';
import * as vscode from 'vscode';
import { CommonOptions } from "child_process";
import { CommandLineBuilder } from "./commandLineBuilder";
import ext from "../ext";
import { execAsync } from './process';
import { AsyncLazy } from './lazy';
import { AzExtErrorButton, IActionContext } from '@microsoft/vscode-azext-utils';
import { isWindows } from './osUtils';

// Twenty seconds: generous, but not infinite
export const DefaultAzCliInvocationTimeout: number = 20 * 1000;
const AzdInstallationUrl: string = 'https://aka.ms/azd-install';
const AzdVersionCacheLifetime = 15 * 60 * 1000; // 15 minutes

enum AzdVersionCheckFailure {
    NotInstalled = 1,
    CannotDetermineVersion = 2
}
let userWarnedAzdMissing: boolean = false;
let azdInstallAttempted: boolean = false;
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

        if (ver === AzdVersionCheckFailure.NotInstalled && !userWarnedAzdMissing && !azdInstallAttempted) {
            userWarnedAzdMissing = true;
            const response = await vscode.window.showWarningMessage(azdNotInstalledMsg(), {}, ...azdNotInstalledUserChoices());
            await response?.callback();
        }
    }, fiveSeconds);
}

export function onAzdInstallAttempted(): void {
    azdInstallAttempted = true;

    // Clear the install state so we'll check again at the next command
    azdVersionChecker.clear();
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

    // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
    let modifiedPath: string = process.env.PATH!;

    // On Unix, the CLI is installed to /usr/local/bin, which is always going to be in the PATH
    // On Windows, the install location varies but is generally at %LOCALAPPDATA%\Programs\Azure Dev CLI, especially
    // when installed the default way, which the extension does.
    // To avoid needing to restart VSCode to get the updated PATH, we'll temporarily add the default install location,
    // as long as it's Windows, AZURE_DEV_CLI_PATH is unset, "Azure Dev CLI" isn't already in the PATH (somewhere else?),
    // and the user did try to install within this session
    if (isWindows() && !process.env.AZURE_DEV_CLI_PATH && !/Azure Dev CLI/i.test(modifiedPath) && azdInstallAttempted) {
        // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
        const defaultAzdInstallLocation = path.join(process.env.LOCALAPPDATA!, 'Programs', 'Azure Dev CLI');
        modifiedPath += `;${defaultAzdInstallLocation}`;
    }

    const combinedEnv = {
        ...process.env,
        ...azDevCliEnv,
        PATH: modifiedPath,
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
    return vscode.l10n.t("Azure Developer CLI is not installed. Would you like to install it?.");
}

function azdNotInstalledUserChoices(): AzExtErrorButton[] {
    const choices: AzExtErrorButton[] = [
        {
            "title": vscode.l10n.t("Install"),
            "callback": async () => {
                await vscode.commands.executeCommand("azure-dev.commands.cli.install", /* shouldPrompt: */ false);
            }
        },
        {
            "title": vscode.l10n.t("Learn More"),
            "callback": async () => {
                await vscode.env.openExternal(vscode.Uri.parse(AzdInstallationUrl));
            }
        },
        {
            "title": vscode.l10n.t("Later"),
            "callback": () => { return Promise.resolve(); /* no-op */ }
        }
    ];
    return choices;
}
