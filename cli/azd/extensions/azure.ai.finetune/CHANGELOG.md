# Release History


## 0.0.18-preview (2026-03-23)

- Fix: `--project-endpoint` and `--subscription` flags now take priority over any previously configured azd environment when running `jobs` commands (list, show, submit, pause, resume, cancel, deploy).
- Fix: Removed warning that incorrectly ignored user-provided flags when an environment was already configured.
- The priority order for endpoint resolution is now: (1) explicit flags, (2) azd environment variables, (3) error with guidance to run `azd ai finetuning init`.

## 0.0.17-preview (2026-02-20)

- Add multi-grader support for reinforcement fine-tuning
- Update azd command descriptions
- Bug fixes

## 0.0.16-preview (2026-02-03)

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
