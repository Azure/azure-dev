# azd Telemetry — Product Overview

> What azd telemetry tells us, where to find it, and how to work with it.
>


## What azd Telemetry Captures

Azure Developer CLI (azd) collects anonymous usage telemetry to understand how developers use the tool, measure feature adoption, and diagnose issues at scale. Users can opt out at any time.

### What We Collect

| Category | Examples |
|----------|---------|
| **Commands** | Which commands are run (`init`, `deploy`, `provision`, `up`), success/failure, duration |
| **Features** | Feature-specific properties (template used, packaging format, auth method, target Azure services) |
| **Errors** | Error codes and categories (ARM errors, auth failures, build failures), **not** user content |
| **Environment** | OS, azd version, execution environment (GitHub Actions, Azure Pipelines, VS Code, etc.) |
| **Extensions** | Which extensions are installed and invoked, extension errors |
| **Performance** | Operation duration, time spent waiting for user input vs. executing |

### What We Don't Collect

- No source code or project file contents
- No Azure credentials, tokens, or connection strings
- No personal names, emails, or IP addresses
- Project names and template names are **hashed** (one-way) — we can count unique projects but can't see what they're called
- Users opt out via `azd config set defaults.collectTelemetry no` or `AZURE_DEV_COLLECT_TELEMETRY=no`

## Key Metrics

| Metric | What It Measures | Where to Find It |
|--------|-----------------|------------------|
| **MAU** (Monthly Active Users) | Unique users per month (by hashed machine ID) | KPIs dashboard |
| **MEU** (Monthly Engaged Users) | Users who run engagement commands (provision, deploy, up) | KPIs dashboard |
| **MDU** (Monthly Dev Users) | Users in local dev environments (not CI/CD) | KPIs dashboard |
| **Success Rate** | % of command executions that succeed | KPIs dashboard, per-command |
| **Error Rate by Category** | Top error categories (ARM, auth, build, network) | Template Health dashboard |
| **Template Adoption** | Which templates are used, by how many users | Template KPIs dashboard |
| **Funnel Completion** | % of users completing init → provision → deploy | User Journeys dashboard |
| **Retention** | Users returning week-over-week / month-over-month | KPIs dashboard |
| **New Users** | First-time users per time period | KPIs dashboard |
| **Provision/Deploy Duration** | P50/P90 operation time by template | Deploy and Provision dashboard |

## Where to Find Dashboards

| Dashboard | Link | What It Shows |
|-----------|------|---------------|
| **Main Dashboard** | [aka.ms/azd/dashboard](https://aka.ms/azd/dashboard) | Primary Power BI report with KPIs, template health, user journeys |
| **Dashboard Collection** | [aka.ms/azd-dashboards](https://aka.ms/azd-dashboards) | All azd-related dashboards |

### Dashboard Areas

| Area | What You'll Find |
|------|-----------------|
| **KPIs** | MAU/MEU/MDU trends, success rates, new users |
| **Template KPIs** | Per-template adoption, success, performance |
| **Template Health** | Error rates, failure patterns, top issues per template |
| **Deploy and Provision** | Operation analysis: durations, errors, Azure services used |
| **User Journeys** | Workflow funnels (init → provision → deploy) |
| **Customer Exploration** | Customer-specific usage exploration |
| **AI Foundry** | AI Foundry template metrics |
| **MCP Tools** | Model Context Protocol tool usage |

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
   - New Kusto functions or reports are needed

### Who to Contact

The telemetry pipeline spans multiple layers. Depending on what you need:

| Need | Who Can Help |
|------|-------------|
| New code instrumentation | Feature engineer + telemetry reviewer |
| New KQL queries or Kusto functions | PM + Feature Engineer |
| New/updated Power BI reports | PM |
| GDPR/privacy review | Automatic via pipeline; manual review only for `CustomerContent` or unhashed PII classifications |

## Privacy and Compliance

- All telemetry is anonymous — no PII is collected
- Identifiers (machine ID, project name, etc.) are one-way hashed
- Users can opt out at any time
- GDPR classification pipeline automatically processes all telemetry fields
- Data retention follows Microsoft standard telemetry retention policies

## Further Reading

| Document | What It Covers | Audience |
|----------|---------------|----------|
| [Architecture](../architecture/telemetry.md) | End-to-end system architecture with diagrams | Engineering, Product |
| [Data Reference](../reference/telemetry-data.md) | Complete schema, all events and fields, KQL examples | Engineering, Product |
| [Feature Telemetry Guide](feature-telemetry.md) | How to add telemetry to new features | Engineering |
| [Dashboards & Reports](../reference/telemetry-dashboards.md) | Power BI reports, Kusto functions, analysis tools | Engineering, Product |
