# ProxyLens

[简体中文](README.md) | [English](README.en.md)

**严选最稳定的节点！**

ProxyLens 是一套可长期运行、可跨平台移植的代理节点质量分析与 sing-box
配置发布服务。首个可安装版本面向 64 位 OpenWrt/ImmortalWrt，集成 procd、
UCI 和 LuCI；核心程序也可在 Windows 与 64 位 Linux 上构建。

它可以导入 Clash/Mihomo 订阅，永久维护节点身份及关键字段变更，通过每个
节点的**实际出口**检测可用性和稳态延迟，使用最近 30 天的数据计算质量，
自动生成分流与节点组，并通过同一个、可识别 User-Agent 的订阅地址向 SFA、
Carton 和原生 sing-box 发布匹配的配置。

当前正式安装包适用于 OpenWrt/ImmortalWrt 25.12+ 的
`aarch64_cortex-a53`。由于目前使用的 SQLite 依赖无法在本项目中编译到
32 位 ARM 和 MIPS，因此暂不宣称支持这些架构。

详细资料：

- [架构说明](docs/architecture.md)
- [OpenWrt 说明](docs/openwrt.md)
- [触发器、超时、重试与并发清单](docs/runtime-events.md)

## OpenWrt 快速安装

首次安装必须先让系统信任 ProxyLens 的公开签名公钥。以 root 身份执行：

```sh
cd /tmp
wget -O /etc/apk/keys/proxylens-apk-public.pem https://github.com/yaodao0yaodao/proxylens/releases/download/v0.3.1/proxylens-apk-public.pem
wget -O proxylens-0.3.1-r1_aarch64_cortex-a53.apk https://github.com/yaodao0yaodao/proxylens/releases/download/v0.3.1/proxylens-0.3.1-r1_aarch64_cortex-a53.apk
echo '1006d19223557eb861ab6ce8904ffe312b6bb78bfac077ed9606e5628cfcbf0d  proxylens-0.3.1-r1_aarch64_cortex-a53.apk' | sha256sum -c -
apk add ./proxylens-0.3.1-r1_aarch64_cortex-a53.apk
```

以后安装由同一私钥签名的升级包，不需要再次安装公钥。私钥不会上传到
GitHub，也不应安装到路由器或提供给其他人。

安装后打开 LuCI 的 **服务 → ProxyLens**，也可以访问
`http://路由器地址:9099/`。管理令牌可通过以下命令读取：

```sh
uci -q get proxylens.main.admin_token
```

## 配置发布与客户端兼容

Web 页面支持创建多个任务，每个任务只有一个私有 sing-box 订阅地址：

- SFA 或带 Android 标识的 sing-box 1.13/1.14 User-Agent 获得 Android 配置。
- Carton 或普通 sing-box 1.13/1.14 User-Agent 获得桌面配置。
- Windows/Linux 原生 sing-box 可以复用对应版本的 Carton 配置。
- 无法识别、版本过旧或尚未验证的客户端返回 HTTP 406，避免误下错误配置。

Carton 在 Windows 和 Linux/CachyOS 上使用同一份配置。桌面 TUN 模式的
大流量下载程序使用 DIRECT；Google Play、Steam、DNS、节点和国家分流由
ProxyLens 针对中国大陆网络进行组合与修正。

## 默认检测与数据保留

- 默认每 60 分钟执行一次完整检测，可设置为 15 分钟至 7 天。
- 每轮依次更新订阅、刷新出口 IP、检测稳态延迟与可用性、重新计算质量并生成配置。
- 请求失败或实测延迟达到 800 ms 时记录为不可用，不保存该次延迟值。
- 分流规则和程序使用的 sing-box 依赖每 24 小时独立检查一次。
- 原始检测与逐轮质量快照完整保留 48 小时，之后汇总成小时数据并保留 90 天；
  24 小时质量汇总保留 1 年，质量计算使用最近 30 天数据。

当源订阅连续 6 小时无法更新时，生成配置会在最上方显示更新失败提示，同时
继续保留最后一次可用节点。上游资源默认直连获取；直连失败时，ProxyLens
可以临时使用所有任务中优先级最高的普通倍率、非中国节点访问。

## 数据与存储

OpenWrt 默认将数据库保存到 `/etc/proxylens/proxylens.db`，避免使用通常位于
内存文件系统的 `/var`。LuCI 可以修改数据库位置；应用设置时会同时移动数据库、
WAL 和 SHM 文件，并拒绝覆盖目标位置已有的数据库。

检测频率是唯一面向用户的检测周期设置。数据库维护和容量限制即使在没有手动
运行任务时也会生效。
