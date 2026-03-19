import { mkdtempSync, rmSync, writeFileSync } from "fs";
import { tmpdir } from "os";
import { join } from "path";
import { azd } from "../test-utils";

describe("Error Recovery - Missing Project Configuration", () => {
  let tempDir: string;

  beforeEach(() => {
    tempDir = mkdtempSync(join(tmpdir(), "azd-error-"));
  });

  afterEach(() => {
    rmSync(tempDir, { recursive: true, force: true });
  });

  test("deploy in empty directory produces actionable error", () => {
    const result = azd("deploy", { cwd: tempDir });
    expect(result.exitCode).not.toBe(0);
    const output = result.stdout + result.stderr;
    // Error should reference the missing config file
    expect(output).toMatch(/azure\.yaml|no project/i);
  });

  test("provision in empty directory produces actionable error", () => {
    const result = azd("provision", { cwd: tempDir });
    expect(result.exitCode).not.toBe(0);
    const output = result.stdout + result.stderr;
    expect(output).toMatch(/azure\.yaml|no project/i);
  });

  test("env show without a project produces output", () => {
    const result = azd("env show", { cwd: tempDir });
    // azd may exit 0 with empty output or non-zero with an error —
    // either way it should not crash and should produce some output
    // or silently succeed.
    const output = result.stdout + result.stderr;
    expect(typeof result.exitCode).toBe("number");
    if (result.exitCode !== 0) {
      expect(output.length).toBeGreaterThan(0);
    }
  });
});

describe("Error Recovery - Invalid Configuration", () => {
  let tempDir: string;

  beforeEach(() => {
    tempDir = mkdtempSync(join(tmpdir(), "azd-badcfg-"));
  });

  afterEach(() => {
    rmSync(tempDir, { recursive: true, force: true });
  });

  test("malformed azure.yaml produces parse error or handles gracefully", () => {
    writeFileSync(join(tempDir, "azure.yaml"), "name: {{{{invalid yaml!@#$");
    const result = azd("deploy", { cwd: tempDir });
    expect(result.exitCode).not.toBe(0);
    const output = result.stdout + result.stderr;
    // Should indicate a YAML parsing problem or config issue
    expect(output).toMatch(/yaml|parse|unmarshal|invalid|error/i);
  });

  test("azure.yaml with unknown service host shows error", () => {
    const config = [
      "name: test-app",
      "services:",
      "  web:",
      "    host: nonexistent-host-type",
      "    project: ./src",
    ].join("\n");
    writeFileSync(join(tempDir, "azure.yaml"), config);
    const result = azd("deploy", { cwd: tempDir });
    expect(result.exitCode).not.toBe(0);
    const output = result.stdout + result.stderr;
    expect(output.length).toBeGreaterThan(0);
  });
});

describe("Error Recovery - Unknown Commands and Flags", () => {
  test("unknown command suggests similar commands", () => {
    const result = azd("provison"); // typo
    expect(result.exitCode).not.toBe(0);
    const output = result.stdout + result.stderr;
    // Should suggest the correct command or list available commands
    expect(output).toMatch(/provision|unknown|command|usage|help/i);
  });

  test("unknown flag produces usage hint", () => {
    const result = azd("version --nonexistent-flag");
    expect(result.exitCode).not.toBe(0);
    const output = result.stdout + result.stderr;
    expect(output).toMatch(/unknown|flag|usage|help/i);
  });

  test("error output is non-empty for every failure", () => {
    const badCommands = [
      "deploy --bad-flag",
      "notacommand",
      "init --template nonexistent://repo",
    ];
    for (const cmd of badCommands) {
      const result = azd(cmd);
      const output = result.stdout + result.stderr;
      expect(output.length).toBeGreaterThan(0);
    }
  });
});
