# Claude Code in magical-sandboxes

`msbx run claude` starts the native Claude Code CLI inside the current project's persistent Debian VM. Sessions launched from the same canonical project share one VM; different projects have separate disks, guest homes, sign-ins, and settings.

Claude runs with `HOME=/home/msbx` and `CLAUDE_CONFIG_DIR=/home/msbx/.claude`. Sign in through Claude Code on the first guest launch. Its native credentials, trusted-folder decisions, model, effort, and settings persist on that project's guest disk. Each project must sign in separately.

From an initialized target project:

```sh
/path/to/magical-sandboxes/bin/msbx init
/path/to/magical-sandboxes/bin/msbx run claude
```

Codex, Claude, OpenCode, and shell sessions can run concurrently in the same project VM. The guest agent adds workers as sessions arrive, with no configured session-count limit; practical concurrency is bounded by VM and Mac resources. The VM powers off three seconds after the final session exits.

## Troubleshooting

- **Repeated trust prompt:** check the project path and whether the same `.msbx-dev/sandboxes/projects/<project-id>/disk.raw` is in use. Claude trust data is in `/home/msbx/.claude` on the guest disk.
- **Native CLI warning or launcher missing:** the wrapper expects `/home/msbx/.local/bin/claude` to link into `/home/msbx/.local/share/claude/versions/`. Rebuild and reprovision the template if the native installer files are missing; existing project VM disks do not update automatically.
- **Login is different in another project:** expected; each project VM has an independent Claude profile.

Deleting the project VM deletes Claude's sign-in and settings.
