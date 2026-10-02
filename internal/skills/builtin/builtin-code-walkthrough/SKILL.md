---
name: builtin-code-walkthrough
description: >-
  Use when the user wants to understand unfamiliar code: a walkthrough or
  guided tour of a project, module, package, or feature; how a flow works end
  to end; onboarding to a codebase; or building context before a change,
  refactor, or review. Applies whenever the intent is understanding existing
  code, even if the word "walkthrough" is never used. Not for a single symbol
  lookup, debugging one specific failure, or planning a change.
---

# Code Walkthrough

The goal is a mental model the reader can use: after reading, they can
predict where a piece of logic lives and what happens when the system runs.
Two quality bars apply to everything you produce:

- **Understandable**: a reader who knows the language but not this repository
  can follow the main flow without opening files.
- **Accurate**: every factual claim traces back to code, docs, or history you
  actually read in this session. Anything else is labeled as inference.

Write the walkthrough in the user's language. Keep code identifiers as-is.

## 1. Scope It Without Interrogating the User

Infer the shape of the walkthrough from the request:

| Shape   | Question it answers                         | Backbone                                  |
| ------- | ------------------------------------------- | ----------------------------------------- |
| Project | How is this whole system organized?         | Map of parts, then one main flow          |
| Module  | What does this package own, and how is it used? | Public surface, internal parts, one flow |
| Flow    | What happens when X occurs?                 | Entry point to final effect, step by step |

Use any stated purpose to choose emphasis. Maintaining code calls for
invariants and pitfalls. Preparing a change calls for the path that change
touches. Reviewing calls for the context around the diff. Skip what the user
says they already know.

Start working immediately. Ask a question only when the target is ambiguous
in a way that changes the work, such as "the auth code" matching three
unrelated modules. One focused question beats a questionnaire.

Pick a depth:

- **Quick**: an in-chat answer of about one screen. Use it when the user asks
  a narrow "how does X work" question.
- **Full**: the complete structure in section 5. Use it for onboarding,
  whole-module, or whole-project requests.

## 2. Gather Evidence, Not Impressions

Work roughly in this order and stop as soon as you can answer the five
questions at the end of this section.

1. **Project-provided orientation.** Read AGENTS.md, CLAUDE.md, README,
   ARCHITECTURE or design docs, and decision records when present. Treat them
   as claims to verify, because docs drift from code.
2. **Entry points.** Find where execution or usage starts: `main`, CLI command
   registration, route tables, exported API, package index files, job or
   event handler registration. Read the build manifest for language,
   framework, and key dependencies.
3. **Structure.** List the target's directories and files. Note sizes only to
   spot where the weight is.
4. **Trace, don't skim.** From an entry point, follow the real call path.
   Prefer go-to-definition, find-references, and call-hierarchy tools when an
   LSP is available. Fall back to Grep for the exact symbol name. Read the
   function bodies on the path, not just their names.
5. **Real usage.** Find how the code is actually used: README and docs
   usage, CLI help, example configs, and real call sites in production code.
   This is where the concrete scenario in section 4 comes from.
6. **Tests, for edge cases.** Tests confirm intended behavior at the edges.
   Do not build the walkthrough's scenario from them. Test inputs are often
   synthetic, mocked, or chosen to hit corner cases, not typical use.
7. **History, for the "why".** Use `git log` on the relevant files, and
   `git log -L` or `git blame` on surprising lines. Commit messages, linked
   issues, and nearby comments are the only reliable evidence of intent.

For broad scans across many files, delegate to a read-only exploration agent
with a specific question and an explicit breadth. Before you repeat any
agent claim that matters, open the cited lines and confirm them yourself.

You have read enough when you can answer these:

1. What problem does this code solve, and for whom?
2. What are its main parts, and what does each one own?
3. How does one representative request, command, or event move through it?
4. What state does it keep, and which invariants and conventions protect it?
5. Where is it fragile, surprising, or easy to misuse?

Do not try to read everything. Depth on the main path beats breadth across
every file.

## 3. Accuracy Rules

Every claim in the output falls into one of three levels. Make the level
visible whenever it is not "verified".

- **Verified**: you read the code that does it. Cite `path:line`.
- **Inferred**: based on names, patterns, or partial reading. Say what the
  inference rests on, for example "inferred from the call site only".
- **Unknown**: you could not determine it. Say so, and say how to find out.

Specific rules:

- **Cite what you read.** Use the line numbers the read tool printed, not
  numbers you counted. If you cannot cite a structural or behavioral claim,
  do not state it as fact.
- **Rationale is the most commonly fabricated claim.** State a "why" as fact
  only when a comment, commit message, doc, or test says it. Otherwise write
  "likely because X, not confirmed" and name what suggests it.
- **Resolve indirection honestly.** Interfaces, dependency injection,
  registries, event buses, reflection, config-driven wiring, code generation,
  and macros all hide the real callee. Find the concrete implementations and
  the place that selects among them. If several exist, name them and say what
  picks one.
- **Do not generalize from one instance.** "All handlers validate input"
  requires checking more than one handler. Otherwise say "the two handlers I
  checked".
