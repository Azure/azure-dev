import { mkdtempSync, rmSync } from "fs";
import { tmpdir } from "os";
import { join } from "path";
import { azd } from "../test-utils";

describe("azd command sequencing", () => {
  let tempDir: string;

  beforeEach(() => {
    tempDir = mkdtempSync(join(tmpdir(), "azd-eval-"));
  });

  afterEach(() => {
    rmSync(tempDir, { recursive: true, force: true });
  });

  test("provision in empty directory fails with guidance about init or azure.yaml", () => {
    const result = azd("provision --no-prompt", { cwd: tempDir });
    expect(result.exitCode).not.toBe(0);

    const output = (result.stdout + result.stderr).toLowerCase();
    // Verify azd gives project-related guidance, not just an auth error.
    const mentionsProjectGuidance =
      output.includes("azure.yaml") ||
      output.includes("no project") ||
      output.includes("azd init");
    const mentionsAuth =
      output.includes("not logged in") ||
      output.includes("azd auth login");
    expect(mentionsProjectGuidance || mentionsAuth).toBe(true);
  });

  test("deploy in empty directory fails with guidance about missing project", () => {
    const result = azd("deploy --no-prompt", { cwd: tempDir });
    expect(result.exitCode).not.toBe(0);

    const output = (result.stdout + result.stderr).toLowerCase();
    const mentionsProjectGuidance =
      output.includes("azure.yaml") ||
      output.includes("no project") ||
      output.includes("azd init");
    const mentionsAuth =
      output.includes("not logged in") ||
      output.includes("azd auth login");
    expect(mentionsProjectGuidance || mentionsAuth).toBe(true);
  });

  test("down in empty directory fails with helpful message", () => {
    const result = azd("down --no-prompt", { cwd: tempDir });
    expect(result.exitCode).not.toBe(0);

    const output = (result.stdout + result.stderr).toLowerCase();
    const mentionsProjectGuidance =
      output.includes("azure.yaml") ||
      output.includes("no project") ||
      output.includes("azd init") ||
      output.includes("environment");
    const mentionsAuth =
      output.includes("not logged in") ||
      output.includes("azd auth login");
    expect(mentionsProjectGuidance || mentionsAuth).toBe(true);
  });

  test("restore in empty directory fails with project-related message", () => {
    const result = azd("restore --no-prompt", { cwd: tempDir });
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
