# azd Telemetry — Product Overview

> What azd telemetry tells us, where to find it, and how to work with it.

> [!NOTE]
> This is the **public** telemetry documentation. A Microsoft-internal companion set of docs
> (data pipeline, Kusto/Power BI reporting, runnable queries) is maintained separately for
> internal maintainers.

## What azd Telemetry Captures

Azure Developer CLI (azd) collects pseudonymized usage telemetry to understand how developers use the tool, measure feature adoption, and diagnose issues at scale. Users can opt out at any time.

### What We Collect

| Category | Examples |
|----------|---------|
| **Commands** | Which commands are run (`init`, `deploy`, `provision`, `up`), success/failure, duration |
| **Features** | Feature-specific properties (template used, packaging format, auth method, target Azure services) |
| **Errors** | Error codes and categories (ARM errors, auth failures, build failures), **not** user content |
| **Environment** | OS, azd version, execution environment (GitHub Actions, Azure Pipelines, VS Code, etc.) |
| **Identity** | Anonymous device proxy (hashed machine ID / device ID); when signed in to Azure, the Entra object ID, tenant ID, and subscription ID (classified as pseudonymized/organizational data) |
| **Extensions** | Which extensions are installed and invoked, extension errors |
| **Performance** | Operation duration, time spent waiting for user input vs. executing |

> **Anonymous vs. signed-in users:** azd does not require sign-in to emit telemetry. Anonymous users are
> counted via a hashed machine/device identifier. When a user is authenticated to Azure, pseudonymized
> identity fields (Entra object ID, tenant ID, subscription ID) are also attached. See the
> [Data Reference](../reference/telemetry-data.md#identity--account-fields) for the exact fields.

### What We Don't Collect

- No source code or project file contents
- No Azure credentials, tokens, or connection strings
- No personal names, emails, or IP addresses
- No raw machine identifiers — the machine/device ID is one-way hashed
- Project names and template names are **hashed** (one-way) — we can count unique projects but can't see what they're called
- Users opt out via `AZURE_DEV_COLLECT_TELEMETRY=no`

## Key Metrics

| Metric | What It Measures |
|--------|-----------------|
| **MAU** (Monthly Active Users) | Unique users per month (by hashed machine ID) |
| **MEU** (Monthly Engaged Users) | Users who run engagement commands (provision, deploy, up) |
| **MDU** (Monthly Dev Users) | Users in local dev environments (not CI/CD) |
| **Success Rate** | % of command executions that succeed |
| **Error Rate by Category** | Top error categories (ARM, auth, build, network) |
| **Template Adoption** | Which templates are used, by how many users |
| **Funnel Completion** | % of users completing init → provision → deploy |
| **Retention** | Users returning week-over-week / month-over-month |
| **New Users** | First-time users per time period |
| **Provision/Deploy Duration** | P50/P90 operation time by template |

> Microsoft-internal dashboards and reporting tools are documented separately for internal maintainers.

## How to Request New Telemetry

### For a New Feature

Work with the feature engineer during development:

1. **During design** — Discuss what questions you want to answer about the feature
2. **During implementation** — Engineer instruments telemetry following the [Feature Telemetry Guide](feature-telemetry.md)
3. **During PR review** — Review the telemetry fields to ensure they answer your product questions
4. **After launch** — Verify data is flowing and dashboards are updated

### For Additional Metrics on an Existing Feature

1. File an issue describing:
   - What question you want to answer
   - What data you think is needed
   - Which feature/commands it relates to
2. Engineering evaluates whether:
   - The data already exists and just needs a query/dashboard
   - New instrumentation is required (code change)

## Privacy and Compliance

- Telemetry contains no direct PII — sensitive identifiers are one-way hashed or classified
- Identifiers (machine ID, project name, etc.) are one-way hashed
- Users can opt out at any time
- All telemetry fields are classified for privacy compliance
- Data retention follows Microsoft standard telemetry retention policies

## Further Reading

| Document | What It Covers | Audience |
|----------|---------------|----------|
| [Architecture](../architecture/telemetry.md) | End-to-end system architecture with diagrams | Engineering |
| [Data Reference](../reference/telemetry-data.md) | Complete schema, all events and fields | Engineering, Product |
| [Feature Telemetry Guide](feature-telemetry.md) | How to add telemetry to new features | Engineering |
