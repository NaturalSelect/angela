---
name: builtin-code-walkthrough
description: >-
  Use when the user wants to understand unfamiliar code: a walkthrough or
  guided tour of a project, module, package, or feature; how a flow works end
  to end; onboarding to a codebase; or building context before a change,
  refactor, or review. Recovers the design document the code embodies, the
  reverse of co-design. Applies whenever the intent is understanding existing
  code, even if the word "walkthrough" is never used. Not for a single symbol
  lookup, debugging one specific failure, or planning a change.
user-invocable: true
disable-model-invocation: true
---

# Code Walkthrough

A walkthrough is the reverse of co-design. Co-design starts from a requirement
and ends in a design document that code is built from. A walkthrough starts
from the code and recovers the design document it embodies, in the same shape,
so a reader gets the problem, the idea, the mechanisms, and the reasoning
without opening files.

Two quality bars apply to everything you produce:

- **Understandable**: a reader who knows the language but not this repository
  can follow the main flow without opening files.
- **Accurate**: every factual claim traces back to code, docs, or history you
  actually read in this session. Anything else is labeled as inference.

A full walkthrough is a **markdown document with SVG images** saved as files
(section 6). A quick one is a short chat answer.

Write the walkthrough in the user's language, including its headings and
labels. Keep code identifiers as-is.

## 1. The Process Runs Backward

| Co-design (requirement to code)            | Walkthrough (code to document)                  |
| ------------------------------------------ | ----------------------------------------------- |
| Align on the requirement                   | Recover the background from what the code guards |
| Choose the direction (the thesis)          | Recover the thesis from the mechanisms          |
| Refine into mechanisms                     | Recover each mechanism, edge cases first        |
| Write the document, worked example last    | Trace one real scenario first                   |
| The built code                             | Scope the target and read it from the outside in |

Read the right column from the bottom up: scope and read the code (sections 2
and 3), then the four recovery steps of section 4 in order. You investigate
bottom-up and write top-down. The document reads like a design document:
Background, then Design, then Trade-offs. The order you found things in does
not matter to the reader.

One difference decides how you write. A co-design document records choices the
user accepted. A walkthrough only observes the result. The problem, the
challenges, and the trade-offs were never written down unless a comment,
commit, or doc says so, so they are reconstructed and must be labeled that
way (section 3).

## 2. Scope It, Then Gather Evidence

### Scope without interrogating the user

Infer the shape from the request:

| Shape   | Question it answers                              | The mechanisms are...                   |
| ------- | ------------------------------------------------ | --------------------------------------- |
| Project | How is this whole system organized?              | The major subsystems and how they combine |
| Module  | What does this package own, and how is it used?  | The ideas inside it and its public surface |
| Flow    | What happens when X occurs?                      | The ideas the path passes through, in order |

Use any stated purpose to choose emphasis. Maintaining code calls for
guarantees and edge cases. Preparing a change calls for the mechanisms that
change touches. Reviewing calls for the context around the diff. Skip what the
user says they already know.

Start working immediately. Ask a question only when the target is ambiguous in
a way that changes the work, such as "the auth code" matching three unrelated
modules. One focused question beats a questionnaire.

Pick a depth:

- **Quick**: an in-chat answer of about one screen, no file. Use it when the
  user asks a narrow "how does X work" question. Keep only the thesis in two
  or three sentences, the real scenario step by step, and where to start
  reading.
- **Full**: the document in section 5, saved as a file, plus a short chat
  summary. Use it for onboarding, whole-module, or whole-project requests, and
  whenever the user asks for a walkthrough, tour, document, or markdown. When
  unsure, choose Full only if the target has more than one part or flow.

### Gather evidence, not impressions

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
5. **Real usage.** Find how the code is actually used: README and docs usage,
   CLI help, example configs, and real call sites in production code. This is
   where the scenario in section 4 comes from.
6. **Tests, for edge cases.** Tests confirm intended behavior at the edges.
   Do not build the scenario from them. Test inputs are often synthetic,
   mocked, or chosen to hit corner cases, not typical use.
