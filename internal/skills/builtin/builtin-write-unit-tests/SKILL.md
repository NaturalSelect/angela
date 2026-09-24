---
name: builtin-write-unit-tests
description: >-
  Use when writing or improving tests: function or class unit tests,
  missing behavior or edge-case coverage, bug regression tests, or
  end-to-end tests for user journeys. Adapt to the target project's
  framework and conventions. Not for merely running existing tests or
  troubleshooting test infrastructure without adding or improving tests.
---

# Write Behavior-Driven Tests

Write tests that distinguish correct behavior from plausible faults, not
merely tests that pass. This skill covers function/class tests and E2E user
journeys. For integration tests, apply the relevant rules to the actual
boundary; do not claim broader coverage than the test provides.

## 1. Establish the Contract and Local Conventions

- Read applicable project instructions, the target code, nearby tests, and
  relevant test configuration. Identify the framework, naming, fixtures,
  helpers, mock-generation workflow, and focused test commands.
- Read relevant requirements, interfaces, and callers to establish intended
  behavior. Implementation shows where faults may occur; the contract
  determines what is correct. Do not copy current output as ground truth.
- Identify inputs, observable outputs, errors, state changes, and side effects.
  Ask when an unresolved ambiguity would materially change the assertions.
  Label explicitly requested characterization tests as observed behavior,
  not proof that the behavior is intended.
- Stay within the requested scope. Do not silently repair production code or
  introduce broad refactors when asked only to write tests. Ask before a
  necessary testability change unless production changes are already in scope.

For simple tasks, proceed directly without a formal design report. Briefly
explain the cases first when contracts are ambiguous, state is complex, or
consequences are significant.

## 2. Use Standard Test and Mock Facilities

Strongly prefer the project's established test framework, assertion
facilities, and mock tools over handwritten substitutes.

- Use framework discovery, parameterization, setup, teardown, and cleanup.
- Use supported assertions for values, errors, and asynchronous outcomes.
  Do not replace them with ad hoc if/throw/panic/exit checks, printed output,
  or manual inspection. Language-native assertions integrated with the
  framework are valid; do not add a library merely to avoid native syntax.
- When replacing a dependency, use the existing mocking library, generator,
  or framework-provided test doubles. Do not hand-roll call counters,
  argument logs, response queues, or a custom mock engine.
- Reuse established, maintained domain fakes where appropriate. A new
  behavioral fake is an exception requiring a concrete reason, not the
  default. Ordinary fixture data and simple framework callbacks do not
  require building a fake implementation.
- Verify generator commands from the repository; do not invent them or
  manually edit generated mocks.
- For simulated external services in E2E tests, prefer established protocol
  simulators or service-virtualization facilities over improvised fake servers.
- If tooling is absent or inadequate, propose the smallest suitable setup.
  Ask before adding dependencies or making project-wide configuration changes
  unless that work is already authorized.

Whether to mock is determined by the verification boundary. Once mocking is
needed, strongly prefer standard tools. Never mock away the behavior or core
chain the test is supposed to verify.

## 3. Micro Tests: Functions and Classes

### Design Cases Against Plausible Faults

For each case, identify the contract, triggering input or state, plausible
fault, and assertion that would detect it. Do not mechanically fill a
happy-path/error/boundary checklist.

Select distinct, relevant cases: representative success, outcome boundaries,
empty or invalid input, expected dependency failures, and meaningful side
effects. Consider time, cancellation, concurrency, or extreme values only
when they apply to the unit and its contract.

For classes, test operation sequences as well as individual methods:
repeated calls, state transitions, state after failure, and instance isolation.
Fresh-instance tests for every method can miss lifecycle bugs. A unit may be
a cohesive group of objects; do not test every private method separately.

Example: if amounts at or above 100 receive a discount of 10, cases
99 -> 99, 100 -> 90, and 101 -> 91 distinguish early activation, a strict
comparison instead of an inclusive one, and discounting only the exact
threshold. Each case earns its place by distinguishing a meaningful fault.

### Choose Independent Assertions

- Prefer small, independently derived expected values. Never repeat the
  production algorithm or call the same code to calculate expected results.
- Use contract-based invariants and input/output relations where useful, but
  understand their limits. Sorting twice being idempotent does not prove
  sorting works: also verify ordering and preservation of elements.
- Assert specific results, errors, and observable state or side effects.
  No exception or a non-null result is insufficient when more is promised.
- Assert error type, code, cause, or text according to the contract. Require
  exact wording only when wording matters.
- Avoid private structure and incidental call order. Assert interaction
  counts or ordering only when externally meaningful behavior requires them.

### Keep Tests Deterministic

Run real, lightweight internal logic. Isolate uncontrollable or expensive
boundaries with the standard facilities above. Unit tests should not require
live services, credentials, or public network access.

Control time and randomness when relevant. Give tests independent mutable
state and temporary resources; register cleanup. Restore process-wide state
and avoid parallel execution when that state is shared without safe isolation.
Use explicit synchronization and bounded waits, not arbitrary sleeps.

## 4. E2E Tests: Verify User Outcomes

### Declare the Real Boundary

Identify the real entry point, components that must execute, external systems
being simulated, and observable completion condition. An entry point may be
a browser, CLI, or public API; E2E is not synonymous with browser testing.

Do not intercept the core operation and return success while claiming the
real chain works. A simulated payment response can verify the application's
handling of that response, not the real payment provider.

### Select Journeys, Not Every Low-Level Case

Prioritize critical successful journeys, important rejection paths, and
contractual recovery behavior. Do not repeat every function boundary at E2E
level. Define initial state, action, and completion for each journey.

