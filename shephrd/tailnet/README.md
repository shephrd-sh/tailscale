# libshephrdtailnet

[shephrd](https://github.com/shephrd-sh)'s tailnet package, built with Go's `-buildmode=c-archive` into `libshephrdtailnet.a`, which the shephrd core daemon links.
It runs the process's one tsnet node from the Tailscale tree beside it, with this fork's patches, and owns `SIGTERM` and `SIGINT`.
It lives in the fork so it builds against exactly that tree, and a Tailscale upgrade rebases it with the rest of the stack.

## The C interface

Two functions, whose version is `abi` in `tailnet.go`:

1. `shephrd_tailnet_abi()` returns the C interface's version, which the core checks before it calls anything else.
2. `shephrd_tailnet_start(control, conns)` takes one end of a stream socketpair, which carries framed commands and events, and one end of a second stream socketpair, which carries each caller's 8-byte id with its descriptor attached to the id's first byte. A stream, unlike a datagram socket, makes the package wait while the core is behind, so no handover is lost; macOS drops a datagram sent to a full queue. The package keeps its own copy of each descriptor open until a `Taken` command names the id, since on macOS a socket in flight can break when its sender closes the last other copy before the receiver reads it.

`abi` rises with every change to an exported function's name, signature or meaning.

## The state directory

`Up` names the node's state directory, which the core creates.
tsnet keeps the node's identity there in `tailscaled.state`, and once the node has run, the package keeps `node.pb` beside it, mode `0600`: a `NodeRecord` with the node's stable id, name and the Tailscale version that ran it.
Before tsnet reads the state, the package refuses to start, with a `FAILED` state, when the record outlived its state (missing or empty, which tsnet would replace with a new identity) or names a newer Tailscale than this build's, since tsnet has no downgrade path.
`Up` with `new_identity` first moves the package's files, `tailscaled*`, `certs` and `node.pb`, into a `replaced-<time>` directory beside them; the core's own files stay.

## The schema

[`proto/shephrd/tailnet/v1/tailnet.proto`](proto/shephrd/tailnet/v1/tailnet.proto) defines the messages on the control socket: each frame is a 4-byte little-endian body length, then one `Command` from the core or one `Event` from the package.
`tailnetpb/` holds the Go types generated from it, and `fixtures/v1/` one encoded message per file, which the Go tests and shephrd's binding both check.

Rules for changing the schema:

1. Change `v1` only by adding messages, arms and fields; anything else is a new version directory.
2. Declare fields in field-number order, and use no `map` fields.
3. Give every enum a zero value named `<ENUM>_UNSPECIFIED`.
4. Mark a removed field or arm `reserved`, by number and name, and never reuse a number.
5. Comment every message, field, arm and enum value.
6. Add a fixture, with its value in `fixtures_test.go`, for each new message.
7. Regenerate with `mise run tailnet:generate` and `mise run tailnet:fixtures`; `mise run tailnet:check` fails when the committed types differ from the schema.

## Releases

`.github/workflows/shephrd-tailnet.yml` releases each push to `downstream` that passes `mise run fork check`, the package's tests and the schema check.
The release is `tailnet-<Tailscale version>-<commit>`, the first 12 characters of the `downstream` commit, so a push that changes nothing releases nothing new.

Each `libshephrdtailnet-<version>-<target>.tar.gz`, for `linux-x64` and `macos-arm64`, holds:

- `lib/libshephrdtailnet.a` and `include/libshephrdtailnet.h`, built with Go from `mise.toml`, `CGO_ENABLED=1`, `-trimpath` and `-tags ts_omit_logtail`, so the archive holds no log uploader; macOS builds for macOS 26.0.
- `bin/testcontrol`, `shephrd/testcontrol` from the same commit: Tailscale's test control server with its DERP and STUN servers, for the core's tests, with flags for a required sign-in and an auth key, and an admin API on loopback only that completes a sign-in, tags a node, expires keys and sends `Debug.Exit`. It is test-only and never ships in an app.
- `bin/tailnet-dial`, `shephrd/tailnet-dial` from the same commit: a tsnet node of its own that forwards each connection it accepts on loopback to one address on the tailnet, so a core's tests reach its tailnet port from another node with that node's identity, as an app does through the system's Tailscale. It is test-only and never ships in an app.
- `proto/` and `fixtures/`, the schema and fixtures above.
- `COMMIT`, the full commit the archive was built from, which shephrd's binding checks against the version it pins.
- `LICENSE`, Tailscale's BSD-3-Clause license.

Each archive has a `.sha256` beside it.
