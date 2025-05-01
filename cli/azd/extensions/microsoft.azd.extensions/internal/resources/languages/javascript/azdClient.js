const grpc = require('@grpc/grpc-js');
const protoLoader = require('@grpc/proto-loader');
const path = require('path');
const fs = require('fs');

class AzdClient {
  constructor() {
    const server = process.env.AZD_SERVER;
    const token = process.env.AZD_ACCESS_TOKEN;

    if (!server || !token) {
      throw new Error('AZD_SERVER and AZD_ACCESS_TOKEN must be set.');
    }

    this._metadata = new grpc.Metadata();
    this._metadata.add('authorization', token);

    const options = {
      keepCase: true,
      longs: String,
      enums: String,
      defaults: true,
      oneofs: true,
    };

    // Loads a .proto file and returns the generated gRPC package
    const loadService = (name) => {
      let protoPath;
      if (process.pkg) {
        protoPath = path.join(path.dirname(process.execPath), 'proto', `${name}.proto`);
        if (!fs.existsSync(protoPath)) {
          protoPath = path.join(__dirname, 'proto', `${name}.proto`);
        }
      } else {
        protoPath = path.join(process.cwd(), 'proto', `${name}.proto`);
      }

      const packageDefinition = protoLoader.loadSync(protoPath, options);
      const proto = grpc.loadPackageDefinition(packageDefinition);

      return proto.azdext;
    };

    const address = server.replace(/^https?:\/\//, '');

    // Load each proto service
    const composeProto = loadService('compose');
    const deploymentProto = loadService('deployment');
    const environmentProto = loadService('environment');
    const eventProto = loadService('event');
    const projectProto = loadService('project');
    const promptProto = loadService('prompt');
    const userConfigProto = loadService('user_config');
    const workflowProto = loadService('workflow');

    this.Compose = new composeProto.ComposeService(address, grpc.credentials.createInsecure());
    this.Deployment = new deploymentProto.DeploymentService(address, grpc.credentials.createInsecure());
    this.Environment = new environmentProto.EnvironmentService(address, grpc.credentials.createInsecure());
    this.Events = new eventProto.EventService(address, grpc.credentials.createInsecure());
    this.Project = new projectProto.ProjectService(address, grpc.credentials.createInsecure());
    this.Prompt = new promptProto.PromptService(address, grpc.credentials.createInsecure());
    this.UserConfig = new userConfigProto.UserConfigService(address, grpc.credentials.createInsecure());
    this.Workflow = new workflowProto.WorkflowService(address, grpc.credentials.createInsecure());
  }
}

module.exports = AzdClient;
