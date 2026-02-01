# Release History


## 0.0.15-preview (2026-02-02)

- Simplified init flow: reduced prompts from 4 to 2 for faster setup
- Added implicit init to all job commands: use `--subscription` (`-s`) and `--project-endpoint` (`-e`) flags to configure and run in a single command

## 0.0.14-preview (2026-01-28)

- Defaulting to supervise when fine tuning method is not return by API
- Adding training Type when cloning a job
- Adding details of grader to cloning process.
- Allow to submit a job with different graders in RFT.

## 0.0.12-preview (2026-01-23)

- Add Project-endpoint parameter to init command

## 0.0.11-preview (2026-01-22)

- Add metadata capability
- Support `AZD_EXT_DEBUG=true` for debugging
- Disable option to create new resource group during `init`

## 0.0.10-preview (2026-01-21)

- Bug fixes

## 0.0.9-preview (2026-01-20)

- Adding missing commands

## 0.0.8-preview (2026-01-13)

- Initial release
