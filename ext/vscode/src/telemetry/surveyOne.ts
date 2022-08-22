// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';
import ext from '../ext';
import { localize } from "../localize";
import { Survey, SurveyRefusal } from "./surveyScheduler";

const buttons = new Map<string, vscode.Uri | SurveyRefusal>();
buttons.set(localize("azure-dev.surveys.surveyOne.button.take", "Take survey"), vscode.Uri.parse(`https://aka.ms/azure-dev/hats?channel=vscode&extensionVersion=${ext.extensionVersion.value}&clientVersion=${vscode.version}`));
buttons.set(localize("azure-dev.surveys.surveyOne.button.never", "Don't ask again"), SurveyRefusal.NeverAgain);
buttons.set(localize("azure-dev.surveys.surveyOne.button.later", "Remind me later"), SurveyRefusal.RemindLater);

export const SurveyOne: Survey = {
    id: 'surveyOne',
    prompt: localize("azure-dev.surveys.surveyOne.prompt", "Can you please take 2 minutes to tell us how the Azure Developer CLI is working for you?"),
    buttons: buttons,
    activationDelayMs: 60 * 1000,
    isEligible: (context: IActionContext) => {
        const stats = ext.activitySvc.getStats();

        // The "is eligible or not" telemetry will be captured by the caller (survey scheduler).
        context.telemetry.properties.totalUserActiveDays = stats.totalActiveDays.toFixed();

        return Promise.resolve(stats.totalActiveDays >= 3);
    }
};
