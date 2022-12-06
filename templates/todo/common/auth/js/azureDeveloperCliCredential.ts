// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

import {
    AccessToken,
    TokenCredential,
} from "@azure/core-auth";
import { CredentialUnavailableError } from "@azure/identity";
import child_process from "child_process";

export class AzureDeveloperCliCredential implements TokenCredential {
    public async getToken(
        scopes: string | string[],
    ): Promise<AccessToken> {
        if (typeof(scopes) === "string") {
            scopes = [scopes];
        }

        try {
            const obj = await getAzdAccessToken(scopes);
            const isNotLoggedInError = obj.stderr?.match("not logged in, run `azd login` to login");

            if (isNotLoggedInError) {
                throw new CredentialUnavailableError(
                    "Please run 'azd login' from a command prompt to authenticate before using this credential."
                );
            }

            if (obj.error && (obj.error as any).code === "ENOENT") {
                throw new CredentialUnavailableError(
                    "Azure Developer CLI could not be found. Please visit https://aka.ms/azure-dev for installation instructions and then, once installed, authenticate to your Azure account using 'azd login'."
                );
            }

            try {
                const resp: { token: string, expiresOn: string} = JSON.parse(obj.stdout);

                return {
                    token: resp.token,
                    expiresOnTimestamp: new Date(resp.expiresOn).getTime()
                };
            } catch (e: any) {
                if (obj.stderr) {
                    throw new CredentialUnavailableError(obj.stderr);
                }

                throw e;
            }
        } catch (err: any) {
            const error =
                err.name === "CredentialUnavailableError"
                    ? err
                    : new CredentialUnavailableError(
                        (err as Error).message || "Unknown error while trying to retrieve the access token"
                    );

            throw error;
        }
    }
}

function getSafeWorkingDir(): string {
    if (process.platform === "win32") {
        if (!process.env.SystemRoot) {
            throw new Error("Azure Developer CLI credential expects a 'SystemRoot' environment variable");
        }
        return process.env.SystemRoot;
    } else {
        return "/bin";
    }
}


function getAzdAccessToken(
    scopes: string[]
): Promise<{ stdout: string; stderr: string; error: Error | null}> {
    return new Promise((resolve, reject) => {
        try {
            child_process.execFile(
                "azd",
                [
                    "auth",
                    "token",
                    "--output",
                    "json",
                    ...scopes.flatMap((scope) => ["--scope", scope])
                ],
                { cwd: getSafeWorkingDir() },
                (error, stdout, stderr) => {
                    resolve({ stdout, stderr, error });
                }
            );
        } catch (err: any) {
            reject(err);
        }
    });
}
