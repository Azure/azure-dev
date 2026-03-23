# Azure Developer CLI (azd) — Rolling 28-Day Telemetry Report

**Period:** Feb 21 – Mar 18, 2026
**Source:** `ddazureclients.kusto.windows.net` / `DevCli` database
**Generated:** 2026-03-21

---

## Executive Summary

| Metric | Value |
|--------|-------|
| **Total Executions** | 10,178,008 |
| **Unique Users** (DevDeviceId) | 129,003 |
| **Overall Success Rate** | 64.5% |
| **Unique Templates Used** | 2,533 |
| **Unique Azure Subscriptions** | 9,183 |
| **Unique Customer Orgs** | 3,857+ |

### Monthly KPI Comparison (from AzdKPIs table)

| KPI | Jan 2026 | Feb 2026 | Δ |
|-----|----------|----------|---|
| MAU (Active1d) | 16,636 | 18,811 | **+13.1%** |
| Engaged (2d) | 3,950 | 4,072 | +3.1% |
| Dedicated (10d) | 369 | 364 | -1.4% |
| Provisions | 59,152 | 63,881 | +8.0% |
| Deployments | 54,958 | 56,728 | +3.2% |
| Successful Provisions | 28,835 | 30,742 | +6.6% |
| Successful Deployments | 41,058 | 43,186 | +5.2% |
| Azure Subscriptions | 6,420 | 7,339 | **+14.3%** |

---

## Daily Active Users Trend

| Date | DAU | Executions |
|------|-----|-----------|
| Feb 21 (Fri) | 504 | 26,306 |
| Feb 22 (Sat) | 3,131 | 142,943 |
| Feb 23 (Sun) | 9,505 | 434,029 |
| Feb 24 (Mon) | 11,499 | 575,189 |
| Feb 25 (Tue) | 11,570 | 622,173 |
| Feb 26 (Wed) | 10,742 | 481,576 |
| Feb 27 (Thu) | 9,665 | 397,341 |
| Feb 28 (Fri) | 3,566 | 223,880 |
| Mar 1 (Sat) | 3,244 | 254,988 |
| Mar 2 (Sun) | 10,093 | 491,753 |
| Mar 3 (Mon) | 10,522 | 458,583 |
| Mar 4 (Tue) | 10,458 | 580,816 |
| Mar 5 (Wed) | 10,359 | 518,339 |
| Mar 6 (Thu) | 9,489 | 500,349 |
| Mar 7 (Fri) | 3,506 | 247,362 |
| Mar 8 (Sat) | 3,267 | 168,655 |
| Mar 9 (Sun) | 10,356 | 563,312 |
| Mar 10 (Mon) | 11,275 | 519,414 |
| Mar 11 (Tue) | 11,004 | 382,957 |
| Mar 12 (Wed) | 10,802 | 425,650 |
| Mar 13 (Thu) | 10,106 | 311,655 |
| Mar 14 (Fri) | 3,586 | 348,198 |
| Mar 15 (Sat) | 3,397 | 162,191 |
| Mar 16 (Sun) | 10,616 | 333,201 |
| Mar 17 (Mon) | 11,241 | 558,356 |
| Mar 18 (Tue) | 11,699 | 448,755 |

> Weekday DAU averages ~10,500–11,500 users. Weekend dips to ~3,200–3,600. Pattern is healthy and consistent.

---

## Top Commands (by executions)

| Command | Executions | Unique Users | Success % |
|---------|-----------|-------------|-----------|
| `azd auth token` | 8,237,762 | 20,692 | 58.9% |
| `azd env set` | 428,612 | 33,758 | 99.5% |
| `azd auth login` | 208,951 | 72,188 | 88.3% |
| `azd env get-values` | 190,212 | 24,792 | 98.9% |
| `azd env get-value` | 173,396 | 10,748 | 84.3% |
| `azd env list` | 153,117 | 18,883 | 99.1% |
| **`azd provision`** | **127,640** | **40,469** | **58.8%** |
| **`azd deploy`** | **115,974** | **43,735** | **80.8%** |
| `azd package` | 76,273 | 20,476 | 92.8% |
| **`azd up`** | **71,276** | **11,110** | **31.0%** ⚠️ |
| `azd config set` | 62,626 | 40,166 | 100% |
| `azd env new` | 47,364 | 29,470 | 93.5% |
| `azd init` | 46,133 | 6,251 | 91.0% |
| `azd env select` | 39,631 | 26,229 | 84.2% |

