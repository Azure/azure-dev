// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
// import { z } from 'zod';
import { IActionContext } from '@microsoft/vscode-azext-utils';
import { SkillCommandArgs, SkillCommandResult } from 'vscode-azure-agent-api';
import { createAzureDevCli } from '../../utils/azureDevCli';
import { getAzDevTerminalTitle, showReadmeFile } from '../cmdUtil';
import { executeAsTask } from '../../utils/executeAsTask';
import { TelemetryId } from '../../telemetry/telemetryId';

export async function agentInitWithEnvironment(context: IActionContext, args: SkillCommandArgs): Promise<SkillCommandResult> {
    const workspacePath = vscode.workspace.workspaceFolders![0].uri;

    args.agentRequest.responseStream.markdown(
        "Ok, I will initialize your application for Azure with the environment name you provided. " +
        "Please continue your interaction in the terminal window."
    );

    // const zodSchema = z.object({ environmentName: z.string() });
    // type Foo = z.infer<typeof zodSchema>;
    // const env: Foo = await args.agent.getTypeChatTranslation(zodSchema, "Foo", , args.agentRequest);
    const environmentName = await args.agent.getResponseAsStringLanguageModelInteraction(
        "The user is trying to give the name for an environment. If they say just one word, assume that is the environment name. " +
        "Otherwise, read their response and determine the environment name that the user is asking for. " +
        "Respond with the environment name and nothing more. " +
        "For example, if the user says \"myenv\", respond with \"myenv\". If the user says \"Name my environment myenv\", respond with \"myenv\".",
        args.agentRequest, { includeHistory: "none", }
    ) || "unknownEnvironment";

    const azureCli = await createAzureDevCli(context);
    const command = azureCli.commandBuilder
        .withArg('init')
        .withNamedArg('-e', {value: environmentName, quoting: vscode.ShellQuoting.Strong});

    await executeAsTask(command.build(), getAzDevTerminalTitle(), {
        alwaysRunNew: true,
        cwd: workspacePath.fsPath,
        env: azureCli.env
    }, TelemetryId.InitAgent).then(() => {
        void showReadmeFile(workspacePath);
    });

    return {
        chatAgentResult: {
            metadata: {
            }
        },
        followUp: [
            { prompt: "Now deploy my application to Azure" }
        ]
    };
}
