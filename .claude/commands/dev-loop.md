# Development Loop

Iterative development cycle: test → analyze → implement → test → enhance tests → test → repeat.

Use this skill when making non-trivial changes to the codebase — refactoring, adding features, fixing bugs, or improving code quality.

## Arguments

- `$ARGUMENTS` — Description of what to work on (e.g. "add integer/number distinction in schema generation", "refactor the auth middleware"). If empty, analyze the codebase and propose improvements.

## Phase 0: Understand

Before changing anything:

1. **Read** all files relevant to the task. Never modify code you haven't read.
2. **Understand** existing patterns, conventions, and architecture.
3. **Identify** what tests exist and what they cover.

## Phase 1: Baseline Test

Run the full test suite and capture which specific tests pass by name.

```
go test -v -count=1 ./... 2>&1 | tee /tmp/test_output_baseline.txt
go test -v -count=1 ./... 2>&1 | grep "^--- PASS:" | sort > /tmp/tests_baseline.txt
```

Every test MUST pass before any changes are made. If tests fail, stop and fix them first — that's the task now.

The file `/tmp/tests_baseline.txt` is the contract. Every named test in that file must still pass after every subsequent phase.

## Phase 2: Coverage Snapshot

Record baseline coverage. This is the floor — coverage must not drop below this after any iteration.

```
go test -coverprofile=/tmp/cover_baseline.out ./... && go tool cover -func=/tmp/cover_baseline.out
```

Note the total coverage percentage.

## Phase 3: Analyze

Based on the task ($ARGUMENTS):

- If a specific task was given: analyze the relevant code, identify what needs to change, and plan the implementation.
- If no task was given: analyze the full codebase for issues — bugs, missing features, code quality problems, testability concerns, correctness gaps. Propose improvements ranked by impact.

Present findings to the user before proceeding. Get confirmation on what to implement.

## Phase 4: Implement

Make the changes. Follow these rules:

- **Small, focused changes** — one concern per iteration. Don't bundle unrelated changes.
- **Preserve existing behavior** unless explicitly changing it.
- **Don't add speculative features** — only what was agreed in Phase 3.
- **Don't "improve" code you're not changing** — no drive-by refactors.

## Phase 5: Post-Change Test

Run the full test suite and verify every baseline test still passes by name.

```
go test -v -count=1 ./... 2>&1 | tee /tmp/test_output_current.txt
go test -v -count=1 ./... 2>&1 | grep "^--- PASS:" | sort > /tmp/tests_current.txt
```

Now check that every test from the baseline is still present and passing:

```
comm -23 /tmp/tests_baseline.txt /tmp/tests_current.txt
```

If this produces any output, those are tests that passed before but no longer pass. This is a **hard stop**.

**Rules:**

1. **Default assumption: your change broke something.** Fix your code, not the test.
2. **Exception: the test was asserting buggy behavior that you intentionally fixed.** In this case you may update the test — but you MUST explicitly call out to the user what changed, what the old assertion was, what the new one is, and why. Never silently update a test to make it pass.
3. **Never delete or rename a previously-passing test** to make the suite green. That hides regressions behind bookkeeping.

New tests appearing in `tests_current.txt` that weren't in `tests_baseline.txt` are fine — that's additive. The contract is one-directional: nothing from the baseline may disappear or fail.

## Phase 6: Enhance Tests

Now improve test coverage for the changes you made:

1. Run coverage analysis:
   ```
   go test -coverprofile=/tmp/cover_current.out ./... && go tool cover -func=/tmp/cover_current.out
   ```

2. Identify uncovered paths in the code you changed or added.

3. Add tests for:
   - Happy paths for new functionality
   - Edge cases and boundary conditions
   - Error paths and invalid inputs
   - Integration between new and existing code

4. Do NOT add tests for:
   - Code you didn't change (unless coverage was already missing and it's relevant)
   - Stubs, dead code, or unreachable paths
   - Filesystem-dependent code that would create fragile tests

5. **Every new test you write must pass immediately.** If a new test fails on arrival, that is a bug — either in your implementation (go back to Phase 4) or in your test. You just wrote the code; you should know what it does. A new test that fails is not "to be fixed later" — it means something is wrong right now.

6. After all new tests pass, **update the baseline** — the new tests are now part of the contract too:
   ```
   go test -v -count=1 ./... 2>&1 | grep "^--- PASS:" | sort > /tmp/tests_baseline.txt
   ```
   The baseline only ever grows. It never shrinks.

## Phase 7: Final Test

Run the full suite one last time and verify the baseline contract:

```
go test -v -count=1 ./... 2>&1 | grep "^--- PASS:" | sort > /tmp/tests_final.txt
comm -23 /tmp/tests_baseline.txt /tmp/tests_final.txt
```

If `comm` produces any output — baseline tests are missing or failing. This includes both the original tests AND any tests added in Phase 6. Hard stop, fix before proceeding.

Then check that coverage did not drop below the Phase 2 baseline:

```
go test -coverprofile=/tmp/cover_final.out ./... && go tool cover -func=/tmp/cover_final.out
```

## Phase 8: Vet

Run static analysis to catch issues the tests won't:

```
go vet ./...
```

Fix any findings.

## Phase 9: Iterate or Stop

Ask: is there more to do for this task?

- If the task from $ARGUMENTS is complete → stop and summarize what was done.
- If there are more improvements to make within the scope → go back to Phase 3.
- If you've run out of meaningful changes → stop. Don't invent work.

## Summary

When done, report:
- What changed (briefly)
- Baseline tests: all still passing (confirm)
- Test count: before → after (new tests added)
- Coverage: before → after
- Any issues discovered but not addressed (with rationale)
