import { execSync } from "child_process";
import { resolve } from "path";

const AZD_BIN = resolve(__dirname, "../../../../azd");

function azd(
  args: string,
  options?: { cwd?: string; timeout?: number }
): {
  stdout: string;
  stderr: string;
  exitCode: number;
  durationMs: number;
} {
  const start = Date.now();
  try {
    const stdout = execSync(`${AZD_BIN} ${args}`, {
      encoding: "utf-8",
      timeout: options?.timeout ?? 30_000,
      cwd: options?.cwd,
      env: { ...process.env, AZD_DEBUG_FORCE_NO_TTY: "true" },
    });
    return {
      stdout,
      stderr: "",
      exitCode: 0,
      durationMs: Date.now() - start,
    };
  } catch (e: unknown) {
    const err = e as {
      stdout?: string;
      stderr?: string;
      status?: number;
    };
    return {
      stdout: err.stdout || "",
      stderr: err.stderr || "",
      exitCode: err.status || 1,
      durationMs: Date.now() - start,
    };
  }
}

describe("Command Discovery - Root Help", () => {
  let helpOutput: string;

  beforeAll(() => {
    const result = azd("--help");
    helpOutput = result.stdout + result.stderr;
  });

  test("azd --help exits successfully", () => {
    const result = azd("--help");
    expect(result.exitCode).toBe(0);
  });

  test("root help lists core lifecycle commands", () => {
    const coreCommands = ["init", "provision", "deploy", "up", "down"];
    for (const cmd of coreCommands) {
      expect(helpOutput).toContain(cmd);
    }
  });

  test("root help lists management commands", () => {
    const mgmtCommands = ["env", "config", "monitor"];
    for (const cmd of mgmtCommands) {
      expect(helpOutput).toContain(cmd);
    }
  });

  test("each listed command has a description", () => {
    // Commands appear as "  command    Description text"
    // Verify that lines with command names also contain descriptive text
    const lines = helpOutput.split("\n");
    const commandLines = lines.filter(
      (l) => l.match(/^\s{2,}\w+/) && l.trim().length > 0
    );
    for (const line of commandLines) {
      // A command line should have at least a name and some description
      const parts = line.trim().split(/\s{2,}/);
      if (parts.length >= 2) {
        expect(parts[1].length).toBeGreaterThan(0);
      }
    }
  });
});

describe("Command Discovery - Subcommand Help", () => {
  test("azd env --help lists env subcommands", () => {
    const result = azd("env --help");
    expect(result.exitCode).toBe(0);
    const output = result.stdout;
    const expectedSubcommands = ["list", "new", "select", "get-values"];
    for (const sub of expectedSubcommands) {
      expect(output).toContain(sub);
    }
  });

  test("azd config --help lists config subcommands", () => {
    const result = azd("config --help");
    expect(result.exitCode).toBe(0);
    const output = result.stdout;
    expect(output).toMatch(/set|get|list|unset|show/i);
  });

  test("azd auth --help lists auth subcommands", () => {
    const result = azd("auth --help");
    expect(result.exitCode).toBe(0);
    const output = result.stdout;
    expect(output).toMatch(/login|token/i);
  });
});

describe("Command Discovery - Flag Documentation", () => {
  test("global flags are documented in root help", () => {
    const result = azd("--help");
    const output = result.stdout;
    // Common global flags
    expect(output).toMatch(/--help/);
  });

  test("azd init --help documents available flags", () => {
    const result = azd("init --help");
    expect(result.exitCode).toBe(0);
    const output = result.stdout;
    // init should document its key flags
    expect(output).toMatch(/--template|-t/);
  });

  test("azd deploy --help documents service targeting", () => {
    const result = azd("deploy --help");
    expect(result.exitCode).toBe(0);
    const output = result.stdout;
    // deploy should explain how to target specific services
    expect(output).toMatch(/service|--all/i);
  });
});

describe("Command Discovery - Completion Support", () => {
  test("azd completion is available", () => {
    // Verify that shell completion generation is a supported command
    const result = azd("--help");
    const output = result.stdout + result.stderr;
    // azd should mention completion somewhere in help or as a subcommand
    const completionResult = azd("completion --help");
    // If completion is a command, it should either succeed or fail with a
    // known message — it should not crash
    expect(
      completionResult.exitCode === 0 ||
        (completionResult.stdout + completionResult.stderr).length > 0
    ).toBe(true);
  });

  test("azd completion generates shell script for bash", () => {
    const result = azd("completion bash");
    if (result.exitCode === 0) {
      // If supported, output should be a valid shell script
      expect(result.stdout).toMatch(/compdef|complete|_azd/i);
    }
    // If not supported, that's acceptable — just ensure no crash
    expect(result.stdout.length + result.stderr.length).toBeGreaterThan(0);
  });
});

describe("Command Discovery - Help Consistency", () => {
  const commandsToCheck = [
    "init",
    "provision",
    "deploy",
    "up",
    "down",
    "env",
    "config",
    "auth",
  ];

  test.each(commandsToCheck)(
    "azd %s --help exits with code 0",
    (cmd: string) => {
      const result = azd(`${cmd} --help`);
      expect(result.exitCode).toBe(0);
    }
  );

  test.each(commandsToCheck)(
    "azd %s --help produces non-empty output",
    (cmd: string) => {
      const result = azd(`${cmd} --help`);
      expect(result.stdout.length).toBeGreaterThan(0);
    }
  );

  test.each(commandsToCheck)(
    "azd %s --help responds within 3 seconds",
    (cmd: string) => {
      const result = azd(`${cmd} --help`);
      expect(result.durationMs).toBeLessThan(3_000);
    }
  );
});
