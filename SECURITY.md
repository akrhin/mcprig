# Security policy

## Supported versions

The latest release gets fixes; older ones do not. At the moment that is `0.4.x`,
and the `latest` / `latest-full` container tags follow it. A fix ships as a new
patch release on [GitHub releases](https://github.com/akrhin/mcprig/releases).

## Reporting a vulnerability

Use GitHub Security Advisories:
<https://github.com/akrhin/mcprig/security/advisories/new>. The report stays
private until a fix is published.

Please do not open a public issue, pull request or discussion for a
vulnerability, and do not post a working exploit or the affected host's details
in public. If the advisory form does not work for you, open an issue that says
only that you have a security report and how to reach you.

## What helps

The mcprig version (`mcprig -version`), OS and architecture; how it was installed
(release archive, container tag, source); which component (`serve`, `panel`,
`stdio`, `init`, `doctor`, `warm`); the exact request or command and what it did
instead of what you expected.

## What to expect

Reports are read by one maintainer, a single operator, so no response time is
promised. Confirmed issues are fixed in a patch release and credited in the
advisory unless you ask otherwise.

## Scope

In scope: the binary (`serve`, `stdio`, `panel`), the panel's HTTP API and its
authentication, access-key handling, the sandbox wrapper, secret masking in tool
answers. Out of scope: downstream MCP servers mcprig proxies, and deployments
that disable auth on purpose (`MCPRIG_AUTH=off`, `"allowAnonymous": true`,
`allowedHosts: ["*"]`).
