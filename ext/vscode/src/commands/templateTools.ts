// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import { Template, AzureDevTemplateProvider } from '../services/AzureDevTemplateProvider';
import { quickPickWorkspaceFolder } from '../utils/quickPickWorkspaceFolder';
import { init } from './init';

const templateProvider = new AzureDevTemplateProvider();

export async function initFromCode(context: IActionContext): Promise<void> {
    const workspaceFolder = await quickPickWorkspaceFolder(context, vscode.l10n.t('Select a workspace folder to initialize'));

    await init(context, workspaceFolder.uri, undefined, undefined);
}

export async function initMinimal(context: IActionContext): Promise<void> {
    const workspaceFolder = await quickPickWorkspaceFolder(context, vscode.l10n.t('Select a workspace folder to create minimal project'));

    // Call azd init with minimal flag
    await vscode.commands.executeCommand('azure-dev.commands.cli.init', workspaceFolder.uri, undefined, { minimal: true });
}

export async function initFromTemplate(context: IActionContext, template?: Template): Promise<void> {
    if (!template) {
        vscode.window.showErrorMessage(vscode.l10n.t('No template selected'));
        return;
    }

    const workspaceFolder = await quickPickWorkspaceFolder(context, vscode.l10n.t('Select a workspace folder to initialize with template'));

    const templatePath = templateProvider.extractTemplatePath(template.source);

    // Call init with template path
    await init(context, workspaceFolder.uri, undefined, { templateUrl: templatePath });
}

export async function searchTemplates(context: IActionContext): Promise<void> {
    const quickPick = vscode.window.createQuickPick<vscode.QuickPickItem & { template?: Template }>();
    quickPick.placeholder = vscode.l10n.t('Search templates (e.g., "react", "python ai", "cosmos")');
    quickPick.matchOnDescription = true;
    quickPick.matchOnDetail = true;

    // Show loading
    quickPick.busy = true;
    quickPick.show();

    // Load all templates
    const templates = await templateProvider.getTemplates();
    quickPick.busy = false;

    quickPick.items = templates.map(t => ({
        label: t.title,
        description: t.tags?.slice(0, 3).join(', '),
        detail: t.description,
        template: t
    }));

    quickPick.onDidChangeValue(async (value) => {
        if (value.length >= 2) {
            quickPick.busy = true;
            const results = await templateProvider.searchTemplates(value);
            quickPick.items = results.map(t => ({
                label: t.title,
                description: t.tags?.slice(0, 3).join(', '),
                detail: t.description,
                template: t
            }));
            quickPick.busy = false;
        } else if (value.length === 0) {
            quickPick.items = templates.map(t => ({
                label: t.title,
                description: t.tags?.slice(0, 3).join(', '),
                detail: t.description,
                template: t
            }));
        }
    });

    quickPick.onDidAccept(async () => {
        const selected = quickPick.selectedItems[0];
        if (selected?.template) {
            quickPick.hide();
            await initFromTemplate(context, selected.template);
        }
    });

    quickPick.onDidHide(() => {
        quickPick.dispose();
    });
}

export async function openGallery(context: IActionContext): Promise<void> {
    await vscode.env.openExternal(vscode.Uri.parse('https://aka.ms/awesome-azd'));
}

export async function openReadme(context: IActionContext, template?: Template): Promise<void> {
    if (!template) {
        vscode.window.showErrorMessage(vscode.l10n.t('No template selected'));
        return;
    }

    // Construct README URL from template source
    const readmeUrl = template.source.endsWith('/')
        ? `${template.source}blob/main/README.md`
        : `${template.source}/blob/main/README.md`;

    await vscode.env.openExternal(vscode.Uri.parse(readmeUrl));
}

export async function openGitHubRepo(context: IActionContext, template?: Template): Promise<void> {
    if (!template) {
        vscode.window.showErrorMessage(vscode.l10n.t('No template selected'));
        return;
    }

    await vscode.env.openExternal(vscode.Uri.parse(template.source));
}