---

## Top Templates (by unique users)

| Template | Users | Executions |
|----------|-------|-----------|
| **azd-init** (interactive init) | 8,427 | 81,078 |
| **azure-search-openai-demo** | 2,542 | 259,785 |
| **chat-with-your-data-solution-accelerator** | 625 | 5,707 |
| **multi-agent-custom-automation-engine** | 550 | 19,742 |
| **todo-nodejs-mongo** | 469 | 1,925 |
| **agentic-applications-for-unified-data-foundation** | 461 | 12,653 |

---

## Execution Environments — IDEs & AI Agents

### All Execution Environments

| Environment | Executions | Unique Users | User Share |
|-------------|-----------|-------------|------------|
| **Desktop** (terminal) | 8,699,096 | 39,274 | 30.4% |
| **GitHub Actions** | 421,968 | 37,759 | 29.2% |
| **Azure Pipelines** | 358,116 | 32,543 | 25.2% |
| **VS Code** (extension) | 93,024 | 16,006 | 12.4% |
| **Visual Studio** | 76,789 | 4,886 | 3.8% |
| **Azure CloudShell** | 19,456 | 3,686 | 2.9% |
| **Claude Code** 🔥 | 367,193 | 699 | 0.5% |
| **GitHub Codespaces** | 36,097 | 1,066 | 0.8% |
| **GitLab CI** | 36,519 | 1,142 | 0.9% |
| **GitHub Copilot CLI** | 50,115 | 579 | 0.4% |
| **OpenCode** | 3,204 | 38 | — |
| **Gemini** | 102 | 12 | — |

### 🤖 AI/LLM Agent Deep Dive

#### Weekly Trend

| Week | Claude Code Users | Claude Code Exec | Copilot CLI Users | Copilot CLI Exec | OpenCode Users | Gemini Users |
|------|------------------|-----------------|-------------------|-----------------|---------------|-------------|
| Feb 16 | 20 | 1,213 | 9 | 177 | 3 | — |
| Feb 23 | 202 | 28,918 | 185 | 12,362 | 17 | 6 |
| Mar 2 | 263 | 133,593 | 228 | 18,716 | 11 | 2 |
| Mar 9 | **263** | **159,470** | 218 | 11,015 | 14 | 2 |
| Mar 16 | 218 | 43,999 | 173 | 7,845 | 12 | 2 |

#### Success Rates Over Time

| Week | Claude Code | Copilot CLI | OpenCode | Gemini |
|------|------------|------------|----------|--------|
| Feb 16 | 23.1% | 79.1% | 75.0% | — |
| Feb 23 | 31.2% | 74.4% | 71.2% | 62.0% |
| Mar 2 | 46.4% | 48.5% | 24.5% | 69.2% |
| Mar 9 | **87.9%** | 69.5% | 66.0% | 80.0% |
| Mar 16 | 82.5% | 63.1% | 74.8% | 20.0% |

#### Top Commands by AI Agent

**Claude Code** (367K executions, 699 users):

| Command | Executions | Users |
|---------|-----------|-------|
| `auth token` | 284,257 | 375 |
| `env list` | 63,530 | 171 |
| `env get-values` | 5,804 | 217 |
| `env set` | 3,859 | 186 |
| `deploy` | 2,920 | 192 |
| `provision` | 1,198 | 170 |
| `up` | 709 | 125 |

**GitHub Copilot CLI** (50K executions, 579 users):

