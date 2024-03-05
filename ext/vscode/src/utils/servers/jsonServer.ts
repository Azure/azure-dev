// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.


// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as http from 'http';
import { randomBytes } from 'crypto';
import { AddressInfo } from 'net';

export type JsonServerResponse<T = SuccessResponseBase | ErrorResponse | CancelledResponse | UndefinedResponse> = { statusCode: number, result?: T };

export type UrlHandler = (messageBody: unknown) => JsonServerResponse | Promise<JsonServerResponse>;

export type SuccessResponseBase = {
    status: 'success';
};

export type ErrorResponse = {
    status: 'error';
    code: string;
    message: string;
};

export type CancelledResponse = {
    status: 'cancelled';
};

export type UndefinedResponse = undefined;

// startServer creates a locally running server that will respond to Azure Dev CLI requests and
// starts listening for requests.  Requests must be authenticated with a key that is returned from this function.
export function startJsonServer(urls: Record<string, UrlHandler>): Promise<{ server: http.Server, endpoint: string, key: string }> {
    const key = randomBytes(32).toString('hex');

    const server = http.createServer(async (req, res) => {
        if (req.headers['content-type'] !== 'application/json' || req.method !== 'POST' || !req.url || !!urls[req.url] ) {
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

                const handler = urls[req.url!];
                const { statusCode, result } = await handler(reqBody);

                res.setHeader('Content-Type', 'application/json');

                if (result) {
                    res.writeHead(statusCode).end(JSON.stringify(result));
                } else {
                    res.writeHead(statusCode).end();
                }
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
