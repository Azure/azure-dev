// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzureWizardPromptStep, IActionContext, IAzureQuickPickItem } from '@microsoft/vscode-azext-utils';

export abstract class SkipIfOneStep<TWizardContext extends IActionContext, TItem> extends AzureWizardPromptStep<TWizardContext> {
    protected constructor(
        private readonly promptTitle: string,
        private readonly noItemsMessage: string
    ) {
        super();
    }

    protected async promptInternal(context: TWizardContext): Promise<TItem> {
        const picks = await this.getPicks(context);

        if (picks.length === 0) {
            context.errorHandling.suppressReportIssue = true;
            throw new Error(this.noItemsMessage);
        } else if (picks.length === 1) {
            return picks[0].data;
        } else {
            return (await context.ui.showQuickPick(picks, { placeHolder: this.promptTitle })).data;
        }
    }

    protected abstract getPicks(context: TWizardContext): Promise<IAzureQuickPickItem<TItem>[]>;
}