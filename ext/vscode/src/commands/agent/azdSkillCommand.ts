// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { getZodSchemaAsTypeScript } from 'typechat/zod';
import { z } from 'zod';
import { IActionContext } from '@microsoft/vscode-azext-utils';
import { SkillCommandArgs, SkillCommandResult } from 'vscode-azure-agent-api';
import { InitWizardContext } from './wizard/InitWizardContext';

const InitWizardInfo = z.object({
    environmentName: z.string().describe('The environment name to use for deployment'),
    azureLocation: z.enum(["EastUS2", "WestUS"]).describe('The Azure location in which to deploy your resources'),
});

const schema = {
    InitWizardInfo
};

export async function azdSkillCommand(context: IActionContext, args: SkillCommandArgs): Promise<SkillCommandResult> {
    const zodToTypeScript = getZodSchemaAsTypeScript(schema);
    const enoughToProceed = 'I now have enough information to proceed.';

    const result = await args.agent.verbatimLanguageModelInteraction(
        `Your task is to determine several pieces of information from the chat history with the user.
        In particular, you need to determine the information to fill out an object with this TypeScript schema: "${zodToTypeScript}".
        If you are able to determine all of that information from the user prompt, thank them for the information
        and tell them you have enough information to proceed, using the exact phrase "${enoughToProceed}". If you are not able to determine some or all of the
        information from the user prompt, ask them a clarifying question for what information is still needed. Always respond in prose without any code samples.`,
        args.agentRequest,
        {
            includeHistory: 'requests',
        }
    );

    const haveEnoughInfo = result.languageModelResponded && result.languageModelResponse.includes(enoughToProceed);

    if (haveEnoughInfo) {
        const info = await args.agent.getTypeChatTranslation(schema, "InitWizardInfo", args.agentRequest);

        if (info === undefined) {
            throw new Error('Failed to get the information needed to proceed.');
        }

        args.agentRequest.responseStream.button({
            command: 'azure-dev.commands.cli.init',
            title: 'Go Go Gadget: AZD Init!',
            arguments: [
                /* selectedFile: */ undefined,
                /* allSelectedFiles: */ undefined,
                /* options: */ {
                    environmentName: info.environmentName,
                    location: info.azureLocation,
                    fromSource: true,
                } satisfies Partial<InitWizardContext>
            ]
        });
    }

    return {
        chatAgentResult: {
            metadata: { }
        }
    };
}
