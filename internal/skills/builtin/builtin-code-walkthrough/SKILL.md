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

A full walkthrough is delivered as a **self-contained HTML page** built from
this skill's template (section 6). A quick one is a short chat answer.

Write the walkthrough in the user's language, including the page's own
headings and labels. Keep code identifiers as-is.

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

- **Quick**: an in-chat answer of about one screen, no file. Use it when the
  user asks a narrow "how does X work" question.
- **Full**: the HTML page described in sections 5 and 6, plus a short chat
  summary. Use it for onboarding, whole-module, or whole-project requests, and
  whenever the user asks for a walkthrough, tour, document, page, or HTML.
  When unsure between the two, choose Full only if the target has more than
  one part or flow.

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
visible whenever it is not "verified". In the HTML page, mark the other two
with the template's `inferred` and `unknown` tags.

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

## 5. Content Structure

Adapt this outline to the target. Drop sections that would be empty or
generic. Never pad a section to fill the outline. For a full walkthrough each
row is one `<section>` of the HTML template.

| Section (template id) | Content |
| --------------------- | ------- |
| Header | Title, the commit it describes, and **In short**: two to four sentences on what it does, who uses it, and the core idea. |
| Key terms (`terms`) | Term, meaning here, defined at `path:line`. Only terms the reader will meet. |
| Map (`map`) | Part, what it owns, where to start reading. Optional traced diagram. |
| How it works (`flow`) | The real-world scenario, followed step by step. Each step: what happens, `path:line`, and **Data now**. Add at most two more flows, only if they differ meaningfully. |
| Parts in depth (`parts`) | Only for parts the flow does not already explain: explanation, trimmed real snippet, and a "why this matters" note. |
| State and invariants (`state`) | Each rule the code relies on, where it is enforced, what breaks if violated. |
| Why it is built this way (`why`) | Each decision with its source: commit, comment, doc, or an explicit inference label. |
| Pitfalls and open questions (`pitfalls`) | Risks and surprises with `path:line`, and unknowns with how to find out. |
| Where to start reading (`start`) | Three to five places in order, each with the reason. |

For a **quick** walkthrough, keep only the short summary, the concrete flow,
and where to start reading, in the chat.

## 6. Build the HTML Page

The template is `angela://skills/builtin-code-walkthrough/template.html`. It
is one file with inline CSS, no script, and no external requests, so it opens
offline from disk. It follows the viewer's light or dark setting, fits a phone
width, and prints cleanly.

### Procedure

1. **Read the template** with the Read tool. The tool prefixes each line with
   a number and a `|`. Those prefixes are not part of the file, so never copy
   them into the output.
2. **Gather the facts first.** Take the commit from
   `git rev-parse --short HEAD` and the branch from
   `git branch --show-current`. If `git status --short` is not empty, say the
   page also reflects uncommitted changes. Finish sections 2 to 4 before you
   write any HTML.
3. **Write the page** with the Write tool in one pass. Start from the whole
   template and replace each `<!-- SLOT: ... -->` marker plus the placeholder
   content after it. Every placeholder is wrapped in `«` and `»`.
   Keep the `<style>` block and the section structure. Delete a whole section
   and its `NAV` entry when it would be empty. Repeat a block as often as the
   content needs, such as table rows, steps, parts, and bullets.
4. **Translate the template's own text** into the user's language: eyebrow,
   headings, table headers, nav labels, the `Data now` label, the footer, and
   the `inferred` and `unknown` tag words. Set the `lang` attribute to match.
5. **Save it** to the path the user gave. Otherwise use
   `.angela/walkthroughs/<target-slug>.html` under the project. That
   directory is Angela's default data directory, which ignores itself in git,
   so the page does not dirty the working tree. If `options.data_directory`
   points elsewhere, use that directory instead. Never write the page into a
   tracked source directory unless the user asked for it there.
6. **Run the self-check** in section 7, then tell the user the file path and
   give a short chat summary: the core idea in two or three sentences and the
   reading order. Do not paste the whole page into the chat.

When the user asks for a change to an existing page, edit that file directly.
Do not rebuild it from the template, and keep its commit line accurate.

### Writing the content

- **Snippets are real code.** Copy the lines from the file as the Read tool
  showed them, trimmed to what matters, usually under 25 lines. Put the
  `path:start-end` in the `figcaption`. Do not retype from memory or tidy the
  code. If you elide lines, write a language-appropriate comment such as
  `// ...` where they were.
