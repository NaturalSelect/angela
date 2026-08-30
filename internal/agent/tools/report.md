Load the full text of a report an agent dispatch produced earlier in this session.

Every `Agent` call returns its output with a header naming a report id (`rpt_` followed by eight hex characters). The text stays recorded for the whole session even after a compaction drops it from the conversation, and this tool brings it back verbatim.

Usage:
- Use the id exactly as it appeared in the report header or in the compaction summary. Never invent or guess an id.
- If the report is still visible in the conversation, read it there instead of calling this tool.
- Only reports produced in the current session can be loaded.
- An unknown id comes back as an error listing the ids this session actually has; pick the right one from that list rather than guessing again.
- The loaded text is returned in full, which can be long. Load a report when you need its detail, not to check whether it exists.
