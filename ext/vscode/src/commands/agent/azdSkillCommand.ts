// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { IActionContext } from '@microsoft/vscode-azext-utils';
import { type SkillCommandArgs, type SkillCommandResult } from 'vscode-azure-agent-api';
import { getAzdLoginStatus } from '../../utils/azureDevCli';

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

    if (!azdInstalled) {
        responseStream.markdown(vscode.l10n.t('First things first, it looks like the Azure Developer CLI is not installed. Let\'s get that taken care of.'));
        responseStream.button({ title: vscode.l10n.t('Install Azure Developer CLI'), command: 'azure-dev.commands.cli.install' });
    }

    if (!azdLoggedIn) {
        responseStream.markdown(vscode.l10n.t('You\'ll need to be logged in with the Azure Developer CLI. Click below to sign in.'));
        responseStream.button({ title: vscode.l10n.t('Sign in with Azure Developer CLI'), command: 'azure-dev.commands.cli.login' });
    }

    if (!workspaceInitialized) {
        responseStream.markdown(vscode.l10n.t('It looks like the workspace is not set up for use with the Azure Developer CLI.'));
        responseStream.button({ title: vscode.l10n.t('Initialize workspace'), command: 'azure-dev.commands.cli.init' });
    }

    responseStream.markdown(vscode.l10n.t('All that\'s left is to deploy your application to Azure!'));
    responseStream.button({ title: vscode.l10n.t('Deploy to Azure'), command: 'azure-dev.commands.cli.up' });

    return {
        chatAgentResult: { }
    };
}

async function isWorkspaceInitialized(context: IActionContext): Promise<boolean> {
    // Look for at most one file named azure.yml or azure.yaml, only at the root, to avoid perf issues
    const fileResults = await vscode.workspace.findFiles('azure.{yml,yaml}', undefined, 1);

    return !!fileResults?.length;
}
