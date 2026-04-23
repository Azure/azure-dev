import { execFileSync } from "child_process";
import { resolve } from "path";

export const AZD_BIN = resolve(__dirname, "../../../azd" + (process.platform === "win32" ? ".exe" : ""));

export interface AzdResult {
  stdout: string;
  stderr: string;
  exitCode: number;
  durationMs: number;
}

export interface AzdOptions {
  cwd?: string;
  timeout?: number;
}

/**
 * Runs an azd CLI command and captures the result.
 *
 * Uses execFileSync to avoid shell injection — arguments are passed as an
 * array and never interpolated into a shell command string.
 *
 * Sets NO_COLOR=1 to strip ANSI codes (stable regex matching) and
 * AZD_FORCE_TTY=false to prevent interactive prompts.
 */
export function azd(args: string, options?: AzdOptions): AzdResult {
  const argList = args.split(/\s+/).filter(Boolean);
  const start = Date.now();
  try {
    const stdout = execFileSync(AZD_BIN, argList, {
      encoding: "utf-8",
      timeout: options?.timeout ?? 30_000,
      cwd: options?.cwd,
      env: { ...process.env, NO_COLOR: "1", AZD_FORCE_TTY: "false" },
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
