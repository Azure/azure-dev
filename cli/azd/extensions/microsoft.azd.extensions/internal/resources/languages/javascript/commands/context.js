const { Command } = require('commander');
const AzdClient = require('../azdClient');
const { unary } = require('../grpcUtils');

function createContextCommand() {
  const cmd = new Command('context');
  cmd.description('Get context of the AZD project and environment.');

  cmd.action(async () => {

    try {
      const client = new AzdClient();

      // === User Config ===
      const configResponse = await unary(client.UserConfig, 'get', {}, client._metadata);
      console.log('User Config:', configResponse?.value?.toString());

      // === Project Info ===
      const projectResponse = await unary(client.Project, 'get', {}, client._metadata);
      console.log('Project:', projectResponse?.project);

      // === Current Environment ===
      const currentEnv = await unary(client.Environment, 'getCurrent', {}, client._metadata);
      const currentEnvName = currentEnv?.environment?.name;
      console.log('Current Environment:', currentEnvName);

      // === All Environments ===
      const envList = await unary(client.Environment, 'list', {}, client._metadata);
      console.log('All Environments:', envList?.environments);

      // === Environment Values ===
      if (currentEnvName) {
        const envValues = await unary(client.Environment, 'getValues', { name: currentEnvName }, client._metadata);
        console.log('Environment Values:', envValues?.keyValues);
      }

      // === Deployment Context ===
      const deployCtx = await unary(client.Deployment, 'getDeploymentContext', {}, client._metadata);
      console.log('Deployment Context:', deployCtx);
    } catch (err) {
      console.error(err.message);
      process.exit(1);
    }
  });

  return cmd;
}

module.exports = { createContextCommand };
