# shephrd-sh/tailscale

Tailscale with the downstream patches that [shephrd](https://github.com/shephrd-sh) embeds through tsnet, and shephrd's tailnet package built on them.
shephrd installs the package's releases with mise as `github:shephrd-sh/tailscale` and pins one by its version, `<Tailscale version>-<commit>`.

## Patches

forkctl manages the stack on `downstream`: `mise run fork status` shows it, and [PATCHES.md](PATCHES.md) lists each patch with why it exists and when it is dropped.
The stack is based on an upstream release tag, and the upstream ref is that release's branch, `release-branch/<minor>`.

1. `exit-hook`: a control-ordered `Debug.Exit` calls `controlclient.ExitHook` when it is set, instead of `os.Exit`, so an embedder can stop its node and keep its process.
2. `logs-dir`: `LocalBackend` calls `logpolicy.LogsDir` only when logtail is compiled in, so a `ts_omit_logtail` build creates no logs directory, such as `~/.local/share/tailscale`, outside its state directory.
3. `shephrd-tailnet`: shephrd's tailnet package in `shephrd/tailnet/`, the c-archive `shephrdd` links, with its schema and fixtures; [its README](shephrd/tailnet/README.md) has the C interface, the schema rules and the release contents.
   The patch also holds `shephrd/testcontrol`, a test control server with an admin API that a test drives from another process. It is test-only: its admin API binds to loopback alone and refuses any other address, the release ships it as `bin/testcontrol` for shephrd's tests, and nothing shephrd ships to users holds it.

Work on the stack through `mise run fork`, never a PATH-global `forkctl` or `stg`.
An upgrade rebases onto the new release tag with `mise run fork rebase`, then publishes with `mise run fork publish`.

## Releases

`.github/workflows/shephrd-tailnet.yml`, in the `fork-tooling` patch, runs on each push to `downstream`.
It audits the stack with `mise run fork check`, checks the Go types against the schema, tests and builds the package on GitHub-hosted runners for `linux-x64` and `macos-arm64`, and publishes release `tailnet-<Tailscale version>-<commit>`.
A change to the package lands here and releases first; a shephrd pull request then raises its pin to the new release.

## Repository settings

Keep these settings, and record a change here in the same commit.

| Setting | Value | Why |
|:--|:--|:--|
| Actions | enabled, allowed actions "selected" | `shephrd-tailnet.yml` releases the package. |
| Allowed actions | `actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1` and `jdx/mise-action@6612f0ef07ed2a736cd1e00a94a065522dd41195` only; nothing GitHub-owned or verified beyond them | The workflow pins exactly these, and no other action can run. |
| Inherited workflows | each disabled | Upstream CI must never run here: it would spend minutes on code the fork does not maintain. |
| Dependency Graph automatic submission | runs; GitHub offers no way to disable it through the API | GitHub's own dynamic workflow submits a dependency snapshot of `go.mod` on GitHub-hosted runners, free for a public repository; it builds and publishes nothing. |
| Runners | GitHub-hosted only | The fork is public, so they cost nothing, and it never reaches shephrd's runners. |
| CodeQL default setup | not configured | It would scan upstream code that the fork does not maintain. |
| Dependabot alerts and updates | off | Upstream owns its dependencies; shephrd tracks the release it pins. |
| Secret scanning and push protection | on | They run on GitHub and stop a secret in a push. |
| Issues, wiki, projects | off | Work is tracked in shephrd. |

Contributions to Tailscale go upstream to [tailscale/tailscale](https://github.com/tailscale/tailscale); this fork takes no pull requests.
