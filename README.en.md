# ProxyLens

[简体中文](README.md) | [English](README.en.md)

**Select the most stable nodes.**

ProxyLens is a long-running, portable proxy quality analyzer and sing-box
configuration publisher. Its first installable release targets 64-bit
OpenWrt/ImmortalWrt with procd, UCI, and LuCI integration; the core also builds
for Windows and 64-bit Linux.

It imports Clash/Mihomo subscriptions, maintains durable node identities and
key field history, measures availability and steady-state latency through each
node's actual **exit**, calculates quality from the latest 30 days, builds
routing and node groups, and publishes matching SFA, Carton, and native
sing-box profiles through one User-Agent-aware subscription URL.

The current release package targets OpenWrt/ImmortalWrt 25.12+ on
`aarch64_cortex-a53`. 32-bit ARM and MIPS are not advertised because the
current SQLite dependency does not compile for those targets in this project.

Further reading:

- [Architecture](docs/architecture.md)
- [OpenWrt notes](docs/openwrt.md)
- [Trigger, timeout, retry, and concurrency inventory](docs/runtime-events.md)

## OpenWrt quick start

The public signing key must be trusted once before the first installation.
Run these commands as root:

```sh
cd /tmp
wget -O /etc/apk/keys/proxylens-apk-public.pem https://github.com/yaodao0yaodao/proxylens/releases/download/v0.3.1/proxylens-apk-public.pem
wget -O proxylens-0.3.1-r1_aarch64_cortex-a53.apk https://github.com/yaodao0yaodao/proxylens/releases/download/v0.3.1/proxylens-0.3.1-r1_aarch64_cortex-a53.apk
echo '1006d19223557eb861ab6ce8904ffe312b6bb78bfac077ed9606e5628cfcbf0d  proxylens-0.3.1-r1_aarch64_cortex-a53.apk' | sha256sum -c -
apk add ./proxylens-0.3.1-r1_aarch64_cortex-a53.apk
```

Later upgrades signed by the same private key do not require the public key to
be installed again. The private key is never uploaded to GitHub and must not
be installed on the router or shared.

Open **Services → ProxyLens** in LuCI or browse to
`http://ROUTER_IP:9099/`. Read the generated management token with:

```sh
uci -q get proxylens.main.admin_token
```

## Profile publishing and compatibility

The Web UI supports multiple tasks. Each task has one private sing-box
subscription URL:

- SFA and sing-box 1.13/1.14 User-Agents marked as Android receive the Android profile.
- Carton and plain sing-box 1.13/1.14 User-Agents receive the desktop profile.
- Native sing-box on Windows/Linux can reuse the matching Carton profile.
- Unknown, outdated, or unverified clients receive HTTP 406 to prevent an incompatible download.

Carton uses the same profile on Windows and Linux/CachyOS. Large download
applications use DIRECT in desktop TUN mode. ProxyLens combines and adjusts
Google Play, Steam, DNS, node, and country routing for mainland China networks.

## Default detection and retention

- A full detection cycle runs every 60 minutes by default and is configurable from 15 minutes to seven days.
- Each cycle refreshes the subscription and exit IPs, measures steady-state latency and availability, recalculates quality, and regenerates profiles.
- A failed request or measured latency of 800 ms or more is unavailable and stores no latency value.
- Routing artifacts and the sing-box dependency used by ProxyLens are checked independently every 24 hours.
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
