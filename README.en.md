# ProxyLens

[简体中文](README.md) | [English](README.en.md)

**Select the most stable nodes.**

ProxyLens is an automatic proxy node selector that runs on your router.

Give it a Clash/Mihomo subscription URL. ProxyLens regularly checks whether
each node works and how much latency it has, selects the more stable nodes, and
publishes one sing-box subscription URL for your clients. SFA on Android,
Carton on desktop, and native sing-box on Windows/Linux can all receive the
right profile from that URL.

Checks run through each node's **actual proxy exit**, not just against the node
server. ProxyLens keeps node details and history in its database and updates
routing rules and node groups automatically, so routine maintenance is not
required.

The current release package targets OpenWrt/ImmortalWrt 25.12+ on
`aarch64_cortex-a53`. 32-bit ARM and MIPS are not advertised because the
current SQLite dependency does not compile for those targets in this project.

The first release primarily targets 64-bit OpenWrt/ImmortalWrt and includes
LuCI and Web management. The core remains portable. Further reading:

- [Architecture](docs/architecture.md)
- [Maintainer handoff and current status (Chinese)](docs/HANDOFF.md)
- [Build, verification and release](docs/MAINTENANCE.md)
- [OpenWrt notes](docs/openwrt.md)
- [Trigger, timeout, retry, and concurrency inventory](docs/runtime-events.md)

## Recommended clients

- **Android:** [Official SFA (sing-box for Android) documentation and downloads](https://sing-box.sagernet.org/clients/android/)
- **Windows, Linux, and CachyOS:** [Carton releases and downloads](https://github.com/821869798/carton/releases)

SFA is the official sing-box Android client. Carton is a third-party sing-box
desktop GUI for Windows and Linux and is not affiliated with the sing-box
project. ProxyLens uses the client's User-Agent to return the matching profile,
so both clients can use the same subscription URL.

## OpenWrt quick start

The public signing key must be trusted once before the first installation.
Run these commands as root:

```sh
cd /tmp
wget -O /etc/apk/keys/proxylens-apk-public.pem https://github.com/yaodao0yaodao/proxylens/releases/download/v0.3.2/proxylens-apk-public.pem
wget -O proxylens-0.3.2-r1_aarch64_cortex-a53.apk https://github.com/yaodao0yaodao/proxylens/releases/download/v0.3.2/proxylens-0.3.2-r1_aarch64_cortex-a53.apk
echo '6892b6228ca07b0153676efa533a7800a9723cc8d7013bd397ebff57c49221d8  proxylens-0.3.2-r1_aarch64_cortex-a53.apk' | sha256sum -c -
apk add ./proxylens-0.3.2-r1_aarch64_cortex-a53.apk
```

Later upgrades signed by the same private key do not require the public key to
be installed again. The private key is never uploaded to GitHub and must not
be installed on the router or shared.

Open **Services → ProxyLens** in LuCI or browse to
`http://ROUTER_IP:9099/`. Read the generated management token with:

```sh
uci -q get proxylens.main.admin_token
```

## Windows

Download `ProxyLens-Windows-x64-0.3.2.zip` from the
[v0.3.2 release](https://github.com/yaodao0yaodao/proxylens/releases/tag/v0.3.2),
extract the complete archive, and run `ProxyLens.exe`. The archive includes the
matching `sing-box.exe`; keep both files together.

The default browser opens after the first successful launch. ProxyLens then
runs silently in the system tray. Double-click the tray icon or choose
**Show ProxyLens** to reopen the management page; the menu also changes the
port or exits the application. The local management page reads the local token
automatically.

## Profile publishing and compatibility

The Web UI supports multiple tasks. Each task has one private sing-box
subscription URL:

- SFA and sing-box 1.13/1.14 User-Agents marked as Android receive the Android profile.
- Carton and plain sing-box 1.13/1.14 User-Agents receive the desktop profile.
- Native sing-box on Windows/Linux can reuse the matching Carton profile.
  Explicit desktop Linux/CachyOS UAs additionally enable TUN `auto_redirect`;
  Android and unknown operating systems do not.
- Unknown, outdated, or unverified clients receive HTTP 406 to prevent an incompatible download.

Carton shares a base profile across Windows and Linux/CachyOS, with explicit
Linux UA adjustments applied when serving the subscription. Large download
applications use DIRECT in desktop TUN mode. ProxyLens combines and adjusts
Google Play, Steam, DNS, node, and country routing for mainland China networks.

In Linux TUN mode, the native Steam process uses DIRECT so its connection
manager, download-region CellID, and content-server directory follow the local
network.  The `steamwebhelper` process used by the store and community remains
proxied.  Fully exit and restart Steam after a subscription rule change to
discard the previous CM session and content-server list.

## Default detection and retention

- A full detection cycle runs every 60 minutes by default and is configurable from 15 minutes to seven days.
- Each cycle refreshes the subscription and exit IPs, measures steady-state latency and availability, recalculates quality, and regenerates profiles.
- A failed request or measured latency of 800 ms or more is unavailable and stores no latency value.
- Routing artifacts update independently every 24 hours. ProxyLens only reads
  the current sing-box version and never downloads, updates, or replaces the core.
- Raw observations and per-cycle quality snapshots remain at full resolution for 48 hours. Older data becomes hourly summaries retained for 90 days; 24-hour quality summaries are retained for one year. Quality calculations use the latest 30 days.

After six continuous hours of subscription refresh failures, generated profiles
show a failure notice at the top while retaining the last usable nodes. Upstream
resources are fetched directly first; if that fails, ProxyLens can temporarily
use the highest-priority ordinary-rate, non-China node from all tasks.

## Data and storage

The OpenWrt database defaults to `/etc/proxylens/proxylens.db`, avoiding `/var`,
which is commonly memory-backed. LuCI can change the database location; applying
the setting moves the database, WAL, and SHM files and refuses to overwrite an
existing database at the destination.

The detection interval is the only user-facing detection schedule. Database
maintenance and the configured size ceiling apply even when no task is run manually.
