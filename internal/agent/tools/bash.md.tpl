Executes a given bash command and returns its output. Long-running commands automatically move to background and return a shell ID.

<cross_platform>
Uses mvdan/sh interpreter (Bash-compatible on all platforms including Windows).
Use forward slashes for paths: "ls C:/foo/bar" not "ls C:\foo\bar".
Common shell builtins and core utils available on Windows.
</cross_platform>

<execution_steps>
1. Directory Verification: If your command will create new directories or files, first use the LS tool to verify the parent directory exists and is the correct location
2. Security Check: Banned commands ({{ .BannedCommands }}) return error - explain to user. Safe read-only commands execute without prompts
3. Command Execution: Execute with proper quoting, capture output
4. Auto-Background: Commands exceeding 1 minute (default, configurable via `auto_background_after`) automatically move to background and return shell ID
5. Output Processing: Truncate if exceeds {{ .MaxOutputLength }} characters
6. Return Result: Include errors, metadata with <cwd></cwd> tags
</execution_steps>

<usage_notes>
- Command required, working_dir optional (defaults to current directory)
- Always quote file paths that contain spaces with double quotes (e.g., `cd "path with spaces/file.txt"`)
- Try to maintain your current working directory throughout the session by using absolute paths and avoiding usage of `cd`. You may use `cd` if the user explicitly requests it. In particular, never prepend `cd <current-directory>` to a `git` command — `git` already operates on the current working tree, and the compound triggers a permission prompt
- Each command runs in an independent shell; the working directory persists between commands but shell state does not
- Chain with ';' or '&&', avoid newlines except in quoted strings
- IMPORTANT: Avoid using this tool to run `find`, `grep`, `cat`, `head`, `tail`, `ls`, `sed`, `awk` and similar commands, unless explicitly instructed or after you have verified that a dedicated tool cannot accomplish your task. Use the appropriate dedicated tool instead — it provides a much better experience for the user and makes tool calls easier to review and approve:
  - File search: Use glob (NOT find or ls)
  - Content search: Use grep (NOT grep or rg)
  - Read files: Use view (NOT cat/head/tail)
  - Edit files: Use edit or multiedit (NOT sed/awk)
  - Write files: Use write (NOT echo >/cat <<EOF)
  - Communication: Output text directly (NOT echo/printf)
{{- if .RgAvailable }}
- Ripgrep (`rg`) is available on this machine, but still prefer the grep tool; reach for `rg` directly only when you need an aggregate the grep tool cannot produce
{{- end }}
</usage_notes>

<description_parameter>
Clear, concise description of what this command does in active voice. Never use words like "complex" or "risk" in the description - just describe what it does.

For simple commands (git, npm, standard CLI tools), keep it brief:
- ls → "List files in current directory"
- git status → "Show working tree status"
- npm install → "Install package dependencies"

For commands that are harder to parse at a glance (piped commands, obscure flags, etc.), add enough context to clarify what it does:
- find . -name "*.tmp" -exec rm {} \; → "Find and delete all .tmp files recursively"
- git reset --hard origin/main → "Discard all local changes and match remote main"
</description_parameter>

<sleep>
- Do not sleep between commands that can run immediately — just run them.
- If you must poll an external process, use a check command (e.g. `gh run view`) rather than sleeping first.
- If you must sleep, keep the duration short to avoid blocking the user.
- If waiting for a background task you started with `run_in_background`, use job_output to read it when you need it — do not poll in a sleep loop.
- Do not retry failing commands in a sleep loop — diagnose the root cause.
</sleep>

<background_execution>
- Set run_in_background=true to run commands in a separate background shell
- Returns a shell ID for managing the background process
- Use job_output tool to view current output from background shell
- Use job_kill tool to terminate a background shell
- IMPORTANT: NEVER use `&` at the end of commands to run in background - use run_in_background parameter instead
- Commands that should run in background:
  * Long-running servers (e.g., `npm start`, `python -m http.server`, `node server.js`)
  * Watch/monitoring tasks (e.g., `npm run watch`, `tail -f logfile`)
  * Continuous processes that don't exit on their own
  * Any command expected to run indefinitely
