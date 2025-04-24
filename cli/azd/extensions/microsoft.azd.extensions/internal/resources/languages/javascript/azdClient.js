import grpc from '@grpc/grpc-js';
import { loadPackageDefinition } from '@grpc/grpc-js';
import protoLoader from '@grpc/proto-loader';
import path from 'path';

const PROTO_PATH = path.resolve('proto');
const OPTIONS = {
  keepCase: true,
  longs: String,
  enums: String,
  defaults: true,
  oneofs: true
};

export class AzdClient {
  constructor(server = process.env.AZD_SERVER, token = process.env.AZD_ACCESS_TOKEN) {
    if (!server || !token) throw new Error('AZD_SERVER and AZD_ACCESS_TOKEN must be set.');

    this.metadata = new grpc.Metadata();
    this.metadata.add('authorization', token);

    this.channelCreds = grpc.credentials.createInsecure();
    this.clientOptions = { 'grpc.default_authority': server };

    const packages = [
      'compose', 'deployment', 'environment', 'event', 'project',
      'prompt', 'user_config', 'workflow'
    ];

    for (const pkg of packages) {
      const protoPath = path.join(PROTO_PATH, `${pkg}.proto`);
      const def = protoLoader.loadSync(protoPath, OPTIONS);
      const grpcPackage = loadPackageDefinition(def).azdext;
      this[capitalize(pkg)] = new grpcPackage[`${capitalize(pkg)}Service`](
        server,
        this.channelCreds
      );
    }
  }

  withMetadata(method, request) {
    return new Promise((resolve, reject) => {
      method.call(this, request, this.metadata, (err, res) => {
        if (err) reject(err);
        else resolve(res);
      });
    });
  }
}

function capitalize(str) {
  return str.charAt(0).toUpperCase() + str.slice(1);
}
