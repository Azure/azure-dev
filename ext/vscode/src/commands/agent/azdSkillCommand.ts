// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { z } from 'zod';
import { IActionContext } from '@microsoft/vscode-azext-utils';
import { SkillCommandArgs, SkillCommandResult } from 'vscode-azure-agent-api';

/* eslint-disable @typescript-eslint/naming-convention */

const zodSchema = z.object({
    environmentName: z.string().describe('The environment name to use for deployment'),
    azureLocation: z.enum(["EastUS2", "WestUS"]).describe('The Azure location in which to deploy your resources'),
});

export async function azdSkillCommand(context: IActionContext, args: SkillCommandArgs): Promise<SkillCommandResult> {
    const result = await args.agent.verbatimLanguageModelInteraction(
        `Your task is to determine several pieces of information from the chat history with the user.
        In particular, you need to determine 1. the environment name (any string) and 2. an Azure location (either EastUS2 or WestUS).
        If you are able to determine all of that information from the user prompt, thank them for the information
        and tell them you have enough information to proceed. If you are not able to determine some or all of the
        information from the user prompt, ask them a clarifying question for what information is still needed.`,
        args.agentRequest,
        {
            includeHistory: "requests"
        }
    );

    if (result.languageModelResponded && /thank/i.test(result.languageModelResponse)) {

    }

    return {
        chatAgentResult: {
            metadata: {

            }
        }
    };
}

/* eslint-enable @typescript-eslint/naming-convention */
