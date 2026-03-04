import { classifyChanges, buildDiffSummary } from "./diff";
import type { FileDiff } from "./types";

describe("classifyChanges", () => {
  it("groups files by category", () => {
    const files: FileDiff[] = [
      { path: "cli/azd/internal/cmd/init.go", status: "modified", additions: 5, deletions: 2 },
      { path: "cli/azd/pkg/tools/tool.go", status: "modified", additions: 3, deletions: 1 },
      { path: "README.md", status: "modified", additions: 10, deletions: 5 },
    ];
    const result = classifyChanges(files);
    expect(result).toHaveLength(3);
    const categories = result.map((c) => c.category).sort();
    expect(categories).toEqual(["api", "behavior", "docs"]);
  });

  it("returns empty array for no files", () => {
    expect(classifyChanges([])).toHaveLength(0);
  });

  it("classifies unknown paths as other", () => {
    const files: FileDiff[] = [
      { path: "some/random/file.txt", status: "added", additions: 1, deletions: 0 },
    ];
    const result = classifyChanges(files);
    expect(result[0].category).toBe("other");
  });

  it("classifies test files", () => {
    const files: FileDiff[] = [
      { path: "some/module/thing_test.go", status: "modified", additions: 10, deletions: 5 },
    ];
    const result = classifyChanges(files);
    expect(result[0].category).toBe("test");
  });

  it("classifies .github paths as infra", () => {
    const files: FileDiff[] = [
      { path: ".github/workflows/ci.yml", status: "modified", additions: 1, deletions: 1 },
    ];
    const result = classifyChanges(files);
    expect(result[0].category).toBe("infra");
  });
});

describe("buildDiffSummary", () => {
  it("builds summary for files with patches", () => {
    const files: FileDiff[] = [
      { path: "file.go", status: "modified", additions: 3, deletions: 1, patch: "+line1\n-line2" },
    ];
    const result = buildDiffSummary(files);
    expect(result).toContain("file.go");
    expect(result).toContain("+3/-1");
    expect(result).toContain("+line1");
  });

  it("truncates when exceeding maxChars", () => {
    const files: FileDiff[] = Array.from({ length: 100 }, (_, i) => ({
      path: `very/long/path/to/file${i}.go`,
      status: "modified" as const,
      additions: 10,
      deletions: 5,
      patch: "x".repeat(500),
    }));
    const result = buildDiffSummary(files, 500);
    expect(result).toContain("truncated");
    expect(result.length).toBeLessThan(1000);
  });

  it("omits patch when it would exceed budget", () => {
    const files: FileDiff[] = [
      { path: "a.go", status: "modified", additions: 1, deletions: 1, patch: "x".repeat(5000) },
    ];
    // Use a maxChars that fits the header but not the patch
    const result = buildDiffSummary(files, 200);
    expect(result).toContain("patch omitted for size");
  });

  it("handles files without patches", () => {
    const files: FileDiff[] = [
      { path: "binary.png", status: "added", additions: 0, deletions: 0 },
    ];
    const result = buildDiffSummary(files);
    expect(result).toContain("binary.png");
    expect(result).toContain("added");
  });
});
