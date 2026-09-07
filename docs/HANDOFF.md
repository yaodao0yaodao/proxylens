# ProxyLens 维护交接（2026-09-08）

本文件供接手的开发者或智能体使用，无需阅读原始对话。先阅读根目录
`AGENTS.md`，再阅读本文件、`architecture.md`、`runtime-events.md`。

## 版本与交付边界

- 仓库：<https://github.com/yaodao0yaodao/proxylens>，维护分支 `main`。
- GitHub 最新正式 Release：`v0.3.2`，包含签名 APK、公钥、校验和、Windows
  x64 完整 ZIP（含 sing-box 1.14.0）。本次交接不发布新二进制版本。
- `c2e816a`：节点合并增加永久别名，数据库从 v13 升级 v14。
- `337fefd`：订阅响应根据明确 Linux/CachyOS 桌面 UA 补充 auto_redirect。
- 上两项尚未包含在 v0.3.2 Release 内，记录在 CHANGELOG 的 Unreleased。
- 上次部署记录：用户路由器运行 `0.3.3-dev`，与 `337fefd` 对应的 ARM64
  二进制 SHA256 为
  `a1098ad9e898c5a95fbc4716c0afb7f8dcf91fe11a9ac4095e2a6682b6bf5e19`。
  这是上次验收记录，不代表本次交接重新登录确认了设备状态。
- 上次路由器 sing-box 为 1.14.0，数据库为 v14。不得用 v0.3.2 程序直接
  打开 v14 数据库回滚；回滚必须使用升级前的匹配数据库备份。
- 2026-09-08 Steam 修复调试版在原路由器部署为 `0.3.3-dev-steam-fix`，
  ARM64 二进制 SHA256 为
  `6f6d8cd0edfb5cf3057a79fca8258e9ac05d347b7325dbf673be92896dbe667d`。
  升级前已通过 `/api/backup` 保存一致性备份，旧二进制也保留在设备的
  `/root/proxylens-backups/`。三个生产任务的 Linux Carton 1.14 配置均通过
  路由器 sing-box 1.14.0 `check`；这仍不等同于 Steam 下载实机验收。

## 当前产品要求（覆盖已废弃的早期方案）

输入 Clash/Mihomo 订阅，定期通过代理出口检测可用性与稳态延迟，按质量
分组后发布同一私有 sing-box 订阅地址，按客户端 UA 返回适配配置。支持多任务。

- 已完全取消下载测速、测速预算、速度/稳定率、下载组、游戏组和各专项优先级。
- 每轮：更新源订阅 → 出口 IP 与延迟检测 → 质量计算 → 配置生成。
  默认每小时，最短 15 分钟。规则独立每 24 小时更新。
- 延迟达到 800ms 或检测失败：记不可用，不存该次延迟。探测公共网络/核心
  故障不能直接计为所有节点故障。具体失败分类见 probe 和 service。
- 评分由置信可用率与延迟决定；具体公式见 architecture 和 quality.go。
  界面延迟是真实测量的稳态值，不能显示物理补偿后的人造 1ms。
- 原始数据和逐轮质量 48 小时；小时观测汇总 90 天；日质量汇总一年；
  当前质量使用最近 30 天。节点记录永久保留，变更历史仅名称/国家/倍率。
- 国家按实际出口 IP 获取。完整检测只为未知国家发起国家查询；规则维护
  刷新已知国家。ASN 按服务器域名解析，不能用出口 ASN 冒充服务器 ASN。
- 名称中文，国家内编号永不复用。新旧节点可能同时修改名称、认证、端口；
  不能只靠名称/short-id 盲目合并，歧义由节点去重界面处理。
- Web 检测次数为成功/总数，可按比值排序，位于可用率前；默认按优先级排序。
- Windows 已有托盘、首次成功启动打开浏览器、本机令牌自动读取、修改端口
  和退出；所有辅助进程应无黑窗口。
- sing-box 核心仅显示版本，不自动升级。

## 两项近期修复的关键点

节点手动合并曾被后续订阅撤销：源订阅继续提供旧的派生 ID，将已移除副本
复活，历史质量留在另一节点。v14 的 `node_identity_aliases` 将副本永久指向
规范 ID，更新订阅先解析别名，自动历史匹配排除已合并副本，重复合并不会再搬
一次历史。链式合并会压平映射。不要删除别名源行或重置国家序号。

