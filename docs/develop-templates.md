# Developing Templates

Unlike fully released [azd templates](https://github.com/topics/azd-templates), the template definition contained in this repository under the `templates` folder is disassembled into multiple directories. This allows re-use of specific assets such as Bicep files, resource assets, tests across templates. To reassemble the assets, a `repoman` file (`repo.yml`) is defined for each template that defines stitching and path rewriting via the `repoman generate` command. To learn more about the `repoman` tooling, see it's source under [generators/repo](../generators/repo).

The `templates` directory in this repository is structured as follows:

```yml
templates: # Root template folder
    todo: # Templates related to the Simple ToDo application
        common: # Common assets shared across templates.
        api: # API servers definition
        web: # Web servers definition
        projects: # The actual definition of how to assemble the template projects. repo.yaml is defined here which references relevant assets from  different directories. 
```

## Building

The simplest option is to build all templates in the repository:

```pwsh
./eng/scripts/Build-Templates.ps1
```

After this command completes, all generated templates can be found under the `.output` folder. `azd` commands can be ran in any of the generated folders for manual testing.

It is recommended to filter to specific templates for faster build times:

## Build templates with matching name

```pwsh
./eng/scripts/Build-Templates.ps1 todo-csharp-mongo
```

Builds the template named `todo-csharp-mongo`.

## Build templates with matching name regex

```pwsh
./eng/scripts/Build-Templates.ps1 python
```

Builds the template with names matching the regular expression `python`.

## Build templates under a specific directory

```pwsh
./eng/scripts/Build-Templates.ps1 -Path ./templates/todo/projects/python-mongo
```

Builds all templates discovered under `./templates/todo/projects/python-mongo`.

## Testing

Manual testing can be done by simply running `azd` commands in the generated template folders under `.output/<project>/generated`. Once you are happy with all the changes, submit a PR with your changes.
