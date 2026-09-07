# OpenWrt / ImmortalWrt

The published v0.3.2 APK targets OpenWrt/ImmortalWrt 25.12+
`aarch64_cortex-a53`. It includes ProxyLens, sing-box, procd, UCI defaults and a
LuCI JavaScript page. Its persistent database defaults to
`/etc/proxylens/proxylens.db`; the probe core is
`/usr/lib/proxylens/sing-box`. Volatile logs and downloaded caches remain under
`/var/lib/proxylens`.

```sh
# First trust the public key and download/check the APK as described in README.
apk add ./proxylens-0.3.2-r1_aarch64_cortex-a53.apk
uci set proxylens.main.enabled='1'
uci commit proxylens
/etc/init.d/proxylens enable
/etc/init.d/proxylens restart
```

Open **Services → ProxyLens** in LuCI. The page controls enablement, port,
public/DDNS base URL, database location and the management-page link. The generated admin
token is stored only in `/etc/config/proxylens`; subscription secrets remain in
the mode-0600 SQLite database and API responses redact their query strings.

## Packaging status

The published APK was hand-built with apk-tools 3. The SDK Makefile is an
integration starting point, not a reproducible description of that bundled
artifact: source staging and sing-box inclusion require validation. ARM32/MIPS
are not supported by this project's current SQLite dependency build. There is
no published IPK or verified multi-target SDK matrix. Record the chosen SDK,
source/core versions and package hashes when implementing a packaging pipeline.
See [maintenance](MAINTENANCE.md) for signing and release-tooling limitations.

`/etc/config/proxylens` is a conffile and `/etc/proxylens` is persistent data,
so normal upgrades preserve configuration and the database. Before upgrade,
download `/api/backup` with a Bearer admin token. Uninstall removes program
files but intentionally leaves `/etc/proxylens`; remove it manually only
when permanent data deletion is intended.

Schema v14 on current main cannot be opened by v0.3.2. A rollback must restore a
matching earlier database backup. Never move or copy a live SQLite DB/WAL set
piecemeal. Device-version evidence and private handoff requirements are recorded
in [HANDOFF.md](HANDOFF.md).

The portable Go core has no LuCI dependency. Windows can run the same binary
with `--data-dir`, `--listen`, and `--sing-box`; platform-specific service/UI
integration stays outside the core packages.