7. **History, for the "why".** Use `git log` on the relevant files, and
   `git log -L` or `git blame` on surprising lines. Commit messages, linked
   issues, and nearby comments are the only reliable evidence of intent.

For broad scans across many files, delegate to a read-only exploration agent
with a specific question and an explicit breadth. Before you repeat any agent
claim that matters, open the cited lines and confirm them yourself.

You have read enough when you can answer these:

1. What problem does this code solve, and for whom?
2. What are its mechanisms, and what does each one own?
3. How does one representative request, command, or event move through them?
4. What state does it keep, and which guarantees protect it?
5. Where is it fragile, surprising, or easy to misuse?

Do not try to read everything. Depth on the main path beats breadth across
every file.

## 3. Accuracy Rules

Every claim in the output falls into one of three levels. Make the level
visible whenever it is not "verified".

- **Verified**: you read the code that does it. Cite `path:line`.
- **Inferred**: based on names, patterns, or partial reading. Start the
  sentence with a bold **Inferred** label and say what the inference rests on,
  for example "from the call site only".
- **Unknown**: you could not determine it. Start with a bold **Unknown** label
  and say how to find out.

Translate the labels into the user's language and use them the same way
throughout.

Specific rules:

- **Cite what you read.** Use the line numbers the read tool printed, not
  numbers you counted. If you cannot cite a structural or behavioral claim, do
  not state it as fact.
- **Reconstructed sections default to inferred.** The problem, the challenges,
  the reason an obvious alternative falls short, and every trade-off are
  recovered, not recorded. State one as fact only when a comment, commit
  message, doc, or test says it, and name that source. Otherwise write
  "likely because X, not confirmed" and name what suggests it. Rationale is
  the most commonly fabricated claim in a walkthrough.
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
- **Generated or vendored code** should be identified as such, with its source
  of truth such as the schema, the `.proto` file, or the SQL file.
- **Run something when it settles a doubt that matters.** An existing test, a
  `--help` output, or a dry-run is fine. Never run anything with side effects
  on real data, services, or the user's working tree.

## 4. Recover the Design

Do these in order. The first two produce the evidence everything else leans on.

### Step 1: Trace one real scenario

Pick the thing a real user or caller most commonly does with this code, in a
realistic setting, and follow it end to end with plausible production values: a
real-looking config, input, or request, not `foo`, mocks, or test fixtures.
"A user runs `app sync --dry-run` against a repo with two remotes" teaches more
than "a request arrives".

- Every step must be what the code really does for that input. If a realistic
  setting takes a branch you did not read, read it or mark the step inferred.
- At each important step, record what the data looks like: its type, its key
  fields, and what changed. Readers lose track of data far more often than of
  control flow.
- Then trace the failure that matters most: the one the code takes the most
  care over, or the one that would hurt most.

This trace becomes the worked example. It is also the skeleton the next steps
hang on.

### Step 2: Recover each mechanism

A mechanism is an idea that can be explained and argued correct on its own,
such as a lock protocol or a retry queue. Cut along ideas, not packages. A
project has five to nine at most. Group the rest under supporting code with one
line.

For each mechanism, recover in this order, which is the reverse of how
co-design builds one. Leave out what a mechanism does not have:

1. **Edge cases**: the inputs and moments the code takes care over, each with
   a concrete case, and the ones it knowingly leaves open.
2. **Guarantees**: what always holds, where it is enforced, and what breaks if
   it is violated.
3. **Flows**: the normal path, then each failure path.
4. **Types and rules**: the data structures grouped by the role that holds
   them, and the rules over them such as a state machine or a mapping table.
5. **Core idea**: why it works, in a few sentences.
6. **What it meets**: which challenge it answers and why the obvious way would
   fall short. This is the reconstructed part, so label it as section 3 says.

Explain connections before internals: who owns which state, where objects are
created and destroyed, where concurrency or process boundaries sit, and how
errors cross parts. Do not translate code line by line. A reader can read a
loop. They cannot easily see the guarantee the loop protects or the caller
that relies on it.

### Step 3: Recover the thesis

State, in one or two sentences, the few mechanisms the solution rests on and
how they combine, such as "two-phase commit across partitions, with multi-mode
locks held under strict two-phase locking".