Verify the promised result, not merely a click, HTTP 200, or success banner.
For an order flow, check that the newly created order has the expected data
and can be retrieved when persistence is part of the promise. Associate the
result with this operation; stale fixtures must not satisfy the assertion.

Prefer user-visible results or public interfaces. Inspect internal storage
only when needed to verify the declared contract, not as a default shortcut.

### Make Execution Safe and Diagnosable

- Use dedicated test environments and independent test data. Do not rely on
  test order or incidental data already present in a shared account.
- Do not perform real payments, send real messages, delete user data, or
  otherwise affect production without explicit authorization for those effects.
- Wait for explicit completion conditions with time limits, not fixed sleeps.
- Clean up resources owned by the test even after failure; never delete
  unrelated data. Use the framework's lifecycle facilities.
- Preserve relevant logs, responses, screenshots, or correlation identifiers
  for diagnosis without exposing credentials or sensitive data.
- Do not use retries to hide instability. Preserve and report initial failures
  even when a retry passes.

## 5. Independent Adversarial Review

Use an independent reviewer for complex branching, stateful or recovery logic,
important permissions or data-integrity guarantees, recurring regressions,
and critical E2E journeys. Simple cases may use self-review.

Use a separate capable general-purpose agent when available and permitted by
current tool and delegation rules. The role requires judgment, not just code
search. Do not invent an agent type. If unavailable, state that review was
self-review, not independent review.

The goal is to find plausible contract violations that tests would miss—not
to enumerate every imaginable exception or claim exhaustive coverage.

### Pass A: Independent Fault Discovery

Give the reviewer requirements, interfaces, relevant callers, target code,
and constraints, but initially withhold the author's proposed case list and
rationale where possible to reduce anchoring. Ask for grounded boundary,
state, side-effect, and dependency-failure scenarios. Treat uncertain
contracts as questions rather than confirmed defects.

### Pass B: Challenge the Written Tests

Then provide the tests and ask:

- Micro: what plausible incorrect implementation could still pass?
- E2E: how could this pass while the user's task remains incomplete?
- Are assertions independent and discriminating? Are async assertions awaited?
- Have mocks replaced the very behavior being verified?
- Could handwritten assertion, mock, or runner infrastructure falsely pass,
  and should established framework facilities replace it?

E2E false-success examples include optimistic UI followed by request failure,
stale records satisfying assertions, non-persistent writes, rejected actions
that still produce side effects, and retries creating duplicate results.

For each finding require: contract basis, fault hypothesis, minimal trigger,
discriminating assertion, and current coverage (covered, missing, or unknown).
The reviewer reports findings; it does not independently edit production code.

The author classifies findings as valid gaps, already covered, contract
ambiguities, or out of scope. Address valid gaps and rerun affected tests.
Recheck fixes as needed, but keep review bounded; do not expand indefinitely.
Record unresolved issues instead of claiming all faults have been exhausted.

## 6. Write Readable Tests and Verify Their Effectiveness

Follow local naming and layout. Names should describe the scenario and
expected outcome. Keep setup, action, and assertions clear, fixtures minimal,
and behavior-relevant values visible. Use parameterization for structurally
similar cases. Multiple related assertions may establish one coherent outcome;
there is no arbitrary one-assertion-per-test rule.

Use snapshots or golden files when whole-output review is meaningful and
consistent with the project. Prefer targeted assertions for narrow behavior.
Inspect expected output and every relevant update; normalize only irrelevant
variation. Never regenerate expected files just to remove a failure.

### Run and Check What Actually Happened

1. Run the narrowest supported command and confirm the intended cases were
   discovered and executed. Exit code zero is not enough: check for zero
   matches, skips, conditional exclusions, and unawaited asynchronous work.
2. Ensure parameterized cases, mock verification, and lifecycle hooks run as
   intended under the framework.
3. Run the relevant surrounding suite, broadening according to scope and
   available environment. Use supported repeat, shuffle, or race checks when
   shared state or concurrency warrants them.
4. Apply project formatting and relevant lint checks. Review the diff for
   unrelated changes, leaked resources, and weakened assertions.

### Establish Fault-Detection Evidence

For ordinary tests, explain which plausible fault the assertion detects.
For regressions, confirm failure on the faulty version and success on the
fixed version when safely possible. Setup or compilation failure is not
proof that the test detects the behavior bug.

Do not revert user work or alter production code in the current working tree
merely to manufacture a red run. Use safe isolated facilities if available;
otherwise report that before-fix behavior was not empirically verified.
Use existing mutation-testing tools on a focused scope when appropriate;
do not introduce a new toolchain by default.

Classify failures before changing anything:

- Product defect: behavior violates the contract; retain the meaningful test
  and report the defect if fixing production code is outside scope.
- Test defect: an incorrect expectation, fixture, or mock needs correction
  based on the contract—not accommodation of a faulty implementation.
- Environment failure: missing services, configuration, or permissions block
  verification; they do not establish whether product behavior is correct.

Never delete, skip, weaken, refresh, or repeatedly retry tests merely to get
an all-green result. One passing run does not prove timing-dependent stability.

## Completion Report

Report behavior and important cases covered, the actual verification boundary,
files changed, commands actually run, outcomes and skips, review findings
addressed, and unresolved gaps or environmental limitations.

Distinguish reasoned fault detection from observed before/after evidence,
self-review from independent review, and mocked boundaries from real ones.
If execution was blocked, say so. Do not present proposed commands as executed
checks or claim exhaustive coverage.
