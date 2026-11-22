# TelegramWindows 设计文档（Go 语言 Windows 客户端）

| 项 | 值 |
| --- | --- |
| 文档状态 | v0.1（架构评审稿） |
| 所属项目 | `LCecho/Telegram`（Android 官方源码 fork，GPLv2） |
| 新增子项目 | `TelegramWindows/`（Go，独立 `go.mod`，不参与 Gradle 构建） |
| 协议参考 | 见 [`API-ANALYSIS.md`](./API-ANALYSIS.md)（含源码行号对照） |
| 目标平台 | Windows 10 1809+ / Windows 11，x64（后续 arm64） |
| 许可 | GPLv2（与仓库一致），不得复用上游 Java/C++ 源码 |

---

## 1. 需求分析

### 1.1 为什么要做

现有仓库只有一个 Android 客户端（454 MB、2,292 个 Java 文件、协议层 42 个 C++ 文件，构建需要 NDK 27 + Gradle 8.13 + 完整子模块）。桌面端缺位。新增 Go 实现的 Windows 客户端的目标是：

1. **零原生依赖**：一个 `tgw.exe`，不需要 OpenSSL/NDK/JRE，拷走即用；
2. **协议可控**：MTProto 全链路自己实现，便于审计、调试、扩展（例如企业内网代理、审计日志）；
3. **可测试**：协议层可脱离 UI 单测，`tgw-cli` 能在 CI 里跑真实握手；
4. **不与 Android 侧耦合**：嵌套 `go.mod`，Gradle 不会扫描到，Android 构建零影响。

### 1.2 用户与场景

| 角色 | 场景 | 诉求 |
| --- | --- | --- |
| 普通桌面用户 | 上班/家用 PC 收发消息 | 启动快、常驻托盘、系统通知 |
| 多设备用户 | 与手机端消息同步 | 历史一致、已读同步、草稿 |
| 受限网络用户 | 需要代理 | SOCKS5 / HTTP / MTProxy |
| 开发者/运维 | 排查协议问题 | `tgw-cli` + 结构化日志 + 抓包回放 |

### 1.3 功能范围

**MVP（M1–M2，必须）**
- 手机号登录（验证码 + 两步验证密码）、登出、多 DC 切换
- 会话列表（置顶/未读/最后消息）、按时间倒序
- 单聊/群聊/频道：历史分页、文本消息收发、回复、编辑、删除
- 消息已读、正在输入（可选）
- 系统通知（点击跳转会话）
- 本地缓存（离线可看历史）
- 代理配置（SOCKS5/HTTP/MTProxy）

**V1（M3，应当）**
- 图片/文件：发送、下载、进度、断点续传、缓存清理
- 头像/表情基础渲染（静态 emoji、webp 贴纸首帧）
- 搜索（本地 FTS5 + 服务端 `messages.search`）
- 多账号（最多 3 个）、账号切换

**非目标（明确不做，避免范围膨胀）**
- 语音/视频通话（`tgcalls`）、直播
- Secret Chat（E2E，`tde2e`）
- Stories、Wallet/TON、内嵌 WebApp（`wamr`）
- 动画贴纸完整渲染（Lottie 播放器）
- 自建更新服务端（首版只做"检查更新 + 打开下载页"）

### 1.4 非功能需求

| 指标 | 目标 |
| --- | --- |
| 冷启动到会话列表可见 | ≤ 1.5 s（缓存优先，网络后到） |
| 常驻内存（1 万条消息缓存） | ≤ 150 MB |
| 单条消息端到端延迟（正常网络） | ≤ 300 ms |
| 可执行文件体积 | ≤ 30 MB（不含 WebView2 运行时） |
| 崩溃率 | 会话内 0 panic 逃逸到 UI 线程；panic 统一 recover 并写 dump |
| 安全 | auth_key 仅以 DPAPI 密文落盘；日志零敏感信息（手机号/验证码/消息正文） |

### 1.5 约束

- **必须自备 `api_id/api_hash`**（上游 README 要求；禁止复用 Android 版的，会被封）。
- 应用名不得含 "Telegram"，图标不得用官方纸飞机。
- 无 cgo（`CGO_ENABLED=0`），保证单文件分发与交叉编译。
- WebView2 运行时：Win11 自带；Win10 需检测缺失并给下载引导。

---

## 2. 总体架构

### 2.1 分层

