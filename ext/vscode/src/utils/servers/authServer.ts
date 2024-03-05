// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as http from 'http';
import { TokenCredential } from '@azure/core-auth';
import { isNotSignedInError } from '../VsCodeAuthenticationCredential';
import { ErrorResponse, JsonServerResponse, SuccessResponseBase, UndefinedResponse, startJsonServer } from './jsonServer';

type AuthServerSuccessResponse = SuccessResponseBase & {
    token: string;
    expiresOn: string;
};

type AuthServerResponse = AuthServerSuccessResponse | ErrorResponse | undefined;

type AuthServerRequest = {
    scopes: string[];
    tenantId?: string;
};

function isValidAuthServerRequest(obj: unknown): obj is AuthServerRequest {
    if (typeof obj !== 'object' || obj === null) {
        return false;
    }

    const maybeAuthServerRequest = obj as AuthServerRequest;

    if (!Array.isArray(maybeAuthServerRequest.scopes) ||
        maybeAuthServerRequest.scopes.length === 0 ||
        maybeAuthServerRequest.scopes.some((a: unknown) => typeof a !== 'string')) {
        return false;
    }

    if (!!maybeAuthServerRequest.tenantId && typeof maybeAuthServerRequest.tenantId !== 'string') {
        return false;
    }

    return true;
}

/**
 * `startAuthServer` creates a locally running server that will respond to Azure Dev CLI authentication requests and
 * starts listening for requests.  Requests must be authenticated with a key that is returned from this function.
 * The provided credential is used to fetch tokens for auth requests.
 **/
export function startAuthServer(credential: TokenCredential): Promise<{ server: http.Server, endpoint: string, key: string }> {
    return startJsonServer({
        // eslint-disable-next-line @typescript-eslint/naming-convention
        '/token?api-version=2023-07-12-preview': async (reqBody: unknown): Promise<JsonServerResponse<AuthServerResponse>> => {
            if (!isValidAuthServerRequest(reqBody)) {
                return { statusCode: 400 } satisfies JsonServerResponse<UndefinedResponse>;
            }

            try {
                const token = await credential.getToken(reqBody.scopes, {
                    tenantId: reqBody.tenantId,
                });

                if (!token) {
                    return { statusCode: 500 } satisfies JsonServerResponse<UndefinedResponse>;
                }

                return {
                    statusCode: 200,
                    result: {
                        status: 'success',
                        token: token.token,
                        expiresOn: new Date(token.expiresOnTimestamp).toISOString()
                    },
                } satisfies JsonServerResponse<AuthServerSuccessResponse>;
            } catch (e: unknown) {
                // If getToken Fails, we want to return 200 OK, but with an error status. This lets
                // the client observe the failure of fetching a token differently from the failure of
                // the auth server.

                let message = (e instanceof Error) ? e.message : 'unknown error';
                let code = "GetTokenError";

                if (isNotSignedInError(e)) {
                    code = "NotSignedInError";
                    message = 'You are not signed in to an Azure account. Please sign in.';
                }

                return {
                    statusCode: 200,
                    result: {
                        status: 'error',
                        code: code,
                        message: message,
                    },
                } satisfies JsonServerResponse<ErrorResponse>;
            }
        }
    });
}
