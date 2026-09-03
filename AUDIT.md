# Security audit

## Scope

Commit: `f2a62edad3d2a23aa91ff4dbe3594bd21e2fc0d3`

Date: 2026-09-03

The review covered every file in `pkg/api`, `pkg/auth`, `pkg/mcpcache`, and `pkg/utils`. It also covered `cmd/root.go`, `cmd/auth.go`, `cmd/mcp.go`, `cmd/mcp_autosync.go`, `cmd/graphql.go`, `cmd/agent.go`, the filesystem and subprocess paths in `cmd/issue.go`, `main.go`, `Makefile`, `flake.nix`, `Formula/linctl.rb`, and `smoke_test.sh`. All repository `init()` declarations, generation directives, embedding directives, environment reads, Viper reads, HTTP clients, filesystem writes, and subprocess calls were searched and inspected.

`main.go` embeds only `README.md`. The repository contains no generation directives. Initialization functions register commands and flags or initialize Viper configuration. They do not perform network access, filesystem writes, or subprocess execution.

No obfuscated code, encoded executable payloads, suspicious hexadecimal strings, production `unsafe`, production `reflect`, or production `syscall` use is present. Reflection appears only in tests. The linked `golang.org/x/sys` module is an indirect dependency.

## Network egress

| Host or destination | Purpose | Source |
|---|---|---|
| `api.linear.app` | All GraphQL queries and mutations, including MCP schema introspection | `pkg/api/client.go:14`, `pkg/api/client.go:95-104` |
| `uploads.linear.app` | Authenticated attachment downloads restricted to HTTPS with redirect re-validation | `cmd/issue.go:2038-2147` |

`linear.app` and `github.com` otherwise appear as displayed links or values sent to Linear. The binary does not fetch those destinations during normal issue, project, authentication, GraphQL, or MCP operations. No telemetry, analytics, crash reporting, advertising, or update-check endpoint exists.

The production API endpoint is constant. Environment variables and Viper configuration cannot replace it. `NewClientWithURL` permits a caller inside the Go package API to select another endpoint, but production commands call `NewClient`.

## Credential handling

The API key precedence is `LINCTL_API_KEY`, the first line returned by `pass` when `LINCTL_PASS_NAME` is configured, then `$HOME/.linctl-auth.json`. The `pass` entry name is separated from options with `--`. `pass insert` receives the key through standard input rather than an argument.

The API client stores the key in memory and sends it only in the `Authorization` request header to the constant Linear GraphQL endpoint. Attachment downloads send it only when the parsed host exactly equals `uploads.linear.app`. External attachment downloads receive no authorization header.

The JSON auth file enforces mode `0600` after every write. Interactive login disables terminal echo while reading the API key. The MCP cache contains schema metadata only, never the API key, and is written through a temporary file with mode `0600` inside a directory created with mode `0700`.

Normal output, JSON output, GraphQL output, MCP output, and error construction do not serialize or log the authorization header. HTTP errors can include Linear response bodies. No code includes the request header in those errors.

## Filesystem writes

| Path | Content and mode | Source |
|---|---|---|
| `$HOME/.linctl-auth.json` | API key JSON, enforced mode `0600` | `pkg/auth/auth.go:91-134` |
| `$HOME/.linctl/mcp-tools-cache.json` | Public GraphQL schema metadata, mode `0600`, directory created with `0700` | `pkg/mcpcache/cache.go:24-29`, `pkg/mcpcache/cache.go:55-98` |
| Current directory by default | Downloaded attachment content capped at 256 MiB, mode determined by `0666` and umask | `cmd/issue.go:1815-1817`, `cmd/issue.go:2058-2115` |
| User-selected output directory or path | Downloaded attachment content capped at 256 MiB and parent directories | `cmd/issue.go:1810-1829`, `cmd/issue.go:2058-2115` |

Attachment filenames from API titles, URLs, and `Content-Disposition` headers pass through `filepath.Base` and character sanitization before joining with the output directory. This prevents filename-based path traversal. A user-supplied output path can intentionally write outside the current directory.

## Subprocesses

| Executable | Arguments and data | Control analysis | Source |
|---|---|---|---|
| `pass` | Fixed actions plus the `LINCTL_PASS_NAME` value after the option terminator. The key is standard input for insertion | API response content cannot affect executable selection or arguments | `pkg/auth/auth.go:26-71` |
| Current linctl executable | Fixed `mcp sync` and quiet-mode arguments | API response content cannot affect executable selection or arguments | `cmd/mcp_autosync.go:35-45` |
| `git` | Fixed `remote get-url origin` arguments | API response content cannot affect executable selection or arguments | `cmd/issue.go:2300-2303` |

The executable names `pass` and `git` resolve through the process `PATH`. This is normal local command resolution and is not influenced by Linear response content.

## Supply chain

Go version: `go1.27.1 darwin/arm64`.

`go mod verify` returned `all modules verified`. `go mod tidy -diff` returned no diff. The committed `go.sum` covers every module needed to build and test linctl. Downloading every unused module declared by Viper added checksum-only entries, which were removed to preserve the tidy module files.

The four direct dependencies match the expected set:

| Module | Version |
|---|---|
| `github.com/fatih/color` | `v1.16.0` |
| `github.com/olekukonko/tablewriter` | `v0.0.5` |
| `github.com/spf13/cobra` | `v1.8.0` |
| `github.com/spf13/viper` | `v1.18.2` |

