# Changelog

## Unreleased

- Stop automatically checking, downloading, or replacing sing-box. The version
  page now reports only the configured core's current version.
- Simplify identity-conflict cards and label the incoming record as waiting for confirmation.
- Fall back across IP-country providers and honor rate-limit cooldowns so a
  usable node does not remain unknown when one provider exhausts its quota.

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
