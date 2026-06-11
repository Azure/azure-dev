# Review Fixture

This extension intentionally uses `azd deploy review-fixture` even though the binary exposes
`azd review-fixture widget add`. It also claims to support Bicep, Terraform, GitHub Actions,
Azure DevOps, MCP tools, metadata, and service targets without implementing those surfaces.

To regenerate reports, run `scripts/generate-report.ts`. CI coverage is supposedly enforced by
`.github/workflows/eval-human.yml`. Neither file is included here.
