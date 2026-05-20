# Release History

## Unreleased

- [[#8243]](https://github.com/Azure/azure-dev/pull/8243) Add `azd ai project set|unset|show` commands for persisting a default Microsoft Foundry project endpoint. Migrated from `azd ai agent project ...`. Endpoints set by the removed `azd ai agent project set` command are still resolved on first read (with a one-time migration notice surfaced by `azd ai project show`).

## 0.0.1-preview - Initial Version