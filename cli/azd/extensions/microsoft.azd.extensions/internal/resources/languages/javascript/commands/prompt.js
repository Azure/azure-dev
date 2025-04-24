import { Command } from 'commander';

export function createPromptCommand(client) {
  const cmd = new Command('prompt');
  cmd.description('Examples of user prompting.');

  cmd.action(async () => {
    const services = await client.Prompt.multiSelect({
      options: {
        message: 'Which Azure services do you use?',
        choices: [
          { label: 'Functions', value: 'functions' },
          { label: 'Static Web Apps', value: 'static-web-apps' }
        ]
      }
    });

    const confirmed = await client.Prompt.confirm({
      options: { message: 'Continue?', defaultValue: true }
    });

    if (!confirmed.value) return;

    const subscription = await client.Prompt.promptSubscription({});
    console.log('Subscription:', subscription?.subscription);

    const resourceGroup = await client.Prompt.promptResourceGroup({
      azureContext: { scope: { subscriptionId: subscription.subscription.id } }
    });

    console.log('Resource Group:', resourceGroup?.resourceGroup);
  });

  return cmd;
}
