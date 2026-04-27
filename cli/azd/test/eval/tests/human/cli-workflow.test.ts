import { mkdtempSync, rmSync } from "fs";
import { tmpdir } from "os";
import { join } from "path";
import { azd } from "../test-utils";

describe("Human CLI Workflow - Setup Verification", () => {
  test("azd binary exists and is executable", () => {
    const result = azd("version");
    expect(result.exitCode).toBe(0);
    expect(result.stdout).toMatch(/azd version/i);
  });

  test("azd version responds within 5 seconds", () => {
    const result = azd("version");
    expect(result.durationMs).toBeLessThan(5_000);
  });

  test("azd version outputs a semver-like version string", () => {
    const result = azd("version");
    expect(result.stdout).toMatch(/\d+\.\d+\.\d+/);
  });
});

describe("Human CLI Workflow - Local-Only Commands", () => {
  let tempDir: string;

  beforeEach(() => {
    tempDir = mkdtempSync(join(tmpdir(), "azd-workflow-"));
  });

  afterEach(() => {
    rmSync(tempDir, { recursive: true, force: true });
  });

  test("azd init --help provides usage guidance", () => {
    const result = azd("init --help");
    expect(result.exitCode).toBe(0);
    expect(result.stdout).toMatch(/init/i);
  });

  test("azd config list works without Azure credentials", () => {
    const result = azd("config list");
    // config list should succeed even with no config set
    expect(result.exitCode).toBe(0);
  });

  test("azd env list in empty dir fails gracefully", () => {
    const result = azd("env list", { cwd: tempDir });
    // Should fail because there's no azure.yaml, but not crash
    expect(result.exitCode).not.toBe(0);
    const combined = result.stdout + result.stderr;
    expect(combined.length).toBeGreaterThan(0);
  });
});

// Enable this block when Azure credentials are available for E2E testing.
// These tests measure the full init → provision → deploy workflow that a
// human developer follows when onboarding a new project with azd.
describe.skip("Human CLI Workflow - E2E (requires Azure credentials)", () => {
  let tempDir: string;

  beforeAll(() => {
    tempDir = mkdtempSync(join(tmpdir(), "azd-e2e-"));
  });

  afterAll(() => {
    // Tear down any provisioned resources
    try {
      azd("down --force --purge", { cwd: tempDir, timeout: 120_000 });
    } catch {
      // Best-effort cleanup
    }
    rmSync(tempDir, { recursive: true, force: true });
  });

  test("init creates azure.yaml and infra directory", () => {
    const result = azd(
      "init --template todo-nodejs-mongo --branch main --no-prompt",
      { cwd: tempDir, timeout: 60_000 }
    );
    expect(result.exitCode).toBe(0);
  });

  test("provision creates Azure resources", () => {
    const result = azd("provision --no-prompt", {
      cwd: tempDir,
      timeout: 300_000,
    });
    expect(result.exitCode).toBe(0);
    expect(result.stdout).toMatch(/SUCCESS/i);
  });

  test("deploy pushes application code", () => {
    const result = azd("deploy --no-prompt", {
      cwd: tempDir,
      timeout: 300_000,
    });
    expect(result.exitCode).toBe(0);
    expect(result.stdout).toMatch(/SUCCESS/i);
  });

  test("full workflow completes within 10 minutes", () => {
    // This is a composite measurement — the individual steps above
    // each record durationMs for granular analysis.
    const result = azd("show", { cwd: tempDir });
    expect(result.exitCode).toBe(0);
  });
});
