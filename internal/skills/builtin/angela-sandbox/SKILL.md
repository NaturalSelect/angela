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

Or interactively in the TUI with the **Enter Sandbox** command-palette entry
(Ctrl+P or `/`, then type `sandbox`; hidden once a sandbox is active or inside a container), which opens
a form for the same filesystem/network settings and a confirmation step
before applying them. Entering the sandbox from the dialog also switches
permissions to yolo mode, since the sandbox already enforces the boundaries
yolo would skip.

## CLI Flags

| Flag | Notes |
|---|---|
| `--sandbox` | Enables the sandbox. Required for `--sandbox-rw`, `--sandbox-ro` and `--sandbox-no-network`. |
| `--sandbox-rw <dir>` | Additional read-write directory. Repeatable. |
| `--sandbox-ro <dir>` | Additional read-only directory. Repeatable. |
| `--sandbox-no-network` | Blocks outbound network for spawned commands. Linux only — fails at startup on macOS. |
| `--no-docker-sandbox` | Ignore an auto-detected Docker/OCI container as "already sandboxed" and apply Landlock on top of it anyway. Also governs the **Enter Sandbox** dialog, so it is legal without `--sandbox`. |

The flags are registered on `angela`, `angela run` and `angela agent create`
(not on `angela server`), and have no environment-variable or `angela.json`
equivalent. Using `--sandbox-rw`, `--sandbox-ro` or `--sandbox-no-network`
without `--sandbox` itself is an error at startup (`--sandbox-rw requires
--sandbox`).

## Default Profile

Without `--sandbox-rw`/`--sandbox-ro`, the default profile:

- Makes the working directory, Angela's data directory, the global config
  directory, and the system temp directory **read-write**.
- Makes the rest of the filesystem (`/`) **read-only** — so everything on
  the machine stays readable. `--sandbox-rw`/`--sandbox-ro` only add to the
  defaults and cannot remove `/`; only the **Enter Sandbox** dialog can drop
  that row.
- Leaves outbound network **unrestricted**.
- Always keeps `/dev/null` writable and `/dev/zero`, `/dev/full`,
  `/dev/random` and `/dev/urandom` readable.

This default set is automatically widened with any directory or single file
an unconditional filesystem allow rule under `permissions.rules` already
covers, so an edit the permission policy already approves without a prompt
doesn't turn into a confusing sandbox I/O error.

## Platform Backends

| Platform | Mechanism | Notes |
|---|---|---|
| Linux (bare) | Landlock (best-effort) | If the kernel doesn't support Landlock, it silently does nothing (no error, no log) — it fails open, so don't assume enforcement on an old kernel. |
| Linux (Docker/OCI container) | Treated as already sandboxed | Detected via `/.dockerenv` or `docker`/`containerd`/`kubepods` in `/proc/1/cgroup`. `--sandbox` (including `-rw`, `-ro` and `-no-network`) is a silent no-op unless `--no-docker-sandbox` is passed, in which case Landlock applies on top. |
| macOS | Seatbelt (`sandbox_init(3)`) | Filesystem only. `--sandbox-no-network` is refused — an already-sandboxed macOS process can't apply a second, tighter profile to only its own children. |
| Other | None | `--sandbox` fails at startup with `failed to enter sandbox: unsupported operation`. |

### Network Blocking (Linux)

`--sandbox-no-network` doesn't use Landlock's network rules. Commands run by
the embedded shell (the Bash tool, hooks, shebang scripts) are started through
a launcher that installs a seccomp filter returning `EPERM` for `connect`,
`bind`, `listen`, `accept`, `sendto`, `sendmsg` and `sendmmsg`. That also
blocks loopback, Unix sockets and DNS for those commands. It does **not**
cover Angela's own provider requests, MCP stdio servers, LSP servers or the
Git tool, and it doesn't depend on Landlock kernel support.

## Irreversibility

Entering the sandbox is **irreversible for the life of the process**: once
applied, access can only be narrowed further, never widened, and a second
`EnterSandbox` call is a no-op. The CLI `--sandbox` is
independent of `--yolo` — it protects against mistakes yolo mode would
otherwise allow straight through. The filesystem restriction is applied
before the database, LSP and MCP servers start, so it covers all of them.

## Client-Server Mode

`--sandbox` and `--no-docker-sandbox` are **not supported** when
`ANGELA_CLIENT_SERVER` is set to a true value (daemon mode); they fail with
`--sandbox is not supported in client-server mode ... run without
ANGELA_CLIENT_SERVER instead`, and the other `--sandbox-*` flags are ignored
there. Restricting the daemon process would restrict every workspace it
serves. `angela server` itself has no sandbox flags, and entering a sandbox is
never available for a daemon-hosted workspace, including from the **Enter
Sandbox** dialog. Run Angela in local mode instead if you need the sandbox.

## Recipes

### Confine writes to the working directory (the rest stays readable)

```bash
angela run --sandbox "refactor this module"
```

### Allow writing to a build output directory outside the working directory

```bash
angela run --sandbox --sandbox-rw /var/build-output "run the build"
```

(The system temp directory is already writable by default.)

### Fully air-gap commands from the network

```bash
angela run --sandbox --sandbox-no-network "run the test suite"
```

Angela's own provider requests still work; only commands the agent runs
through its shell lose network access.

### Force Landlock even inside a container

```bash
angela --sandbox --no-docker-sandbox
```

## Troubleshooting

**A `--sandbox-rw`/`--sandbox-ro` path (or a permission rule's implied path)
doesn't exist** — Angela logs "Sandbox path does not exist, restriction will
not apply to it", but that happens before log setup, so it normally never
reaches `angela logs`. Both backends silently drop rules for missing paths,
so create the directory first if you need it enforced.

**`--sandbox --sandbox-no-network` fails at startup on macOS** — this
combination is refused there by design; drop `--sandbox-no-network` on
macOS or apply network restriction at a different layer (e.g. a permission
deny rule with `mode: "domain"`).

**`--sandbox` fails with `unsupported operation`** — the platform has no
implemented backend (only Linux and macOS are supported). On Linux a kernel
without Landlock does not fail; it silently enforces nothing.

**`--sandbox` fails in daemon mode** — expected; see Client-Server Mode
above. Run without `ANGELA_CLIENT_SERVER` set.

**A tool call that should be scoped in still fails with an I/O error** — the
path may fall outside the read-write set (everything is readable by default,
so this is usually a write). Add it with `--sandbox-rw`, or check whether a
permission rule already covers it (those are folded in automatically) but the
concrete path differs from what you expect (e.g. a symlink resolving
elsewhere).
