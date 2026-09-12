# Security

Trajectory reads local Codex session data. Prompts, commands, tool results, and
saved investigations may contain credentials, proprietary code, or personal
information. The server binds to loopback and checks browser origins; keep it
local rather than exposing it through a proxy, tunnel, or public interface.
Other processes running on your machine can access its unauthenticated API.

## Reporting a vulnerability

Use this repository's **Security → Report a vulnerability** option when available.
If private reporting is unavailable, open an issue asking the maintainer for a
private reporting channel, without including exploit details or sensitive data.
Do not publish session logs, database files, credentials, or a working exploit in
a public issue while arranging private disclosure.

Include the affected commit/version, operating system, reproduction steps using
synthetic data, expected impact, and any proposed fix. This project currently
targets fixes on the latest development revision; older revisions have no
separate security maintenance commitment.

## Sharing diagnostics

Prefer demo mode or a minimal synthetic rollout. Before sharing screenshots,
exports, or error messages, remove tokens, personal paths, prompts, command
arguments, tool output, and identifying task metadata. The viewer's truncation
limits are not redaction. Saved investigation databases retain frozen evidence;
keep them out of issues and pull requests. If a credential has already been
exposed, revoke or rotate it rather than relying on deleting the attachment.
