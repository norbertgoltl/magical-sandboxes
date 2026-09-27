# Codex in magical-sandboxes

`msbx run codex` starts the native Codex CLI inside the current project's persistent Debian VM. The VM is shared by sessions launched from the same canonical project directory. Each project has a separate VM disk, Codex home, login, and settings.

## Sign-in and persistent state

Codex runs with `CODEX_HOME=/home/msbx/.codex`. Sign in from the guest Codex UI/CLI on its first launch. `auth.json`, project trust, selected model, and other native Codex settings remain on that project's guest disk after Codex and the VM stop. Other projects need their own sign-in.

The Codex wrapper uses `/home/msbx/.codex` on the project VM disk. Sign in and configure Codex separately in each project's VM. Host Codex installation is not required.

Codex manages its app-server daemon under `CODEX_HOME/packages/app-server-daemon/`. The guest home is on the executable persistent guest filesystem, so the daemon runs from its managed installation path.

## Run and troubleshoot

From an initialized target project:

```sh
/path/to/magical-sandboxes/bin/msbx init
/path/to/magical-sandboxes/bin/msbx run codex
```

Multiple Codex sessions, shell sessions, and other harnesses can run concurrently in this project's VM. The guest agent grows the worker pool on demand without a configured session-count limit; practical concurrency is bounded by VM and Mac resources. The VM shuts down three seconds after the final session exits; starting another session during that grace period reuses it.

- **Login or model/trust settings reset:** check that the same project path and project VM disk are being used. These files live in `/home/msbx/.codex` on the guest disk.
- **Managed app-server daemon cannot execute:** check guest disk mount options and permissions under `/home/msbx/.codex/packages/app-server-daemon/`. Do not move `CODEX_HOME` into `/run` or the VirtioFS project share.
- **App server reports `File exists` or cannot connect to its control socket:** the VM removes a dangling control-socket symlink on boot. If the error persists, inspect `/home/msbx/.codex/app-server-control/app-server-control.sock`.
- **Session hangs after host disconnect:** inspect `manager.log` under the repository's `.msbx-dev/sandboxes/projects/<project-id>/` and the guest's `msbx-agent` journal. The guest agent should receive the disconnected session, signal its process group, and return its worker to the pool.
- **Authentication is unexpectedly different from another project:** this is expected; native Codex credentials are intentionally per-project.

Deleting a project VM deletes its guest Codex login and settings.
