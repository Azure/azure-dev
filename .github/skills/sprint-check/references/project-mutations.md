# Project V2 GraphQL Mutations

Reference for reading and writing GitHub Projects V2 fields.

## Project Info

- **Organization**: Azure
- **Project number**: 182
- **Key fields**: Sprint (iteration), Priority (single select), Initiative (single select)

## Prerequisites

The `gh` CLI must be authenticated with `project` scope:
```bash
gh auth refresh --scopes project
```

## Reading Field Options

Before setting a field value, you need the field ID and option IDs.

### Get all field IDs and options

```bash
gh api graphql -f query='{
  organization(login: "Azure") {
    projectV2(number: 182) {
      id
      field(name: "Priority") {
        ... on ProjectV2FieldCommon { id name }
        ... on ProjectV2SingleSelectField {
          id name
          options { id name }
        }
      }
    }
  }
}'
```

Repeat for "Initiative" and "Sprint" fields.

### Get Sprint iterations

```bash
gh api graphql -f query='{
  organization(login: "Azure") {
    projectV2(number: 182) {
      field(name: "Sprint") {
        ... on ProjectV2IterationField {
          id
          configuration {
            iterations { id title startDate duration }
            completedIterations { id title startDate duration }
          }
        }
      }
    }
  }
}'
```

## Adding an Issue to the Project

Before setting fields, the issue must be a project item.

```bash
# Get the issue's node ID
gh api graphql -f query='{
  repository(owner: "Azure", name: "azure-dev") {
    issue(number: ISSUE_NUMBER) { id }
  }
}'
```

```bash
# Add to project (returns the project item ID)
gh api graphql -f query='
mutation {
  addProjectV2ItemById(input: {
    projectId: "PROJECT_ID"
    contentId: "ISSUE_NODE_ID"
  }) {
    item { id }
  }
}'
```

Store the returned `item.id` — you need it for field mutations.

## Setting Field Values

### Set Priority (single select)

```bash
gh api graphql -f query='
mutation {
  updateProjectV2ItemFieldValue(input: {
    projectId: "PROJECT_ID"
    itemId: "ITEM_ID"
    fieldId: "PRIORITY_FIELD_ID"
    value: { singleSelectOptionId: "OPTION_ID" }
  }) {
    projectV2Item { id }
  }
}'
```

### Set Initiative (single select)

Same mutation shape as Priority, with Initiative field ID and option ID.

### Set Sprint (iteration)

```bash
gh api graphql -f query='
mutation {
  updateProjectV2ItemFieldValue(input: {
    projectId: "PROJECT_ID"
    itemId: "ITEM_ID"
    fieldId: "SPRINT_FIELD_ID"
    value: { iterationId: "ITERATION_ID" }
  }) {
    projectV2Item { id }
  }
}'
```

## Common Workflow

1. **Get project ID**: from the initial project query
2. **Get field IDs**: Priority field ID, Initiative field ID, Sprint field ID
3. **Get option IDs**: for Priority → list options, for Initiative → list options, for Sprint → list iterations
4. **Check if issue is in project**: query issue's `projectItems`
5. **If not in project**: add via `addProjectV2ItemById`
6. **Set fields**: use `updateProjectV2ItemFieldValue` for each field

Cache the project ID and field IDs across operations — they don't change within a session.
