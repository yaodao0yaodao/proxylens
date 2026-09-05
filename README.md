# ProxyLens

[简体中文](README.md) | [English](README.en.md)

**严选最稳定的节点！**

ProxyLens 是一个装在路由器上的节点自动筛选工具。

你只需要填入 Clash/Mihomo 订阅地址。ProxyLens 会定期检查每个节点是否可用、
延迟高不高，自动挑出更稳定的节点，再生成一条可以直接添加到客户端的
sing-box 订阅地址。手机上的 SFA、电脑上的 Carton，以及 Windows/Linux 原生
sing-box，都可以从这条地址取得适合自己的配置。

节点检测针对的是**实际代理出口**，不是只检查节点服务器能否连接。节点资料、
历史检测结果和名称变化会保存在数据库中，分流规则和节点分组也会自动更新，
平时不需要手工维护。

当前正式安装包适用于 OpenWrt/ImmortalWrt 25.12+ 的
`aarch64_cortex-a53`。由于目前使用的 SQLite 依赖无法在本项目中编译到
32 位 ARM 和 MIPS，因此暂不宣称支持这些架构。

首个版本主要安装在 64 位 OpenWrt/ImmortalWrt 上，提供 LuCI 和 Web 管理页面。
核心程序保留跨平台能力。详细资料：

- [架构说明](docs/architecture.md)
- [OpenWrt 说明](docs/openwrt.md)
- [触发器、超时、重试与并发清单](docs/runtime-events.md)

## 推荐客户端

- **Android 手机：**[SFA（sing-box for Android）官方说明与下载](https://sing-box.sagernet.org/zh/clients/android/)
- **Windows、Linux、CachyOS 电脑：**[Carton 发布与下载](https://github.com/821869798/carton/releases)

SFA 是 sing-box 官方 Android 客户端。Carton 是适用于 Windows 和 Linux 的
第三方 sing-box 图形客户端，不属于 sing-box 官方项目。ProxyLens 会根据
客户端的 User-Agent 自动返回对应配置，两端使用同一条订阅地址即可。

## OpenWrt 快速安装

首次安装必须先让系统信任 ProxyLens 的公开签名公钥。以 root 身份执行：

```sh
cd /tmp
wget -O /etc/apk/keys/proxylens-apk-public.pem https://github.com/yaodao0yaodao/proxylens/releases/download/v0.3.2/proxylens-apk-public.pem
wget -O proxylens-0.3.2-r1_aarch64_cortex-a53.apk https://github.com/yaodao0yaodao/proxylens/releases/download/v0.3.2/proxylens-0.3.2-r1_aarch64_cortex-a53.apk
echo '6892b6228ca07b0153676efa533a7800a9723cc8d7013bd397ebff57c49221d8  proxylens-0.3.2-r1_aarch64_cortex-a53.apk' | sha256sum -c -
apk add ./proxylens-0.3.2-r1_aarch64_cortex-a53.apk
```

以后安装由同一私钥签名的升级包，不需要再次安装公钥。私钥不会上传到
GitHub，也不应安装到路由器或提供给其他人。

安装后打开 LuCI 的 **服务 → ProxyLens**，也可以访问
`http://路由器地址:9099/`。管理令牌可通过以下命令读取：

```sh
uci -q get proxylens.main.admin_token
```

## Windows 运行

从 [v0.3.2 Release](https://github.com/yaodao0yaodao/proxylens/releases/tag/v0.3.2)
下载 `ProxyLens-Windows-x64-0.3.2.zip` 并完整解压，直接运行 `ProxyLens.exe`。
压缩包已经包含匹配的 `sing-box.exe`，不要只复制其中一个文件。

首次成功启动会自动打开默认浏览器，以后程序常驻系统托盘并完全静默运行。
双击托盘图标或右键选择“显示软件”可重新打开管理页；右键还可以修改端口或退出。
本机管理页会自动读取本机令牌，无需手动填写。

## 配置发布与客户端兼容

Web 页面支持创建多个任务，每个任务只有一个私有 sing-box 订阅地址：

- SFA 或带 Android 标识的 sing-box 1.13/1.14 User-Agent 获得 Android 配置。
- Carton 或普通 sing-box 1.13/1.14 User-Agent 获得桌面配置。
- Windows/Linux 原生 sing-box 可以复用对应版本的 Carton 配置；UA 明确包含
  `Linux` 或 `CachyOS` 时，TUN 会额外启用 Linux 推荐的 `auto_redirect`。
- 无法识别、版本过旧或尚未验证的客户端返回 HTTP 406，避免误下错误配置。

Carton 在 Windows 和 Linux/CachyOS 上复用同一份基础配置，并在发布时应用
安全的平台差异。桌面 TUN 模式的
大流量下载程序使用 DIRECT；Google Play、Steam、DNS、节点和国家分流由
ProxyLens 针对中国大陆网络进行组合与修正。

## 默认检测与数据保留

- 默认每 60 分钟执行一次完整检测，可设置为 15 分钟至 7 天。
- 每轮依次更新订阅、刷新出口 IP、检测稳态延迟与可用性、重新计算质量并生成配置。
- 请求失败或实测延迟达到 800 ms 时记录为不可用，不保存该次延迟值。
- 分流规则每 24 小时独立更新一次。ProxyLens 只读取当前 sing-box 版本，
  不会自动下载、更新或替换核心。
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
