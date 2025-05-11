const grpc = require('@grpc/grpc-js');

const { ComposeServiceClient } = require('./proto/compose_grpc_pb');
const { DeploymentServiceClient } = require('./proto/deployment_grpc_pb');
const { EnvironmentServiceClient } = require('./proto/environment_grpc_pb');
const { EventServiceClient } = require('./proto/event_grpc_pb');
const { ProjectServiceClient } = require('./proto/project_grpc_pb');
const { PromptServiceClient } = require('./proto/prompt_grpc_pb');
const { UserConfigServiceClient } = require('./proto/user_config_grpc_pb');
const { WorkflowServiceClient } = require('./proto/workflow_grpc_pb');

class AzdClient {
  constructor() {
    const server = process.env.AZD_SERVER;
    const token = process.env.AZD_ACCESS_TOKEN;

    if (!server || !token) {
      throw new Error('AZD_SERVER and AZD_ACCESS_TOKEN must be set.');
    }

    this._metadata = new grpc.Metadata();
    this._metadata.add('authorization', token);

    const address = server.replace(/^https?:\/\//, '');

    const credentials = grpc.credentials.createInsecure();

    this.Compose = new ComposeServiceClient(address, credentials);
    this.Deployment = new DeploymentServiceClient(address, credentials);
    this.Environment = new EnvironmentServiceClient(address, credentials);
    this.Events = new EventServiceClient(address, credentials);
    this.Project = new ProjectServiceClient(address, credentials);
    this.Prompt = new PromptServiceClient(address, credentials);
    this.UserConfig = new UserConfigServiceClient(address, credentials);
    this.Workflow = new WorkflowServiceClient(address, credentials);
  }

  withAuthMetadata(callback) {
    return (request, callbackFn) => callback(request, this._metadata, callbackFn);
  }
}

module.exports = AzdClient;
