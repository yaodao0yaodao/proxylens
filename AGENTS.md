# ProxyLens maintenance instructions

Read `docs/HANDOFF.md` first, then `docs/architecture.md` and
`docs/runtime-events.md`. The current source and tests are authoritative when
older conversation requirements conflict. Chinese is the default user-facing
language; keep `README.en.md` consistent with `README.md`.

- Check current official upstream documentation before changing sing-box
  formats, platform behavior, OpenWrt packaging, or client compatibility.
- Prefer structured diagnostics for browsers, desktops and phones. Do not
  repeatedly drive screenshots; ask the owner to perform an interactive step
  if structured diagnostics cannot resolve it.
- Preserve tasks, tokens, canonical node IDs, country sequences and historical
  measurements. Back up the database before migration or live repair. Use the
  API backup or a SQLite-consistent snapshot, not a live database file copy.
- Never commit real subscriptions, generated profiles, device databases,
  credentials, private signing keys, or local attachment files.
- Probe through the actual node exit. Throughput testing was explicitly
  removed; do not restore speed tests, budgets, speed scores or speed groups.
- Never add Linux `auto_redirect` to Windows or Android profiles. Android UAs
  can contain Linux. Unknown operating systems retain the portable profile.
- Do not automatically update the sing-box dependency. Unknown core versions
  must not silently receive a supposedly compatible subscription.
- Do not change published release assets/tags to disguise a new build. Use a
  new release version when publishing, and preserve the existing signing key.
- Run relevant tests and supported-target builds. A configuration `check` is
  not a successful TUN/network runtime test; report the distinction.
- Device access must come from the owner's private credentials and current
  authorization; documentation contains no reusable production credentials.

Build and release instructions: `docs/MAINTENANCE.md`. Outstanding limitations
and prior deployment evidence: `docs/HANDOFF.md`.
