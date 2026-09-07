# Changelog

## Unreleased

- Keep the Linux Steam client process DIRECT in TUN mode so its CM session,
  download-region CellID, and content-server directory use the local network;
  keep `steamwebhelper` store/community traffic proxied.

- Add maintainer handoff, build/deployment guidance and agent instructions;
  correct stale platform documentation and include Windows in CI cross-builds.
- Persist explicit and automatic node-identity merges as canonical aliases, so
  later subscription refreshes cannot revive a discarded duplicate or detach
  its accumulated quality history.
- Enable `auto_redirect` for subscription requests that explicitly identify a
  Linux/CachyOS desktop, while keeping Windows, Android, and unknown-platform
  configurations compatible.

## 0.3.2 - 2026-09-05

- Disable subscription copying until the task has generated its first sing-box
  configuration, and show the live "正在生成配置" state instead.
- Route only Microsoft Store regional/package delivery and Windows Update
  endpoints directly with local DNS in desktop profiles, while keeping login,
  purchase, licensing, catalog, and other Store traffic proxied.
- Stop automatically checking, downloading, or replacing sing-box. The version
  page now reports only the configured core's current version.
- Simplify identity-conflict cards and label the incoming record as waiting for confirmation.
- Fall back across IP-country providers and honor rate-limit cooldowns so a
  usable node does not remain unknown when one provider exhausts its quota.
- Add a native Windows tray application with first-run browser opening, port
  editing, local-token login, persistent local data paths, and a clean exit.
- Run ProxyLens and every sing-box/helper subprocess without console windows on
  Windows so scheduled checks remain completely silent.
- Split large probe runs into bounded sing-box batches to prevent readiness
  timeouts on large subscriptions and low-memory routers.
- Show detection successes and totals separately from confidence-adjusted
  availability and allow sorting by the detection ratio.

## 0.3.1 - 2026-09-04

- Show complete safe node identity details for every merge candidate.
- Allow high-cost groups to fall back to the ordinary-group benchmark when
  the medium-cost group is empty.
- Route the no-Japan fallback directly to automatic selection without a
  selector cycle.
- Support selecting a distribution-managed sing-box installation.
- Add a standard command-line version response.

## 0.3.0 - 2026-09-04

- Add synchronized subscription, exit, latency, quality, and configuration cycles.
- Generate version-aware SFA, Carton, and native sing-box configurations.
- Add durable node identity, explicit conflict resolution, and dynamic quality groups.
- Add OpenWrt procd, UCI, LuCI, database retention, backup, and storage controls.
