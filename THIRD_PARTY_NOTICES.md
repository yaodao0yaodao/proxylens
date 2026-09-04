# Third-party notices

ProxyLens uses:

- `modernc.org/sqlite` (BSD-3-Clause) and transitive packages.
- `gopkg.in/yaml.v3` (MIT/Apache-2.0).
- `github.com/google/uuid` (BSD-3-Clause).
- MetaCubeX `meta-rules-dat` remote sing-box rule sets. They are not copied
  into ProxyLens; clients fetch current `.srs` files from that project.
- The Carton desktop download-exemption process list is refreshed every rule
  cycle from the actively maintained blackmatrix7 `ios_rule_script` Download
  list. Built-in Windows/Linux names remain as an offline fallback.

The standalone OpenWrt release archive bundles an unmodified official sing-box 1.14.0
ARM64 musl executable, licensed under GPL-3.0-or-later. Its license is installed
at `/usr/share/licenses/proxylens/SING_BOX_LICENSE`; the exact corresponding
source is available from:

https://github.com/SagerNet/sing-box/tree/v1.14.0
