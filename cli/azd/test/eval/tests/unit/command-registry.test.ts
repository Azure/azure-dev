import { azd } from "../test-utils";

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
