# Sakamoto 产品需求文档（PRD）

版本：1.0 · 日期：2026-10-12 · 状态：基于当前软件状态回写（现状基线）
关联文档：[README](../README.md)、[tui](tui.md)、[configuration](configuration.md)、[dns-protection](dns-protection.md)、[import](import.md)、[icloud](icloud.md)、[s3-sync](s3-sync.md)、[migration](migration.md)、[virtual-location-feasibility](virtual-location-feasibility.md)、[virtual-location-setup](virtual-location-setup.md)、[release-checklist](release-checklist.md)、[skills.md](../skills.md)

> 本文档描述 **已实现并可验证的现状**，而非理想化目标。凡标注"限制/边界"的条目均为产品承诺的一部分：不夸大能力，不宣称功能对等。

---

## 1. 产品概述

`sakamoto` 是一个围绕 SagerNet sing-box 核心（v1.14.2+，native API / libbox 模式）的跨平台代理控制器，灵感来自 Shadowrocket 的 Home / Config / Data / Settings 工作流。**与 Shadowrocket、SagerNet 无任何隶属或背书关系**。

三条产品线：

| 平台 | 形态 | 分发 |
|---|---|---|
| macOS | 鼠标可用的终端 TUI + launchd 守护 | Homebrew tap / 源码构建，当前 tag `v0.2.13` |
| iOS | 原生 SwiftUI App + PacketTunnel 扩展 | TestFlight / App Store 签名已验证（当前 0.1.x） |
| Android | 原生 Material 3 Kotlin App | 签名 APK 发布（当前 v0.1.6），Android 7.0+ |

核心卖点：一套共享 Go 逻辑（`pkg/mobile*`），三端一致的配置语义；Shadowrocket `.conf` 一键导入；可选的受保护 DNS、回退看守、虚拟定位等高级能力全部显式 opt-in。

## 2. 背景与要解决的问题

1. sing-box 原生无桌面 GUI；macOS 用户需要一个不依赖 GUI 框架、可脚本化、可审计的控制器。
2. Shadowrocket 配置（订阅、规则、include）是事实标准，但桌面/移动端缺少保真、可回滚的导入路径。
3. 代理 + DNS + 系统代理 + 回退是多组件协作问题，用户需要"状态可见、失败可恢复"，而非黑盒。
4. 测试定位类需求（虚拟定位）在 iOS/Android 上没有公开、诚实的实现路径说明；本项目以可行性研究驱动实现，明示边界。

## 3. 目标用户

- 主要：已有 Shadowrocket/sing-box 配置、希望在 macOS 上以终端工作流管理代理的高级个人用户。
- 次要：需要在 iOS/Android 上运行同一套节点/规则语义的同一批用户。
- 非目标：需要图形化订阅市场、一键翻墙、多人共享分发的用户；需要 HTTP rewrite / MITM / JavaScript 脚本的用户（sing-box 不实现，不做对等承诺）。

## 4. 设计原则（不可变约束）

来自 `skills.md` 与既有实现，属产品级 invariants：

1. **永不提交运行时私密文件**：API secret、nodes.txt、订阅 token、config.json、`.srs`、日志、proxy-restore.json、auto-proxy.json、pending 密钥。
2. **链式出站方向固定**：SOCKS 出口经 `MainProxy` + `detour` 链接；Shadowrocket `FINAL,DIRECT` 映射为 `route.final=direct`。
3. **回退语义固定**：`RealityAuto → OthersAuto` 有序回退，必须消费新鲜 URL-test 结果才切换；手动选择不被覆盖。
4. **互斥原则**：Shadowrocket 与 sing-box 不得同时持有活动 TUN/VPN；守护进程检测到会停掉 sing-box。
5. **导入失败关闭**：include / 远程规则集加载失败时不替换上一个可用配置。
6. **状态词表有意义**：`Untested` / `Testing…` / `Reachable Nms` / `Failed/timed out`；选中点不等于连通。
7. **同步只动源文件**：iCloud/S3 仅同步源（nodes、本地 conf 及其递归相对 include），永不同步生成态、日志、socket、凭证；不覆盖并发编辑。
8. **能力诚实**：probe 成功只证明所示路径；DNS 保护不宣称零泄漏；虚拟定位不宣称系统级 GPS 覆盖。

