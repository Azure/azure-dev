// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AccessToken, GetTokenOptions, TokenCredential } from '@azure/core-auth';

// TODO(ellismg): This code is more or less lifted from @microsoft/vscode-azext-azureauth. It would be ideal
// to share more of it if we can.
//
// We need to consider cases where a user's account is in multiple tenants and only some of them are signed in.  Today,
// in this case `VSCodeAzureSubscriptionProvider`'s getSubscriptions() method will throw `NotSignedInError` because some
// the tenants are not signed in and it can not return a complete response.
//
// I think the thing we want to do is subclass VSCodeAzureSubscriptionProvider and override `getTenantFilters()` such that
// we only request sessions for the tenant that the user requested.  However, in the case where an explicit tenant ID is not
// passed, we need a way to say: "filter to exclude everything except the user's default tenant", which is less clear.

/**
 * An error indicating the user is not signed in.
 */
export class NotSignedInError extends Error {
    public readonly isNotSignedInError = true;

    constructor() {
        super(vscode.l10n.t('You are not signed in to an Azure account. Please sign in.'));
    }
}

/**
 * Tests if an object is a `NotSignedInError`. This should be used instead of `instanceof`.
 *
 * @param error The object to test
 *
 * @returns True if the object is a NotSignedInError, false otherwise
 */
export function isNotSignedInError(error: unknown): error is NotSignedInError {
    return !!error && typeof error === 'object' && (error as NotSignedInError).isNotSignedInError === true;
}

/**
 *
 * A TokenCredential that uses the VS Code Authentication API to get tokens.
 *
 */
export class VsCodeAuthenticationCredential implements TokenCredential {
    async getToken(scopes: string | string[], options?: GetTokenOptions | undefined): Promise<AccessToken | null> {
        const scopeSet = new Set<string>(scopes);

        if (typeof scopes === 'string') {
            scopeSet.add(scopes);
        } else if (Array.isArray(scopes)) {
            scopes.forEach(scope => scopeSet.add(scope));
        }

        // getSession recognizes this special scope and uses it to set the tenant ID that is used when
        // requesting a token. This scope is not actually included in the request that is sent to get
        // the token, getSession removes it from the list of scopes before sending the request.
        if (options?.tenantId) {
            scopeSet.add(`VSCODE_TENANT:${options.tenantId}`);
        }

        let session = await vscode.authentication.getSession('microsoft', Array.from(scopeSet), { silent: true });
        if (!session) {
            // If we couldn't get a sessions silently, the user may not be logged in, so try prompting them.
            session = await vscode.authentication.getSession('microsoft', Array.from(scopeSet), { createIfNone: true, clearSessionPreference: true });
            if (!session) {
                throw new NotSignedInError();
            }
        }

        // The object returned by getSession does not have an expiration time associated with it, but the GetToken response
        // needs one.
        //
        // We can pull the `exp` claim from the token and use it. If for some reason, we can't find find the claim, we'll
        // just set the expiration time to 0. This will mean that the token appears to be expired, however in practice the
        // `getSession` API handles refreshing the token if it was close to expiring before returning it to us, and the Azure
        // SDKs will not fail if the token appears to be expired (they may end up requesting a token for each operation instead
        // of being able to cache it themselves in the client, but that's not a big deal, since `getSession` will be able to
        // elide the refresh if it is not needed.
        let expiresOnTimestamp = 0;
        try {
             const expClaim = JSON.parse(Buffer.from(session.accessToken.split('.')[1], 'base64').toString()).exp;
             if (typeof expClaim === 'number') {
                // The exp claim in the JWT is the number of seconds since the Unix Epoch, but the `expiresOnTimestamp` property
                // is the number of milliseconds since the Unix Epoch, so we need to multiply by 1000.
                expiresOnTimestamp = expClaim * 1000;
             }
        } catch {
            // Some issue parsing the token, so not much we can do here, just leave the expiration time as 0 (the Unix Epoch).
        }

        return {
            token: session.accessToken,
            expiresOnTimestamp: expiresOnTimestamp
        };
    }
}
