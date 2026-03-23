# azd Telemetry: Improvement Opportunities Report

**Period:** Rolling 28 days (Feb 21 – Mar 18, 2026)  
**Generated:** 2026-03-22 (updated 2026-03-23)  
**Source:** Telemetry report + GitHub issue/PR cross-reference

---

## Executive Summary

azd processes **10.2M executions/28d** with a **64.5% overall success rate**. However, this headline number is misleading — `auth token` alone accounts for **3.38M of 3.61M failures (94%)**. Excluding auth token, the real command failure rate is closer to **~6%**.

The three largest improvement opportunities are:

| Opportunity | Volume Impact | Status |
|---|---|---|
| 1. **UnknownError bucket** | 2.35M errors with zero classification | 🟢 PR #7241 (in review) |
| 2. **Auth error misclassification** | 610K errors in wrong category | 🟢 PR #7235 (all CI green) |
| 3. **`azd up` at 31% success** | 49K failures, 11K users | 🟢 PR #7236 (auth pre-flight, all CI green) |

---

## Opportunity 1: UnknownError — The Biggest Blind Spot

### Scale
- **2,352,993 errors/28d** classified as `UnknownError`
- **6,403 users** affected
- **69.5%** of all `auth token` failures
- Currently provides **zero diagnostic signal**

### Root Cause (confirmed)
`EndWithStatus(err)` in several high-volume code paths bypasses `MapError()` — the central error classification function. The telemetry converter then falls back to `"UnknownFailure"` when the span status description is empty or unrecognized.

### Fix: PR #7241 (in review)
- MCP tool handler and Copilot agent spans now route through `MapError()`
- `EndWithStatus` fallback prefixed with `internal.` for consistency

### Existing Work
| Issue/PR | Status | Coverage |
|---|---|---|
| **#7239** / PR #7241 | 🟢 PR open, CI green | Routes spans through MapError |
| #6796 "Reduce Unknown error bucket" | ✅ Closed (partial fix) | Addressed some categories |
| #6576 "Follow up on unknown errors classification" | ✅ Closed | Follow-up from #6796 |
| #5743 "Better unknown error breakdowns" | ✅ Closed | Added `errorType()` introspection |

---

## Opportunity 2: Auth Error Classification ✅ IN PROGRESS

### Scale
- **610K errors/28d** miscategorized as `"unknown"` instead of `"aad"`
- **~17%** of all auth token errors

### Fix: PR #7235 (all CI green, awaiting review)
| Error | Before | After |
|---|---|---|
| `auth.login_required` (544K) | unknown ❌ | aad ✅ |
| `auth.not_logged_in` (66K) | unknown ❌ | aad ✅ |
| `azidentity.AuthenticationFailedError` (8.7K) | unknown ❌ | aad ✅ (new code: `auth.identity_failed`) |

### Related Issues
| Issue | Status | Relationship |
|---|---|---|
| **#7233** | 🟢 PR #7235 open | Direct fix |
| #7104 "Revisit UserContextError categories" | 🟡 Open | Downstream — better error buckets for agents |

---

## Opportunity 3: `azd up` Reliability (31% Success Rate)

### Scale
- **49,154 failures/28d**, **11,110 users**
- The flagship compound command (provision + deploy)
- Failure cascades: a provision failure = `up` failure even if deploy would succeed

### Error Breakdown for `azd up`
| Error Category | Count | Users | Preventable? |
|---|---|---|---|
| `error.suggestion` | 7,036 | 1,928 | ⚠️ Partially — many are auth errors wrapped in suggestions |
| `arm.deployment.failed` | 4,725 | 1,466 | ❌ Infrastructure errors (quota, policy, region) |
| `arm.400` | 2,540 | 940 | ⚠️ Some are preventable with validation |
| `Docker.missing` | 1,028 | 540 | ✅ **100% preventable** with pre-flight check |

