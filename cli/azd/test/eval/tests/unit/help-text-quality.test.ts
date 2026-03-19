import { execSync } from "child_process";
import { resolve } from "path";

const AZD_BIN = resolve(__dirname, "../../../../azd");

function azd(args: string): { stdout: string; stderr: string; exitCode: number } {
  try {
    const stdout = execSync(`${AZD_BIN} ${args}`, {
      encoding: "utf-8",
      timeout: 30_000,
      env: { ...process.env, NO_COLOR: "1" },
    });
    return { stdout, stderr: "", exitCode: 0 };
  } catch (e: any) {
    return { stdout: e.stdout || "", stderr: e.stderr || "", exitCode: e.status || 1 };
  }
}

const COMMANDS_WITH_HELP = [
  "init", "provision", "deploy", "up", "down",
  "env", "monitor", "show", "auth", "config",
  "restore", "build", "package", "pipeline",
];

describe("azd help text quality", () => {
  test.each(COMMANDS_WITH_HELP)(
    "%s --help contains Usage and Flags sections",
    (cmd) => {
      const result = azd(`${cmd} --help`);
      expect(result.exitCode).toBe(0);
      expect(result.stdout).toMatch(/\bUsage\b/);
      expect(result.stdout).toMatch(/\bFlags\b|\bGlobal Flags\b/);
    }
  );

  test.each(COMMANDS_WITH_HELP)(
    "%s --help has a meaningful description (> 10 chars)",
    (cmd) => {
      const result = azd(`${cmd} --help`);
      expect(result.exitCode).toBe(0);

      // The description is the text before the first section header.
      // Typically the first non-empty line(s) before "Usage".
      const lines = result.stdout.split("\n");
      const descriptionLines: string[] = [];
      for (const line of lines) {
        if (/^Usage\b/.test(line)) break;
        if (line.trim().length > 0) {
          descriptionLines.push(line.trim());
        }
      }
      const description = descriptionLines.join(" ");
      expect(description.length).toBeGreaterThan(10);
    }
  );

  test.each(COMMANDS_WITH_HELP)(
    "--help flag works on %s command",
    (cmd) => {
      const result = azd(`${cmd} --help`);
      expect(result.exitCode).toBe(0);
      expect(result.stdout.length).toBeGreaterThan(0);
    }
  );

  test("root help text contains project description", () => {
    const result = azd("--help");
    expect(result.exitCode).toBe(0);
    // The root help should describe what azd does
    expect(result.stdout).toMatch(/Azure Developer CLI|azd/i);
  });
});