```
┌─────────────────────────────────────────────────────────────────┐
│ 表现层   internal/ui/win  (WebView2 宿主 + Win32 窗口/托盘/通知)  │
│          web/  (HTML + TS 前端，通过 JSON-RPC 调后端)             │
└───────────────────────────┬─────────────────────────────────────┘
                            │  JSON-RPC 2.0（postMessage / 命名管道）
┌───────────────────────────▼─────────────────────────────────────┐
│ 应用层   internal/app     用例编排：Login / SendMessage /         │
│         LoadHistory / UpdatePump / EventBus（有序事件 → UI）      │
└───────────────────────────┬─────────────────────────────────────┘
┌───────────────────────────▼─────────────────────────────────────┐
│ 领域层   internal/tg      高层客户端：Auth、Dialogs、Messages、   │
│         Peers 缓存、Files（上传下载分片）、Updates 应用           │
├──────────────────────────────────────────────────────────────────┤
│ RPC 层   internal/tl      生成类型 + 编解码注册表（constructor id）│
├──────────────────────────────────────────────────────────────────┤
│ 协议层   internal/mtproto crypto / handshake / session /          │
│         transport / rpc / dcs / proxy                             │
├──────────────────────────────────────────────────────────────────┤
│ 网络     net.Dialer（TCP/TLS）+ SOCKS5/HTTP/MTProxy 拨号器         │
├──────────────────────────────────────────────────────────────────┤
│ 存储     internal/store  SQLite(WAL) + 文件缓存 + DPAPI 密钥       │
└──────────────────────────────────────────────────────────────────┘
```

**依赖方向铁律**：上层可依赖下层，下层**禁止**反向依赖；`mtproto` 与 `tl` 不得 import `store`/`app`/`ui`（用接口反转）。

### 2.2 目录结构

```
TelegramWindows/
├─ go.mod                      module github.com/LCecho/Telegram/TelegramWindows
├─ Makefile                    build / test / lint / gen / run
├─ cmd/
│  ├─ tgw/                      GUI 主程序（Windows 专用，build tag windows）
│  └─ tgw-cli/                 无界面：selftest / login / send / dump
├─ internal/
│  ├─ mtproto/
│  │  ├─ crypto/               AES-IGE、KDF、RSA-PKCS1、DH、SHA
│  │  ├─ handshake/            req_pq → DH → dh_gen_ok（含 PFS temp key）
│  │  ├─ session/              msg_id、seq_no、salt、去重窗口、重放
│  │  ├─ transport/            EF / EE / DD / TLS 四态 + 嗅探
│  │  ├─ rpc/                  rpc_result / rpc_error / container / gzip / ack
│  │  └─ dcs/                  DC 表、配置下发、迁移
│  ├─ tl/                      生成代码：types_gen.go、registry_gen.go（禁手改）
│  ├─ tgen/                    TL schema 解析与代码生成（cmd 形式：tgen/main.go）
│  ├─ schema/                  api.tl / mtproto.tl（版本化，带来源与校验和）
│  ├─ tg/                      Auth、Updates、Peers、Messages、Files
│  ├─ store/                   SQLite 仓储、迁移、FTS5
│  ├─ app/                     用例 + EventBus + UI 网关
│  ├─ ui/win/                  WebView2 宿主、窗口、托盘、Toast、DPI、剪贴板
│  ├─ sec/                     DPAPI、内存清零、日志脱敏、公钥 pin
│  ├─ update/                  版本检查 + Ed25519 签名校验
│  └─ platform/                单实例锁、开机启动、数据目录
├─ web/                        前端源码与构建产物（index.html + assets）
├─ docs/
│  ├─ DESIGN.md                本文
│  └─ API-ANALYSIS.md          协议事实与源码对照
└─ testdata/                   golden 帧、TL 向量、握手脚本
```

### 2.3 进程与线程模型

单进程，UI 与 core 同进程（首版简化；双进程可后续拆分）：

| 执行体 | 职责 | 约束 |
| --- | --- | --- |
| UI 线程（Win32 STA） | WebView2 消息循环 | 禁止阻塞；所有 core 调用走异步 RPC |
| 每 DC 1 个 writer goroutine | 串行组包、加密、写 socket | **唯一**写者，杜绝 msg_id 竞态 |
| 每连接 1 个 reader goroutine | 读帧、解密、分发 | 只投递到 channel，不做业务 |
| UpdatePump（1 个） | 串行应用 updates + 落库 | 与 DB 同事务推进 pts/qts/seq |
| 分片上传/下载 worker 池 | 带令牌桶 | 与 RPC 分连接，不阻塞消息 |

