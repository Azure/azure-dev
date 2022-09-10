// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { IActionContext, callWithTelemetryAndErrorHandling } from '@microsoft/vscode-azext-utils';
import ext from '../ext';
import { TelemetryId } from './telemetryId';

const surveyRespondedKeyPrefix = `${ext.azureDevExtensionNamespace}/surveys/response`;
const surveyFlightPrefix = `azure-dev_`;
const lastSurveySessionKey = `${ext.azureDevExtensionNamespace}/surveys/lastSession`;

// A random value between 0 and jitterTime will be added to- or subtracted from the activation delay
const jitterTime = 3000; // 3 seconds

export enum SurveyRefusal {
    NeverAgain = 0,
    RemindLater = 1
}

export interface Survey {
    id: string;
    prompt: string;
    buttons: Map<string, vscode.Uri | SurveyRefusal>;
    activationDelayMs: number;
    isEligible(context: IActionContext): Promise<boolean>;
}

export function scheduleSurveys(persistentStore: vscode.Memento, surveys: Survey[]) {
    if (!vscode.env.isTelemetryEnabled) {
        return;
    }

    for (const survey of surveys) {
        const jitter = Math.round(Math.random() * jitterTime * 2) - jitterTime;

        const timer = setTimeout(
            async () => {
                clearTimeout(timer);
                await executeSurvey(persistentStore, survey);
            },
            survey.activationDelayMs + jitter
        );
    }
}

export function getSurveyFlightName(s: Survey) {
    return `${surveyFlightPrefix}${s.id}`;
}

async function executeSurvey(persistentStore: vscode.Memento, survey: Survey): Promise<void> {
    try {
        const shouldPrompt = await callWithTelemetryAndErrorHandling(TelemetryId.SurveyCheck, (context: IActionContext) => surveyCheck(persistentStore, context, survey));

        if (shouldPrompt) {
            await callWithTelemetryAndErrorHandling(TelemetryId.SurveyPromptResponse, (context: IActionContext) => surveyPrompt(persistentStore, context, survey));
        }
    } catch {
        // Best effort--do not bother the user with failed survey attempts.
    }
}

async function surveyCheck(persistentStore: vscode.Memento, context: IActionContext, survey: Survey): Promise<boolean> {
    context.telemetry.properties.surveyId = survey.id;

    const promptedDuringCurrentSession = persistentStore.get<string>(lastSurveySessionKey) === vscode.env.sessionId;
    const alreadyResponded = persistentStore.get<boolean>(`${surveyRespondedKeyPrefix}/${survey.id}`, false);
    const eligible = await survey.isEligible(context);
    const flighted: boolean = await ext.experimentationSvc?.isCachedFlightEnabled(getSurveyFlightName(survey)) ?? false;

    context.telemetry.properties.promptedDuringCurrentSession = promptedDuringCurrentSession.toString();
    context.telemetry.properties.alreadyResponded = alreadyResponded.toString();
    context.telemetry.properties.eligible = eligible.toString();
    context.telemetry.properties.flighted = flighted.toString();

    return !promptedDuringCurrentSession && !alreadyResponded && eligible && flighted;
}

async function surveyPrompt(persistentStore: vscode.Memento, context: IActionContext, survey: Survey): Promise<void> {
    context.telemetry.properties.surveyId = survey.id;
    await persistentStore.update(lastSurveySessionKey, vscode.env.sessionId);

    const buttons = Array.from(survey.buttons.keys());
    const result = await vscode.window.showInformationMessage(survey.prompt, ...buttons);
    const response = (result === undefined) ? SurveyRefusal.RemindLater : survey.buttons.get(result) ?? SurveyRefusal.RemindLater;

    if (response instanceof vscode.Uri) {
        context.telemetry.properties.accepted = 'true';
        context.telemetry.properties.response = response.toString();
        await persistentStore.update(`${surveyRespondedKeyPrefix}/${survey.id}`, true);

        await vscode.env.openExternal(response);
    } else {
        context.telemetry.properties.accepted = 'false';
        context.telemetry.properties.response = SurveyRefusal[response];
        if (response === SurveyRefusal.NeverAgain) {
            await persistentStore.update(`${surveyRespondedKeyPrefix}/${survey.id}`, true);
        }
    }
}
