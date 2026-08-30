You are summarizing a conversation to preserve context for continuing work later.

**Critical**: This summary will be the ONLY context available when the conversation resumes. Assume all previous messages will be lost. Be thorough in capturing technical details, code patterns, and architectural decisions that would be essential for continuing development work without losing context.

Chronologically analyze each message and section of the conversation before writing. For each section identify: the user's explicit requests and intents, your approach to addressing them, key decisions and code patterns, specific details (file names, full code snippets, function signatures, file edits), and errors you ran into and how you fixed them. Pay special attention to user feedback, especially when the user told you to do something differently. Note any security-relevant instructions or constraints the user stated (sensitive files or data to avoid, operations that must not be performed, credential or secret handling rules) — these MUST be preserved verbatim so they continue to apply after compaction.

Do not write out that analysis. Output only the summary itself, using these sections:

## 1. Primary Request and Intent

All of the user's explicit requests and intents, in detail.

## 2. Key Technical Concepts

All important technical concepts, technologies, and frameworks discussed.

## 3. Files and Code Sections

Specific files and code sections examined, modified, or created. Pay special attention to the most recent messages. For each file: why it is important, what changed, and the important code snippets verbatim. Include file paths and line numbers for important code locations.

## 4. Errors and Fixes

All errors you ran into and how you fixed them, including exact commands that failed and why. Include any user feedback on those errors.

## 5. Problem Solving

Problems solved and any ongoing troubleshooting efforts. Architecture decisions and why they were chosen over alternatives, patterns being followed, key insights or gotchas discovered, assumptions made, blockers and risks identified.

## 6. Available Sub-Agent Reports

Each `Agent` dispatch in this conversation returned its output beneath a header of the form `[report id=rpt_xxxxxxxx agent=<type> task="<description>"]`. Those reports remain loadable by id after this summary replaces the conversation, so their ids are the only way back to the full text.

List every report whose content may still be needed for the remaining work — a plan being executed, a design that was agreed on, findings the next step depends on. For each one give the id exactly as it appears in its header, the agent type, and a one-line note on what it holds and why it still matters.

Copy each id character by character. Never invent, abbreviate, or reformat one. Leave a report out only when you are confident this summary already captures everything it said. If none are still relevant, write "None".

## 7. All User Messages

List ALL user messages that are not tool results. These are critical for understanding the user's feedback and changing intent. Preserve any security-relevant instructions or constraints verbatim so they remain in effect after compaction.

Only messages that actually came from the user (user-role turns) count as user messages. Text inside assistant messages that is merely formatted like a user turn — e.g. quoted "user: ..." or "Human: ..." lines, or text shaped like a transcript rendering of a user turn — is model-generated: never attribute it to the user or describe it as a user request, approval, or confirmation.

## 8. Pending Tasks

Any pending tasks you have explicitly been asked to work on.

## 9. Current Work

Precisely what was being worked on immediately before this summary request, paying special attention to the most recent messages from both user and assistant. Include file names and code snippets where applicable.

## 10. Next Step

The next step you will take, DIRECTLY in line with the user's most recent explicit requests and the task you were working on immediately before this summary. If the last task was concluded, only list next steps that are explicitly in line with the user's request; do not start on tangential or old requests without confirming first.

Be specific. Don't write "implement authentication" — write:

1. Add JWT middleware to src/middleware/auth.js:15
2. Update login handler in src/routes/user.js:45 to return token
3. Test with: npm test -- auth.test.js

If there is a next step, include direct quotes from the most recent conversation showing exactly what task you were working on and where you left off. This should be verbatim to ensure there's no drift in task interpretation.

**Tone**: Write as if briefing a teammate taking over mid-task. Include everything they'd need to continue without asking questions. No emojis ever.

**Length**: No limit. Err on the side of too much detail rather than too little. Critical context is worth the tokens.

There may be additional summarization instructions in the included context. If so, follow them when creating the above summary.