**并发铁律**
1. 任何 socket 读写只允许上述拥有者执行；其他 goroutine 通过 channel 投递。
2. `msg_id` 用连接级 `atomic.Int64`，生成函数保证单调且 4 对齐。
3. 不使用全局锁保护共享状态；状态归属单一 goroutine（actor 模型）。
4. 所有阻塞调用带 `context`，取消必须可传播（关闭连接 → 所有 pending RPC 立即返回 `ErrCanceled`）。

### 2.4 关键接口（草案）

```go
// internal/mtproto
type Transport interface {
    Send(ctx context.Context, payload []byte) error
    Recv(ctx context.Context) ([]byte, error)
    QuickAck() <-chan uint32       // bit31 清除后的 ack id
    Close() error
}

type Session interface {
    Invoke(ctx context.Context, req tl.Object, opts ...CallOption) (tl.Object, error)
    // opts: 目标 DC、连接类型（generic/download/upload）、是否要 ack、超时
}

type DCProvider interface {           // 由 dcs 包实现，便于测试替身
    Primary() DC
    ByID(id int) (DC, bool)
    Apply(cfg *tl.Config)
}
```

```go
// internal/tg
type Client struct{}                 // 组合 Session + Store + EventBus
func (c *Client) Login(ctx context.Context, p LoginParams) (AuthState, error)
func (c *Client) Dialogs(ctx context.Context, limit int) ([]Dialog, error)
func (c *Client) History(ctx context.Context, peer Peer, before int32, limit int) ([]Message, error)
func (c *Client) SendText(ctx context.Context, peer Peer, text string, replyTo int32) (Message, error)
func (c *Client) RunUpdates(ctx context.Context) error   // 常驻
```

```go
// internal/store
type Store interface {
    UpsertPeers(ctx context.Context, tx Tx, peers []Peer) error
    SaveMessage(ctx context.Context, tx Tx, m *Message) error
    History(ctx context.Context, peerID int64, before, limit int32) ([]Message, error)
    UpdatesState(ctx context.Context) (UpdatesState, error)
    AdvanceState(ctx context.Context, tx Tx, s UpdatesState) error
}
```

```go
// internal/app —— UI 只认这个（JSON-RPC 方法名与下表一致）
type Gateway interface {
    Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
    Events() <-chan Event       // 推送：message.new / dialog.update / conn.state
}
```

### 2.5 核心流程

**登录**
```
UI --auth.sendCode--> app --auth.sendCode--> tg --> mtproto(未授权会话)
                                    <-- sentCode(type/nextType/timeout)
UI --auth.signIn(code)--> ... --SESSION_PASSWORD_NEEDED--> UI 收密码
UI --auth.checkPassword--> SRP 计算（sec/srp）--> ready
  ready 后：help.getConfig → 写 DC 表 → updates.getState → UpdatePump 启动
```

**发消息**
```
UI --messages.send--> app(校验长度/权限) --> tg.SendText
  → 本地先落库（outbox, state=sending）→ 事件 message.new(local) 推 UI
  → mtproto.Invoke(messages.sendMessage)（随机 id 用于幂等）
  → 响应 updates → UpdatePump 用真实 msg_id 替换本地记录 → 事件 message.sent
失败：FLOOD_WAIT → 保持 outbox + 定时重试；永久错误 → state=failed（UI 可重发）
```

**收更新**
```
reader → rpc/updates 分流 → UpdatePump 串行:
  1) 校验 seq/pts 连续性；有洞 → updates.getDifference（含 date）
  2) 事务内：写 peers/messages + 推进 pts/qts/seq/date
  3) 提交后发事件（先库后事件，保证 UI 读到的与库一致）
```

### 2.6 存储设计

SQLite（`modernc.org/sqlite`，纯 Go），WAL 模式，`synchronous=NORMAL`。

```sql
create table peers (
  id integer primary key, type integer not null, access_hash integer,
  username text, title text, phone text, photo_id integer, updated_at integer
);
create table dialogs (
  peer_id integer primary key, top_msg_id integer, unread_count integer,
  unread_mentions integer, pinned integer, mute_until integer,
  last_msg_date integer, draft text
);
create table messages (
  id integer primary key autoincrement,
  peer_id integer not null, msg_id integer not null, date integer not null,
  out integer not null, state integer not null,    -- 0 sent 1 sending 2 failed
  sender_id integer, text text, reply_to integer, media_json text, raw blob
);
create unique index ux_messages on messages(peer_id, msg_id);
create index ix_messages_date on messages(peer_id, date desc);
create table files (id integer primary key, dc_id integer, access_hash integer,
  size integer, mime text, local_path text, state integer);
create table kv (k text primary key, v blob);      -- updates state / dc options / flags
create virtual table msg_fts using fts5(text, content='messages', content_rowid='id');
```