| Command | Executions | Users |
|---------|-----------|-------|
| `auth token` | 18,303 | 319 |
| `env get-value` | 6,747 | 69 |
| `env set` | 4,140 | 203 |
| `env get-values` | 3,766 | 214 |
| `deploy` | 3,043 | 187 |
| `provision` | 2,957 | 208 |
| `up` | 1,903 | 172 |

> Notable: Copilot CLI is also running **MCP operations** (`mcp.validate_azure_yaml`, `mcp.iac_generation_rules`, `mcp.infrastructure_generation`, etc.) — 70 total MCP calls from 19+ users.

#### AI Agent Key Insights

- 🔥 **Claude Code is exploding** — went from 20 users to 263 in 4 weeks, with 367K executions (more than Copilot CLI despite fewer users). Heavy `auth token` loop suggests agentic workflow patterns.
- **Claude Code success rate improved dramatically** — from 23% to 88% in 4 weeks, suggesting rapid integration maturation.
- **Copilot CLI** is steady at ~200 users/week with 50K executions. Already using azd's MCP tools.
- **AI agents collectively**: ~1,300 users, 420K executions — about **4% of total azd volume** and growing fast.

---

## Customer Breakdown

### By Segment

| Segment | Customers | Users | Subscriptions | Executions |
|---------|-----------|-------|---------------|------------|
| **SMB Commercial** | 1,852 | 26,637 | 2,732 | 473K |
| **Unspecified** | 993 | 21,622 | 2,466 | 445K |
| **Upper Majors Commercial** | 336 | 11,325 | 588 | 83K |
| **Strategic Commercial** | 188 | 5,054 | 564 | 113K |
| **Corporate Commercial** | 234 | 3,370 | 822 | 41K |
| **Upper Majors Public Sector** | 118 | 2,434 | 175 | 19K |
| Majors Growth Commercial | 34 | 418 | 44 | 5K |
| Strategic Public Sector | 29 | 264 | 49 | 5K |

### Top Customers (by unique users)

| Customer | Segment | Country | Users | Subs | Executions | Success% |
|----------|---------|---------|-------|------|------------|----------|
| **CoreAI - Platform & Tools** | Internal | 🇺🇸 US | 9,246 | 35 | 73K | 86.9% |
| **Investec Bank** | Upper Majors | 🇬🇧 UK | 6,471 | 74 | 15K | 90.3% |
| **Microsoft** | Internal | 🇺🇸 US | 3,976 | 978 | 149K | 82.8% |
| **Cloud + AI** | Internal | 🇨🇳 CN | 3,066 | 183 | 107K | 96.3% |
| **Vee Friends** | SMB | 🇺🇸 US | 2,585 | 1 | 8K | 99.0% |
| **Grupo Cosan** | Upper Majors | 🇧🇷 BR | 1,667 | 1 | 5K | 100% |
| **Puerto Rico PRITS** | Public Sector | 🇵🇷 PR | 1,422 | 2 | 4K | 94.9% |
| **H&M** | Strategic | 🇸🇪 SE | 1,107 | 40 | 8K | 91.7% |
| **Jersey Telecoms** | Corporate | 🇬🇧 UK | 951 | 2 | 7K | 99.6% |
| **Volkswagen** | Strategic | 🇩🇪 DE | 664 | 17 | 39K | 98.6% |
| **Microsoft Security** | Internal | 🇺🇸 US | 640 | 15 | 14K | 95.7% |
| **IBM** | Strategic | 🇺🇸 US | 385 | 7 | 4K | 83.2% |
| **Deloitte** | Strategic | 🇺🇸 US | 329 | 10 | 1K | 96.3% |
| **bp** | Strategic | 🇬🇧 UK | 271 | 9 | 6K | 97.9% |
| **Allianz Technology** | Strategic | 🇩🇪 DE | 233 | 8 | 11K | 99.8% |
| **Rabobank** | Strategic | 🇳🇱 NL | 248 | 8 | 462 | 92.2% |
| **EY Global** | Strategic | 🇺🇸 US | 201 | 25 | 2K | 71.6% |
| **Mercedes-Benz** | Strategic | 🇩🇪 DE | 195 | 6 | 3K | 99.0% |

