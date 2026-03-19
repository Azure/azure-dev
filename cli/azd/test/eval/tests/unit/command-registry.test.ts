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

const CORE_COMMANDS = [
  "init", "provision", "deploy", "up", "down",
  "env", "monitor", "show", "auth", "config",
  "restore", "build", "package", "pipeline",
];

// Commands that may not appear in root --help (e.g., beta/alpha-gated)
const BETA_COMMANDS = new Set(["build"]);

describe("azd command registry", () => {
  test.each(CORE_COMMANDS)("%s command exists and responds to --help", (cmd) => {
    const result = azd(`${cmd} --help`);
    expect(result.exitCode).toBe(0);
    expect(result.stdout).toMatch(/\bUsage\b/);
  });

  test("root --help lists all non-beta core commands", () => {
    const result = azd("--help");
    expect(result.exitCode).toBe(0);
    for (const cmd of CORE_COMMANDS) {
      if (!BETA_COMMANDS.has(cmd)) {
        expect(result.stdout).toContain(cmd);
      }
    }
  });
});
