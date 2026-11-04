const assert = require("node:assert/strict");
const fs = require("node:fs");

function parseReview(output) {
    const starts = [...output.matchAll(/\{\s*"body"\s*:/g)].reverse();
    for (const { index: start } of starts) {
        let depth = 0;
        let quoted = false;
        let escaped = false;
        for (let end = start; end < output.length; end++) {
            const char = output[end];
            if (quoted) {
                if (escaped) escaped = false;
                else if (char === "\\") escaped = true;
                else if (char === '"') quoted = false;
            } else if (char === '"') {
                quoted = true;
            } else if (char === "{") {
                depth++;
            } else if (char === "}" && --depth === 0) {
                try {
                    const review = JSON.parse(output.slice(start, end + 1));
                    if (typeof review.body === "string" && Array.isArray(review.comments)) {
                        return review;
                    }
                } catch {
                    // A malformed diagnostic candidate should not hide an earlier review.
                }
                break;
            }
        }
    }
    throw new Error("No complete review object found");
}

function testParser() {
    const expected = {
        body: 'final {with braces}, "quotes", and \\slashes',
        comments: [{ path: "test.go", line: 42, side: "RIGHT", body: 'nested } and "quote"' }],
    };
    const json = JSON.stringify(expected);
    assert.deepEqual(parseReview(json), expected);
    assert.deepEqual(
        parseReview('prefix {"body":"diagnostic object","comments":[]} then ' + json + "\ntrailing log"),
        expected,
    );
    assert.deepEqual(parseReview(json + '\n{"status":"done"}'), expected);
    assert.deepEqual(parseReview(json + '\n{"body": <invalid>}'), expected);
    assert.throws(() => parseReview("no review object"), /No complete review object/);
    assert.throws(() => parseReview('{"body":"incomplete"'), /No complete review object/);
}

const args = process.argv.slice(2);
if (args[0] === "--self-test") {
    testParser();
    args.shift();
}
if (args.length === 0) {
    throw new Error("At least one review assertion is required");
}

const review = parseReview(fs.readFileSync(0, "utf8"));
for (const expression of args) {
    const check = new Function("review", `return (${expression});`);
    assert.equal(check(review), true, `Failed review assertion: ${expression}`);
}
console.log(`Passed ${args.length} review assertions`);