Check it against the scenario trace in both directions. Every mechanism the
thesis names must appear in the trace, and every mechanism the trace relied on
must appear in the thesis. A mismatch means a mechanism was cut wrongly or
missed.

### Step 4: Recover the background

Work out what the mechanisms were built to survive.

- **Problem**: who has it and the concrete cases that show it. Prefer the
  words of a README, issue, or commit over your own.
- **Challenges**: what any solution to this problem must meet, one line each,
  such as "two clients may change the same entry at once". Derive them from
  what the mechanisms guard against. Work to be done, such as moving code, is
  not a challenge.

Then check coverage. A challenge that no mechanism meets means you misread the
code or the code has a gap, so report which. A mechanism that meets no
challenge means a challenge is missing or the mechanism is incidental.

Behavior the code visibly does not handle goes to Follow-up work.

## 5. The Document

The document has the same shape as a co-design document, so it can later seed a
co-design session for a change. Write it for a reader who was not here, in the
user's language: reasoning in prose, the rest in the forms below. Describe the
design in its own terms rather than as an index into the code, and give each
mechanism a pointer to where it lives.

Give each idea the form that makes it checkable. Prose lets both sides agree
while picturing different things.

- A type with its fields, in the repository's language, for data. Show it as a
  trimmed real snippet when the exact shape matters.
- A table for rules and mappings.
- A sequence diagram image for interactions.
- A worked example with real values for how the pieces combine.

Do not retell function bodies. A snippet earns its place when it shows a
guarantee or a rule that prose cannot state as precisely. Keep it under about
25 lines.

### Outline

Adapt it to the target. Drop sections that would be empty or generic. Never
pad a section to fill the outline.

| Section | Content |
| ------- | ------- |
| Header | Title, the commit it describes, a note that it was recovered from code and not agreed with the authors, and **In short**: two to four sentences on what it does, who uses it, and the core idea. |
| 1. Background | The problem with its concrete cases, then the challenges, one line each. Define overloaded domain words at first use, and add a short term table only when the reader will meet more than three. |
| 2. Design | The thesis and an image of how the mechanisms combine. Then one subsection per mechanism with whichever of these it has, in this order: what it meets, core idea, types, rules, flow, guarantees, edge cases, where it lives. Then the worked example. |
| 3. Trade-offs | Each significant choice, what it gave up, and its source or an inference label. |
| 4. Risks and preconditions | What must be true for the code to work, where it is fragile or easy to misuse with `path:line`, and what you could not verify. |
| 5. Follow-up work | Unknowns with how to find out, challenges nothing meets, and behavior the code visibly leaves out. Omit if empty. |
| 6. Where to start reading | Three to five places in order, each with the reason, so the reader can continue alone. |

The worked example is the trace from step 1. Write it as numbered steps, each
with what happens, the `path:line`, and the data now. Give the normal path
first, then the failure that matters most. Include a sequence image when more
than two components take part.

Quality rules for the content:

- **Lead with the mental model.** The In short paragraph comes first. An
  analogy is welcome when it is accurate, and harmful when it is only
  approximately right.
- **One idea per step.** Four to eight steps in the main flow. If a step needs
  a paragraph and two snippets, split it.
- **Explain the mechanisms, not the file list.** A directory listing with a
  guess per folder is not a design.
- **Calibrate to the reader.** If they say they know Go but not React, explain
  React ideas through Go equivalents.
- **Cite consistently.** Put every `path:line` in backticks, relative to the
  repository root. Snippets are real code copied from what the read tool
  showed, with `path:start-end` in the line above. If you elide lines, write a
  language-appropriate comment such as `// ...` where they were.

## 6. Write the Files

### Where things go

- The document goes to the path the user gave. Otherwise use
  `.angela/walkthroughs/<target-slug>.md` under the project. That directory is
  Angela's default data directory, which ignores itself in git, so the files do
  not dirty the working tree. If `options.data_directory` points elsewhere, use
  that directory instead.
- Images go in a sibling directory named `<target-slug>-images/`, one SVG file
  per image, named `NN-short-name.svg`. Link them with relative paths such as
  `![alt](<target-slug>-images/01-thesis.svg)` so the document keeps working
  when the pair is moved together.