---

## Error Analysis

### Overall Failure Rate Trend

| Week | Total Exec | Failures | Fail Rate |
|------|-----------|----------|-----------|
| Feb 16 | 168K | 87K | 52.0% |
| Feb 23 | 2,989K | 1,050K | 35.1% |
| Mar 2 | 2,966K | 1,015K | 34.2% |
| Mar 9 | 2,713K | 948K | 35.0% |
| Mar 16 | 1,340K | 513K | 38.3% |

### Success Rates by Command

| Command | Total | Failures | Success% | Users |
|---------|-------|----------|----------|-------|
| `auth token` | 8,236,682 | 3,384,478 | **58.9%** | 20,689 |
| `auth login` | 208,930 | 24,476 | 88.3% | 72,184 |
| `provision` | 127,630 | 52,639 | **58.8%** | 40,466 |
| `deploy` | 115,961 | 22,243 | 80.8% | 43,735 |
| `package` | 76,268 | 5,520 | 92.8% | 20,476 |
| **`up`** | **71,271** | **49,154** | **31.0%** ⚠️ | 11,110 |
| `env new` | 47,355 | 3,098 | 93.5% | 29,463 |
| `init` | 46,132 | 4,171 | 91.0% | 6,251 |
| `down` | 15,830 | 2,761 | 82.6% | 2,982 |
| `restore` | 4,979 | 1,993 | **60.0%** | 693 |

### Top Result Codes (failure reasons)

| Result Code | Count | Users | Category |
|-------------|-------|-------|----------|
| `UnknownError` | 2,383,049 | 8,128 | Uncategorized |
| `auth.login_required` | 547,007 | 1,984 | Auth |
| `internal.errors_errorString` | 462,344 | 16,978 | Internal |
| `auth.not_logged_in` | 66,331 | 1,769 | Auth |
| `error.suggestion` | 33,762 | 7,475 | User guidance |
| `service.arm.deployment.failed` | 22,033 | 4,025 | ARM |
| `service.aad.failed` | 13,619 | 1,276 | AAD |
| `internal.azidentity_AuthenticationFailedError` | 8,732 | 196 | Auth (identity) |
| `service.arm.validate.failed` | 6,426 | 1,220 | ARM |
| `service.arm.400` | 6,371 | 1,479 | ARM |
| `tool.bicep.failed` | 5,099 | 1,437 | Bicep |
| `tool.pwsh.failed` | 4,931 | 688 | PowerShell |
| `tool.docker.failed` | 4,655 | 1,024 | Docker |
| `tool.dotnet.failed` | 3,732 | 1,345 | .NET |
| `user.canceled` | 3,630 | 1,739 | User action |
| `tool.Docker.missing` | 2,936 | 784 | Docker not installed |
| `tool.terraform.failed` | 2,872 | 355 | Terraform |

### Provision / Deploy / Up Error Breakdown

| Command | Error Category | Result Code | Count | Users |
|---------|---------------|-------------|-------|-------|
| `provision` | ARM | arm.deployment.failed | 12,395 | 3,908 |
| `up` | ARM | error.suggestion | 7,036 | 1,928 |
| `up` | ARM | arm.deployment.failed | 4,725 | 1,466 |
| `provision` | ARM | arm.validate.failed | 3,393 | 1,208 |
| `provision` | ARM | arm.400 | 3,130 | 1,143 |
| `provision` | Bicep | bicep.failed | 2,715 | 1,311 |
| `up` | ARM | arm.400 | 2,540 | 940 |
| `provision` | PowerShell | pwsh.failed | 2,053 | 560 |
| `provision` | Terraform | terraform.failed | 1,873 | 335 |
| `deploy` | Docker | docker.failed | 1,652 | 692 |
| `up` | Docker | Docker.missing | 1,028 | 540 |
| `deploy` | .NET | dotnet.failed | 1,304 | 559 |

