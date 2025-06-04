const grpc = require('@grpc/grpc-js');

const { ComposeServiceClient } = require('./generated/proto/compose_grpc_pb');
const { DeploymentServiceClient } = require('./generated/proto/deployment_grpc_pb');
const { EnvironmentServiceClient } = require('./generated/proto/environment_grpc_pb');
const { EventServiceClient } = require('./generated/proto/event_grpc_pb');
const { ProjectServiceClient } = require('./generated/proto/project_grpc_pb');
const { PromptServiceClient } = require('./generated/proto/prompt_grpc_pb');
const { UserConfigServiceClient } = require('./generated/proto/user_config_grpc_pb');
const { WorkflowServiceClient } = require('./generated/proto/workflow_grpc_pb');

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
