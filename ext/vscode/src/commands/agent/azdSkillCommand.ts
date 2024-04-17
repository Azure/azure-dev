// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { IActionContext } from '@microsoft/vscode-azext-utils';
import { type SkillCommandArgs, type SkillCommandResult } from 'vscode-azure-agent-api';
import { getAzdLoginStatus } from '../../utils/azureDevCli';
import { InitCommandArguments } from '../init';
import { InstallCliCommandArguments } from '../installCli';
import { LoginCliCommandArguments } from '../loginCli';
import { UpCommandArguments } from '../up';

export async function azdSkillCommand(context: IActionContext, args: SkillCommandArgs): Promise<SkillCommandResult> {
    const responseStream = args.agentRequest.responseStream;

    responseStream.progress(vscode.l10n.t('Determining what to do...'));

    const loginStatus = await getAzdLoginStatus();

    const azdInstalled = loginStatus !== undefined;
    const azdLoggedIn = azdInstalled && loginStatus.status === 'success';
    const workspaceInitialized = await isWorkspaceInitialized(context);

    context.telemetry.properties.azdInstalled = azdInstalled.toString();
    context.telemetry.properties.azdLoggedIn = azdLoggedIn.toString();
    context.telemetry.properties.workspaceInitialized = workspaceInitialized.toString();
    let step = 1;

    responseStream.markdown(vscode.l10n.t('You can use the [Azure Developer CLI](https://aka.ms/azd) to identify the services necessary for running your app on Azure. It can also quickly provision the services and deploy to Azure.\n\n'));
    responseStream.markdown(vscode.l10n.t('Here are the specific steps you\'ll need to follow:\n\n'));

    if (!azdInstalled) {
        responseStream.markdown(vscode.l10n.t('**{0}. Install the Azure Developer CLI**\n\nFirst things first, it looks like the Azure Developer CLI is not installed. Let\'s get that taken care of.', step++));
        responseStream.button({ title: vscode.l10n.t('Install Azure Developer CLI'), command: 'azure-dev.commands.cli.install', arguments: [
            /* shouldPrompt */ true,
            /* fromAgent */ true,
        ] satisfies InstallCliCommandArguments});
    }

    if (!azdLoggedIn) {
        responseStream.markdown(vscode.l10n.t('**{0}. Run azd auth login**\n\nYou\'ll need to be logged in with the Azure Developer CLI. The `azd auth login` command will help you log in. It may open a browser window.', step++));
        responseStream.button({ title: vscode.l10n.t('Run `azd auth login` in the terminal'), command: 'azure-dev.commands.cli.login', arguments: [
            /* fromAgent */ true,
        ] satisfies LoginCliCommandArguments});
    }

    if (!workspaceInitialized) {
        responseStream.markdown(vscode.l10n.t('**{0}. Run azd init to initialize your app**\n\nThe `azd init` command will analyze your application, identify necessary Azure services, and create the needed configuration files.', step++));
        responseStream.button({ title: vscode.l10n.t('Run `azd init` in the terminal'), command: 'azure-dev.commands.cli.init', arguments: [
            /* selectedFile */ undefined,
            /* allSelectedFiles */ undefined,
            /* options */ { useExistingSource: true },
            /* fromAgent */ true,
        ] satisfies InitCommandArguments});
    }

    responseStream.markdown(vscode.l10n.t('**{0}. Run azd up to provision and deploy your app**\n\nOnce your application has been initialized you can use the `azd up` command to provision app services and deploy to Azure.', step++));
    responseStream.button({ title: vscode.l10n.t('Run `azd up` in the terminal'), command: 'azure-dev.commands.cli.up', arguments: [
        /* selectedFile */ undefined,
        /* fromAgent */ true,
    ] satisfies UpCommandArguments });

    return {
        chatAgentResult: { }
    };
}

async function isWorkspaceInitialized(context: IActionContext): Promise<boolean> {
    // Look for at most one file named azure.yml or azure.yaml, only at the root, to avoid perf issues
    const fileResults = await vscode.workspace.findFiles('azure.{yml,yaml}', undefined, 1);

    return !!fileResults?.length;
}
