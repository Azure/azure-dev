jest.mock("@actions/core");

import { validateResult, type RawAnalysisResult } from "./analyze";

describe("validateResult", () => {
  const SOURCE_REPO = "Azure/azure-dev";
  const DOCS_REPO = "MicrosoftDocs/azure-dev-docs-pr";

  it("returns noImpact for empty impacts array", () => {
    const raw: RawAnalysisResult = { impacts: [], summary: "No changes", noImpact: true };
    const result = validateResult(raw, SOURCE_REPO, DOCS_REPO);
    expect(result.noImpact).toBe(true);
    expect(result.impacts).toHaveLength(0);
  });

  it("accepts valid impacts", () => {
    const raw: RawAnalysisResult = {
      impacts: [
        {
          repo: SOURCE_REPO,
          path: "docs/test.md",
          action: "update",
          reason: "API changed",
          priority: "high",
        },
      ],
      summary: "1 doc impacted",
      noImpact: false,
    };
    const result = validateResult(raw, SOURCE_REPO);
    expect(result.impacts).toHaveLength(1);
    expect(result.noImpact).toBe(false);
    expect(result.impacts[0].action).toBe("update");
    expect(result.impacts[0].priority).toBe("high");
  });

  it("filters out impacts with invalid action", () => {
    const raw: RawAnalysisResult = {
      impacts: [
        { repo: SOURCE_REPO, path: "docs/test.md", action: "destroy", reason: "test", priority: "high" },
      ],
      summary: "Test",
      noImpact: false,
    };
    const result = validateResult(raw, SOURCE_REPO);
    expect(result.impacts).toHaveLength(0);
    expect(result.noImpact).toBe(true);
  });

  it("filters out impacts with invalid priority", () => {
    const raw: RawAnalysisResult = {
      impacts: [
        { repo: SOURCE_REPO, path: "docs/test.md", action: "update", reason: "test", priority: "critical" },
      ],
      summary: "Test",
      noImpact: false,
    };
    const result = validateResult(raw, SOURCE_REPO);
    expect(result.impacts).toHaveLength(0);
  });

  it("filters out impacts with path traversal (..)", () => {
    const raw: RawAnalysisResult = {
      impacts: [
        { repo: SOURCE_REPO, path: "../../etc/passwd", action: "update", reason: "test", priority: "high" },
      ],
      summary: "Test",
      noImpact: false,
    };
    const result = validateResult(raw, SOURCE_REPO);
    expect(result.impacts).toHaveLength(0);
  });

  it("filters out impacts with absolute paths", () => {
    const raw: RawAnalysisResult = {
      impacts: [
        { repo: SOURCE_REPO, path: "/etc/passwd", action: "update", reason: "test", priority: "high" },
      ],
      summary: "Test",
      noImpact: false,
    };
    const result = validateResult(raw, SOURCE_REPO);
    expect(result.impacts).toHaveLength(0);
  });

  it("filters out impacts targeting unknown repos", () => {
    const raw: RawAnalysisResult = {
      impacts: [
        { repo: "Evil/repo", path: "docs/test.md", action: "update", reason: "test", priority: "high" },
      ],
      summary: "Test",
      noImpact: false,
    };
    const result = validateResult(raw, SOURCE_REPO, DOCS_REPO);
    expect(result.impacts).toHaveLength(0);
  });

  it("filters out impacts with invalid repo format", () => {
    const raw: RawAnalysisResult = {
      impacts: [
        { repo: "not-valid", path: "docs/test.md", action: "update", reason: "test", priority: "high" },
      ],
      summary: "Test",
      noImpact: false,
    };
    const result = validateResult(raw, SOURCE_REPO);
    expect(result.impacts).toHaveLength(0);
  });

  it("sanitizes reason and summary fields (strips HTML)", () => {
    const raw: RawAnalysisResult = {
      impacts: [
        {
          repo: SOURCE_REPO,
          path: "docs/test.md",
          action: "update",
          reason: '<script>alert("xss")</script>Real reason',
          priority: "high",
        },
      ],
      summary: '<img src=x onerror=alert(1)>Summary text',
      noImpact: false,
    };
    const result = validateResult(raw, SOURCE_REPO);
    expect(result.impacts[0].reason).not.toContain("<script>");
    expect(result.impacts[0].reason).toContain("Real reason");
    expect(result.summary).not.toContain("<img");
    expect(result.summary).toContain("Summary text");
  });

  it("truncates reason to MAX_REASON_LENGTH (200)", () => {
    const longReason = "a".repeat(500);
    const raw: RawAnalysisResult = {
      impacts: [
        { repo: SOURCE_REPO, path: "docs/test.md", action: "update", reason: longReason, priority: "high" },
      ],
      summary: "Test",
      noImpact: false,
    };
    const result = validateResult(raw, SOURCE_REPO);
    expect(result.impacts[0].reason.length).toBeLessThanOrEqual(200);
  });

  it("truncates summary to MAX_SUMMARY_LENGTH (500)", () => {
    const longSummary = "b".repeat(1000);
    const raw: RawAnalysisResult = {
      impacts: [],
      summary: longSummary,
      noImpact: true,
    };
    const result = validateResult(raw, SOURCE_REPO);
    expect(result.summary.length).toBeLessThanOrEqual(500);
  });

  it("limits impacts to MAX_IMPACTS (15)", () => {
    const manyImpacts = Array.from({ length: 30 }, (_, i) => ({
      repo: SOURCE_REPO,
      path: `docs/file${i}.md`,
      action: "update",
      reason: `reason ${i}`,
      priority: "medium",
    }));
    const raw: RawAnalysisResult = { impacts: manyImpacts, summary: "Many", noImpact: false };
    const result = validateResult(raw, SOURCE_REPO);
    expect(result.impacts.length).toBeLessThanOrEqual(15);
  });

  it("handles non-array impacts gracefully", () => {
    const raw = { impacts: null, summary: "Test", noImpact: false } as unknown as RawAnalysisResult;
    const result = validateResult(raw, SOURCE_REPO);
    expect(result.impacts).toHaveLength(0);
    expect(result.noImpact).toBe(true);
  });

  it("handles missing reason field", () => {
    const raw: RawAnalysisResult = {
      impacts: [
        { repo: SOURCE_REPO, path: "docs/test.md", action: "update", reason: undefined as unknown as string, priority: "high" },
      ],
      summary: "Test",
      noImpact: false,
    };
    const result = validateResult(raw, SOURCE_REPO);
    // reason is not a string → filtered out
    expect(result.impacts).toHaveLength(0);
  });

  it("allows known repos when both are provided", () => {
    const raw: RawAnalysisResult = {
      impacts: [
        { repo: SOURCE_REPO, path: "docs/a.md", action: "update", reason: "r1", priority: "high" },
        { repo: DOCS_REPO, path: "articles/b.md", action: "create", reason: "r2", priority: "medium" },
      ],
      summary: "Two repos",
      noImpact: false,
    };
    const result = validateResult(raw, SOURCE_REPO, DOCS_REPO);
    expect(result.impacts).toHaveLength(2);
  });

  it("allows any repo when no known repos are provided", () => {
    const raw: RawAnalysisResult = {
      impacts: [
        { repo: "Some/repo", path: "docs/a.md", action: "update", reason: "r1", priority: "high" },
      ],
      summary: "Test",
      noImpact: false,
    };
    // No sourceRepo or docsRepo → knownRepos is empty → allow all valid formats
    const result = validateResult(raw);
    expect(result.impacts).toHaveLength(1);
  });

  it("sanitizes suggestedChanges when present", () => {
    const raw: RawAnalysisResult = {
      impacts: [
        {
          repo: SOURCE_REPO,
          path: "docs/test.md",
          action: "update",
          reason: "needs update",
          suggestedChanges: '<img src=x>Add new section about ![img](evil)',
          priority: "low",
        },
      ],
      summary: "Test",
      noImpact: false,
    };
    const result = validateResult(raw, SOURCE_REPO);
    expect(result.impacts[0].suggestedChanges).not.toContain("<img");
    expect(result.impacts[0].suggestedChanges).not.toContain("![");
  });
});
