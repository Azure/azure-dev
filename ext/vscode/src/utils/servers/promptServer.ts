// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as http from 'http';
import { CancelledResponse, ErrorResponse, JsonServerResponse, SuccessResponseBase, UndefinedResponse, startJsonServer } from './jsonServer';
import { isUserCancelledError } from '@microsoft/vscode-azext-utils';

type PromptServerSuccessResponse = SuccessResponseBase & {
    value: string | string[] | boolean | number;
};

type PromptServerResponse = PromptServerSuccessResponse | ErrorResponse | CancelledResponse | undefined;

const AllPromptTypes = ['string', 'password', 'select', 'multiSelect', 'confirm'] as const;
type PromptTypeTuple = typeof AllPromptTypes;
type PromptType = PromptTypeTuple[number];

type PromptServerRequest = {
    type: PromptType;
    options: {
        message: string;
        help: string | undefined;
        options: string[] | undefined;
        defaultValue: string | undefined;
    }
};

function isValidPromptServerRequest(obj: unknown): obj is PromptServerRequest {
    if (typeof obj !== 'object' || obj === null) {
        return false;
    }

    const maybePromptServerRequest = obj as PromptServerRequest;

    if (!Array.isArray(maybePromptServerRequest.type) || !AllPromptTypes.includes(maybePromptServerRequest.type)) {
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

    if (!!maybePromptServerRequest.options.options && (!Array.isArray(maybePromptServerRequest.options.options) || maybePromptServerRequest.options.options.some((a: unknown) => typeof a !== 'string'))) {
        return false;
    }

    if (!!maybePromptServerRequest.options.defaultValue && typeof maybePromptServerRequest.options.defaultValue !== 'string') {
        return false;
    }

    return true;
}

/**
 * `startPromptServer` creates a locally running server that will respond to Azure Dev CLI prompt requests and
 * starts listening for requests.  Requests must be authenticated with a key that is returned from this function.
 **/
export function startPromptServer(): Promise<{ server: http.Server, endpoint: string, key: string }> {
    return startJsonServer({
        // eslint-disable-next-line @typescript-eslint/naming-convention
        '/prompt?api-version=2024-02-14-preview': async (reqBody: unknown): Promise<JsonServerResponse<PromptServerResponse>> => {
            if (!isValidPromptServerRequest(reqBody)) {
                return { statusCode: 400 } satisfies JsonServerResponse<UndefinedResponse>;
            }

            try {
                switch (reqBody.type) {
                    case 'string':
                    case 'password': {
                        const value = promptString(reqBody.type === 'password', reqBody.options.message, reqBody.options.defaultValue, reqBody.options.help);
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
                        const value = promptSelect(reqBody.type === 'multiSelect', reqBody.options.message, reqBody.options.options!, reqBody.options.defaultValue, reqBody.options.help);
                        return {
                            statusCode: 200,
                            result: {
                                status: 'success',
                                value: value,
                            },
                        } satisfies JsonServerResponse<PromptServerSuccessResponse>;
                    }
                    case 'confirm': {
                        const value = promptConfirmation(reqBody.options.message, reqBody.options.help);
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

            return {
                statusCode: 200,
                result: {
                    status: 'success',
                    token: token.token,
                    expiresOn: new Date(token.expiresOnTimestamp).toISOString()
                }
            } satisfies JsonServerResponse<PromptServerSuccessResponse>;
        }
    });
}

function promptString(isPassword: boolean, message: string, defaultValue?: string, help?: string): string {
    return '';
}

function promptSelect(isMulti: boolean, message: string, options: string[], defaultValue?: string, help?: string): string | string[] {
    return '';
}

function promptConfirmation(message: string, help?: string): boolean {
    return false;
}