### Existing Work
| Issue/PR | Status | Coverage |
|---|---|---|
| **#7234** / PR #7236 | 🟢 PR open, CI green | Auth pre-flight + `--check` flag for agents |
| **#7240** Docker pre-flight | 🟡 Open (assigned spboyer) | Suggest remoteBuild when Docker missing |
| **#7179** Preflight: Azure Policy blocking local auth | 🟢 PR open (vhvb1989) | Detects policy-blocked deployments early |
| **#7115** Never abort deployment on validation errors | 🟡 Open | More resilient deploy behavior |
| **#3533** Verify dependency tools per service | 🟡 Open (good first issue) | Check only tools needed, not all |

### Recommended Next Steps
1. **Ship #7234** (auth pre-flight middleware) — prevents late auth failures
2. **Ship #7240** (Docker pre-flight) — catch Docker/tool missing before starting
3. **Decompose `up` failure reporting** — show provision vs deploy failures separately so users know what succeeded
4. **Add ARM quota pre-flight** — check quota before deploying (prevents the most common ARM failures)

---

## Opportunity 4: `internal.errors_errorString` — Untyped Errors

### Scale
- **462,344 errors/28d**, **16,978 users**
- These are bare `errors.New()` calls without typed sentinels
- Each one is a classification opportunity lost

### What's Happening
Code paths that return `errors.New("something failed")` or `fmt.Errorf("failed: %w", err)` where the inner error is also untyped end up in the catch-all bucket with the opaque name `errors_errorString`.

### Recommended Next Steps
1. **Audit hot paths** — Find the top `errors.New()` call sites in auth token, provision, and deploy code paths
2. **Add typed sentinels progressively** — Start with the highest-volume error messages
3. **Test enforcement** — The test suite already has `allowedCatchAll` enforcement (errors_test.go line 791). Expand this pattern to prevent regressions.

---

## Opportunity 5: AI Agent Optimization

### Scale
- **420K executions/28d** from AI agents (~4% of total, growing fast)
- Claude Code: **367K executions**, 699 users (exploding growth)
- Copilot CLI: **50K executions**, 579 users (using MCP tools)

### Key Pain Points
| Issue | Impact | Status |
|---|---|---|
| `auth token` failure rate: 61% (Copilot CLI), 41% (Claude Code) | Agents retry in loops, wasting cycles | 🟢 PR #7236 (`--check` flag) |
| No machine-readable auth status | Agents parse error messages | 🟢 PR #7236 (`expiresOn` in JSON) |
| Agent init errors lack remediation hints | Adoption barrier | 🟡 #6820 open |
| Extension compatibility issues | `env refresh` broken with agents ext | 🟡 #7195 open |

### Existing Work
| Issue/PR | Status | Coverage |
|---|---|---|
| **#7234** / PR #7236 | 🟢 PR open, CI green | `azd auth token --check` + `expiresOn` in status |
| **#7202** Evaluation and testing framework | 🟢 PR open (spboyer) | Systematic testing for agent flows |
| **#6820** Agent init error remediation hints | 🟡 Open | Better error UX for agent setup |
| **#7195** `env refresh` + agent extension | 🟡 Open | Compatibility fix |
| **#7156** Hosted agent toolsets | 🟡 Open | Azure AI Foundry integration |

---

## Opportunity 6: ARM Deployment Resilience

### Scale
- **22,033 `arm.deployment.failed`/28d**, 4,025 users
- **6,426 `arm.validate.failed`/28d**, 1,220 users
- **6,371 `arm.400`/28d**, 1,479 users

### Existing Work
| Issue/PR | Status | Coverage |
|---|---|---|
| **#6793** Investigate 500/503 ARM errors | 🟡 Open | ~707 preventable transient failures |
| **#7179** Preflight: Azure Policy detection | 🟢 PR open | Catches policy blocks before deploy |

