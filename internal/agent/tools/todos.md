Update the todo list for the current session. Use it proactively and often to track progress and pending tasks.

- Use it for multi-step work: 3 or more distinct steps, tasks needing careful planning, or a list of things the user asked for. Skip it for a single straightforward task, trivial work, or purely conversational requests.
- Each call sends the complete list and replaces the previous one.
- Every task needs both `content` (imperative, e.g. "Fix authentication bug") and `active_form` (present continuous, e.g. "Fixing authentication bug").
- Keep exactly one task `in_progress` at a time, and mark it in_progress before you start working on it.
- Mark a task `completed` as soon as it is done. Do not batch completions.
- ONLY mark a task completed when it is fully accomplished. If tests are failing, the implementation is partial, you hit an unresolved error, or you could not find a needed file or dependency, leave it in_progress and add a new task describing what must be resolved.
- Remove tasks that are no longer relevant from the list entirely.