用户的一条日本 VLESS 节点已修复并实际更新订阅验收，规范节点保留 200/200
检测，副本保持移除。此前备份在用户设备 `/root/proxylens-backups/` 中；具体
节点、账号、地址由用户私下提供，不放入公开仓库。

Linux 差异在 `web.enableLinuxAutoRedirect` 的响应阶段加入，数据库仍存四种
基础配置：sfa / sfa-1.13 / carton / carton-1.14。响应变体计算独立 ETag，并带
`Vary: User-Agent`。例：`sing-box/1.14.0 Linux CachyOS`。未声明 OS 的 Carton
无法凭空判断运行在 Linux；Android（常含 Linux）不启用。Windows 官方
1.13.19/1.14.0 对开启该字段的 TUN 配置会报 invalid argument，不能全端添加。

## 代码地图

| 位置 | 职责 |
| --- | --- |
| cmd/proxylens | 启停、后台维护、Windows 托盘与静默子进程 |
| internal/config | 命令行、环境变量、平台默认路径 |
| internal/subscription、naming | 拉取/解析、身份输入、倍率、中文名称 |
| internal/store | schema/migration、别名与合并、保留/汇总、在线备份 |
| internal/probe | sing-box 批次、出口 IP、预热与延迟、Direct 基线 |
| internal/quality | 置信可用率、加权延迟、优先级 |
| internal/service | 调度/互斥/取消、任务设置、异步国家查询、配置更新 |
| internal/generator | 分组选择、DNS/分流、1.13/1.14 格式 |
| internal/rules、dependency | 规则缓存与下载；仅查询核心版本 |
| internal/web | 管理 API、订阅分发、嵌入静态 Web 页面 |
| openwrt | UCI/procd、LuCI、安装/卸载脚本和 SDK 配方 |

## 接手时需明确的限制

1. 只有 ARM64 OpenWrt APK 和 Windows x64 ZIP 有正式发布记录；不要宣传
   32 位 ARM/MIPS 支持，目前 SQLite 依赖限制这些目标。
2. `.github/workflows/test.yml` 运行 Go 测试与交叉编译，不自动签名/发布。
   `openwrt/Makefile` 是 SDK 配方，不等同于目前带核心的手工 APK 产物。
   完整包的通用、隔离环境打包流水线仍待实现，参见 MAINTENANCE。
3. Linux 1.13 格式和 1.14 格式在设备上通过过 1.14 核心 check；不能将其
   描述为两个版本都完成了 CachyOS TUN 实机测试。Windows 反例另有实测。
4. 当前 UA 平台判断使用字符串包含检查。将来加强识别时应增加矛盾 OS
   标识、非标准客户端及 Android 的回归覆盖，未知平台保持保守行为。
5. Steam 修复前的 Linux `content_log.txt` 显示：下载区域已明确设为武汉
   CellID 159，但目录仍选择 `hkg1`/`tyo3`。当前修复只把 Linux 主进程
   `steam`/`steamcmd` 直连，保留 `steamwebhelper` 代理。客户端更新订阅后必须
   完全退出并重启 Steam，再以日志中的 CellID 和实际源站做验收。Windows 系统
   代理模式有同类现象：该入站没有进程元数据，桌面 Carton 配置已把 CM 域名
   `steamserver.net` 固定直连并使用本地 DNS（同提交纳入 Unreleased），实测
   `connection_log.txt` 显示 CM 会话曾被代理出口带偏到 `tyo3`/`hkg1`。注意
   Carton 每次启用系统代理都会用内置常量覆写 `ProxyOverride`，因此客户端
   注册表绕过不可持久，订阅规则才是持久层；`Steam.exe` 进程规则仍然禁止。
6. `go test` 中有依赖本地 sing-box 文件的条件跳过；新机器没有该文件时，
   测试通过不代表全部真实配置都校验过。
7. 流量/完整配置/数据库含秘密。管理任务响应有授权后使用的源地址；公开
   日志及问题报告仍需脱敏。不能把用户提供的流量授权视作无限期授权。

## 建议接手顺序

1. 查看 git 状态、最新 commit/Release 和本文件时间，避免把调试版当正式版。
2. 按 MAINTENANCE 在全新检出上构建与测试，阅读条件跳过项目。
3. 有设备维护需求时，从用户获取私有访问材料，读服务版本、UCI、进程参数
   和 API 状态；任何数据迁移前下载一致性备份。
4. 用脱敏复现与测试定位问题，再改实现；不重置生产数据来掩盖身份/调度缺陷。
5. 发布时使用新版本、原签名密钥、明确核心版本、校验和和新 Release。
