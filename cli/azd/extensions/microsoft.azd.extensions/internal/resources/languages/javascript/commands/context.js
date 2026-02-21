const { Command } = require('commander');
const AzdClient = require('../azdClient');
const { unary } = require('../grpcUtils');

const { EmptyRequest } = require('../generated/proto/models_pb');
const {
  GetEnvironmentValuesRequest
} = require('../generated/proto/environment_pb');

function createContextCommand() {
  const cmd = new Command('context');
  cmd.description('Get context of the azd project and environment.');

  cmd.action(async () => {
    try {
      const client = new AzdClient();

      // === User Config ===
      const configResponse = await unary(
        client.UserConfig,
        'get',
        new EmptyRequest(),
        client._metadata
      );
      console.log('User Config:', configResponse?.getValue?.().toString());

      // === Project Info ===
      const projectResponse = await unary(
        client.Project,
        'get',
        new EmptyRequest(),
        client._metadata
      );
      console.log('Project:', projectResponse?.getProject?.()?.toObject());

      // === Current Environment ===
      const currentEnv = await unary(
        client.Environment,
        'getCurrent',
        new EmptyRequest(),
        client._metadata
      );
      const currentEnvName = currentEnv?.environment?.name;
      console.log('Current Environment:', currentEnv?.getEnvironment?.()?.getName());

      // === All Environments ===
      const envList = await unary(
        client.Environment,
        'list',
        new EmptyRequest(),
        client._metadata
      );
      console.log('All Environments:', envList?.getEnvironmentsList?.()?.map(env => env.toObject()));

      // === Environment Values ===
      if (currentEnvName) {
        const req = new GetEnvironmentValuesRequest();
        req.setName(currentEnvName);

        const envValues = await unary(
          client.Environment,
          'getValues',
          req,
          client._metadata
        );
        console.log('Environment Values:', envValues?.getKeyValuesList?.());
      }

      // === Deployment Context ===
      const deployCtx = await unary(
        client.Deployment,
        'getDeploymentContext',
        new EmptyRequest(),
        client._metadata
      );
      console.log('Deployment Context:', deployCtx);
    } catch (err) {
      console.error(err.message);
      process.exit(1);
    }
  });

  return cmd;
}

module.exports = { createContextCommand };