- Commands that should NOT run in background:
  * Build commands (e.g., `npm run build`, `go build`)
  * Test suites (e.g., `npm test`, `pytest`)
  * Git operations
  * File operations
  * Short-lived scripts
</background_execution>

<git_safety>
- NEVER update the git config
- NEVER run destructive git commands (push --force, reset --hard, checkout ., restore ., clean -f, branch -D) unless the user explicitly requests these actions. Taking unauthorized destructive actions is unhelpful and can result in lost work, so it's best to ONLY run these commands when given direct instructions
- Before running any destructive operation, consider whether there is a safer alternative that achieves the same goal. Only use destructive operations when they are truly the best approach
- NEVER skip hooks (--no-verify, --no-gpg-sign, etc) unless the user explicitly requests it. If a hook fails, investigate and fix the underlying issue
- NEVER run force push to main/master, warn the user if they request it
- CRITICAL: Always create NEW commits rather than amending, unless the user explicitly requests a git amend. When a pre-commit hook fails, the commit did NOT happen — so --amend would modify the PREVIOUS commit, which may result in destroying work or losing previous changes. Instead, after hook failure, fix the issue, re-stage, and create a NEW commit
- When staging files, prefer adding specific files by name rather than using "git add -A" or "git add .", which can accidentally include sensitive files (.env, credentials) or large binaries
- NEVER commit changes unless the user explicitly asks you to. It is VERY IMPORTANT to only commit when explicitly asked, otherwise the user will feel that you are being too proactive
- IMPORTANT: Never use git commands with the -i flag (like git rebase -i or git add -i) since they require interactive input which is not supported
- IMPORTANT: Do not use --no-edit with git rebase commands, as the --no-edit flag is not a valid option for git rebase
</git_safety>

<git_message_quality>
These rules apply whenever creating or updating commit messages, PR titles, or PR bodies:

- Messages MUST be understandable to someone unfamiliar with the codebase.
- Before creating or updating a message, verify this litmus test: a new contributor reading only the commit message or PR title/body should understand what problem this solves, why it matters, and the impact without opening files, reading the diff, or knowing internal code names.
- Avoid code identifiers, filenames, function names, and implementation details unless they are necessary for understanding the user-facing impact.
- Bad: "Add NameFromHex with sync.Once lazy init"
- Good: "Improve color name lookup performance while keeping startup fast"
</git_message_quality>

<commit_messages>
Commit messages are for future readers scanning history. Before committing:

- Follow <git_message_quality>.
- Draft a concise 1-2 sentence message focusing on why the change exists and what outcome it enables, not a list of files or implementation details.
- Use clear, accurate verbs ("add"=new capability, "update"=enhancement, "fix"=bug fix) and avoid generic messages.
- The first line MUST be under 72 characters.
- Add a body only when it is needed to explain the reasoning, tradeoffs, or important context; wrap body lines at 72 characters.
- If the change is internal-only, still describe the benefit or maintenance outcome rather than naming private code.
- Bad: "fix: nil pointer in session.go"
- Good: "fix: prevent session loading from crashing on missing metadata"
- Bad: "refactor: move PromptBuilder into internal/agent"
- Good: "refactor: make prompt assembly easier to maintain"
</commit_messages>

<git_commits>
Only create commits when requested by the user. If unclear, ask first. When the user asks you to create a new git commit, follow these steps carefully.

You can call multiple tools in a single response. When multiple independent pieces of information are requested and all commands are likely to succeed, run multiple tool calls in parallel for optimal performance. The numbered steps below indicate which commands should be batched in parallel.

Follow <git_safety> throughout.

1. Run the following bash commands in parallel:
   - Run a git status command to see all untracked files. IMPORTANT: Never use the -uall flag as it can cause memory issues on large repos.
   - Run a git diff command to see both staged and unstaged changes that will be committed.
   - Run a git log command to see recent commit messages, so that you can follow this repository's commit message style.

