# Repo Generator

Generates azure-sample style repositories for applications based on a repo template manifest.

## Usage

```
  _ __ ___ _ __   ___  _ __ ___   __ _ _ __
 | '__/ _ \ '_ \ / _ \| '_ ` _ \ / _` | '_ \
 | | |  __/ |_) | (_) | | | | | | (_| | | | |
 |_|  \___| .__/ \___/|_| |_| |_|\__,_|_| |_|
          |_|
Usage: repoman generate [options]

Generates a new repo based on a template configuration

Options:
  --debug                       When set writes verbose output to the console (default: false)
  -o --output <output>          The output path for the generated template
  -s, --source <source>         The template source location (default: ".")
  -t --templateFile <template>  The repo template manifest location (default: "./repo.yaml")
  -u --update                   When set will commit and push changes to the specified remotes & branches (default: false)
  -r --remote <targetRemote>    The remote name used while committing back to the target repos
  -b --branch <targetBranch>    The target branch name for committing back to the target repos
  -m --message <message>        Custom commit message used for committing back to the target repos
  -h, --help                    display help for command
```

## Examples

The following are examples of repo templates

[Node JS / React / Mongo](../../templates/todo/projects/nodejs-mongo/repo.yaml)

[Python / React / Mongo](../../templates/todo/projects/python-mongo/repo.yaml)

## Developing

### Install prerequisites

```bash
npm ci
```

### Build

```bash
npm run build
```

### Watch

Monitors typescript files and continuously re-compiles in the background

```bash
npm run watch
```

### Running

There are a couple different options to run locally

```bash
# Simulate global package install. 
# Run this 1 time per development session
npm link

# Run in a separate terminal window
npm run watch

# All in one
npm link && npm run watch

# Execute CLI command (run in its own terminal window)
repoman generate -s <source> -o <output>
```