- Never write into a tracked source directory unless the user asked for it
  there.

### Procedure

1. **Gather the facts first.** Take the commit from `git rev-parse --short
   HEAD` and the branch from `git branch --show-current`. If `git status
   --short` is not empty, say the document also reflects uncommitted changes.
   Finish section 4 before you write anything.
2. **Decide the section list and the images.** Decide which outline sections
   the document keeps, in order, and which images it needs, so the shape is
   final before anything is written.
3. **Write the skeleton** with the Write tool: the title, the commit note, the
   In short paragraph, and a final line containing only
   `<!-- NEXT-SECTION -->`.
4. **Add one section at a time.** For each kept section in order, finish it,
   then use the Edit tool to replace `<!-- NEXT-SECTION -->` with that section
   followed by `<!-- NEXT-SECTION -->` again. When a section embeds an image,
   write the SVG file with the Write tool first, then add the section that
   links it. One Edit adds one complete section. Never combine several
   sections into one call, and never leave a section half filled to come back
   to it. Tell the user in a few words which section you just added, so they
   can follow the document growing.
5. **Close the document.** After the last section, use the Edit tool to delete
   the `<!-- NEXT-SECTION -->` line. Run the self-check in section 8, then tell
   the user the file path and give a short chat summary: the thesis in two or
   three sentences and the reading order. Do not paste the whole document into
   the chat.

Building in pieces keeps each tool call small, shows progress while it
happens, and means a failed call costs one section instead of the whole
document.

When the user asks for a change to an existing walkthrough, edit that file
directly. Do not rebuild it, and keep its commit note accurate.

## 7. Images

The document carries images. A full walkthrough has at least the image of how
the mechanisms combine and a sequence image of the worked example when more
than two components take part. Add a state machine or a data-layout image when
a mechanism turns on one. If the target is a single mechanism, one image is
enough. Stay near six images or fewer. If a sentence says it faster than a
drawing, write the sentence.

Draw the images yourself as SVG files. Do not use an image generation tool for
them, because a generated picture invents boxes and arrows nobody traced.

### What to draw

- **Draw what you traced.** Every box is a real symbol, package, or process
  and every arrow is a call, message, or data movement you followed. Label the
  arrows with the verb: `calls`, `publishes`, `reads`. An unlabeled arrow only
  says "related somehow". Never draw a generic layered diagram from
  convention.
- **Match the stakes.** A one-hop question is three boxes. A flow that crosses
  a queue needs the queue, the producer, the consumer, and the direction.
  Draw what the reader's understanding turns on, not an inventory.
- **Highlight one thing.** Use the `focus` class on the part the image is
  about and `node` on the rest.
- **Sequence images** have one column per participant with a dashed lifeline,
  and one horizontal arrow per message, numbered to match the steps of the
  worked example in the text.
- **Keep text short.** Labels of one to three words. Explanations go in the
  caption below the image, not in the drawing.

### How to build one

