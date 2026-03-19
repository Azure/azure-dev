import { execSync } from "child_process";
import { resolve } from "path";
import { mkdtempSync, rmSync } from "fs";
import { tmpdir } from "os";
import { join } from "path";

const AZD_BIN = resolve(__dirname, "../../../../azd");

function azdInDir(
  args: string,
  cwd: string
): { stdout: string; stderr: string; exitCode: number } {
  try {
    const stdout = execSync(`${AZD_BIN} ${args} --no-prompt`, {
      encoding: "utf-8",
      timeout: 60_000,
      cwd,
      env: { ...process.env, NO_COLOR: "1" },
    });
    return { stdout, stderr: "", exitCode: 0 };
  } catch (e: any) {
    return { stdout: e.stdout || "", stderr: e.stderr || "", exitCode: e.status || 1 };
  }
}

describe("azd command sequencing", () => {
  let tempDir: string;

  beforeEach(() => {
    tempDir = mkdtempSync(join(tmpdir(), "azd-eval-"));
  });

  afterEach(() => {
    rmSync(tempDir, { recursive: true, force: true });
  });

  test("provision in empty directory fails with guidance about init or azure.yaml", () => {
    const result = azdInDir("provision", tempDir);
    expect(result.exitCode).not.toBe(0);

    const output = (result.stdout + result.stderr).toLowerCase();
    // Should mention what's missing so the user knows what to do.
    // In CI without auth, azd may report an auth error instead of a project error.
    const mentionsGuidance =
      output.includes("azure.yaml") ||
      output.includes("init") ||
      output.includes("project") ||
      output.includes("no project") ||
      output.includes("logged in") ||
      output.includes("login") ||
      output.includes("auth");
    expect(mentionsGuidance).toBe(true);
  });

  test("deploy in empty directory fails with guidance about missing project", () => {
    const result = azdInDir("deploy", tempDir);
    expect(result.exitCode).not.toBe(0);

    const output = (result.stdout + result.stderr).toLowerCase();
    const mentionsGuidance =
      output.includes("azure.yaml") ||
      output.includes("init") ||
      output.includes("project") ||
      output.includes("no project") ||
      output.includes("logged in") ||
      output.includes("login") ||
      output.includes("auth");
    expect(mentionsGuidance).toBe(true);
  });

  test("down in empty directory fails with helpful message", () => {
    const result = azdInDir("down", tempDir);
    expect(result.exitCode).not.toBe(0);

    const output = (result.stdout + result.stderr).toLowerCase();
    const mentionsGuidance =
      output.includes("azure.yaml") ||
      output.includes("init") ||
      output.includes("project") ||
      output.includes("no project") ||
      output.includes("environment") ||
      output.includes("logged in") ||
      output.includes("login") ||
      output.includes("auth");
    expect(mentionsGuidance).toBe(true);
  });

  test("restore in empty directory fails with project-related message", () => {
    const result = azdInDir("restore", tempDir);
    expect(result.exitCode).not.toBe(0);

    const output = (result.stdout + result.stderr).toLowerCase();
    const mentionsGuidance =
      output.includes("azure.yaml") ||
      output.includes("init") ||
      output.includes("project") ||
      output.includes("no project");
    expect(mentionsGuidance).toBe(true);
  });
});
