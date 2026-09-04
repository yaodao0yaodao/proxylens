#!/bin/sh
set -eu

archive="${1:-proxylens-openwrt-aarch64.tar.gz}"
[ "$(id -u)" = 0 ] || { echo '请以 root 运行'; exit 1; }
[ -f "$archive" ] || { echo "找不到 $archive"; exit 1; }

config_backup="/tmp/proxylens-config-backup.$$"
[ -f /etc/config/proxylens ] && cp -p /etc/config/proxylens "$config_backup"
tar -xzf "$archive" -C /
[ -f "$config_backup" ] && { cp -p "$config_backup" /etc/config/proxylens; rm -f "$config_backup"; }
chmod 0755 /usr/bin/proxylens /usr/lib/proxylens/sing-box /etc/init.d/proxylens
chmod 0600 /etc/config/proxylens
/etc/init.d/rpcd restart
rm -f /tmp/luci-indexcache /tmp/luci-modulecache/* 2>/dev/null || true
/etc/init.d/proxylens enable
uci set proxylens.main.enabled='1'
# Remove legacy speed-test budget entries. Schema v13 also removes their
# database counterparts because throughput testing no longer exists.
uci -q delete proxylens.main.speed_budget_daily_mib
database_path="$(uci -q get proxylens.main.database_path || true)"
if [ -z "$database_path" ] || [ "$database_path" = '/var/lib/proxylens/proxylens.db' ]; then
	uci set proxylens.main.database_path='/etc/proxylens/proxylens.db'
fi
uci -q delete proxylens.main.speed_budget_monthly_mib || true
uci commit proxylens
/etc/init.d/proxylens restart
echo "ProxyLens 已安装：http://$(uci -q get network.lan.ipaddr):$(uci -q get proxylens.main.port)/"