Start every file from this skeleton and change only the viewBox size and the
shapes. Colors come only from the classes in the `style` block. The opaque
background rectangle keeps the image readable in both light and dark viewers,
so do not remove it and do not add other colors.

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 720 160" width="720" height="160">
  <title>«One sentence stating what the image shows.»</title>
  <style>
    text { font: 14px system-ui, -apple-system, "PingFang SC", "Microsoft YaHei", "Noto Sans CJK SC", sans-serif; fill: #1c1917; }
    .bg { fill: #ffffff; }
    .soft { fill: #57534e; }
    .node { fill: #ffffff; stroke: #1c1917; stroke-width: 1.5; }
    .focus { fill: #eef2ff; stroke: #4338ca; stroke-width: 2.5; }
    .edge { fill: none; stroke: #57534e; stroke-width: 1.5; }
    .life { fill: none; stroke: #a8a29e; stroke-width: 1; stroke-dasharray: 4 4; }
    .arrowhead { fill: #57534e; }
  </style>
  <defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse"><path class="arrowhead" d="M0 0 L10 5 L0 10 z"/></marker></defs>
  <rect class="bg" width="720" height="160"/>
  <rect class="node" x="20" y="50" width="160" height="60" rx="8"/>
  <text x="100" y="85" text-anchor="middle">«Caller»</text>
  <path class="edge" d="M180 80 H278" marker-end="url(#arrow)"/>
  <text class="soft" x="229" y="68" text-anchor="middle">«verb»</text>
  <rect class="focus" x="280" y="50" width="160" height="60" rx="8"/>
  <text x="360" y="85" text-anchor="middle">«Part»</text>
</svg>
```

Rules for every file:

- Use a `viewBox` between 600 and 800 wide, and set `width` and `height` to the
  same numbers. Leave generous padding and align boxes to a grid with even
  gaps.
- Nothing is rendered while you work, so size by arithmetic. A Latin character
  is about 8 units wide at 14px and a CJK character about 14 units. Keep every
  label inside its box with margin, and shorten it if it does not fit.
- No `<script>`, `<foreignObject>`, `<image>`, web font, or URL other than the
  `xmlns` attribute from the skeleton. The document must work offline and must
  not leak the project's code to a third party.

In the document, put each image on its own line as `![alt](path)` where the
alt text states the claim of the image, and put a one-sentence caption in
italics directly under it that says what to notice. Name the figure number in
the caption so the text can refer to it.

## 8. Self-Check Before Delivering

Run this pass on the draft. Fix what fails; do not just note it.

**Content**

- **Citations**: spot-check cited lines, especially any you did not re-read
  after a long exploration. Each must still show what you claim.
- **Symbols**: every function, type, and file you name exists, spelled exactly
  as in the code.
- **Arrows**: every relation in an image or table was traced, not assumed.
- **Rationale**: the problem, every challenge, every "why", and every
  trade-off has a source or an explicit inference label.
- **Coverage**: every challenge is met by a named mechanism, and every
  mechanism meets a challenge.
- **Thesis against trace**: the thesis and the worked example name the same
  mechanisms.
- **Vagueness**: find steps like "then it processes the data" or "handles the
  request" and replace them with what actually happens.
- **Realism**: the scenario is something a real user or caller would do, with
  realistic values. Replace any test fixture, mock, or placeholder input.
- **Cold reader**: someone who knows the language but not this repository
  could follow the main flow from your text alone.
- **Signal**: delete lines that only restate a file or directory name without
  adding meaning.

**Files** (skip for a quick, in-chat walkthrough)

- **No leftovers**: search the saved document for `NEXT-SECTION`, `«`, and
  `»`, and each SVG for `«` and `»`. Any hit is an unfilled placeholder or a
  forgotten marker. Fix every one.
- **Images exist**: every `![...](...)` path points to a file you wrote, and
  every SVG you wrote is linked from the document. Each has alt text and a
  caption.
- **Well-formed SVG**: every file parses as XML, for example with
  `python3 -c "import sys, xml.dom.minidom as m; m.parse(sys.argv[1])" FILE`
  or `xmllint --noout FILE` when one of them is installed. If neither is, read
  the file back and check that every tag is closed.
- **Colors**: no color appears in an SVG outside the skeleton's palette.
- **Language**: the document's own headings and labels match the user's
  language.
- **Commit note**: the commit hash is the one from `git rev-parse`, and any
  uncommitted state is stated.

## Anti-Patterns

- Walking through a unit test or a toy input instead of real usage.
- A directory listing with a one-line guess per folder presented as an
  architecture overview.
- Generic layer diagrams such as "api to service to model" drawn from
  convention instead of traced from code.
- Confident problem statements, challenges, or rationale with no source.
- Writing the document in the order you read the code instead of in design
  order, or retelling the code line by line instead of explaining mechanisms.
- Turning the walkthrough into a code review. Flag risks only where they
  matter for understanding or correctness.
- Asking a battery of questions before reading any code.
- Exhaustive coverage of every file at the cost of explaining the main path.
- Images that are decoration, generated by an image tool, or drawn from memory.
- Leaving the skeleton's placeholders, the section marker, or a sample box in a
  delivered file.
- Writing the whole document in a single tool call instead of a skeleton plus
  one section per edit.
- Pasting the full document into the chat instead of saving the file.
