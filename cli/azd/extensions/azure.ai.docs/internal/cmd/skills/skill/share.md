---
short: Cross-team / cross-project sharing via download (extracted or raw .zip) with safe-extract guarantees.
order: 30
---
# Skill share: cross-project / cross-team workflows

Skills live on **one** Foundry project. To move a skill to a different project, share it with a teammate, or version it in Git, you download it from the source project and (optionally) re-upload it to the destination. `azd ai skill download` is the workhorse.

For CLI flag details, see `manage`. For runtime wiring inside a Hosted agent, see `consume`.

## Prerequisite: the source skill must be downloadable

`download` returns HTTP 404 for skills with `has_blob=false`. Only skills created from a `.zip` are downloadable:

| Created via                                            | `has_blob` | Downloadable? |
| ------------------------------------------------------ | ---------- | ------------- |
| `create --description ... --instructions ...`          | `false`    | NO            |
| `create --file ./SKILL.md`                             | `false`    | NO            |
| `create --file ./skill.zip`                            | `true`     | YES           |

If the skill you want to share was created inline or from a SKILL.md, you can't pull it back down -- the original content lives only in the JSON `inline_content` field, and Foundry does not synthesize a ZIP from it. Re-create from a `.zip` before publishing if a round-trip is on the roadmap.

## Default download: extracted layout

```bash
azd ai skill download greet-user
```

Writes to `.agents/skills/greet-user/` (relative to the current working directory) and prints the extracted file list. Each file inside the archive is written to its corresponding path under that directory. The destination is created if missing.

`--output-dir <path>` overrides the destination. Use this when you want to write into your repo's source tree directly:

```bash
azd ai skill download greet-user --output-dir ./shared-skills/greet-user
```

`--version <ver>` pulls a non-default version. Useful for backports or for verifying that a previously-pinned version is still recoverable:

```bash
azd ai skill download greet-user --version v2
```

## Raw download: single .zip

When you need the unmodified archive bytes (for checking into Git as a blob, attaching to an email, or auditing the on-wire payload), use `--raw`:

```bash
azd ai skill download greet-user --raw --output-dir ./downloads
```

Writes `./downloads/greet-user.zip` (`<name>.zip` -- the version is not part of the file name; if you need it in the file name, rename after the fact).

`--raw` and `--output-dir` are independent of each other. `--raw` without `--output-dir` writes to `.agents/skills/greet-user/greet-user.zip` (still extracted-layout-style output dir, just with the archive sitting inside it instead of extracted contents).

## Conflict handling: `--force`

The default behavior refuses to overwrite an existing file at the destination, both for extracted mode and `--raw` mode. `--force` overrides this.

In `--raw` mode, the CLI also Lstat's the existing target archive -- never following symlinks -- and refuses to clobber a symlink or any non-regular file even with `--force`. Remove the entry manually first.

In extracted mode, the safe-extractor compares per-file and refuses to clobber any existing file without `--force`. Foreign files at the destination (not in the archive) are always preserved.

## Safe-extract guarantees

The download path runs every ZIP through a strict extractor before writing anything to disk. These checks defeat archives that would otherwise own the destination:

| Threat                                                | Behavior                                                                |
| ----------------------------------------------------- | ----------------------------------------------------------------------- |
| Zip-slip (`../escape/secret.key`)                     | Rejected with `CodeSkillArchiveUnsafe`. Nothing written.                |
| Symlink entry                                         | Rejected with `CodeSkillArchiveUnsafe`. Nothing written.                |
| Oversized archive (total decompressed size > limit)   | Rejected with `CodeSkillArchiveUnsafe`. Nothing written.                |
| Per-entry oversized file                              | Rejected with `CodeSkillArchiveUnsafe`. Nothing written.                |
| Existing file at target path without `--force`        | Rejected with `CodeSkillOutputCollision`. Suggests `--force`.            |
| Malformed ZIP                                         | Rejected with `CodeInvalidParameter` -- the service returned a bad blob. |

The check happens **before** any file is written, so a partial extraction never leaves debris at the destination.

## Round-trip recipe: project A to project B

```bash
# 1) Sign in and target the SOURCE project.
azd auth login
azd ai skill download greet-user --raw --output-dir ./out

# 2) Switch to the DESTINATION project.
#    Re-point the active endpoint via env, global config, or flag.
azd env set AZURE_AI_PROJECT_ENDPOINT "https://<dest-account>.services.ai.azure.com/api/projects/<dest>"

# 3) Re-upload as a new skill on the destination project.
azd ai skill create greet-user --file ./out/greet-user.zip
```

The re-created skill is independent: it has a fresh `skill_id`, starts at its first version, and tracks its own `default_version` / `latest_version` on the destination project. The name is what binds the two; coordinate on naming if multiple teams may import the same skill.

## Round-trip recipe: edit then re-publish

```bash
# 1) Pull the current default version.
azd ai skill download greet-user --raw --output-dir ./tmp

# 2) Unzip, edit SKILL.md (and any sibling files), re-zip with SKILL.md
#    at the archive ROOT.
cd ./tmp
unzip greet-user.zip -d ./greet-user-src
# ... edit ./greet-user-src/SKILL.md ...
(cd ./greet-user-src && zip -r ../greet-user-v2.zip .)
cd ..

# 3) Publish as a new version on the SAME project (uses create --force
#    because update rejects .zip; this deletes the old skill first --
#    the file-name guard refuses if SKILL.md's name doesn't match).
azd ai skill create greet-user --file ./tmp/greet-user-v2.zip --force
```

Important: `create --force` deletes ALL existing versions before re-creating. If you need to preserve the prior version, use the metadata-only repoint flow instead (`update --set-default-version`) -- but that requires the prior version to already exist on Foundry; you cannot upload a NEW immutable version via a ZIP without `create --force`.

## Round-trip recipe: version a skill in Git

Treat the downloaded `.zip` (or the extracted tree) as the source of truth in your repository.

```bash
# Pull every release version locally and commit them.
for v in v1 v2 v3; do
  azd ai skill download greet-user --version "$v" --raw \
    --output-dir "./skills/greet-user/$v" --force
done
git add ./skills/greet-user
git commit -m "skill: greet-user v1-v3 snapshots"
```

On a clean checkout of another project, `azd ai skill create greet-user --file ./skills/greet-user/v3/greet-user.zip` re-establishes the skill on the destination project.

## Permission notes

Both `download` and `create` require the **Foundry User** role on the relevant project. Cross-project moves require this role on BOTH projects. If you only have read access on the source and write access on the destination, the download succeeds and the upload also succeeds (the role check is per-project).

## What `download` does NOT do

* It does NOT register the skill with any deployed Hosted agent. The agent reads SKILL.md files from its container image at build time -- you still need to redeploy after refreshing local copies. See `consume`.
* It does NOT verify the SHA / signature of the archive. If you need provenance guarantees, sign the archive yourself before publishing and verify after pulling.
