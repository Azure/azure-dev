// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as http from 'http';
import * as vscode from 'vscode';
import { CancelledResponse, ErrorResponse, JsonServerResponse, SuccessResponseBase, UndefinedResponse, startJsonServer } from './jsonServer';
import { DialogResponses, IActionContext, UserCancelledError, callWithTelemetryAndErrorHandling, isUserCancelledError } from '@microsoft/vscode-azext-utils';

type PromptServerSuccessResponse = SuccessResponseBase & {
    value: string | string[];
};

type PromptServerResponse = PromptServerSuccessResponse | ErrorResponse | CancelledResponse | undefined;

const AllPromptTypes = ['string', 'password', 'select', 'multiSelect', 'confirm', 'directory'] as const;
type PromptTypeTuple = typeof AllPromptTypes;
type PromptType = PromptTypeTuple[number];

type PromptServerRequest = {
    type: PromptType;
    options: {
        message: string;
        help: string | undefined;
        choices: SelectChoice[] | undefined;
        defaultValue: string | undefined;
    }
};

type SelectChoice = {
    value: string;
    detail: string | undefined;
};

/**
 * {@link startPromptServer} creates a locally running server that will respond to Azure Dev CLI prompt requests and
 * starts listening for requests.  Requests must be authenticated with a key that is returned from this function.
 *
 * The code in this extension refers to this prompting-in-VSCode process as "external prompting" (as in, it is external
 * to `azd`). It does not apply in all cases.
 **/
export function startPromptServer(): Promise<{ server: http.Server, endpoint: string, key: string }> {
    return startJsonServer({
        // eslint-disable-next-line @typescript-eslint/naming-convention
        '/prompt?api-version=2024-02-14-preview': async (reqBody: unknown): Promise<JsonServerResponse<PromptServerResponse>> => {
            return (await callWithTelemetryAndErrorHandling('promptServer.prompt', async (actionContext: IActionContext) => {
                if (!isValidPromptServerRequest(reqBody)) {
                    return { statusCode: 400 } satisfies JsonServerResponse<UndefinedResponse>;
                }

                try {
                    switch (reqBody.type) {
                        case 'string':
                        case 'password': {
                            const value = await promptString(actionContext, reqBody.type === 'password', reqBody.options.message, reqBody.options.defaultValue, reqBody.options.help);
                            return {
                                statusCode: 200,
                                result: {
                                    status: 'success',
                                    value: value,
                                },
                            } satisfies JsonServerResponse<PromptServerSuccessResponse>;
                        }
                        case 'select':
                        case 'multiSelect': {
                            const value = await promptSelect(actionContext, reqBody.type === 'multiSelect', reqBody.options.message, reqBody.options.choices!, reqBody.options.defaultValue, reqBody.options.help);
                            return {
                                statusCode: 200,
                                result: {
                                    status: 'success',
                                    value: value,
                                },
                            } satisfies JsonServerResponse<PromptServerSuccessResponse>;
                        }
                        case 'confirm': {
                            const value = await promptConfirmation(actionContext, reqBody.options.message, reqBody.options.help);
                            return {
                                statusCode: 200,
                                result: {
                                    status: 'success',
                                    value: value,
                                },
                            } satisfies JsonServerResponse<PromptServerSuccessResponse>;
                        }
                        case 'directory': {
                            const value = await promptDirectory(actionContext, reqBody.options.message, reqBody.options.help);
                            return {
                                statusCode: 200,
                                result: {
                                    status: 'success',
                                    value: value,
                                },
                            } satisfies JsonServerResponse<PromptServerSuccessResponse>;
                        }
                    }
                } catch (e: unknown) {
                    if (isUserCancelledError(e)) {
                        return { statusCode: 200, result: { status: 'cancelled' } } satisfies JsonServerResponse<CancelledResponse>;
                    }

                    throw e;
                }
            }))!;
        }
    });
}

async function promptString(context: IActionContext, isPassword: boolean, message: string, defaultValue: string | undefined, help: string | undefined): Promise<string> {
    return await context.ui.showInputBox({
        prompt: message,
        placeHolder: help,
        password: isPassword,
        ignoreFocusOut: true,
        value: defaultValue,
    });
}

async function promptSelect(context: IActionContext, isMulti: boolean, message: string, choices: SelectChoice[], defaultValue: string | undefined, help: string | undefined): Promise<string | string[]> {
    const pickChoices: vscode.QuickPickItem[] = choices.map(choice => { return { label: choice.value, description: choice.detail }; });

    const quickPickOptions: vscode.QuickPickOptions = {
        placeHolder: help,
        title: message,
        ignoreFocusOut: true,
    };

    // This is done this way, instead of just `{ canPickMany: isMulti }`, to allow TypeScript to better infer the type of the result object(s) returned
    if (isMulti) {
        const results = await context.ui.showQuickPick(pickChoices, { ...quickPickOptions, canPickMany: true, isPickSelected: p => p.label === defaultValue});
        return results.map(r => r.label);
    } else {
        const result = await context.ui.showQuickPick(pickChoices, quickPickOptions);
        return result.label;
    }
}

async function promptConfirmation(context: IActionContext, message: string, help: string | undefined): Promise<string> {
    const result = await context.ui.showWarningMessage(
        message,
        { modal: true, detail: help },
        ...[ DialogResponses.yes, DialogResponses.no ]
    );

    return result === DialogResponses.yes ? 'true' : 'false';
}

async function promptDirectory(context: IActionContext, message: string, help: string | undefined): Promise<string> {
    const selection = await context.ui.showOpenDialog({
        canSelectFiles: false,
        canSelectFolders: true,
        canSelectMany: false,
        title: message,
    });

    if (selection.length === 0) {
        throw new UserCancelledError();
    }

    return selection[0].fsPath;
}

function isValidPromptServerRequest(obj: unknown): obj is PromptServerRequest {
    if (typeof obj !== 'object' || obj === null) {
        return false;
    }

    const maybePromptServerRequest = obj as PromptServerRequest;

    if (typeof maybePromptServerRequest.type !== 'string' || !AllPromptTypes.includes(maybePromptServerRequest.type)) {
        return false;
    }

    if (typeof maybePromptServerRequest.options !== 'object' || maybePromptServerRequest.options === null) {
        return false;
    }

    if (typeof maybePromptServerRequest.options.message !== 'string') {
        return false;
    }

    if (!!maybePromptServerRequest.options.help && typeof maybePromptServerRequest.options.help !== 'string') {
        return false;
    }

    if ((maybePromptServerRequest.type === 'select' || maybePromptServerRequest.type === 'multiSelect') && !maybePromptServerRequest.options.choices) {
        return false;
    }

    if (!!maybePromptServerRequest.options.choices && (!Array.isArray(maybePromptServerRequest.options.choices) || !maybePromptServerRequest.options.choices.every(isValidSelectChoice))) {
        return false;
    }

    if (!!maybePromptServerRequest.options.defaultValue && typeof maybePromptServerRequest.options.defaultValue !== 'string') {
        return false;
    }

    return true;
}

function isValidSelectChoice(obj: unknown): obj is SelectChoice {
    if (typeof obj !== 'object' || obj === null) {
        return false;
    }

    const maybeSelectChoice = obj as SelectChoice;

    if (typeof maybeSelectChoice.value !== 'string') {
        return false;
    }

    if (!!maybeSelectChoice.detail && typeof maybeSelectChoice.detail !== 'string') {
        return false;
    }

    return true;
}