## 5. 功能需求与实现状态

### 5.1 macOS TUI（已实现）

| 功能 | 需求 | 状态 |
|---|---|---|
| Home 页 | 连接/断开、TUN 启动 vs 探测通过的双相状态、链摘要与 SOCKS 出口、节点组延迟、左键选节点、右键 Test/Details、滚轮 | ✅ |
| 流量模式 | `m` 循环 Rule→Global→Direct，经 native API 校验并验证结果；旧配置如实报"需重新生成"；保留私有/Tailscale 排除、广告 reject、STUN/QUIC 阻断 | ✅ |
| Config 页 | 导入 Shadowrocket `.conf`（URL/本地，递归相对 include）、浏览 General/规则/DNS、订阅源管理、手动节点链接、Policy 规则（proxy/direct/reject，支持精确/`*.`/`keyword:`/`cidr:`/URL） | ✅ |
| 设置表单 | TUI 内浮层表单编辑 `sakamoto.yaml`：选项原地循环、自定义值聚焦编辑；`Ctrl+S`/`Esc` 提交/丢弃 | ✅ |
| Regenerate | 重生成 config.json 与 `.srs`；远程源同时刷新；需断开后重连生效 | ✅ |
| Data 页 | 流量、活动连接（点击查路由、可关闭）、日志（含服务器地址警告） | ✅ |
| Settings 页 | 可点击开关与值编辑；保护 DNS、回退链、系统代理、S3、iCloud、实验项 | ✅ |
| About/Copyright | 半块字符肖像（truecolor/ANSI-256/ASCII 降级，`SAKAMOTO_ASCII_ART=1`）、署名与非隶属声明 | ✅ |
| 键盘回退 | Tab/1–5、j/k、Enter、Esc、c、u、a、n、e、g、m、q；SGR 鼠标（Ghostty 验证） | ✅ |
| 速度测试 | `internal/tui/speedtest.go` | ✅ |
| 节点编辑遮蔽 | 编辑提示中遮蔽节点链接/订阅 URL，`Ctrl+R` 揭示，`Ctrl+U` 清空 | ✅ |

### 5.2 macOS 系统服务（已实现）

| 功能 | 需求 | 状态 |
|---|---|---|
| `sakamoto setup` | 交互式安装 LaunchDaemon/LaunchAgent；`--check` 只读预检；拒绝替换已存在/残缺服务；不启动 VPN | ✅ |
| root supervisor（daemon） | 以 root 管理 sing-box TUN；用户 socket（0600）接收 TUI 的 connect/disconnect | ✅ |
| watcher（watch） | 自动组回退（RealityAuto→OthersAuto，`recover_after` 连续健康才切回）；无 TUI 也运行；尊重手动选择 | ✅ |
| `sakamoto recover` | 请求 watcher 立即做一轮 MainProxy URL 测试（供 pi-proxy-guard 类集成）；不重启 TUN、不强制节点；单实例、限用户、限速 | ✅ |
| 重启边界 | `RunAtLoad`/`KeepAlive` 拉起 supervisor/watcher，但**不自动连 TUN**；受保护 DNS 的快照恢复仅在恢复成功且 53 空闲时发生 | ✅（文档承诺） |
| `rotate-api` | 预存新 API 密钥，下次正常连接时应用；`--apply-now` 接受短暂重连；密钥不打印 | ✅ |
| 系统代理（浏览器） | `system_proxy.enabled` + `mixed_inbound.enabled` 时，连接期间写入 macOS HTTP/HTTPS 代理，断开恢复；`proxy-restore.json` 本地私有 | ✅ |
| 受保护系统 DNS | opt-in：单 sing-box 进程 loopback TCP/UDP 53；UDP+TCP 双向健康后才接管；`dns-restore.json` 快照区分 static/DHCP；`dns-restore`/`dns-prepare` 子命令；Direct 模式下 DNS 仍受保护 | ✅（root 守护路径） |
| Shadowrocket 互斥 | 检测到 Shadowrocket 重连即停 sing-box | ✅ |
| 迁移 | `~/.config/sakamoto → ~/.sakamoto`：备份、断开、恢复系统代理、替换服务、回滚；活 socket 下不替换 plist | ✅ |