- **Escape code.** Replace `&` with `&amp;`, `<` with `&lt;`, and `>` with
  `&gt;` inside every `<pre>` and `<code>`. An unescaped `<` silently eats the
  rest of the line in a browser.
- **Pin only what is not obvious.** Add `<b class="pin">N</b>` at the end of a
  code line and a matching `<li>` in the `annot` list, in the same order. Use
  at most four pins per snippet, and explain what the line protects or who
  relies on it. Never narrate what the line plainly says.
- **Tag the evidence.** Use `<span class="tag inferred">` and
  `<span class="tag unknown">` next to the claim they qualify, followed by what
  the inference rests on or how to find out. Untagged claims must be verified
  and carry a `path:line` nearby.
- **Cite consistently.** Every `path:line` goes in `<code class="cite">`.
  Paths are relative to the repository root.
- **Show data as data.** Put the shape in the `Data now` block as a trimmed
  type, a JSON-like literal, or a one-line description. Use realistic values
  from the scenario, not `foo`.
- **Callouts are rare.** Use `.callout` for one "why this matters" per part and
  `.callout.warn` for a real hazard. A page where everything is a callout has
  none.
- **One idea per step.** Four to eight steps in the main flow. If a step needs
  a paragraph and two snippets, split it.

### Diagrams

A diagram earns its place only when it shows a mechanism a cold reader would
otherwise assemble from prose. If a sentence says it faster, write the
sentence.

- **Draw what you traced.** Every box is a real symbol, package, or process
  and every arrow is a call, message, or data movement you followed. Label the
  arrows with the verb: `calls`, `publishes`, `reads`. An unlabeled arrow only
  says "related somehow".
- **Match the stakes.** A one-hop question is three boxes. A flow that crosses
  a queue needs the queue, the producer, the consumer, and the direction.
  Draw what the reader's understanding turns on, not an inventory.
- **Highlight one thing.** Use the `focus` class on the part the page is
  about, and `node` on the rest.
- **Use the template's classes only.** Color every shape through `node`,
  `focus`, `edge`, `arrowhead`, and `soft`. Never write a hex value, a named
  color, `white`, or `black` into an SVG, because the page renders in both
  light and dark and a fixed color fails in one of them.
- **Size by `viewBox`.** Give each SVG a fixed `viewBox` with a width of
  600 to 800, and no `width` or `height` attributes. The CSS scales it, so
  text lands at roughly 11 to 16 pixels. Leave generous padding, and align
  boxes to a grid with even gaps.
- **Keep text short.** Labels of one to three words. Explanations go in the
  `figcaption`, not in the drawing.
- **Markers need unique ids.** Give each SVG its own marker id such as
  `arrow-map` and `arrow-flow`, because ids are shared across the page.
- **Accessible.** Each SVG has `role="img"` and an `aria-label` stating its
  claim, and sits in a `<figure>` whose `figcaption` repeats that claim.
- **Self-contained.** No `<script>`, `<style>`, `<foreignObject>`, or external
  image inside an SVG.

### Layout rules to keep

- The page must never scroll sideways. Wide tables and SVGs already sit in a
  `.scroll` wrapper, so wrap any new wide element the same way.
- Do not add JavaScript, web fonts, CDN links, or remote images. A
  walkthrough must work offline and must not leak the project's code to a
  third party.
- Do not hardcode colors outside the `:root` token blocks. If a hue needs
  tuning, change it in both the light block and the dark block.

## 7. Self-Check Before Delivering

Run this pass on the draft. Fix what fails; do not just note it.

**Content**

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

**HTML file** (skip for a quick, in-chat walkthrough)

- **No leftovers**: search the saved file for `SLOT:`, `«`, and `»`. Any hit
  is an unfilled placeholder. Fix every one.
- **Navigation**: each nav link matches an `id` that exists, and no section
  without a nav entry remains.
- **Balanced markup**: every opened tag is closed, and each `<pre>` and
  `<code>` has its `<`, `>`, and `&` escaped.
- **Colors**: no hex value, `white`, or `black` appears in any SVG.
- **Language**: the page's own labels match the user's language, and `lang`
  is set.
- **Version line**: the commit hash is the one from `git rev-parse`, and any
  uncommitted state is stated.

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
- Publishing the template with its placeholders, sample rows, or demo diagram
  still in it.
- A page that is mostly prose with no snippet, or mostly snippets with no
  explanation of what the reader should notice.
- Pasting the full HTML into the chat instead of saving the file.
