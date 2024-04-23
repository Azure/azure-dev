// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as path from 'path';
import * as semver from 'semver';
import * as vscode from 'vscode';
import { CommonOptions } from "child_process";
import { CommandLineBuilder } from "./commandLineBuilder";
import ext from "../ext";
import { execAsync } from './process';
import { AsyncLazy } from './lazy';
import { AzExtErrorButton, IActionContext } from '@microsoft/vscode-azext-utils';
import { isWindows } from './osUtils';
import { setVsCodeContext } from './setVsCodeContext';

// Twenty seconds: generous, but not infinite
export const DefaultAzCliInvocationTimeout: number = 20 * 1000;
const AzdLoginCheckCacheLifetime = 15 * 60 * 1000; // 15 minutes

let azdInstallAttempted: boolean = false;
const azdLoginChecker = new AsyncLazy<LoginStatus | undefined>(getAzdLoginStatus, AzdLoginCheckCacheLifetime);

export interface LoginStatus {
    readonly status: 'success' | 'unauthenticated' | string;
    readonly expiresOn?: string;
}

interface VersionInfo {
    readonly azd: {
        readonly version: string;
        readonly commit: string;
    };
}

export type Environment = { [key: string]: string };
export type AzureDevCli = {
    commandBuilder: CommandLineBuilder,
    env: Environment
    spawnOptions: (cwd?: string) => CommonOptions
};

export async function createAzureDevCli(context: IActionContext): Promise<AzureDevCli> {
    const loginStatus = await azdLoginChecker.getValue();
    if (!loginStatus) {
        context.errorHandling.suppressReportIssue = true;
        context.errorHandling.buttons = azdNotInstalledUserChoices();
        throw new Error(azdNotInstalledMsg());
    }

    return createCli();
}

export function scheduleAzdVersionCheck(): void {
    const oneSecond = 1 * 1000;
    // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
    const minimumSupportedVersion = semver.coerce('1.8.0')!;

    setTimeout(async () => {
        const versionResult = await getAzdVersion();

        if (versionResult && !semver.gte(versionResult, minimumSupportedVersion)) {
            // We won't show a warning if AZD is not installed, but if it is installed and less than the minimum, we will warn

            const install: vscode.MessageItem = {
                title: vscode.l10n.t('Update'),
            };

            const later: vscode.MessageItem = {
                title: vscode.l10n.t('Later'),
            };

            const title = vscode.l10n.t(
                'The minimum version of the Azure Developer CLI supported by the extension is {0}, but you have {1}. Would you like to update?',
                minimumSupportedVersion.version,
                versionResult.version
            );

            void vscode.window.showWarningMessage(title, { modal: false }, install, later).then(async (result) => {
                if (result === install) {
                    await vscode.commands.executeCommand('azure-dev.commands.cli.install', /* shouldPrompt: */ false);
                }
            });
        }
    }, oneSecond);
}

export function scheduleAzdSignInCheck(): void {
    const oneSecond = 1 * 1000;

    setTimeout(async () => {
        const result = await azdLoginChecker.getValue();

        if (result !== undefined) {
            // If we've reached this point, AZD is installed. We can set the VSCode context that the walk-through uses
            await setVsCodeContext('hideAzdInstallStep', true);

            // If the user is logged in, we can also set the login context
            if (result.status === 'success') {
                await setVsCodeContext('hideAzdLoginStep', true);
            }
        }
    }, oneSecond);
}

export function scheduleAzdYamlCheck(): void {
    const oneSecond = 1 * 1000;

    setTimeout(async () => {
        // Look for at most one file named azure.yml or azure.yaml, only at the root, to avoid perf issues
        // If one exists, the scaffold step will be hidden from the walkthrough
        const fileResults = await vscode.workspace.findFiles('azure.{yml,yaml}', undefined, 1);

        if (fileResults?.length) {
            await setVsCodeContext('hideAzdScaffoldStep', true);
        }
    }, oneSecond);
}

export function onAzdInstallAttempted(): void {
    azdInstallAttempted = true;

    // Clear the install+login state so we'll check again at the next command
    azdLoginChecker.clear();
}

export function onAzdLoginAttempted(): void {
    // Clear the install+login state so we'll check again at the next command
    azdLoginChecker.clear();
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

async function getAzdVersion(): Promise<semver.SemVer | undefined> {
    const cli = createCli();
    const command = cli.commandBuilder.withArgs(['version', '--output', 'json']).build();
    try {
        const stdout = (await execAsync(command, cli.spawnOptions())).stdout;
        const result = JSON.parse(stdout) as VersionInfo;

        return semver.coerce(result?.azd.version) ?? undefined;
    } catch {
        // If AZD is not installed, return `undefined`
        return undefined;
    }
}

export async function getAzdLoginStatus(): Promise<LoginStatus | undefined> {
    const cli = createCli();
    const command = cli.commandBuilder.withArgs(['auth', 'login', '--check-status', '--output', 'json']).build();
    try {
        const stdout = (await execAsync(command, cli.spawnOptions())).stdout;
        const result = JSON.parse(stdout) as LoginStatus;

        return result;
    } catch {
        // If AZD is not installed, return `undefined`
        return undefined;
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
    return vscode.l10n.t("Azure Developer CLI is not installed. Would you like to install it? [Learn More](https://aka.ms/azd-install)");
}

function azdNotInstalledUserChoices(): AzExtErrorButton[] {
    const choices: AzExtErrorButton[] = [
        {
            title: vscode.l10n.t("Install"),
            callback: async () => {
                await vscode.commands.executeCommand("azure-dev.commands.cli.install", /* shouldPrompt: */ false);
            }
        },
        {
            title: vscode.l10n.t("Later"),
            callback: () => { return Promise.resolve(); /* no-op */ }
        }
    ];
    return choices;
}

// isAzdCommand returns true if this is the command to run azd.
export function isAzdCommand(command: string): boolean {
    return command === getAzDevInvocation()[0] || command.startsWith(`${getAzDevInvocation()[0]} `);
}
