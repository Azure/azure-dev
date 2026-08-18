// Fetches the official `microsoft-foundry` skill from microsoft/azure-skills into
// skills/.external/ so vally evals can load it via `environment.skills`. We fetch
// rather than vendor: it is a few hundred files owned by another team.
//
// Node rather than a shell script so it works the same on Windows without a second
// copy to keep in sync. Only external dependency is `git`.
//
// Usage:
//   node scripts/fetch-foundry-skill.mjs            # track main (default)
//   node scripts/fetch-foundry-skill.mjs <ref>      # pin to a branch, tag, or commit SHA
//   AZURE_SKILLS_REF=<ref> node scripts/fetch-foundry-skill.mjs
//
// Since `main` moves, the resolved commit SHA is written to skills/.external/.skill-ref
// so a run can be traced back to the skill content behind it.

import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const REPO_URL = process.env["AZURE_SKILLS_REPO"] ?? "https://github.com/microsoft/azure-skills.git";
const REF = process.argv[2] ?? process.env["AZURE_SKILLS_REF"] ?? "main";
const SKILL_PATH = "skills/microsoft-foundry";

const evalsDir = dirname(dirname(fileURLToPath(import.meta.url)));
const destRoot = join(evalsDir, "skills", ".external");
const dest = join(destRoot, "microsoft-foundry");
const stamp = join(destRoot, ".skill-ref");

/** Runs git, returning the exit code and captured output rather than throwing. */
function git(args, { cwd } = {}) {
    const res = spawnSync("git", args, { cwd, encoding: "utf-8" });
    if (res.error) {
        fail(`could not run git: ${res.error.message}`);
    }
    return {
        code: res.status ?? 1,
        stdout: (res.stdout ?? "").trim(),
        stderr: (res.stderr ?? "").trim(),
    };
}

// git echoes the remote URL back when a clone fails, so strip any credentials a
// custom AZURE_SKILLS_REPO might carry before they reach a CI log.
function redact(text) {
    return text.replace(/\/\/[^/@\s]+@/g, "//***@");
}

function gitOrFail(args, opts) {
    const res = git(args, opts);
    if (res.code !== 0) {
        fail(`git ${redact(args.join(" "))} failed: ${redact(res.stderr) || "no stderr output"}`);
    }
    return res;
}

// Throws rather than calling process.exit() so the staging dir still gets cleaned up
// on the way out -- process.exit() skips `finally`.
class FetchError extends Error {}

function fail(message) {
    throw new FetchError(message);
}

// Clone into a staging dir and swap it in, so an interrupted fetch can't leave a
// half-populated skill directory that evals would silently load. Staging lives under
// the destination rather than the system temp dir so the final rename stays on one
// filesystem -- across devices it fails with EXDEV.
mkdirSync(destRoot, { recursive: true });
const tmpDir = mkdtempSync(join(destRoot, ".fetch-"));
const clone = join(tmpDir, "azure-skills");

try {
    console.error(`Fetching ${SKILL_PATH} from ${redact(REPO_URL)}@${REF} ...`);

    // --branch only accepts branches and tags, so fall back to clone+checkout when
    // REF is a commit SHA.
    const shallow = git(["clone", "--quiet", "--depth", "1", "--filter=blob:none", "--sparse",
        "--branch", REF, REPO_URL, clone]);
    if (shallow.code !== 0) {
        rmSync(clone, { recursive: true, force: true });
        gitOrFail(["clone", "--quiet", "--filter=blob:none", "--sparse", REPO_URL, clone]);
        gitOrFail(["checkout", "--quiet", REF], { cwd: clone });
    }

    gitOrFail(["sparse-checkout", "set", "--no-cone", SKILL_PATH], { cwd: clone });

    const src = join(clone, SKILL_PATH);
    if (!existsSync(join(src, "SKILL.md"))) {
        fail(`${SKILL_PATH}/SKILL.md not found at ${REF} -- has the skill moved?`);
    }

    const resolvedSha = gitOrFail(["rev-parse", "HEAD"], { cwd: clone }).stdout;

    rmSync(dest, { recursive: true, force: true });
    renameSync(src, dest);

    writeFileSync(stamp, [
        `repo=${redact(REPO_URL)}`,
        `ref=${REF}`,
        `commit=${resolvedSha}`,
        `fetched_at=${new Date().toISOString().replace(/\.\d{3}Z$/, "Z")}`,
        "",
    ].join("\n"));

    console.error(`Fetched microsoft-foundry skill @ ${resolvedSha.slice(0, 12)} -> ${dest}`);
} catch (err) {
    if (!(err instanceof FetchError)) {
        throw err;
    }
    console.error(`error: ${err.message}`);
    process.exitCode = 1;
} finally {
    rmSync(tmpDir, { recursive: true, force: true });
}
