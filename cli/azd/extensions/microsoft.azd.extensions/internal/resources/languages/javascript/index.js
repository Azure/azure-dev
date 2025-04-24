// index.js
import { Command } from 'commander';
import { AzdClient } from './azdClient.js';
import { createContextCommand } from './commands/context.js';
import { createPromptCommand } from './commands/prompt.js';
import { createListenCommand } from './commands/listen.js';

const program = new Command();
program.name('azd-extension');

const client = new AzdClient();

program.addCommand(createContextCommand(client));
program.addCommand(createPromptCommand(client));
program.addCommand(createListenCommand(client));

program.parseAsync(process.argv);