密钥保护：`auth_key`、`api_hash`、DB 加密密钥材质统一放 `sec`，用 `CryptProtectData`（DPAPI，当前用户作用域）加密后落盘；内存中 `[]byte` 用完 `memclr`。

缓存策略：消息按会话保留最近 N 条（默认 5,000）+ 30 天；媒体缓存 LRU 上限 2 GB，超限清理最久未用。

### 2.7 UI 技术选型

选定 **WebView2 + HTML/TS**（`internal/ui/win` 自写宿主，或 `github.com/jchv/go-webview2` 打底）：

| 方案 | 优点 | 缺点 | 结论 |
| --- | --- | --- | --- |
| WebView2 + HTML | 消息列表/富文本/图片渲染成本低；前端热更新 | 依赖运行时；需处理 DPI/焦点/拖拽 | **采用** |
| Win32 原生控件（walk） | 无额外运行时；原生观感 | 富文本/emoji/滚动性能要大量自绘 | 备选 |
| Fyne | 纯 Go、跨平台 | 非原生观感、大列表性能一般 | 不采用 |

通讯：`window.chrome.webview.postMessage` ↔ Go 端 `WebMessageReceived`；统一 JSON-RPC 2.0，方法名与下文 API 表一致；大列表用分页 + 虚拟滚动。

---

## 3. 关键设计决策（ADR 摘要）

| 编号 | 决策 | 理由 | 代价 |
| --- | --- | --- | --- |
| ADR-001 | **自研 MTProto**，不依赖 `gotd/td` | 无第三方 API 变更风险；协议可控可审计；与"分析本仓库协议层"的任务目标一致 | 需自测握手/加密，周期长 |
| ADR-002 | **禁止 cgo**，纯 Go | 单文件分发、交叉编译、无 OpenSSL 依赖 | AES-IGE/DH 需自实现（约 400 行 + 向量测试） |
| ADR-003 | **schema 驱动代码生成**，不手写 TL | 上游每 1–2 周更新 schema | 需维护生成器（首版支持子集：常用类型 + 全部 mtproto 层） |
| ADR-004 | **SQLite（modernc）**，不用 bbolt | 需要 FTS5 搜索、复杂查询、事务内推进更新状态 | 依赖体积略大、写入需串行化 |
| ADR-005 | **单进程 + actor 模型** | 首版复杂度最低；UI 与 core 通过 channel 解耦，后续可拆双进程 | 单进程崩溃即整体退出（靠 panic 边界兜底） |
| ADR-006 | **嵌套 go.mod** | 与 Gradle 完全隔离；`go test ./...` 不受 Android 文件干扰 | 上游若加根级 go.work 需协调 |
| ADR-007 | **JSON-RPC 而非 gRPC** | WebView2 原生支持 postMessage；调试友好（可抓明文） | 无强类型，靠生成 TS 类型约束 |

---

## 4. 安全与合规

1. **api_id/api_hash 不入库**：从 `%APPDATA%\tgw\config.json`（用户自填）或环境变量读取，`.gitignore` 排除。
2. **公钥 pin**：只接受指纹 `0xd09d1d85de64fd85` 的生产公钥（见 `API-ANALYSIS.md` §4）。
3. **日志脱敏**：手机号打码（`+86 138****0000`）、验证码/密码永不落日志、消息正文只在 debug 级且默认关闭。
4. **传输安全**：默认 `ProtocolTypeEE`；不做证书固定（MTProto 自身加密），但 MTProxy 输出须校验 secret。
5. **GPLv2 合规**：不复制上游 Java/C++ 代码；派生实现同样 GPLv2；分发时附 LICENSE。
6. **命名与品牌**：应用名 `TGW`（或用户自定），不得使用 Telegram 官方名称/图标（上游 README 明确要求）。

---

## 5. 测试与验收

