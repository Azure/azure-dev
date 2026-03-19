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

describe("azd flag validation", () => {
  describe("--output json flag", () => {
    test("version --output json produces valid JSON", () => {
      const result = azd("version --output json");
      expect(result.exitCode).toBe(0);

      let parsed: unknown;
      expect(() => {
        parsed = JSON.parse(result.stdout);
      }).not.toThrow();
      expect(parsed).toBeDefined();
    });
  });

  describe("--no-prompt flag", () => {
    const COMMANDS_WITH_PROMPT = [
      "init", "provision", "deploy", "up", "down",
    ];

    test.each(COMMANDS_WITH_PROMPT)(
      "%s accepts --no-prompt flag without unknown-flag error",
      (cmd) => {
        // Run with --help alongside --no-prompt to avoid actual execution
        const result = azd(`${cmd} --no-prompt --help`);
        // --no-prompt should not cause an "unknown flag" error
        const output = (result.stdout + result.stderr).toLowerCase();
        expect(output).not.toContain("unknown flag");
        expect(output).not.toContain("unknown shorthand flag");
      }
    );
  });

  describe("--environment / -e flag", () => {
    // env is a command group; it does not itself accept -e
    const COMMANDS_WITH_ENV = [
      "provision", "deploy", "up", "down",
    ];

    test.each(COMMANDS_WITH_ENV)(
      "%s --help mentions environment flag",
      (cmd) => {
        const result = azd(`${cmd} --help`);
        expect(result.exitCode).toBe(0);

        const output = result.stdout.toLowerCase();
        const mentionsEnvFlag =
          output.includes("--environment") || output.includes("-e");
        expect(mentionsEnvFlag).toBe(true);
      }
    );
  });

  describe("invalid flag handling", () => {
    test("unknown flag produces an error message", () => {
      const result = azd("--this-flag-does-not-exist");
      expect(result.exitCode).not.toBe(0);

      const output = (result.stdout + result.stderr).toLowerCase();
      const mentionsError =
        output.includes("unknown flag") ||
        output.includes("unknown command") ||
        output.includes("error");
      expect(mentionsError).toBe(true);
    });

    test("unknown flag on subcommand produces error", () => {
      const result = azd("init --totally-bogus-flag");
      expect(result.exitCode).not.toBe(0);

      const output = (result.stdout + result.stderr).toLowerCase();
      const mentionsError =
        output.includes("unknown flag") ||
        output.includes("error");
      expect(mentionsError).toBe(true);
    });

    test("error output suggests valid usage on bad flag", () => {
      const result = azd("deploy --nonexistent");
      expect(result.exitCode).not.toBe(0);

      const output = result.stdout + result.stderr;
      // Should include some help or usage guidance
      const hasGuidance =
        output.includes("Usage:") ||
        output.includes("usage") ||
        output.includes("--help") ||
        output.includes("unknown flag");
      expect(hasGuidance).toBe(true);
    });
  });
});
