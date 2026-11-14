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
| `--no-sandbox-container-permission` | Skip the automatic grant of detected Docker/Podman/Buildah directories and sockets (see Default Profile). Also governs the **Enter Sandbox** dialog, so it is legal without `--sandbox`. Same as `options.no_sandbox_container_permission`. |

The flags are registered on `angela`, `angela run`, `angela agent create` and
`angela acp`. Only `--no-sandbox-container-permission` has an `angela.json`
equivalent (`options.no_sandbox_container_permission`); the rest have no
environment-variable or `angela.json` equivalent. Using `--sandbox-rw`,
`--sandbox-ro` or `--sandbox-no-network`
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

When the `docker`, `podman` or `buildah` CLI is on `PATH`, the profile also
opens up what those tools need, limited to paths that exist:

- **Docker:** `$DOCKER_CONFIG` (else `~/.docker`) read-write, plus the sockets
  `/var/run/docker.sock`, `/run/docker.sock`, `$XDG_RUNTIME_DIR/docker.sock`,
  the Docker Desktop sockets under `~/.docker`, and any `unix://` path in
  `$DOCKER_HOST`.
- **Podman/Buildah:** `$XDG_DATA_HOME/containers` (else
  `~/.local/share/containers`), `$XDG_RUNTIME_DIR/containers`,
  `$XDG_RUNTIME_DIR/libpod`, `$XDG_CONFIG_HOME/containers` and `/var/tmp`
  read-write, plus `$XDG_RUNTIME_DIR/podman/podman.sock`,
  `/run/podman/podman.sock` and any `unix://` path in `$CONTAINER_HOST`. Only
  when running as root, the rootful storage under `/var/lib/containers/storage`,
  `/run/containers/storage` and `/run/libpod` is added too.

On Landlock ABI 9 or newer, connecting to any pathname Unix socket
is denied unless it is listed, so the sockets are granted individually. They
show up as `unix_sockets` in `angela_info`. **A container daemon socket is an
escape hatch:** whoever can use it can start a container that mounts any host
path, bypassing the filesystem rules. Pass `--no-sandbox-container-permission`
or set `options.no_sandbox_container_permission` to `true` to turn this off.
Custom `graphroot` locations from `storage.conf` are not detected; add them
with `--sandbox-rw`.

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

**`podman run` or `podman build` fails inside the sandbox** — Landlock forbids
`mount(2)` and `pivot_root(2)` and sets no-new-privileges, so a rootless
Podman cannot start containers in-process however many paths are granted. Use
`podman --remote` against a running `podman.socket`, or use Docker, whose CLI
only talks to a daemon outside the sandbox.

**`docker ps` fails with `permission denied` on the socket** — either
`--no-sandbox-container-permission` (or
`options.no_sandbox_container_permission`) is set, or the socket lives at a
path that is not detected. Add a non-default socket with `DOCKER_HOST=unix://...`
before launching, or confirm what was granted in the `unix_sockets` line of
`angela_info`.

**A tool call that should be scoped in still fails with an I/O error** — the
path may fall outside the read-write set (everything is readable by default,
so this is usually a write). Add it with `--sandbox-rw`, or check whether a
permission rule already covers it (those are folded in automatically) but the
concrete path differs from what you expect (e.g. a symlink resolving
elsewhere).