### 5.3 实验能力（macOS，已实现）

- **Unmatched 三态策略**：`off`（直连）/`on`（走代理）/`auto`（仅在重复、互相印证的 unmatched 直连拨号超时且代理健康后学习域名，存 `auto-proxy.json`，本地私有）；不复试 HTTP 挑战、不改已被代理规则命中的主机。
- **CF region auto-proxy**：独立开关；识别 unmatched 直连上的 Cloudflare 区域拒绝信号（如 error 1009），写入 proxy 规则并重连；忽略普通 403/挑战/DNS 失败/已有代理路由。
- **Direct failure threshold**：默认 3（1–20 次间隔性直连失败）。

### 5.4 iOS App（已实现/部分验证）

| 功能 | 状态 |
|---|---|
| `Libbox.xcframework`（libbox+mobilecore，iOS+模拟器，`with_tailscale`+`with_gvisor`，单 Go runtime/进程） | ✅ 构建脚本 |
| Xcode App + PacketTunnel 扩展（xcodegen，`project.yml` 目标 iOS 16） | ✅ 未签名构建验证 |
| 内置 Tailscale 端点（状态流、exit node、logout、ping；Keychain 存 auth key；启动注入配置） | ✅ |
| Config：Import / Policy / Nodes & sources / Generate·Apply（`mobileconf`+`importer.go` 真实现、已测） | ✅ |
| Home/Config/Data/Settings 四页 + 代理链、规则检视、批量节点测试 | ✅ |
| iCloud 源同步（actor、冲突安全 baseline、`NSFileCoordinator`、security-scoped bookmark 选 TUI 的 Drive 文件夹；容器 id 曾用占位符，生产签名导出已验证 entitlement） | ✅ |
| Widgets（小/中/大，iOS 17+ 交互）、Shortcuts/Siri（Connect/Disconnect/Toggle/Status）、Control Center（iOS 18+，直接操作已存 profile，不开 App） | ✅ |
| **虚拟定位**（见 §5.6）：受信设备 CA → 隧道内重写 Apple WLOC 响应；MapKit 选点/搜索；iOS 27+ 按参考项目兼容性禁用 | ✅（opt-in，边界明示） |
| Tailscale 网络恢复：path 回调刷新接口前缀/flags/MTU | ✅ |
| SimAgentationPlus 模拟器检视（独立 `project.sim-agentation.yml`，仅 Debug 模拟器，iOS 17+） | ✅ 开发工具 |
| 设备端 VPN 行为真机验证 | ⚠️ 仍标注需 runtime verification |

### 5.5 Android App（已实现）

| 功能 | 状态 |
|---|---|
| 原生 Material 3 VPN 客户端（Kotlin + libbox/mobilecore/mobileexperiment AAR + JNI） | ✅ v0.1.6 签名 APK + SHA256SUMS |
| 原生 VPN 诊断、节点/组编辑、widgets、通知控制、可选实验项 | ✅ |
| 虚拟定位：AOSP `LocationManager.addTestProvider` 路径 + GMS `FusedLocationProviderClient.setMockMode` 适配；MapLibre Native + OpenFreeMap 瓦片 + Photon 搜索（无 map key、无 GMS 依赖）；路线模拟（定时 stops） | ✅（需用户在开发者选项选定 mock location app；`isMock` 不伪装） |
| 深色模式下 route overlay 可读性 | ✅ 已修 |
| Tailscale 传输会话保持 | ✅ |
| 发布签名与 debug 签名不同，切换需先保存数据再手动迁移 | ✅ 文档承诺 |

### 5.6 共享 Go 包层

`pkg/`：`mobileconf`（conf 解析/Policy/分享链接/ondemand）、`mobilecore`（核心控制契约）、`mobilegen`（生成 + Tailscale 路由 + relay + network）、`mobilelocation`（定位代理/编码）、`mobileexperiment`、`mobilerelay`、`sourcesync`（S3 bundle，三端同一引擎与线格式 `PREFIX/sources-v1.json`，ETag + `If-Match`/`If-None-Match` 条件写）。

