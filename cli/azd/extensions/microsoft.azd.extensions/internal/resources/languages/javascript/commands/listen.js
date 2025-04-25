const { Command } = require('commander');
const { AzdClient } = require('../azdClient');
const { EventManager } = require('../eventManager');

function createListenCommand() {
  const cmd = new Command('listen');
  cmd.description('Starts the extension and listens for events.');

  cmd.action(async () => {
    const client = new AzdClient();
    const eventManager = new EventManager(client);

    const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

    // Project Event: preprovision
    await eventManager.addProjectEventHandler('preprovision', async () => {
      for (let i = 1; i <= 20; i++) {
        console.log(`${i}. Doing important work in JS extension...`);
        await sleep(250);
      }
    });

    // Service Event: prepackage
    await eventManager.addServiceEventHandler('prepackage', async () => {
      for (let i = 1; i <= 20; i++) {
        console.log(`${i}. Doing important work in JS extension...`);
        await sleep(250);
      }
    });

    // Start receiving events
    try {
      await eventManager.receive();
    } catch (err) {
      console.error('Error while receiving events:', err.message);
    }
  });

  return cmd;
}

module.exports = { createListenCommand };
