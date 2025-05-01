const { Command } = require('commander');
const AzdClient = require('../azdClient');
const { unary } = require('../grpcUtils');
const logger = require('../logger');

function createContextCommand() {
  const cmd = new Command('context');
  cmd.description('Get context of the AZD project and environment.');

  cmd.action(async () => {

    try {
      const client = new AzdClient();

      // === User Config ===
      const configResponse = await unary(client.UserConfig, 'get', {}, client._metadata);
      logger.info('User Config:', { value: configResponse?.value?.toString() });

      // === Project Info ===
      const projectResponse = await unary(client.Project, 'get', {}, client._metadata);
      logger.info('Project Info:', { project: projectResponse?.project });

      // === Current Environment ===
      const currentEnv = await unary(client.Environment, 'getCurrent', {}, client._metadata);
      const currentEnvName = currentEnv?.environment?.name;
      logger.info('Current Environment:', { name: currentEnvName });

      // === All Environments ===
      const envList = await unary(client.Environment, 'list', {}, client._metadata);
      logger.info('All Environments:', { environments: envList?.environments });

      // === Environment Values ===
      if (currentEnvName) {
        const envValues = await unary(client.Environment, 'getValues', { name: currentEnvName }, client._metadata);
        logger.info('Environment Values:', { keyValues: envValues?.keyValues });
      }

      // === Deployment Context ===
      const deployCtx = await unary(client.Deployment, 'getDeploymentContext', {}, client._metadata);
      logger.info('Deployment Context:', { context: deployCtx });
    } catch (err) {
      logger.error('Error in context command', { error: err.message, stack: err.stack });
      process.exit(1);
    }
  });

  return cmd;
}

module.exports = { createContextCommand };
