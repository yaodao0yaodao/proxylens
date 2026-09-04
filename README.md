# ProxyLens

ProxyLens is a long-running, cross-platform proxy quality analyzer and sing-box
configuration publisher. The first shell targets 64-bit OpenWrt/ImmortalWrt and adds
procd, UCI and LuCI integration; the core also builds for Windows and 64-bit Linux.
The current release package targets `aarch64_cortex-a53`; 32-bit ARM and MIPS are
not advertised because the SQLite dependency does not currently compile for them.

It imports Clash/Mihomo subscriptions, keeps durable node and field state,
measures availability and latency through each node's **exit**, calculates robust 30-day quality
scores, builds cost-aware groups, and publishes SFA and Carton configs through
one User-Agent-aware URL.

See [docs/architecture.md](docs/architecture.md) and
[docs/openwrt.md](docs/openwrt.md). The complete trigger, timeout, retry, and
concurrency inventory is in [docs/runtime-events.md](docs/runtime-events.md).

## OpenWrt quick start

For OpenWrt/ImmortalWrt 25.12 and newer, copy the signed APK and its public
signing key to the router, then run as root:

```sh
cp proxylens-apk-public.pem /etc/apk/keys/
apk add proxylens-0.3.1-r1_aarch64_cortex-a53.apk
```

Open **Services → ProxyLens** in LuCI or browse to
`http://ROUTER_IP:9099/`. Read the generated management token with:

```sh
uci -q get proxylens.main.admin_token
```

Create any number of tasks in the Web UI. Each task gets one universal private
subscription URL. ProxyLens returns the Android profile for `SFA/1.13+` or a
versioned `sing-box/1.13.x`/`1.14.x ... Android` User-Agent, and the desktop profile for
`Carton/` or a plain versioned `sing-box/1.13.x`/`1.14.x` User-Agent. Native sing-box on
Windows/Linux therefore reuses the matching Carton 1.13 or 1.14+ artifact rather
than duplicating identical data. Unknown or older clients receive HTTP 406 so
an accidental browser request never downloads the wrong profile. Future core
formats are rejected until they have been checked rather than being guessed compatible.

## Default schedule

- One detection cycle runs every 60 minutes by default and is configurable from
  15 minutes to seven days. A cycle refreshes the subscription, resolves each
  exit IP, measures steady-state latency and availability, recalculates quality,
  then regenerates client profiles.
- Latency is the availability gate. A failed request or an observed latency of
  800 ms or more writes one unavailable result and stores no latency value.
- Provider rule artifacts update every 24 hours independently. They have no
  user-facing frequency setting. The same maintenance pass checks the official
  stable sing-box release used by ProxyLens probes, verifies GitHub's SHA-256
  digest and a smoke configuration, selects the official musl build on
  OpenWrt/Alpine (glibc on ordinary Linux), and replaces it atomically only
  when newer. A failed large archive transfer retries no more than once every
  six hours.

Raw observations and per-cycle quality snapshots stay at full resolution for
48 hours. Older observations become hourly summaries retained for 90 days;
24-hour quality summaries are retained for one year. Quality uses the most
recent 30 days. Maintenance and the configured database size ceiling apply
even when no task is manually run.

The detection interval is the only user-facing detection setting. The OpenWrt
UCI page exposes service state, port, public
base URL and SQLite path. Changing the path while applying LuCI settings moves
the database (including WAL/SHM files) and refuses to overwrite an existing
destination. `/etc/proxylens/proxylens.db` is the persistent internal-flash
default on OpenWrt; `/var` is normally a RAM-backed filesystem and is therefore
not used for the database.

Google Play control/API traffic, including `services.googleapis.cn`, always
uses consistent remote DNS and proxy egress. Economy mode only sends the
maintained `google-play@cn` mainland CDN subset DIRECT. Steam depot traffic
under `steamcontent.com` uses local DNS and DIRECT so CDN locality follows the
client network and Steam's selected download region without hard-coding a city;
Steam login, store and community traffic still uses the proxy.

When a subscription cannot be refreshed continuously for six hours, generated
profiles put an update-failure selector at the top and retain the last usable
nodes. Other important rule, dependency and task failures use the same visible
selector mechanism. Direct access is attempted first for upstream data;
on failure ProxyLens can temporarily use the highest-priority ordinary-rate,
non-China node across all tasks.

Carton uses the same generated profile on Windows and Linux/CachyOS; native
sing-box CLI on those systems uses that desktop profile too. The desktop-only
large-download DIRECT rule contains executable names for both platforms because
Carton's default subscription User-Agent identifies its sing-box version but
not the operating system. Domain, DNS, node, country and Google Play routing
remain platform-independent.
