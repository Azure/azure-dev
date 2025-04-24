// index.js
const { Command } = require('commander');
const program = new Command();

program
  .name('company.js')
  .description('A sample AZD extension in JavaScript')
  .version('0.0.1');

program
  .command('hello')
  .description('Say hello')
  .action(() => {
    console.log('Hello from company.js!');
  });

program.parse(process.argv);
