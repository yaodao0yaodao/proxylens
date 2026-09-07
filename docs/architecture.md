# Architecture and quality model

ProxyLens separates portable code from platform glue:

1. The core owns subscriptions, durable state, scheduling, quality analysis,
   configuration generation and the HTTP API.
2. A probe adapter starts one short-lived sing-box process for a whole batch.
   Every listener is pinned to exactly one outbound, so availability, RTT and
   public IP are measurements of the **node exit**.
3. OpenWrt supplies procd/UCI/LuCI integration. Windows already supplies a native
   tray launcher and browser management UI around the same portable core.

The primary node identity uses a subscription-provided short ID or another
protocol-stable unique identifier whenever one exists. For protocols without a
standard ID, a server-independent continuity key plus normalized source name is
used only when the match is unique. Ambiguous matches are recorded in
`identity_conflicts` and exposed for an explicit manual merge; they are never
silently guessed. Credential and endpoint rotation updates the current
connection document and its revision rather than creating meaningless key
history. Node numbers are
monotonic within each measured exit country and are never recycled. Each
country starts at 1; an unknown exit remains unnumbered. A country change
consumes a new number from that country's durable sequence. Schema v2 safely
renumbers legacy rows once and preserves sequences independently from deletion.

Schema v14 persists merges in `node_identity_aliases`: subsequent incoming IDs
resolve to the canonical node before matching. Alias sources are excluded from
historical resurrection and alias chains are flattened when merged again.

Only original source name, measured exit country and traffic multiplier have
append-only change history in `node_fields`. Their active version is never
rewritten on a no-op refresh. Connection parameters live only in the current
node row; `config_revision` prevents measurements from an obsolete connection
configuration influencing its replacement.

Exit geography is cached in memory and queried through multiple providers.
When a provider returns HTTP 429, its `Retry-After` cooldown is honored and a
fallback provider is used; country-only lookup remains available if providers
with ASN data are unavailable. The source name is never substituted for the
measured exit country.

Every configurable detection cycle performs subscription refresh, exit-IP
discovery, steady-state latency/availability, 30-day
quality calculation and profile generation in that order. The minimum interval
is 15 minutes. Raw observations and per-cycle quality snapshots are retained
for 48 hours; measurements are then rolled into 90 days of hourly summaries.
One 24-hour quality summary per node is retained for a year.

Quality uses a 90% Wilson lower confidence bound for availability and a 14-day
half-life. A latency probe warms the node connection once, requires at least two
successful 204 responses from three attempts, and stores a conservative P75
together with a same-batch DIRECT baseline. At 800 ms or above it stores one
unavailable observation and no latency. Displayed latency remains the real
observed steady-state value. For scoring, common DIRECT jitter above its recent
10th-percentile baseline is removed, but the corrected value cannot fall below
that DIRECT floor. The exit country's approximate mainland-China physical RTT
contributes only a 20% route-efficiency allowance; 80% of latency utility stays
based on absolute corrected latency, so compensation cannot create an
artificial 1 ms near-perfect score. Displayed latency is a weighted P75/robust
blend, so occasional poor responses remain visible in the experience value.

Priority is `availability^0.75 × latency^0.25`, a geometric utility scaled to
100, with an additional confidence penalty below five availability
observations. Availability therefore remains the dominant factor. Throughput
is deliberately not measured or used for scoring.

Outage-hour vectors are compared with cosine similarity. Automatic groups first
keep the relatively best candidates, then use dissimilar outage periods and add
nodes only when they cover an observed weak hour. They retain at least two when
available and are capped at eight to keep mobile URLTest overhead bounded.
sing-box urltest uses a 100 ms tolerance and does not interrupt existing
connections, preventing meaningless small differences from causing switches.

The generated group order is proxy selector, ordinary, medium, high,
casual-use, all nodes, automatic, Japan automatic, package information.
Important alert selectors, when present, precede the proxy selector. Ordinary uses
non-China nodes at or below 1x (90% of best, 5–8 nodes).
Casual-use selects nodes at or below 0.1x within 90% of its best and keeps at
least three when available. All nodes are ordered by location, multiplier and
normal priority. Medium/high are emitted only when they beat the required
preceding benchmark; the former China group is removed. Unknown exits remain in storage
and probing but are not emitted. Google Play stable mode sends
`services.googleapis.cn` and the complete Google Play set to remote DNS and
proxy before China-direct rules.

ProxyLens does not perform throughput tests or present speed-test traffic
accounting. Its small latency and exit-discovery requests still traverse the
selected proxy exit. SFA/Carton traffic is local to those clients and is not
claimed as server-side data unless a future client reports per-outbound
counters explicitly.

ASN metadata is bound to the current server/domain and is resolved again only
after that server value changes. Exit IP is refreshed during detection. Country
lookups during detection are limited to unknown countries; rule maintenance
also refreshes known exit geography. Names follow updated measured geography.

Carton is stored as one dual-platform Windows/Linux artifact. Website, DNS,
country and node-selection rules are identical, and application bypass rules
include both `.exe` and Linux executable variants. At publication time an
explicit desktop `Linux` or `CachyOS` User-Agent enables `auto_redirect` on the
TUN inbound. Windows, Android, and unknown-platform requests retain the base
artifact because Windows sing-box rejects Linux auto-redirect initialization.

Subscription attempts and successful refreshes have separate timestamps. A
failure never advances the last-success value; after six continuous hours the
publisher adds a visible warning selector, and successful refresh removes it.
The management API reports the SQLite/WAL/SHM byte total and free bytes on the
containing partition. Full-resolution traffic history remains bounded, so no
misleading lifetime probe-traffic total is calculated.

ProxyLens uses the sing-box executable selected by configuration but does not
download, update, or replace it. The version page runs the configured binary's
`version` command and displays only the detected version. Core lifecycle and
upgrades remain the responsibility of the installed package or system administrator.
Mihomo is an input-format compatibility target, not a runtime dependency, so it
is not downloaded or updated. Direct upstream access is preferred; a failure
may use a short-lived local HTTP proxy through the best ordinary-rate,
non-China node across all tasks.
