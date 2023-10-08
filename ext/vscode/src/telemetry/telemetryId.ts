// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Defines telemetry IDs for custom telemetry events exposed by the extension.
// Note that command invocations are covered by the AzExtUtils library,
// which automatically captures the duration and success/failure information for every command.
// Every event includes duration and success/failure information.
// Some additional data is captured for certain events; see comments for individual enum members.
export enum TelemetryId {
    // Reported when extension is activated.
    // Extra data captured: extension package load time and extension activation time.
    Activation = 'azure-dev.activate',

    // Reported when extension is activated.
    Deactivation = 'azure-dev.deactivate',

    // Reported when "dotenv" task is executed.
    // The result and total duration is reported for all task outcomes.
    // If the task fails, the reason for failure is reported via 'error' property.
    // For target task not found, we capture the name of the missing task.
    // For child task failure, we capture the name, duration, and exit code of the child task.
    DotEnvTask = 'azure-dev.tasks.dotenv',

    // Reported when 'deploy' CLI command is invoked.
    DeployCli = 'azure-dev.commands.cli.deploy.task',

    // Reported when 'infra delete' CLI command is invoked.
    // Extra data captured: whether the "purge" option was used.
    InfraDeleteCli = 'azure-dev.commands.cli.infra-delete.task',

    // Reported when 'login' CLI command is invoked.
    LoginCli = 'azure-dev.commands.cli.login-cli.task',

    // Reported when 'pipeline config' CLI command is invoked.
    PipelineConfigCli = 'azure-dev.commands.cli.pipeline-config.task',

    // Reported when 'provision' CLI command is invoked.
    ProvisionCli = 'azure-dev.commands.cli.provision.task',

    // Reported when 'restore' CLI command is invoked.
    RestoreCli = 'azure-dev.commands.cli.restore.task',

    // Reported when 'package' CLI command is invoked.
    PackageCli = 'azure-dev.commands.cli.package.task',

    // Reported when 'up' CLI command is invoked.
    UpCli = 'azure-dev.commands.cli.up.task',

    // Reported when 'down' CLI command is invoked.
    DownCli = 'azure-dev.commands.cli.down.task',

    // Reported when 'init' CLI command is invoked.
    InitCli = 'azure-dev.commands.cli.init.task',

    // Reported when 'env new' CLI command is invoked.
    EnvNewCli = 'azure-dev.commands.cli.env-new.task',

    // Reported when 'env refresh' CLI command is invoked.
    EnvRefreshCli = 'azure-dev.commands.cli.env-refresh.task',

    // Reported when 'env list' CLI command is invoked.
    EnvListCli = 'azure-dev.commands.cli.env-list.task',

    // Reported when the product evaluates whether to prompt the user for a survey.
    // We capture
    // - whether the user was already offered the survey,
    // - whether the user was prompted during current session (for any survey)
    // - whether the user is eligible for given survey
    // - whether the user is flighted for the survey
    SurveyCheck = 'azure-dev.survey-check',

    // Captures the result of a survey prompt
    SurveyPromptResponse = 'azure-dev.survey-prompt-response',

    WorkspaceViewApplicationResolve = 'azure-dev.views.workspace.application.resolve',
    WorkspaceViewEnvironmentResolve = 'azure-dev.views.workspace.environment.resolve',

    // Reported when diagnostics are provided on an azure.yaml document
    AzureYamlProvideDiagnostics = 'azure-dev.azureYaml.provideDiagnostics',

    // Reported when the document drop edit provider is invoked
    AzureYamlProvideDocumentDropEdits = 'azure-dev.azureYaml.provideDocumentDropEdits',

    // Reported when the project rename provider is invoked
    AzureYamlProjectRenameProvideWorkspaceEdits = 'azure-dev.azureYaml.projectRename.provideWorkspaceEdits',
}
