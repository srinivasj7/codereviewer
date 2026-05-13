// Package fixtures holds canned values used by tests.
package fixtures

import "codereviewer/internal/ports"

// SmokeDiffContent is a small unified diff used by smoke and budget tests.
const SmokeDiffContent = `diff --git a/src/handler.ts b/src/handler.ts
index abcdef0..1234567 100644
--- a/src/handler.ts
+++ b/src/handler.ts
@@ -10,7 +10,7 @@ export function handle(req: Request): Response {
-  const user = req.body.user;
+  const user = req.body.user ?? '';
   if (!user) {
     return error400('user required');
   }
`

// SmokeDiff returns the UnifiedDiff for the smoke test.
func SmokeDiff() ports.UnifiedDiff {
	return ports.UnifiedDiff{
		HeadSha: "abc123",
		BaseSha: "000000",
		Content: SmokeDiffContent,
		Files: []ports.DiffFile{{
			Path:   "src/handler.ts",
			Status: "modified",
			Hunks: []ports.DiffHunk{{
				StartLine: 10,
				EndLine:   16,
				Content:   SmokeDiffContent,
			}},
		}},
	}
}
