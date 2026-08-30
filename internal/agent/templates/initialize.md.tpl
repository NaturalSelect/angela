Analyze this codebase and create/update **{{.Config.Options.InitializeAs}}** to help future agents work effectively in this repository.

**First**: Check if directory is empty or contains only config files. If so, stop and say "Directory appears empty or only contains config. Add source code first, then run this command to generate {{.Config.Options.InitializeAs}}."

**Goal**: Document what an agent needs to know to work in this codebase - commands, patterns, conventions, gotchas, overall architecture, how components fit together

**Discovery process**:

1. Check directory contents with `LS`
2. Look for existing rule files (`.cursor/rules/*.md`, `.cursorrules`, `.github/copilot-instructions.md`, `claude.md`, `agents.md`) - only read if they exist, and carry over the important parts
3. If there is a README.md, read it and include the important parts
4. Identify project type from config files and directory structure
5. Find build/test/lint commands from config files, scripts, Makefiles, or CI configs
6. Read representative source files to understand code patterns, architecture, control/data flow
7. If {{.Config.Options.InitializeAs}} exists, read it and suggest improvements rather than starting over

**Content to include**:

- Essential commands (build, test, run, deploy, etc.) - whatever is relevant for this project
- Code organization and structure, application architecture and control/data flow
- Naming conventions and style patterns
- Testing approach and patterns
- Important gotchas or non-obvious patterns
- Any project-specific context from existing rule files

**What to leave out**:

- Do not repeat yourself, and do not include obvious instructions like "Provide helpful error messages to users", "Write unit tests for all new utilities", or "Never include sensitive information in code or commits"
- Do not list every component or file structure that can be easily discovered
- Do not include generic development practices that apply to any project
- Do not make up sections such as "Common Development Tasks", "Tips for Development", or "Support and Documentation" unless they are expressly included in files you actually read

**Note:** LLM agents learn and adapt to their context as they obtain it, so mentioning obvious details they would immediately pick up from reading a file or two is actively detrimental. Keep the principles of progressive disclosure in mind and focus primarily on non-obvious knowledge that saves the agent from trial-and-error discovery: gotchas, implicit conventions, commands with surprising flags, and context that isn't self-evident from the code in a single file.

**Format**: Clear markdown sections. Use your judgment on structure based on what you find. Aim for completeness over brevity - include everything an agent would need to know.

**Critical**: Only document what you actually observe. Never invent commands, patterns, or conventions. If you can't find something, don't include it.