2. Analyze all staged changes (both previously staged and newly added) and draft a commit message:
   - Summarize the nature of the changes (eg. new feature, enhancement to an existing feature, bug fix, refactoring, test, docs, etc.). Ensure the message accurately reflects the changes and their purpose.
   - Do not commit files that likely contain secrets (.env, credentials.json, etc). Warn the user if they specifically request to commit those files.
   - Draft the message following <commit_messages>, and review the draft against the litmus test in <git_message_quality> before committing.
   - Do not run additional commands to read or explore code beyond git commands.

3. Run the following commands in parallel:
   - Add relevant untracked files to the staging area. Don't commit files already modified at conversation start unless relevant.
   - Create the commit{{ if or (eq .Attribution.TrailerStyle "assisted-by") (eq .Attribution.TrailerStyle "co-authored-by")}} with attribution{{ end }}. In order to ensure good formatting, ALWAYS pass the commit message via a HEREDOC:
     git commit -m "$(cat <<'EOF'
Commit message here.

{{ if .Attribution.GeneratedWith }}
💘 Generated with Angela
{{ end}}
{{if eq .Attribution.TrailerStyle "assisted-by" }}

Assisted-by: Angela:{{ .ModelID }}
{{ else if eq .Attribution.TrailerStyle "co-authored-by" }}

Co-Authored-By: Angela <angela@users.noreply.github.com>
{{ end }}
EOF
)"
   - Run git status after the commit completes to verify success. Note: git status depends on the commit completing, so run it sequentially after the commit.

4. If the commit fails due to a pre-commit hook, the commit did NOT happen. Fix the issue, re-stage, and create a NEW commit — never `--amend`.

Notes: If there are no changes to commit (no untracked files and no modifications), do not create an empty commit. Do not push to the remote repository unless the user explicitly asks you to. Do not use the todos or agent tools while committing.
</git_commits>

<pull_requests>
{{ if .GhAvailable -}}
Use the `gh` command via the bash tool for ALL GitHub-related tasks including working with issues, pull requests, checks, and releases. If given a GitHub URL use the `gh` command to get the information needed.
{{- end }}

IMPORTANT: When the user asks you to create a pull request, follow these steps carefully:

1. Run the following bash commands in parallel, in order to understand the current state of the branch since it diverged from the main branch:
   - Run a git status command to see all untracked files (never use -uall flag)
   - Run a git diff command to see both staged and unstaged changes that will be committed
   - Check if the current branch tracks a remote branch and is up to date with the remote, so you know if you need to push to the remote
   - Run a git log command and `git diff [base-branch]...HEAD` to understand the full commit history for the current branch (from the time it diverged from the base branch)

2. Analyze all changes that will be included in the pull request, making sure to look at all relevant commits (NOT just the latest commit, but ALL commits that will be included in the pull request!!!), and draft a pull request title and summary:
   - Follow <git_message_quality>
   - Keep the PR title short (under 70 characters)
   - Use the description/body for details, not the title
   - Ensure the summary reflects ALL changes since the base branch diverged
   - Do not run commands beyond git context, and check for sensitive information

3. Run the following commands in parallel:
   - Create new branch if needed
   - Push to remote with -u flag if needed
   - Create PR using `gh pr create` with the format below. Use a HEREDOC to pass the body to ensure correct formatting:
     gh pr create --title "the pr title" --body "$(cat <<'EOF'
## Summary
<1-3 bullet points>

## Test plan
<checklist of TODOs for verifying the pull request>
{{ if .Attribution.GeneratedWith }}
💘 Generated with Angela
{{- end }}
EOF
)"

Important:
- Do not use the todos or agent tools while creating a PR
- Never update git config
- Return the PR URL when you're done, so the user can see it

Other common operations:
- View comments on a GitHub PR: gh api repos/foo/bar/pulls/123/comments
</pull_requests>

<examples>
Good: pytest /foo/bar/tests
Bad: cd /foo/bar && pytest tests
</examples>
