# OpenCode in magical-sandboxes

`msbx run opencode` starts OpenCode inside the current project's persistent Debian VM. Sessions from the same canonical project share one VM and guest HOME. Other projects use separate VM disks, OpenCode profiles, sign-ins, and settings.

OpenCode uses its native guest paths under `/home/msbx`, including `/home/msbx/.local/share/opencode/auth.json`. Sign in with OpenCode from the guest on first launch. The credentials and settings persist on that project's guest disk.

From an initialized target project:

```sh
/path/to/magical-sandboxes/bin/msbx init
/path/to/magical-sandboxes/bin/msbx run opencode
```

Codex, Claude Code, OpenCode, and shell sessions can run concurrently in one project VM. The guest agent adds workers as sessions arrive with no configured session-count limit; practical concurrency is bounded by VM and Mac resources. The VM shuts down three seconds after the last session exits.

## Troubleshooting

- **OpenCode asks to sign in again:** verify that the same project VM disk is used. The `auth.json` file is stored on the guest disk.
- **Different provider login in another project:** expected; OpenCode credentials are independent per project.

Deleting a project VM deletes its OpenCode login and settings.
