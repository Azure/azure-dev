/**
 * Shared sanitization utilities for doc-monitor action.
 *
 * IMPORTANT: Image stripping MUST precede link conversion,
 * because `![alt](url)` contains the `[alt](url)` sub-pattern.
 * Processing links first would partially consume image markup,
 * leaving a stray `!` prefix.
 */

/** Strip HTML tags from text. */
export function stripHtml(value: string): string {
  return value.replace(/<[^>]*>/g, "");
}

/** Remove markdown images `![alt](url)`. */
export function stripMarkdownImages(value: string): string {
  return value.replace(/!\[([^\]]*)\]\([^)]*\)/g, "");
}

/** Convert markdown links `[text](url)` to just the visible text. */
export function convertMarkdownLinks(value: string): string {
  return value.replace(/\[([^\]]*)\]\([^)]*\)/g, "$1");
}

/** Strip control characters, keeping `\n` (0x0A), `\r` (0x0D), and `\t` (0x09). */
export function stripControlChars(value: string): string {
  return value.replace(/[\x00-\x08\x0B\x0C\x0E-\x1F]/g, "");
}

/**
 * Full sanitization for AI-generated plain text.
 * Strips HTML, markdown images, converts markdown links to text,
 * and removes control characters.
 */
export function sanitizePlainText(value: string): string {
  return stripControlChars(convertMarkdownLinks(stripMarkdownImages(stripHtml(value))));
}

/**
 * Sanitization for embedding in markdown PR bodies.
 * Strips HTML and markdown images but keeps regular markdown links intact.
 */
export function sanitizeForMarkdown(value: string): string {
  return stripControlChars(stripMarkdownImages(stripHtml(value)));
}

/**
 * Escape injection vectors for markdown table cells.
 * Applies full plain-text sanitization plus table-specific escaping.
 */
export function escapeTableCell(value: string): string {
  return sanitizePlainText(value)
    .replace(/`/g, "")      // strip backticks (prevent code span injection)
    .replace(/\|/g, "\\|")  // escape pipe (table syntax)
    .replace(/\r/g, "")     // strip carriage returns
    .replace(/\n/g, " ");   // collapse newlines to spaces
}
