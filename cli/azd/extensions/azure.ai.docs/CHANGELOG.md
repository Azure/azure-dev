# Release History

## 0.0.1-preview - Initial Version

### Added

* `azd ai doc skill` command group with topics for the Foundry skill
  resource (`overview`, `manage`, `share`, `consume`). Covers the
  `azure.ai.skills` extension lifecycle: the versioned skill model
  (`default_version` / `latest_version`), the `azd ai skill` CLI
  reference, cross-project sharing via download / re-upload, and
  agent-side wiring (`skill_directories`) for Hosted agents.
* `azd ai doc install` parent command group for embedded-pack
  installers, hosting the renamed `install skill` child below.

### Changed

* Renamed `azd ai doc skills install` to `azd ai doc install skill`.
  The new `install` parent groups embedded-pack installers; the `skill`
  child copies the bundled `azd-ai-skill` coding-agent pack into the
  user's project (the existing `--target` / `--path` / `--force` /
  `--output` flag surface is unchanged). No backwards-compatible alias.
* The embedded `SKILL.md` router now lists the Foundry skill resource
  docs alongside agent / connection / toolbox docs and adds
  `azd ai skill` to the `allowed-tools` list.