- **Names are not behavior.** Confirm what a function does from its body.
  `validateUser` might also write to the database.
- **Code beats docs.** When they disagree, describe the code and point out the
  discrepancy.
- **Generated or vendored code** should be identified as such, with its
  source of truth such as the schema, the `.proto` file, or the SQL file.
- **Run something when it settles a doubt that matters.** An existing test, a
  `--help` output, or a dry-run is fine. Never run anything with side effects
  on real data, services, or the user's working tree.

## 4. Make It Understandable

- **Lead with the mental model.** Open with two to four sentences: what it
  does, who calls it, and the core idea. An analogy is welcome when it is
  accurate, and harmful when it is only approximately right.
- **Anchor on a real-world scenario.** Pick the thing a real user or caller
  most commonly does with this code, in a realistic setting. Follow it end
  to end with plausible production values: a real-looking config, input,
  or request, not `foo`, mocks, or test fixtures. "A user runs
  `app sync --dry-run` against a repo with two remotes" teaches more than
  "a request arrives". This trace is the center of the walkthrough.
- **Keep the scenario faithful.** Every step must be what the code really
  does for that input. If a realistic setting takes a branch you did not
  read, read it or mark the step as inferred.
- **Show the data, not just the calls.** At each important step, say what
  the data looks like: its type, its key fields, and what changed. Readers
  lose track of data far more often than control flow.
- **Explain connections before internals.** Cover who owns which state, where
  objects are created and destroyed, where concurrency or process boundaries
  sit, and how errors propagate across parts.
- **Define vocabulary once.** When the code uses domain terms or overloaded
  words such as "session", "context", or "agent", define them before using
  them. Only include terms the reader will actually meet.
- **Explain what the code does not say directly.** Do not translate code line
  by line. A reader can read a loop. They cannot easily see the invariant
  the loop protects or the caller that relies on it.
- **Keep the map small.** Show five to nine parts. Group the rest under
  "supporting code" with one line. Name a file only when the reader needs to
  go there.
- **Diagrams must be traced.** Every box is a real symbol, package, or
  process. Every arrow is a call, import, or message you followed. Never draw
  a generic layered diagram from convention. A short numbered list is often
  clearer than a diagram.
- **Calibrate to the reader.** If they say they know Go but not React, explain
  React ideas through Go equivalents. Skip what they told you they know.
- **End with a reading order.** Tell the reader which three to five places to
  open first, and why, so they can continue alone.

## 5. Output Structure

Adapt this template to the target. Drop sections that would be empty or
generic. Never pad a section to fill the template.

```markdown
# <Target> walkthrough

> Describes commit <short hash>.

**In short.** <Two to four sentences: what it does, who uses it, core idea.>

## Key terms
| Term | Meaning here | Defined at |
| ---- | ------------ | ---------- |

## Map
| Part | Owns | Start reading at |
| ---- | ---- | ---------------- |

<Optional small diagram of traced relations only.>

## How it works: <real-world scenario>
1. <What happens, in plain words.> `path:line`
   Data now: <shape or key values>.
2. ...

<Repeat for at most two more flows, only if they differ meaningfully.>

## State, invariants, and conventions
- <Rule the code relies on, where it is enforced, what breaks if violated.>

## Why it is built this way
- <Decision.> Source: <commit, comment, doc, or "inferred from ...">.

## Pitfalls and open questions
- <Risk or surprise, with `path:line`.>
- <Unknown, with how to find out.>

## Where to start reading
1. `path:line`: <why this first>.
```

For a **quick** walkthrough, keep only the short summary, the concrete flow,
and where to start reading.

Deliver the walkthrough in the chat by default. Write it to a file only when
the user asks. In that case, use the path they give or the project's existing
docs convention, and keep the commit line so readers know which version the
document describes.

## 6. Self-Check Before Delivering

Run this pass on the draft. Fix what fails; do not just note it.

- **Citations**: spot-check cited lines, especially any you did not re-read
  after a long exploration. Each must still show what you claim.
- **Symbols**: every function, type, and file you name exists, spelled
  exactly as in the code.
- **Arrows**: every relation in the map or diagram was traced, not assumed.
- **Rationale**: every "why" has a source or an explicit inference label.
- **Vagueness**: find steps like "then it processes the data" or "handles the
  request" and replace them with what actually happens.
- **Realism**: the scenario is something a real user or caller would do, with
  realistic values. Replace any test fixture, mock, or placeholder input.
- **Cold reader**: someone who knows the language but not this repository
  could follow the main flow from your text alone.
- **Signal**: delete lines that only restate a file or directory name without
  adding meaning.

## Anti-Patterns

- Walking through a unit test or a toy input instead of real usage.
- A directory listing with a one-line guess per folder presented as an
  architecture overview.
- Generic layer diagrams such as "api to service to model" drawn from
  convention instead of traced from code.
- Confident rationale with no source.
- Turning the walkthrough into a code review. Flag risks only where they
  matter for understanding or correctness.
- Asking a battery of questions before reading any code.
- Exhaustive coverage of every file at the cost of explaining the main path.
