#!/usr/bin/env Node
const fs = require('fs');
const path = require('path');
const yaml = require('js-yaml');
const { Command } = require('commander');
const { createContextCommand } = require('./commands/context');
const { createPromptCommand } = require('./commands/prompt');
const { createListenCommand } = require('./commands/listen');

// Load extension metadata
let namespace = 'demoX';
let version = '0.0.1';

try {
  const extensionYaml = fs.readFileSync(path.join(__dirname, 'extension.yaml'), 'utf8');
  const parsed = yaml.load(extensionYaml);
  namespace = parsed?.namespace || namespace;
  version = parsed?.version || version;
} catch (err) {
  console.warn('[WARN] Failed to load extension.yaml. Using default namespace and version.');
}

const program = new Command();

program
  .name(namespace)
  .description('azd CLI tool')
  .version(version);

program.addCommand(createContextCommand());
program.addCommand(createPromptCommand());
program.addCommand(createListenCommand());

program.parseAsync(process.argv);