### Recommended Next Steps
1. **Add retry logic for transient 500/503** (#6793) — ~707 preventable failures
2. **Pre-flight quota check** — many ARM 400s are quota exceeded
3. **Surface inner deployment error codes** in user-facing messages

---

## Opportunity 7: Tool Installation UX

### Scale
- **Docker.missing**: 2,936 failures, 784 users
- **tool.docker.failed**: 4,655 failures, 1,024 users
- **tool.dotnet.failed**: 3,732 failures, 1,345 users
- **tool.bicep.failed**: 5,099 failures, 1,437 users

### Existing Work
| Issue/PR | Status | Coverage |
|---|---|---|
| **#7240** Docker pre-flight + remoteBuild suggestion | 🟡 Open (assigned spboyer) | Detect upfront, suggest alternative |
| **#5715** Docker missing → suggest remoteBuild | 🟡 Open | Original request (superseded by #7240) |
| **#3533** Per-service tool validation | 🟡 Open (good first issue) | Only check needed tools |
| **#6424** Pre-tool-validation lifecycle event | 🟡 Open | Extension hook for guidance |

---

## Deeper Questions To Investigate

These are questions the current data could answer but the report doesn't address:

### Metric Integrity
1. **Report two success rates** — Overall (64.5%) AND excluding auth token (~94%). The current headline is misleading; `auth token` is 80% of all executions and dominates every metric.

### `azd up` Decomposition
2. **Stage-level failure attribution** — Where in the `up` flow does it fail? 70% at provision, 20% at deploy, 10% at both? Without this, we can't prioritize which stage to fix first.

### Hidden Problem Commands
3. **`azd restore` at 60% success** — 1,993 failures, 693 users. A restore operation failing 40% of the time is a red flag nobody's investigating.
4. **`env get-value` at 84.3%** — A read-only key lookup failing 15.7% for 10,748 users. Likely missing env/key/project — but should be surfaced as a UX problem, not swallowed.
5. **`auth login` at 88.3%** — 24,476 login failures. Login should approach 100%. What's actually failing?

### Template & Customer Analysis
6. **Template × command success matrix** — `azure-search-openai-demo` has 259K executions. If that template has a high failure rate, it skews numbers for 2,542 users. Need per-template success rates.
7. **Strategic account success variance** — EY at 71.6% vs Allianz at 99.8%. Same tool, 28-point gap. Understanding WHY (template? region? subscription policies?) enables targeted customer success.

### Error Quality
8. **What's inside `error.suggestion`?** — 33,762 errors classified as `error.suggestion` but the inner error varies wildly. The `classifySuggestionType` inner code exists in telemetry — should be surfaced alongside the wrapper code.

### User Journey & Retention
9. **End-to-end funnel** — `init` (91%) → `provision` (58.8%) → `deploy` (80.8%). What's the **cohort completion rate**? If a user inits, what % ever successfully deploy?
10. **First-run vs repeat failure rate** — Are tool failures (Docker, Bicep, dotnet) concentrated on first-run users or recurring? First-run = onboarding UX fix. Recurring = bug.
11. **Failure → churn correlation** — Engaged (2+ days) is only 4,072/18,811 MAU (21.6%). Are users who hit `azd up` failures coming back? If not, the 31% success rate is also a **retention problem**.

### AI Agent Journey
12. **Agent → deployment conversion** — Agents call `auth token` heavily but how many get to `provision`/`deploy`? What's the agent user journey funnel vs desktop users?

---

## Priority Matrix (Updated)

| Priority | Opportunity | Volume | Effort | Status |
|---|---|---|---|---|
| 🟢 **In Review** | UnknownError classification | 2.35M | Medium | PR #7241 |
| 🟢 **In Review** | Auth error categories | 610K | Small | PR #7235 |
| 🟢 **In Review** | Auth pre-flight + agent `--check` | 610K cascade | Medium | PR #7236 |
| 🟡 **Filed** | Docker pre-flight | 2.9K + 4.7K | Small | #7240 |
| 🟡 **P1** | `azd up` decomposition | 49K | Medium | Needs Kusto query |
| 🟡 **P2** | `errors_errorString` audit | 462K | Large (ongoing) | No active issue |
| 🟡 **P2** | Template success matrix | Unknown | Small (query) | Needs Kusto query |
| 🟡 **P2** | `restore` / `env get-value` investigation | 4K | Small | No active issue |
| 🟡 **P2** | ARM resilience | 35K | Medium | #6793, #7179 |
| 🟢 **P3** | AI agent journey analysis | 420K | Small (query) | Needs Kusto query |

---

*Cross-referenced against Azure/azure-dev open issues and PRs as of 2026-03-23.*
