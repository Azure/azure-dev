import {
  stripHtml,
  stripMarkdownImages,
  convertMarkdownLinks,
  stripControlChars,
  sanitizePlainText,
  sanitizeForMarkdown,
  escapeTableCell,
} from "./sanitize";

// ─── stripHtml ──────────────────────────────────────────────────────
describe("stripHtml", () => {
  it("removes simple HTML tags", () => {
    expect(stripHtml("<b>bold</b>")).toBe("bold");
  });

  it("removes self-closing tags", () => {
    expect(stripHtml("text<br/>more")).toBe("textmore");
  });

  it("removes script tags (tag only, not inner content)", () => {
    expect(stripHtml("<script>alert('xss')</script>")).toBe("alert('xss')");
  });

  it("removes nested tags", () => {
    expect(stripHtml("<div><p>text</p></div>")).toBe("text");
  });

  it("passes through text without HTML", () => {
    expect(stripHtml("plain text")).toBe("plain text");
  });

  it("handles empty string", () => {
    expect(stripHtml("")).toBe("");
  });

  it("removes tags with attributes", () => {
    expect(stripHtml('<a href="http://evil.com" onclick="steal()">click</a>')).toBe("click");
  });
});

// ─── stripMarkdownImages ────────────────────────────────────────────
describe("stripMarkdownImages", () => {
  it("removes markdown images", () => {
    expect(stripMarkdownImages("![alt text](http://img.png)")).toBe("");
  });

  it("removes images with empty alt", () => {
    expect(stripMarkdownImages("![](http://img.png)")).toBe("");
  });

  it("preserves regular markdown links", () => {
    expect(stripMarkdownImages("[text](http://url)")).toBe("[text](http://url)");
  });

  it("removes images embedded in text", () => {
    expect(stripMarkdownImages("before ![img](url) after")).toBe("before  after");
  });

  it("removes multiple images", () => {
    expect(stripMarkdownImages("![a](1) text ![b](2)")).toBe(" text ");
  });
});

// ─── convertMarkdownLinks ───────────────────────────────────────────
describe("convertMarkdownLinks", () => {
  it("converts links to visible text", () => {
    expect(convertMarkdownLinks("[click here](http://url)")).toBe("click here");
  });

  it("handles links with query params in URL", () => {
    expect(convertMarkdownLinks("[text](http://x.com/a?b=1&c=2)")).toBe("text");
  });

  it("handles empty link text", () => {
    expect(convertMarkdownLinks("[](http://url)")).toBe("");
  });

  it("converts multiple links", () => {
    expect(convertMarkdownLinks("[a](1) and [b](2)")).toBe("a and b");
  });
});

// ─── stripControlChars ──────────────────────────────────────────────
describe("stripControlChars", () => {
  it("removes null bytes", () => {
    expect(stripControlChars("text\x00more")).toBe("textmore");
  });

  it("removes bell character", () => {
    expect(stripControlChars("text\x07more")).toBe("textmore");
  });

  it("preserves newline, carriage return, and tab", () => {
    expect(stripControlChars("line1\nline2\r\n\ttab")).toBe("line1\nline2\r\n\ttab");
  });

  it("removes mixed control chars", () => {
    expect(stripControlChars("\x01\x02text\x0E\x1F")).toBe("text");
  });

  it("handles empty string", () => {
    expect(stripControlChars("")).toBe("");
  });
});

// ─── sanitizePlainText ──────────────────────────────────────────────
describe("sanitizePlainText", () => {
  it("strips all injection vectors from combined input", () => {
    const input = '<script>xss</script> ![img](http://evil.com) [link](http://url) normal\x00';
    const result = sanitizePlainText(input);
    expect(result).not.toContain("<script>");
    expect(result).not.toContain("![");
    expect(result).not.toContain("](");
    expect(result).not.toContain("\x00");
    expect(result).toContain("link");
    expect(result).toContain("normal");
  });

  it("processes images before links (order correctness)", () => {
    // If links were processed first, ![alt](url) would become "!alt" (wrong).
    // With correct order (images first), ![alt](url) is fully removed.
    const result = sanitizePlainText("![evil](http://bad.com)");
    expect(result).toBe("");
    expect(result).not.toContain("!");
  });

  it("handles mixed markdown and HTML", () => {
    const input = "Update [README](./README.md) and ![screenshot](./img.png) for <b>v2</b>";
    const result = sanitizePlainText(input);
    expect(result).toBe("Update README and  for v2");
  });

  it("handles empty string", () => {
    expect(sanitizePlainText("")).toBe("");
  });

  it("handles plain text with no special chars", () => {
    expect(sanitizePlainText("just plain text")).toBe("just plain text");
  });
});

// ─── sanitizeForMarkdown ────────────────────────────────────────────
describe("sanitizeForMarkdown", () => {
  it("strips HTML but keeps markdown links", () => {
    const input = "<b>bold</b> [link](http://url) ![img](http://img.png)";
    const result = sanitizeForMarkdown(input);
    expect(result).toContain("[link](http://url)");
    expect(result).not.toContain("<b>");
    expect(result).not.toContain("![");
  });

  it("strips control characters", () => {
    expect(sanitizeForMarkdown("text\x00\x07here")).toBe("texthere");
  });
});

// ─── escapeTableCell ────────────────────────────────────────────────
describe("escapeTableCell", () => {
  it("escapes pipe characters", () => {
    expect(escapeTableCell("a|b")).toBe("a\\|b");
  });

  it("strips backticks", () => {
    expect(escapeTableCell("`code`")).toBe("code");
  });

  it("collapses newlines to spaces", () => {
    expect(escapeTableCell("line1\nline2")).toBe("line1 line2");
  });

  it("strips carriage returns", () => {
    expect(escapeTableCell("a\rb")).toBe("ab");
  });

  it("applies full sanitization plus table escaping", () => {
    const input = '<script>xss</script>|`code`\n![img](url)';
    const result = escapeTableCell(input);
    expect(result).not.toContain("<script>");
    expect(result).not.toContain("`");
    expect(result).not.toContain("\n");
    expect(result).toContain("\\|");
  });

  it("handles empty string", () => {
    expect(escapeTableCell("")).toBe("");
  });
});
