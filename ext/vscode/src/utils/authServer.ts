
// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as http from 'http';
import { randomBytes } from 'crypto';
import { AccessToken, TokenCredential } from '@azure/core-auth';
import { isNotSignedInError } from './VsCodeAuthenticationCredential';
import { AddressInfo } from 'net';

// startAuthServer creates a locally running server that will respond to Azure Dev CLI authentication requests and
// starts listening for requests.  Requests must be authenticated with a key that is returned from this function.
// The provided credential is used to fetch tokens for auth requests.
export function startAuthServer(credential: TokenCredential): Promise<{ server: http.Server, endpoint: string, key: string }> {
    const key = randomBytes(32).toString('hex');

    const server = http.createServer(async (req, res) => {
        if (req.headers['content-type'] !== 'application/json' || req.method !== 'POST' || req.url !== '/token?api-version=2023-07-12-preview') {
            res.writeHead(400).end();
            return;
        }

        if (req.headers['authorization'] !== `Bearer ${key}`) {
            res.writeHead(401).end();
            return;
        }

        const body: Buffer[] = [];
        req.on('data', (chunk) => { body.push(chunk); });
        req.on('end', async () => {
            try {
                const reqBody = JSON.parse(Buffer.concat(body).toString());
                if (typeof reqBody !== 'object') {
                    res.writeHead(400).end();
                    return;
                }

                if (!reqBody.scopes ||
                    !Array.isArray(reqBody.scopes) ||
                    reqBody.scopes.length === 0 ||
                    reqBody.scopes.some((a: unknown) => typeof a !== 'string') ||
                    reqBody.tenantId && typeof reqBody.tenantId !== 'string') {
                    res.writeHead(400).end();
                    return;
                }

                res.setHeader('Content-Type', 'application/json');

                let token: AccessToken | null;

                try {
                    token = await credential.getToken(reqBody.scopes, {
                        tenantId: reqBody.tenantId,
                    });
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

                    res.writeHead(200).end(
                        JSON.stringify({
                            status: 'error',
                            code: code,
                            message: message,
                        })
                    );
                    return;
                }

                if (!token) {
                    res.writeHead(500).end();
                    return;
                }

                res.writeHead(200).end(
                    JSON.stringify({
                        status: 'success',
                        token: token.token,
                        expiresOn: new Date(token.expiresOnTimestamp).toISOString(),
                    })
                );
            } catch {
                res.writeHead(500).end();
                return;
            }
        });
    });

    return new Promise<{server: http.Server, endpoint: string, key: string}>((resolve) => {
        server.listen({
            host: '127.0.0.1',
            port: 0,
        }, () => resolve({ server, endpoint: `http://127.0.0.1:${(server.address() as AddressInfo).port}`, key }));
    });
}
