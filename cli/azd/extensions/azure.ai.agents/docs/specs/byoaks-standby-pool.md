# Hosted agent standby pool

## Goal

BYO AKS benchmarks show a substantial startup-performance improvement when a
warm pool is available. The goal is to offer users a performance option that
keeps capacity warm instead of relying only on cold starts.

## Current status

The feature is **still in alpha**. The
[REST API specification proposal](https://github.com/Azure/azure-rest-api-specs/pull/46752)
is not yet merged, and the [azd implementation](https://github.com/Azure/azure-dev/pull/10826)
is a draft. The interface and availability may change before release.

Currently, explicit standby configuration is supported **only with the BYO AKS
preview enabled and an eligible BYO AKS project binding**.

## Interface

Enable the existing opt-in in the shell running azd:

```powershell
$env:AZD_ALPHA_ENABLE_AGENTS_BYO_AKS = "true"
```

Add this to an existing hosted-agent service in `azure.yaml`:

```yaml
agentEndpoint:
  standbyPool:
    size: 3
```

This maps to REST `agent_endpoint.standby_pool.size`, not the agent version
definition. `size` is an integer from **0 to 50**, applied independently to each
serving version, including canaries.

- Omitted, null, or empty pool/size: no pool field is sent. Creation uses the
  server default of 0; updates preserve the existing target.
- Explicit `0`: stop refill, not immediate reclamation of existing warm members.
- Explicit size with the preview disabled or without eligible AKS hosting,
  including ADC: return an error rather than silently ignore the setting.

Normal `azd deploy` applies the configuration. To change only the target without
creating another version, edit `size` and run:

```sh
azd ai agent endpoint update my-agent
azd ai agent endpoint show my-agent
```

Success means **target saved**, not warm-up complete. Only management create/PATCH
writes containing an explicit size use `2025-11-15-preview`; invocation and session
API versions remain unchanged.

## azd user experience by scenario

`my-agent` below is the agent service name in `azure.yaml`. The preview switch
does not set a positive target automatically.

| Case | User action | azd behavior |
| --- | --- | --- |
| First deployment on BYO AKS | Enable the preview, bind the project through `uses`, set `size: 3`, then run `azd deploy my-agent`. | Deploy the version and save the endpoint target. Success does not wait for three warm instances. |
| Existing BYO AKS project | Declare its project `endpoint` and set the matching `AZURE_AI_PROJECT_ID`; enable the preview. | Verify project identity and account AKS hosting through read-only ARM requests before agent writes. No reprovisioning is needed. |
| Change an existing target | Edit `size`, then run `azd ai agent endpoint update my-agent`. | Confirm endpoint/card configuration updated; create no version and preserve undeclared selector, auth, and protocol settings. |
| Stop refill | Set `size: 0`, then run endpoint update. | Save explicit zero. Existing warm members are not immediately drained; normal claims and expiry remain available. |
| Inspect the target | Run `azd ai agent endpoint show my-agent` (add `--output json` for scripts). | Show the saved target when returned by the service, not ready capacity or warm-up progress. JSON uses `agent_endpoint.standby_pool.size`. |
| No standby setting | Omit the pool/size or use null/empty values. | Send no pool change; preserve the target on update. No standby-specific opt-in is required. |
| Preview disabled | Supply an explicit size, including zero, without enabling the preview. | Return `feature_not_enabled` with enablement guidance before authentication or mutations. |
| Unsupported or unverified hosting | Supply an explicit size for ADC, an ambiguous project binding, or an adopted project whose ARM verification fails. | Return an error before agent writes; do not silently ignore the setting or fall back to another project. |
| Invalid size | Supply a non-integer or a value outside 0–50. | Reject locally before authentication or mutations. |
| Deployment succeeds, endpoint update fails | Run normal deployment and encounter a subsequent endpoint PATCH failure. | Return failure explaining the partial success. Retain the deployed version and direct the user to retry endpoint update; do not delete resources. |

There is no new standby command, CLI flag, warm-up spinner, or readiness counter.
Positive targets consume resources independently for each serving version.