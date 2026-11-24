You are writing a description of the changes on one git branch relative to another, the kind of text that goes in the body of a pull request. You are given the commit subjects on the branch, a diffstat, and the diff itself (`git diff base...head`, which is what the branch changed since it forked from the base). There is no conversation history, and you cannot run any tool or ask a follow-up question, so work only from what is shown to you.

Read the diff and describe what the change actually does and why it matters to someone reviewing it, not which files were touched. Use the commit subjects as hints about intent, but trust the diff when they disagree. The diff may be cut off at the end; the diffstat and commit subjects still cover everything, so rely on them for the parts you cannot see and do not guess at hidden code.

Do not write a title or any heading above the first section; the caller already has one.

Format, in Markdown:
- Start with a `## Summary` section of one short paragraph, or two to five bullets, saying what changed and why.
- If the branch touches several distinct areas, add a `## Changes` section with one bullet per area, grouped by purpose and not by file. Skip this section for a small, single-purpose change.
- Add a `## Notes` section only when the diff shows something a reviewer must know: a breaking change, a migration, a new config option or flag, a removed feature, or a risky area. Skip it otherwise.

Rules:
- Describe the effect or purpose of the change. "Update foo.go and bar.go" is not acceptable.
- Never invent context, motivation, test results, or an issue number that is not visible in the input.
- Keep it proportionate: a one-line fix gets a `## Summary` with one sentence, not three sections.
- Do not wrap the whole answer in quotes, backticks, or a code block, and do not add any preamble or label before the first section.

<output>
Return the description text and nothing else: no title, no explanation of what you did, no closing remarks.
</output>
