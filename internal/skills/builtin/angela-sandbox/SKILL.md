---
name: angela-sandbox
description: Use when the user wants to restrict Angela or the commands it runs using OS-level sandboxing — configuring --sandbox, --sandbox-rw, --sandbox-ro, --sandbox-no-network flags, the /sandbox TUI command, understanding Landlock/Seatbelt/Docker enforcement, or troubleshooting a sandbox permission error.
---

# Angela Sandbox

Angela can restrict its own process (and everything it spawns) to a set of
filesystem paths using OS-level isolation, instead of relying only on
permission prompts. This is a defense-in-depth mechanism: even if a tool
call is approved, the sandbox stops it from touching anything outside the
allowed paths.

## How It Differs From Permissions

Permission rules (see `angela-permissions`) decide whether a call is
*approved*. The sandbox decides whether a call can *physically succeed* at
the OS level, regardless of approval. Use both together: permissions for
day-to-day prompting behavior, sandbox for a hard guarantee that survives a
model mistake or a bug in the permission logic.

## Enabling the Sandbox

```bash
angela --sandbox
angela run --sandbox "..."

# Add extra directories on top of the defaults (repeatable)
angela run --sandbox --sandbox-rw /extra/writable --sandbox-ro /extra/readable "..."

# Also block outbound network access for commands the agent runs
# (Angela's own provider requests are unaffected)
angela run --sandbox --sandbox-no-network "..."
```

Or interactively in the TUI with the `/sandbox` command, which opens a form
for the same filesystem/network settings before applying them.

## CLI Flags

| Flag | Notes |
|---|---|
| `--sandbox` | Enables the sandbox. Required for any of the flags below to have an effect. |
| `--sandbox-rw <dir>` | Additional read-write directory. Repeatable. |
| `--sandbox-ro <dir>` | Additional read-only directory. Repeatable. |
| `--sandbox-no-network` | Blocks outbound network for spawned commands. Linux only — fails at startup on macOS. |
| `--no-docker-sandbox` | Ignore an auto-detected Docker/OCI container as "already sandboxed" and apply Landlock on top of it anyway. |

Using any `--sandbox-*` refinement flag without `--sandbox` itself is an
error at startup.

## Default Profile

Without `--sandbox-rw`/`--sandbox-ro`, the default profile:

- Makes the working directory, Angela's data directory, the global config
  directory, and the system temp directory **read-write**.
- Makes the rest of the filesystem (`/`) **read-only**.
- Leaves outbound network **unrestricted**.

This default set is automatically widened with any directory an
unconditional filesystem allow rule under `permissions.rules` already
covers, so an edit the permission policy already approves without a prompt
doesn't turn into a confusing sandbox I/O error.

## Platform Backends

| Platform | Mechanism | Notes |
|---|---|---|
| Linux (bare) | Landlock | If the kernel doesn't support Landlock, degrades to a safe no-op instead of failing. |
| Linux (Docker/OCI container) | Treated as already sandboxed | Skipped unless `--no-docker-sandbox` is passed, in which case Landlock applies on top. |
| macOS | Seatbelt (`sandbox_init(3)`) | `--sandbox-no-network` is refused — an already-sandboxed macOS process can't apply a second, tighter profile to only its own children. |
| Other | None | `--sandbox` fails at startup with "not supported". |

## Irreversibility

Entering the sandbox is **irreversible for the life of the process**: once
applied, access can only be narrowed further, never widened, and a second
`EnterSandbox` call is a no-op. This is why `--sandbox` is independent of
`--yolo` — it protects against mistakes yolo mode would otherwise allow
straight through.

## Client-Server Mode

`--sandbox` is **not supported** when `ANGELA_CLIENT_SERVER=1` (daemon mode).
Restricting the daemon process would restrict every workspace it serves, and
entering a sandbox for a single daemon-hosted workspace on the user's behalf
isn't available. Run Angela in local mode instead if you need the sandbox.

## Recipes

### Restrict to the working directory only, no other reads

```bash
angela run --sandbox "refactor this module"
```

### Allow writing to a build output directory outside the working directory

```bash
angela run --sandbox --sandbox-rw /tmp/build-output "run the build"
```

### Fully air-gap commands from the network

```bash
angela run --sandbox --sandbox-no-network "run the test suite"
```

Angela's own provider requests still work; only commands the agent spawns
lose network access.

### Force Landlock even inside a container

```bash
angela --sandbox --no-docker-sandbox
```

## Troubleshooting

**"Sandbox path does not exist, restriction will not apply to it"** — a
configured `--sandbox-rw`/`--sandbox-ro` path (or a permission rule's
implied path) doesn't exist yet. Both backends silently drop rules for
missing paths, so create the directory first if you need it enforced.

**`--sandbox --sandbox-no-network` fails at startup on macOS** — this
combination is refused there by design; drop `--sandbox-no-network` on
macOS or apply network restriction at a different layer (e.g. a permission
deny rule with `mode: "domain"`).

**`--sandbox` fails with "not supported"** — the platform has no
implemented backend (only Linux and macOS are supported), or Landlock is
unavailable and something else in the chain failed. Check `angela logs`.

**`--sandbox` fails in daemon mode** — expected; see Client-Server Mode
above. Run without `ANGELA_CLIENT_SERVER` set.

**A tool call that should be scoped in still fails with an I/O error** — the
path may fall outside both `ReadWrite` and `ReadOnly`. Add it with
`--sandbox-rw`/`--sandbox-ro`, or check whether a permission rule already
covers it (those are folded in automatically) but the concrete path differs
from what you expect (e.g. a symlink resolving elsewhere).
