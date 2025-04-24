import { Command } from 'commander';

export function createContextCommand(client) {
  const cmd = new Command('context');
  cmd.description('Get context of the AZD project and environment.');

  cmd.action(async () => {
    const config = await client.UserConfig.get({});
    console.log('User Config:', config?.value);

    const project = await client.Project.get({});
    console.log('Project:', project?.project);

    const currentEnv = await client.Environment.getCurrent({});
    const envs = await client.Environment.list({});

    console.log('Environments:', envs.environments);
    console.log('Current Environment:', currentEnv?.environment?.name);

    const envValues = await client.Environment.getValues({ name: currentEnv?.environment?.name });
    console.log('Environment Values:', envValues?.keyValues);

    const deployContext = await client.Deployment.getDeploymentContext({});
    console.log('Deployment Context:', deployContext);
  });

  return cmd;
}
