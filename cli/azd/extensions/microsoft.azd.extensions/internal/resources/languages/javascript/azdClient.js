const grpc = require('@grpc/grpc-js');
const protoLoader = require('@grpc/proto-loader');
const path = require('path');

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

    const load = (name) => {
      const protoPath = path.join(__dirname, 'proto', `${name}.proto`);
      const pkgDef = protoLoader.loadSync(protoPath, options);
      const proto = grpc.loadPackageDefinition(pkgDef);
      return proto.microsoft.azd;
    };

    const address = server.startsWith('http') ? server : `http://${server}`;

    this.Compose = new load('compose').ComposeService(address, grpc.credentials.createInsecure());
    this.Deployment = new load('deployment').DeploymentService(address, grpc.credentials.createInsecure());
    this.Environment = new load('environment').EnvironmentService(address, grpc.credentials.createInsecure());
    this.Events = new load('event').EventService(address, grpc.credentials.createInsecure());
    this.Project = new load('project').ProjectService(address, grpc.credentials.createInsecure());
    this.Prompt = new load('prompt').PromptService(address, grpc.credentials.createInsecure());
    this.UserConfig = new load('user_config').UserConfigService(address, grpc.credentials.createInsecure());
    this.Workflow = new load('workflow').WorkflowService(address, grpc.credentials.createInsecure());
  }

  _withMetadata(method) {
    return (...args) => {
      const callback = args[args.length - 1];
      const metadata = this._metadata;
      method.call(null, ...args.slice(0, -1), metadata, callback);
    };
  }
}

module.exports = { AzdClient };
