import { Command } from 'commander';
import { EventManager } from '../eventManager.js';

export function createListenCommand(client) {
  const cmd = new Command('listen');
  cmd.description('Start listening for events.');

  cmd.action(async () => {
    const manager = new EventManager(client);

    await manager.addProjectEventHandler('preprovision', async () => {
      for (let i = 1; i <= 20; i++) {
        console.log(`${i}. Doing work in JavaScript extension...`);
        await new Promise(r => setTimeout(r, 250));
      }
    });

    await manager.addServiceEventHandler('prepackage', async () => {
      for (let i = 1; i <= 20; i++) {
        console.log(`${i}. Service work in JavaScript extension...`);
        await new Promise(r => setTimeout(r, 250));
      }
    });

    await manager.receive();
  });

  return cmd;
}