The linctl binary links 17 third-party modules in addition to the four direct dependencies:

`github.com/fsnotify/fsnotify v1.7.0`, `github.com/hashicorp/hcl v1.0.0`, `github.com/magiconair/properties v1.8.7`, `github.com/mattn/go-colorable v0.1.13`, `github.com/mattn/go-isatty v0.0.20`, `github.com/mattn/go-runewidth v0.0.9`, `github.com/mitchellh/mapstructure v1.5.0`, `github.com/pelletier/go-toml/v2 v2.1.0`, `github.com/sagikazarmark/slog-shim v0.1.0`, `github.com/spf13/afero v1.11.0`, `github.com/spf13/cast v1.6.0`, `github.com/spf13/pflag v1.0.5`, `github.com/subosito/gotenv v1.6.0`, `golang.org/x/sys v0.15.0`, `golang.org/x/text v0.14.0`, `gopkg.in/ini.v1 v1.67.0`, and `gopkg.in/yaml.v3 v3.0.1`.

`go list -m all` selects 97 third-party modules. The additional 75 modules are declared by Viper's module graph for remote configuration providers and tests. `go mod why` confirms linctl does not need representative modules including `cloud.google.com/go`, `github.com/hashicorp/consul/api`, and `go.etcd.io/etcd/client/v3`. They are not linked into the binary. The existing `vendor-dependencies` branches were not checked out.

## Findings

### Medium: Arbitrary attachment egress

Location: `cmd/issue.go:1870-1887`, `cmd/issue.go:2035-2047`, `cmd/issue.go:2172-2187`

Status: Fixed

Impact: A Linear attachment record can contain an arbitrary HTTP or HTTPS URL. A user who downloads that attachment causes linctl to issue a GET from the local machine, including to loopback or private-network addresses. The Linear API key is not sent to those hosts.

Recommendation: Permit `uploads.linear.app` by default. Require an explicit external-download option for other hosts and reject loopback, link-local, and private addresses after DNS resolution.

### Medium: Unbounded attachment downloads

Location: `cmd/issue.go:2045-2046`, `cmd/issue.go:2067-2074`

Status: Fixed

Impact: The attachment client has no timeout and copies the response body without a size limit. A malicious or faulty attachment host can hang the command or consume available disk space.

Recommendation: Add request and response-header timeouts, enforce a configurable byte limit, and remove partial files after failure.

### Medium: Echoed interactive API key

Location: `pkg/auth/auth.go:192-199`

Status: Fixed

Impact: Interactive login reads the API key from the terminal with a buffered reader. The terminal can echo the key to the screen and retain it in terminal scrollback.

Recommendation: Read terminal input with echo disabled and retain piped-input support for automation.

### Medium: Existing auth file permissions

Location: `pkg/auth/auth.go:94-106`

Status: Fixed

Impact: `os.WriteFile` applies `0600` only when creating a file. A pre-existing permissive `$HOME/.linctl-auth.json` retains its old mode when overwritten with a key.

Recommendation: Use an atomic temporary file, apply `0600`, and rename it into place. Reject symlinks at the destination.

### Low: Background MCP synchronization

Location: `cmd/mcp.go:162-165`, `cmd/mcp_autosync.go:15-45`

Impact: MCP commands can start a detached authenticated schema-introspection request when the cache is stale. Output and errors are discarded.

Recommendation: Keep `LINCTL_SKIP_AUTO_MCP_SYNC=1` set when deterministic foreground-only behavior is required.

### Low: Nonstandard credential path

Location: `pkg/auth/auth.go:85-92`

Impact: The fallback credential file is written directly under `$HOME` rather than `$HOME/.config` or `$HOME/.linctl`.

Recommendation: Store credentials under an application configuration directory or use the configured password manager.

## Remediation

### Attachment egress

`cmd/issue.go:2038-2147` validates the initial URL and every redirect as HTTPS on the exact `uploads.linear.app` host. `cmd/issue_cmd_test.go:308-339` pins the URL allowlist and redirect rejection.

### Attachment size

`cmd/issue.go:2058-2115` reads one byte beyond the 256 MiB limit, rejects oversized responses, and removes partial files. `cmd/issue_cmd_test.go:341-365` serves a body one byte over a test limit and verifies rejection and cleanup.

### Attachment filenames

`cmd/issue.go:2151-2193` applies `filepath.Base` and rejects empty, `.`, and `..` names before joining the filename to the output directory. `cmd/issue_cmd_test.go:367-391` pins traversal and sentinel handling.

### API key input

`pkg/auth/auth.go:205-238` uses `term.ReadPassword` when standard input is a terminal and preserves piped input for automation. `pkg/auth/auth_test.go:150-181` pins selection of the no-echo terminal reader.

### Auth file permissions

`pkg/auth/auth.go:100-134` applies mode `0600` after every credential write and applies mode `0700` to a parent directory created by the tool. `pkg/auth/auth_test.go:130-148` verifies that saving repairs a permissive existing file.

## Verdict

PASS for building and using with a workspace-write API key.

No high-severity exfiltration, authorization-header disclosure to a non-Linear host, or arbitrary code execution from Linear response content was found. Attachment downloads accept only HTTPS URLs on `uploads.linear.app`, re-validate redirects, cap response bodies at 256 MiB, and remove partial files after failure. Interactive authentication disables terminal echo and enforces user-only credential file permissions. Disabled automatic MCP synchronization avoids the remaining background-request finding.
