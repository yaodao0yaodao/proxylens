# OpenWrt / ImmortalWrt

The release IPK contains the aarch64 core, procd init script, UCI defaults and a
LuCI JavaScript page. Its persistent database defaults to
`/etc/proxylens/proxylens.db`; the probe core is
`/usr/lib/proxylens/sing-box`. Volatile logs and downloaded caches remain under
`/var/lib/proxylens`.

```sh
opkg install proxylens_*.ipk
uci set proxylens.main.enabled='1'
uci commit proxylens
/etc/init.d/proxylens enable
/etc/init.d/proxylens restart
```

Open **Services → ProxyLens** in LuCI. The page controls enablement, port,
public/DDNS base URL and provides the management-page link. The generated admin
token is stored only in `/etc/config/proxylens`; subscription secrets remain in
the mode-0600 SQLite database and API responses redact their query strings.

## Reproducible package matrix

Build inside the matching official OpenWrt SDK; no router-side compiler is
required. The same package recipe emits IPK on OpenWrt 24.10 SDKs and APK on
25.12 SDKs. Supported targets are `aarch64_generic`, `arm_cortex-a7_neon-vfpv4`,
`mipsel_24kc`, and `x86_64`. Run `make package/proxylens/compile V=sc` in each
clean SDK after placing this source under `package/proxylens`. Record the SDK
release, target, git/source archive SHA256 and resulting package SHA256. This
repository does not claim packages that were not actually built.

`/etc/config/proxylens` is a conffile and `/etc/proxylens` is persistent data,
so normal upgrades preserve configuration and the database. Before upgrade,
download `/api/backup` with a Bearer admin token. Uninstall removes program
files but intentionally leaves `/etc/proxylens`; remove it manually only
when permanent data deletion is intended.

The portable Go core has no LuCI dependency. Windows can run the same binary
with `--data-dir`, `--listen`, and `--sing-box`; platform-specific service/UI
integration stays outside the core packages.
