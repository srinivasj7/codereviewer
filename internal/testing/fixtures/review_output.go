package fixtures

// SmokeSingleSuggestion is a one-comment LLM JSON response whose
// category is "suggestion" (so the status check stays at success).
const SmokeSingleSuggestion = `[
  {
    "file": "src/handler.ts",
    "start_line": 10,
    "end_line": 12,
    "comment": "Consider an explicit null-check instead of the ?? coercion so the contract is documented.",
    "category": "suggestion",
    "severity": "low"
  }
]`

// SmokeWithBug includes a bug-category comment which causes the
// status check to fail. Useful for testing the failure path.
const SmokeWithBug = `[
  {
    "file": "src/handler.ts",
    "start_line": 12,
    "end_line": 12,
    "comment": "User input is used without validation; potential injection vector.",
    "category": "bug",
    "severity": "high"
  }
]`
