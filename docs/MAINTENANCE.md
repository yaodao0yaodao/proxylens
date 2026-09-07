# Build, verify, deploy and release

Use Go 1.26 or the version required by `go.mod`. There is no Node build step:
`internal/web/static` is embedded by Go. Use a fresh checkout to prove the build
does not depend on ignored `work/` files.

```sh
go test ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '-s -w -X main.version=0.3.3-dev' -o proxylens-linux-arm64 ./cmd/proxylens
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w -X main.version=0.3.3-dev' -o proxylens-linux-amd64 ./cmd/proxylens
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags '-s -w -H windowsgui -X main.version=0.3.3-dev' -o ProxyLens.exe ./cmd/proxylens
```

These are POSIX-shell commands; in PowerShell set `$env:GOOS`, `$env:GOARCH`,
and `$env:CGO_ENABLED` separately. Release builds replace the dev version with
the actual new version. `go test` may skip a bundled-core validation when
`work/singbox/windows/sing-box-1.13.19-windows-amd64/sing-box.exe` is absent;
inspect `internal/generator/generator_test.go` before claiming full core checks.

## Local smoke test

Run with a separate empty data directory and a loopback listen address:

```sh
./proxylens-linux-amd64 --listen 127.0.0.1:19099 --data-dir ./scratch-data --sing-box /absolute/path/to/sing-box
```

Use no production subscription for the basic HTTP smoke test. `/healthz` is the
health endpoint; `/api/health` is not. `/api/local-token` is restricted to local
requests. Management calls use `Authorization: Bearer <admin-token>`.
`GET /api/backup` returns a consistent SQLite snapshot. `GET /api/tasks`,
`/api/overview`, `/api/version-info`, and `/api/settings` provide diagnostics.
`GET /sub/<publish-token>` needs a supported client UA. Downloaded configuration
and tokens are secrets. For real profile validation, run `sing-box check -c`
with the matching version/OS before any TUN startup.

## Device deployment

Record the installed binary version/hash, sing-box version, UCI, database path
and schema first. Download an authenticated backup and retain the old binary.
Stop procd before replacing the executable, check the upload hash, restore
executable permission, then start `/etc/init.d/proxylens`. Verify `/healthz`,
tasks, logs, and selected subscription variants. Do not copy a live DB/WAL set
piecemeal. Do not downgrade a migrated database without a matching backup.

Windows ZIPs must include the matching `sing-box.exe`, licenses and third-party
notices. Test tray launch, no repeated browser opening, local token login,
port change, exit, and silent helper processes on Windows, not just cross-build.

## APK and signing handoff

The published v0.3.2 APK was hand-built with apk-tools 3 `mkpkg`, compatibility
`3.0.0_rc9`, target `aarch64_cortex-a53`, version `0.3.2-r1`, dependencies
`ca-bundle luci-base`. It bundles the core and `openwrt/root` resources, uses
`openwrt/scripts/post-install` for post-install/post-upgrade and
`openwrt/scripts/pre-deinstall` before removal. UCI defaults are staged as
`/usr/share/proxylens/proxylens.default` to preserve existing user settings.
The final package must be verified and installation simulated with the target
OpenWrt apk implementation, not only an unrelated Alpine version.

The legacy local script `work/build_signed_apk.py` is deliberately ignored: it
depends on a specific owner's router, local binaries and Python dependencies,
and temporarily signs on that router. It is NOT a portable or endorsed release
pipeline. A successor should implement packaging in an isolated build
environment with explicit inputs, not copy that script into public automation.
Use `openwrt/Makefile` as SDK integration starting material, not as proof of
reproducing the bundled release; source staging and core inclusion must be
validated for the chosen SDK before publishing.

On the original workstation the owner's private signing key is under
`~/.proxylens/keys/proxylens-apk-private.pem`. Never print, upload to GitHub,
silently regenerate, or store it on target devices. A new maintainer needs a
private handoff from the owner, or the owner performs signing. Losing this key
requires an explicit trust-key rotation on installed routers. The public key
is available in v0.3.2 Release; its published file SHA256 is
`e59a471e5564fe719f0d27365ba611f930009c734ab188b1d8328deed4c1b193`.

For a new release: choose a new version, align CHANGELOG/package metadata/docs,
record commit and upstream core source/license/version, build supported targets,
verify APK signature and target install/upgrade, smoke-test Windows ZIP, generate
SHA256SUMS, then create a new tag and Release. Do not overwrite v0.3.2 assets.
GitHub receives source and public artifacts only; no device backup or private key.

GitHub's [official release guidance](https://docs.github.com/en/code-security/concepts/supply-chain-security/immutable-releases)
recommends attaching all artifacts to a draft before publishing an immutable
release. Check repository settings before selecting that workflow.
