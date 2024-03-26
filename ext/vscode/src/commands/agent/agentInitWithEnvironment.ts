// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// import * as vscode from 'vscode';
import { z } from 'zod';
import { IActionContext } from '@microsoft/vscode-azext-utils';
import { SkillCommandArgs, SkillCommandResult } from 'vscode-azure-agent-api';
// import { createAzureDevCli } from '../../utils/azureDevCli';
// import { getAzDevTerminalTitle, showReadmeFile } from '../cmdUtil';
// import { executeAsTask } from '../../utils/executeAsTask';
// import { TelemetryId } from '../../telemetry/telemetryId';
import { init } from '../init';

export async function agentInitWithEnvironment(context: IActionContext, args: SkillCommandArgs): Promise<SkillCommandResult> {
    // const workspacePath = vscode.workspace.workspaceFolders![0].uri;
    const zodSchema = z.object({ environmentName: z.string() });
    const env = await args.agent.getTypeChatTranslation({ "Environment": zodSchema }, "Environment", args.agentRequest, { includeHistory: "none"});
    const environmentName = env?.environmentName || "myAzdEnvironment";

    args.agentRequest.responseStream.progress("Initializing your application for Azure");

    // args.agentRequest.responseStream.markdown(
    //     "Ok, I will initialize your application for Azure with the environment name you provided. " +
    //     "Please continue your interaction in the terminal window."
    // );

    await init(context, undefined, undefined, { environmentName });

    // const azureCli = await createAzureDevCli(context);
    // const command = azureCli.commandBuilder
    //     .withArg('init')
    //     .withNamedArg('-e', {value: environmentName, quoting: vscode.ShellQuoting.Strong});

    // await executeAsTask(command.build(), getAzDevTerminalTitle(), {
    //     alwaysRunNew: true,
    //     cwd: workspacePath.fsPath,
    //     env: azureCli.env
    // }, TelemetryId.InitAgent).then(() => {
    //     void showReadmeFile(workspacePath);
    // });

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
