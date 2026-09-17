# Agent operation statistics

<!-- cspell:ignore tostring leftouter isnull strcat tobool countif todouble -->

Reuse existing command completion records for totals, successes and failures.
The only additional data is bounded text in the existing `extension.event` field
of `ext.usage`: `agent.operation.v1.<operation>.<category>.<telephony>`.
There are no new attributes, core spans, result codes, exporters or pipelines.
Existing `agent.context.resolved` fields and deduplication are unchanged.

| Segment | Fixed values |
|---|---|
| operation | `init`, `provision`, `deploy` |
| category | `hosted`, `hosted_invocations_ws`, `prompt`, `workflow`, `voice_managed`, `voice_byom`, `voice_hosted_wrapper`, `unknown` |
| telephony | `none`, `enabled`, `unknown` |

Voice aliases normalize to the same category. BYOM is the configured model mode,
not a guess from a customer model/deployment name. `invocations_ws` denotes a
transport, not proof of audio usage. Telephony means configured bindings, not
successful binding creation or phone calls. No names, paths, model names,
identifiers, credentials, prompts or configuration payloads are emitted.

## Coverage and behavior

- Extension init collects explicit kind intent, refining it at existing selection,
  definition/adoption and reuse points. RunE return attempts to report the last
  classification on success **or failure**. Unknown intent is not inferred from
  later success. Failure before RunE, process termination or unavailable telemetry
  can have no marker. Success/failure remain owned by existing command telemetry.
- Agent preprovision/predeploy handlers report in-memory service classifications
  before their existing work. Failures before the hooks (such as package or config
  errors) may lack markers. Skipped services that never enter the hook are absent.
- No additional project/file/Azure queries are made for classification. Unresolved
  root `$ref` or an external definition override is unknown; no speculative reads.
- Distinct operation/category/telephony tuples are attempted once per reporter
  process. Provision cannot suppress deploy, and init refinements do not double
  count earlier intent. The original reporter's behavior is unchanged.
- Uses the existing best-effort reporter, no retries, one-second total deadline
  per batch. Init preserves trace metadata with a bounded uncancelled reporting
  context on return; telemetry failures never replace command errors/return values.
  There is bounded latency, not zero overhead. No global background worker is added.
- **Core `azd init` is not the extension command.** Its totals/results already
  exist, but its agent type remains unknown in this extension-only change. Reuse
  inside `azd ai agent init` can be classified from its existing project read.

## Counting contract

Count completed command spans, **not marker rows or agents**. For mixed projects,
keep a sorted category combination and one command count; never assign one
project failure as each service's individual outcome. A hosted voice project may
contain both `hosted_invocations_ws` and `voice_hosted_wrapper`.

The left join below preserves failures without markers. Unclassified core commands
include both non-agent projects and early-failed agent projects: they cannot be
claimed as agent-only usage. Same-operation commands in a shared trace are marked
ambiguous instead of guessing which service/marker belongs to which command.
Do not add parent `up` and child operations together into one total.

Telemetry opt-out, nonofficial ZIP/dev installs, old hosts, the shared 100-event
invocation budget, crashes and ingestion delays can lose markers/completions.
The additional vocabulary is bounded to 72 possible tuples across three operations.
These are counts of **observed completions**, not an absolute census of users.
Report unknown/ambiguous coverage alongside classified rates, and use a fixed
host/extension release cohort. Installation alone does not prove agent involvement.

## KQL — existing Application Insights requests shape

No shared ingestion function needs modification. Validate this query against the
authorized destination and adapt table/column aliases for cooked Kusto/LENS data.
It has not been run against production customer data in this change.
The [synthetic fixture](operation-telemetry-fixture.kql) can be run without customer
tables: it expects four completions, two successes, two failures and one unclassified
failure, even though one mixed deploy has duplicate/multiple marker rows. This is
a query-engine acceptance fixture, not a claim of local Kusto execution.

```kusto
let since = ago(7d);
let Markers = requests
| where timestamp >= since - 1d
| where name == "ext.usage"
| where tostring(customDimensions["extension.id"]) == "azure.ai.agents"
| extend marker = tostring(customDimensions["extension.event"])
| parse marker with "agent.operation.v1." operation "." category "." telephony
| where operation in ("init", "provision", "deploy")
| where category in ("hosted", "hosted_invocations_ws", "prompt", "workflow",
                     "voice_managed", "voice_byom", "voice_hosted_wrapper", "unknown")
| where telephony in ("none", "enabled", "unknown")
| summarize categories=make_set(category), phones=make_set(telephony)
    by operation_Id, operation;
let Completed = requests
| where timestamp >= since
| extend command = tostring(customDimensions["cmd.entry"])
| where (name == "ext.run" and command == "cmd.ai.agent.init")
    or name in ("cmd.init", "cmd.provision", "cmd.deploy")
| summarize arg_max(timestamp, *) by operation_Id, id
| extend operation = case(name == "ext.run", "init", name == "cmd.init", "init",
                          name == "cmd.provision", "provision", "deploy")
| extend scope = iff(name == "ext.run", "agent_extension", "core");
let Cardinality = Completed
| summarize commandSpans=count() by operation_Id, operation, scope;
Completed
| join kind=leftouter Cardinality on operation_Id, operation, scope
| join kind=leftouter Markers on operation_Id, operation
| extend attribution = case(name == "cmd.init", "core_init_unclassified",
                            commandSpans != 1, "ambiguous_trace",
                            isnull(categories), "no_marker", "classified")
| extend agentTypes = iff(attribution == "classified",
                          strcat_array(array_sort_asc(categories), "+"), "unknown")
| extend phoneConfiguration = iff(attribution == "classified",
                                  strcat_array(array_sort_asc(phones), "+"), "unknown")
| extend succeeded = tobool(success), code = tostring(resultCode)
| summarize total=count(), successes=countif(succeeded == true),
            failures=countif(succeeded == false), missingResult=countif(isnull(succeeded)),
            cancellations=countif(code startswith "user.canceled"
              or code in ("internal.operation_cancelled", "internal.operation_aborted"))
    by scope, operation, attribution, agentTypes, phoneConfiguration
| extend successRate = iff(successes + failures > 0, todouble(successes)/(successes+failures), real(null)),
         failureRate = iff(successes + failures > 0, todouble(failures)/(successes+failures), real(null))
```

`total = successes + failures + missingResult`. Cancellations are a diagnostic
subset, not an extra addition to total. The host's existing success/resultCode
semantics are preserved (a graceful nil-error cancellation can remain success).
`ext.usage.success` is never used to determine business outcome. A marker without
a completion is not proof of failure. Unknown types before classification remain
unknown; these rates must not be advertised as complete per-type population rates.

## Validation and rollout

Fake-host tests check actual submitted event names, nil attribute maps,
per-operation deduplication, concurrent calls, type privacy and unchanged init
failure behavior. Existing telemetry tests continue to assert the original contract.
Test official-registry ingestion and trace joins before using a production KPI;
local ZIP sources remain deliberately rejected by host admission. These additional
event values and their product metadata need normal extension telemetry review;
using existing fields does not bypass privacy rules. No customer telemetry was
accessed or uploaded by the unit tests.
