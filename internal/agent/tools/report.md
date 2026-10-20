Load the full text of a report that was produced or imported earlier in this session.

Every `Agent` call returns its output with a header naming a report id (`rpt_` followed by eight hex characters). The user can also import a report from a file, and it gets an id the same way. The text stays recorded for the whole session even after a compaction drops it from the conversation, and this tool brings it back verbatim.

Usage:
- Use the id exactly as it appeared in the report header, in the compaction summary, or in the message where the user gave it to you. Never invent or guess an id.
- If the report is still visible in the conversation, read it there instead of calling this tool.
- Only reports from this conversation, and from the conversation it was forked from, can be loaded.
- An unknown id comes back as an error listing the ids this session actually has; pick the right one from that list rather than guessing again.
- The loaded text is returned in full, which can be long. Load a report when you need its detail, not to check whether it exists.
