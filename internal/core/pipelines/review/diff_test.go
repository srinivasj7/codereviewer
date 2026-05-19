package review

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const sampleDiff = `diff --git a/src/cart.ts b/src/cart.ts
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/src/cart.ts
@@ -0,0 +1,5 @@
+import config from "./config.json";
+
+export function add(a: number, b: number) {
+  return a + b;
+}
diff --git a/src/utils.ts b/src/utils.ts
index 2222222..3333333 100644
--- a/src/utils.ts
+++ b/src/utils.ts
@@ -10,4 +10,7 @@ export function clamp(n: number, lo: number, hi: number): number {
   if (n < lo) return lo;
   if (n > hi) return hi;
   return n;
+}
+export function neg(n: number): number {
+  return -n;
 }
`

func TestValidRightLines_PicksUpAddedAndContextLines(t *testing.T) {
	got := validRightLines(sampleDiff)

	// src/cart.ts is a new file: lines 1..5 are all added.
	for ln := 1; ln <= 5; ln++ {
		assert.True(t, got["src/cart.ts"][ln], "src/cart.ts line %d should be valid", ln)
	}
	assert.False(t, got["src/cart.ts"][6], "src/cart.ts line 6 is past EOF")

	// src/utils.ts hunk starts at line 10; right-side rows are 10,11,12,13,14,15,16.
	// Line 13 is the trailing `}` of clamp (context), 14 starts the new fn.
	for ln := 10; ln <= 16; ln++ {
		assert.True(t, got["src/utils.ts"][ln], "src/utils.ts line %d should be valid", ln)
	}
	assert.False(t, got["src/utils.ts"][17], "src/utils.ts line 17 is past the hunk")
	assert.False(t, got["src/utils.ts"][9], "src/utils.ts line 9 is before the hunk")
}

func TestCommentLinesValid(t *testing.T) {
	v := validRightLines(sampleDiff)

	// Single-line at valid end_line.
	assert.True(t, commentLinesValid(v, "src/cart.ts", 0, 3))
	// Multi-line span both in hunk.
	assert.True(t, commentLinesValid(v, "src/cart.ts", 3, 5))
	// File not in diff at all.
	assert.False(t, commentLinesValid(v, "src/never.ts", 0, 1))
	// End_line past hunk.
	assert.False(t, commentLinesValid(v, "src/cart.ts", 0, 99))
	// Start_line outside hunk, end_line inside — still invalid.
	assert.False(t, commentLinesValid(v, "src/cart.ts", 99, 3))
}

func TestParseHunkRightStart(t *testing.T) {
	cases := map[string]int{
		"@@ -0,0 +1,5 @@":             1,
		"@@ -10,4 +10,7 @@ ctx text":  10,
		"@@ -1 +1 @@":                  1,
		"@@ -10,4 +20 @@":              20,
	}
	for hdr, want := range cases {
		got, ok := parseHunkRightStart(hdr)
		assert.True(t, ok, "header %q should parse", hdr)
		assert.Equal(t, want, got, "header %q", hdr)
	}
	_, ok := parseHunkRightStart("not a hunk header")
	assert.False(t, ok)
}
