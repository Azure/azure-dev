// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import { SkillCommandArgs, SkillCommandResult } from 'vscode-azure-agent-api';
import { up } from '../up';

export async function agentUp(context: IActionContext, args: SkillCommandArgs): Promise<SkillCommandResult> {
    args.agentRequest.responseStream.markdown('Ok. I will deploy your application to Azure using the Azure Developer CLI.');

    await up(context, undefined);

    return {
        chatAgentResult: {
            metadata: {
                hello: "world",
            }
        },
        followUp: [
            // TODO: Add follow-up messages here
        ]
    };
}
