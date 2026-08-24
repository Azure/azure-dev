// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { AzureWizard, AzureWizardPromptStep, IActionContext } from '@microsoft/vscode-azext-utils';

class NoPromptStep extends AzureWizardPromptStep<IActionContext> {
    public async prompt(): Promise<void> {
        // No input is required for this compatibility test.
    }

    public shouldPrompt(): boolean {
        return false;
    }
}

suite('AzureWizard compatibility', () => {
    test('runs prompt steps without removed Node utility APIs', async () => {
        const context = {
            telemetry: {
                properties: {},
            },
            ui: {},
        } as unknown as IActionContext;
        const wizard = new AzureWizard(context, {
            promptSteps: [new NoPromptStep()],
        });

        await wizard.prompt();
    });
});