`internal/`：`config`、`core`（session/mode/probe/node/configstate/notice）、`gen`（生成器 + golden 测试）、`sbclient`、`svc`（守护）、`sysdns`、`sysproxy`、`watch`（recovery）、`experiment`、`icloud`、`s3sync`、`tui`、`security`。

### 5.7 同步功能（已实现，opt-in）

- **iCloud Drive**：二次确认后开启；`include_conf` 同步本地 conf + 递归相对 include；`additional_paths` 列 nodes/其他源文件；watcher 内热重载无需重连；不覆盖并发编辑。
- **S3/R2/MinIO**：path-style + SigV4 + HTTPS 强制、拒绝重定向；仅 GetObject/PutObject 权限；掩码输入密钥存本地私有文件；watcher 每分钟同步；冲突即停。

## 6. 非功能需求

| 维度 | 要求 | 现状 |
|---|---|---|
| 隐私 | 凭证不出本地；iCloud/S3 不同步生成态/密钥/日志；CA 私钥存 App Group 设备专属 Keychain | ✅ |
| 安全 | API socket 0600、限用户单实例限速；密钥永不打印；TLS 校验不可关（`never set insecure`） | ✅ |
| 许可 | 代码 GPL-3.0-or-later（依赖 sing-box 同许可）；About 肖像 CC BY 2.0 单独署名 | ✅ |
| 质量 | `go test ./...`、`-race`、`vet`、pinned golangci-lint v2.14.0、CI 强制；TUI 在 110x30 与 72x20、64x16/80x24/120x36 验证 | ✅ |
| 构建 | mise 管理工具链；`cli:build` 打包 arm64/Intel + ad-hoc 签名校验；`-trimpath` + `go-size-profile.json` 瘦身策略；macOS 保持 CGO | ✅ |
| 诚实性 | 所有能力声明附边界（probe、DNS、虚拟定位、重启不自连） | ✅ |

## 7. 验收标准（关键场景）

1. 全新 Homebrew 安装 → 拷模板 → 导入 Shadowrocket conf（含相对 include）→ `sing-box check` 通过 → `setup` 安装服务 → Connect 到 *Network reachable*。
2. 断开后重连：TUN 不自连（文档承诺），watcher/daemon 存活。
3. 导入失败：保留上一个可用配置，状态不损坏。
4. Shadowrocket 重连：sing-box 被停，无双 TUN。
5. iCloud/S3 并发编辑：冲突停手，不互相覆盖。
6. iOS 虚拟定位：CA 信任 → 选点 → 开模拟 → VPN 连接 → 诊断面板显示改写计数；iOS 27+ 拒绝启用。
7. Android 虚拟定位：未选 mock location app 时显示清晰设置引导；运行中权限被撤可捕获。
8. 公开仓库 gate：`release-checklist.md` 中 secret-audit grep、功能 gate 全绿。

## 8. 已知限制（产品承诺边界）

- 不实现 Shadowrocket 的 HTTP URL/Header/Body rewrite、MITM、JS 响应脚本。
- `.db.rule` 编译产物不等于原始 `.conf`；规则需对照当前导出验证。
- probe/延迟数字不构成全网可用性证明；DNS 保护不覆盖 App 自带 DoH/硬编码 DNS。
- 重启后不自动连接 TUN；受保护 DNS 不自动恢复。
- iOS 虚拟定位是网络响应重写，非 GPS 注入；Android 定位标记 `isMock`，客户端可拒绝。

## 9. 待办 / 后续方向（Open items）

- iOS 设备端 VPN 行为真机回归（TestFlight 前）。
- iOS 生产 iCloud 容器 id 最终确认（当前 entitlement 已验证、文档曾记占位符）。
- Android Fused adapter 在 GMS 认证设备上的评估继续。
- 工作区未提交改动：`SystemSurfaces`（iOS/Android）、`virtual-location-setup.md`、新增 `tools/main.go` —— 需确认归属（疑似系统表面/虚拟定位工具）。
- 无人值守自动连接为独立 opt-in 工作项（当前明确不承诺）。