| 层级 | 内容 | 门槛 |
| --- | --- | --- |
| 单元 | AES-IGE 向量、KDF、DH 大数、msg_id/seq_no 规则、去重窗口 | 覆盖 ≥ 80%（`mtproto` 包 ≥ 90%） |
| 编解码 | 所有生成的 TL 类型 round-trip（`tl` 包自动生成测试） | 全绿 |
| 回放 | `testdata/frames/` golden 帧解密→解包→再组包逐字节一致 | 全绿 |
| 集成 | `tgw-cli selftest`：真实 DC 完成 `req_pq_multi` + DH（不发短信） | 手动/CI 可选，3 分钟内 |
| 端到端 | 登录→拉历史→发消息→重启恢复 | 手工清单，每里程碑过一遍 |
| UI | 关键路径冒烟（启动、切会话、发消息、通知点击） | 手工 + 截图留档 |

`make test` 默认**不联网**；联网用例用 `-tags=online` 隔离。

---

## 6. 里程碑

| 里程碑 | 交付 | 验收标准 |
| --- | --- | --- |
| **M0 骨架**（3–5 天） | 嵌套 `go.mod`、目录骨架、CI 脚本（build/test/lint）、Win32 空窗口 + WebView2 加载 `web/index.html` | `make build` 产出 `tgw.exe`；窗口可开可关 |
| **M1 协议层**（2–3 周） | `mtproto`：crypto/handshake/session/transport/rpc；`tlgen` + `schema`；`tgw-cli selftest` | 真实握手成功生成 auth_key；golden 帧回放全绿 |
| **M2 可用聊天**（3–4 周） | 登录（含 2FA）、会话列表、历史分页、发文本、UpdatePump、SQLite、通知 | 双端（手机/PC）消息互见；杀进程重启历史不丢 |
| **M3 媒体与体验**（3–4 周） | 图片/文件收发、断点续传、头像、搜索、托盘与未读角标 | 100 MB 文件上传不阻塞消息；缓存可清理 |
| **M4 打磨**（2 周） | 多账号、代理 UI、更新检查、崩溃上报、安装包（zip/msi） | 冷启动 ≤1.5 s；内存 ≤150 MB |

---

## 7. 风险与未决问题

| 风险 | 影响 | 缓解 |
| --- | --- | --- |
| MTProto 2.0 加密细节实现偏差 | 连不上/被服务端断连 | golden 帧 + 与 `core.telegram.org` 向量逐字节比对；必要时与 Android 版抓包对照 |
| TL schema 版本漂移 | 新字段/构造器缺失 | `schema/` 记录来源 commit + 校验和；`make gen` 一键更新 |
| 无 `api_id` | 无法联调 | 用户自备；`selftest` 支持只跑 crypto/握手前的本地用例 |
| WebView2 运行时缺失（Win10） | 白屏 | 启动检测 + 引导安装链接 |
| 上游 GPL 合规 | 法律风险 | 独立实现、保留 LICENSE、不复制源码 |
| 双写不一致（DB vs UI） | 消息重复/丢失 | 单写者 UpdatePump + 事务内推进 state + 事件后置 |
| FLOOD_WAIT 处理不当 | 账号临时封禁 | 持久化退避、禁止热重试、熔断开关 |

未决：
1. 是否引入 `gotd/td` 仅作**测试对照**（不参与构建）？
2. 多账号是否共用一进程（单 DB 多 schema）还是多进程？
3. 更新通道：自建 HTTP 还是 GitHub Releases？

---

## 8. UI ↔ Core JSON-RPC 方法表（首版）

| 方法 | 参数 | 返回/事件 |
| --- | --- | --- |
| `auth.sendCode` | `{phone}` | `{type, nextType, timeout}` |
| `auth.signIn` | `{code, phoneHash}` | `{state:"ready"}` / `{needPassword:true}` |
| `auth.checkPassword` | `{password}` | `{state:"ready"}` |
| `auth.logout` | `{}` | `{ok:true}` |
| `dialogs.list` | `{limit}` | `Dialog[]` |
| `messages.history` | `{peerId, before, limit}` | `Message[]` |
| `messages.send` | `{peerId, text, replyTo}` | `{localId}` |
| `messages.read` | `{peerId, maxId}` | `{ok:true}` |
| `media.download` | `{fileId, saveAs}` | 事件 `media.progress` |
| `net.setProxy` | `{type, host, port, user, pass, secret}` | `{ok:true}` |
| 事件 | — | `message.new` / `message.sent` / `message.failed` / `dialog.update` / `conn.state` / `auth.state` |

---

## 9. 参考

- 协议事实与源码行号：[`API-ANALYSIS.md`](./API-ANALYSIS.md)
- 仓库协作指令：[`../../AGENTS.md`](../../AGENTS.md)
- 官方文档：<https://core.telegram.org/mtproto>、<https://core.telegram.org/api>、<https://core.telegram.org/api/obtaining_api_id>