### 🔍 `cmd.auth.token` Error Deep Dive

`auth token` accounts for **3.38M of 3.61M total failures (94%)**. Breakdown:

| Result Code | Error Category | Count | Users | % of Auth Token Failures |
|-------------|---------------|-------|-------|--------------------------|
| `UnknownError` | unknown | 2,352,993 | 6,403 | **69.5%** |
| `auth.login_required` | unknown | 543,972 | 1,353 | **16.1%** |
| `internal.errors_errorString` | unknown | 398,164 | 5,285 | **11.8%** |
| `auth.not_logged_in` | unknown | 65,643 | 1,233 | 1.9% |
| `service.aad.failed` | aad | 12,246 | 658 | 0.4% |
| `internal.azidentity_AuthenticationFailedError` | unknown | 7,258 | 65 | 0.2% |

> ⚠️ **Classification gap:** 99.6% of auth token failures are categorized as "unknown" even when result codes like `auth.login_required` and `auth.not_logged_in` provide clear signals. Only `service.aad.failed` (12K) gets properly categorized.

#### Auth Token Failure Rate by Execution Environment

| Environment | Total | Failures | Fail % |
|-------------|-------|----------|--------|
| **Desktop** | 7,888,323 | 3,250,684 | **41.2%** |
| **Claude Code** | 284,257 | 116,750 | **41.1%** |
| **GitHub Copilot CLI** | 18,302 | 11,156 | **61.0%** ⚠️ |
| **GitHub Actions** | 12,915 | 383 | 3.0% ✅ |
| **GitHub Codespaces** | 10,580 | 1,417 | 13.4% |
| **Azure Pipelines** | 9,692 | 2,219 | 22.9% |
| **GitLab CI** | 5,147 | 0 | 0% ✅ |
| **OpenCode** | 2,053 | 908 | 44.2% |

> CI/CD environments have near-zero auth token failures (service principal auth). Interactive/agent environments hit ~41-61% failure rates due to token expiry and "not logged in" retries.

---

## Key Takeaways & Recommendations

### 📈 Growth Signals
1. **MAU up 13% MoM**, Azure subscriptions up **14%** — strong growth trajectory
2. **AI agent adoption is hockey-sticking** — Claude Code went from 20→263 users in 4 weeks
3. **3,857+ customer organizations** actively using azd, with marquee enterprise logos (VW, H&M, bp, Mercedes, Allianz, IBM)

### ⚠️ Areas of Concern
1. **`azd up` at 31% success rate** — the flagship command. Compounds provision + deploy failures. 49K failures affecting 11K users.
2. **`auth token` dominates failure volume** — 3.38M failures, but most are expected retry patterns (token refresh). Inflates overall failure rate from ~6% (excluding auth token) to ~35%.
3. **Error classification is broken** — 2.35M errors classified as `UnknownError` with no detail. `auth.login_required` (544K) is categorized as "unknown" despite having a clear result code.

### 🔧 Suggested Actions
1. **Fix error categorization** — Map `auth.login_required`, `auth.not_logged_in`, and `azidentity_AuthenticationFailedError` result codes to an `auth` error category instead of `unknown`.
2. **Investigate `UnknownError`** (2.35M) — Add better error introspection to surface what's actually failing.
3. **Improve `azd up` reliability** — At 31% success, the compound command needs better pre-flight checks, clearer error messages, and possibly staged rollback.
4. **Address Docker-not-installed** (2,936 failures, 784 users) — Better pre-req detection and user guidance before attempting container deployments.
5. **Optimize for AI agents** — Claude Code and Copilot CLI are growing fast. Consider agent-specific auth flows (non-interactive token acquisition) and reducing the `auth token` retry loop noise.
6. **Monitor EY Global** — 71.6% success rate for a Strategic account is below the 90%+ benchmark seen at other enterprise customers.

---

*Report generated from `AzdOperations`, `AzdKPIs`, and `Templates` tables on the `ddazureclients` Kusto cluster.*
