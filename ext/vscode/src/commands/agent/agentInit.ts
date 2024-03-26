// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IActionContext } from '@microsoft/vscode-azext-utils';
import { SkillCommandArgs, SkillCommandResult } from 'vscode-azure-agent-api';

export async function agentInit(context: IActionContext, args: SkillCommandArgs): Promise<SkillCommandResult> {
    args.agentRequest.responseStream.markdown(
        "Since you have the [Azure Developer CLI](https://aka.ms/azd) installed, I recommend using it to deploy your app to Azure. " +
        "This is a non-destructive action and will only add new files to your workspace." +
        "\n\n---" +
        "\n\nAn [environment](https://aka.ms/azd) will be created--what do you want to name the environment?"
    );

    return {
        chatAgentResult: {
            metadata: {
            }
        },
        followUp: [
            { prompt: "Create an environment with the name 'myenv'" }
        ]
    };
}
