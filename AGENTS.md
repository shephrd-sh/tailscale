# shephrd-sh/tailscale

Tailscale with the downstream patches that [shephrd](https://github.com/shephrd-sh/shephrd) embeds through tsnet.
shephrd's Go package in `core/shephrd-tailnet/go/` replaces `tailscale.com` with a commit of the `downstream` branch, and its `pin.env` records that commit and the upstream version.

## Patches

forkctl manages the stack on `downstream`: `mise run fork status` shows it, and [PATCHES.md](PATCHES.md) lists each patch with why it exists and when it is dropped.
The stack is based on an upstream release tag, and the upstream ref is that release's branch, `release-branch/<minor>`.

1. `exit-hook`: a control-ordered `Debug.Exit` calls `controlclient.ExitHook` when it is set, instead of `os.Exit`, so an embedder can stop its node and keep its process.
2. `logs-dir`: `LocalBackend` calls `logpolicy.LogsDir` only when logtail is compiled in, so a `ts_omit_logtail` build creates no logs directory, such as `~/.local/share/tailscale`, outside its state directory.

Work on the stack through `mise run fork`, never a PATH-global `forkctl` or `stg`.
An upgrade rebases onto the new release tag with `mise run fork rebase`, then publishes with `mise run fork publish`.

## Repository settings

The fork runs nothing on any runner.
Keep these settings, and record a change here in the same commit.

| Setting | Value | Why |
|:--|:--|:--|
| Actions | disabled | The fork has no workflow of its own, and upstream CI must never run on shephrd's runners. |
| Inherited workflows | each disabled | A second guard if Actions is enabled again. |
| CodeQL default setup | not configured | It would scan upstream code that the fork does not maintain. |
| Dependabot alerts and updates | off | Upstream owns its dependencies; shephrd tracks the release it pins. |
| Secret scanning and push protection | on | They run on GitHub and stop a secret in a push. |
| Issues, wiki, projects | off | Work is tracked in shephrd. |

Contributions go upstream to [tailscale/tailscale](https://github.com/tailscale/tailscale); this fork takes no pull requests.
