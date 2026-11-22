# Telegram API 与项目架构分析（Android 参考实现）

> 目的：为 `TelegramWindows/`（Go 语言 Windows 客户端）提供**可落地的协议事实**与**源码对照地图**。
> 本文所有"事实"均来自本仓库源码（commit `21f7d10`，`update to 13.2.2 (7303)`），行号可直接跳转核对；标注"规范"的条目以 <https://core.telegram.org/mtproto> 为准，落地时须用测试向量校验。
> 上游：<https://github.com/LCecho/Telegram>（fork 自 DrKLO/Telegram）。

---

## 1. 仓库基本情况

| 项 | 值 |
| --- | --- |
| 分支 / HEAD | `master` / `21f7d10`（13.2.2，versionCode 7303） |
| 工作树规模 | 454 MB，14,181 个文件（`.git` 浅克隆 102 MB） |
| Java | 2,292 个 `.java`；`org.telegram.messenger` 175 个；`org.telegram.ui` 991 个 |
| C/C++ | 2,929 个源文件；`jni/tgnet` 42 个文件 |
| 构建 | Gradle 8.13、compileSdk 36、minSdk 23、NDK 27.2.12479018、Java 8 语法级别 |
| 子模块 | `jni/td`(TDLib)、`jni/tgcalls`、`ffmpeg`、`libvpx`、`dav1d`、`libyuv`、`openh264`、`boringssl`、`absl`、`wamr`、`tlottie`、`twidget`、`jlatexmath`、`TMessagesProj_Modules/media`(media3) |

### 1.1 五层结构（自上而下）

```
┌──────────────────────────────────────────────────────────────┐
│ org.telegram.ui            ~991 文件  Activity/Fragment/Cell  │  表现层
├──────────────────────────────────────────────────────────────┤
│ org.telegram.messenger     175 文件   控制器/缓存/媒体/通知    │  领域层
│   MessagesController 25,702 行  MessagesStorage 19,249 行     │
│   SendMessagesHelper 12,592 行  MediaDataController 10,350 行 │
├──────────────────────────────────────────────────────────────┤
│ org.telegram.tgnet         TLRPC.java 3,067,236 B（生成）      │  RPC / TL 层
│   ConnectionsManager.java 77,661 B；tl/ 18 个命名空间文件      │
├──────────────────────────────────────────────────────────────┤
│ jni/TgNetWrapper.cpp       747 行（RegisterNatives + 回调）    │  JNI 桥
├──────────────────────────────────────────────────────────────┤
│ jni/tgnet                  42 文件（MTProto 原生实现）         │  协议层
│   ConnectionsManager.cpp 192,230 B  Datacenter.cpp 59,730 B    │
│   Handshake.cpp 50,388 B  MTProtoScheme.cpp 45,369 B          │
│   ConnectionSocket.cpp 36,790 B  ConnectionSession.cpp         │
└──────────────────────────────────────────────────────────────┘
```

关键结论：**协议与网络全部在 C++（tgnet），Java 只负责 TL 对象与业务。** Go 客户端要替换的正是 `jni/tgnet + TgNetWrapper + Java 连接层`这三段，业务层只能"参照语义重写"，不能复用代码。

---

## 2. TL 语言与序列化

- TL 由 `constructor id`（4 字节小端 uint32）+ 字段序列化构成；`mtproto.tl`（协议层）与 `api.tl`（应用层）两套 schema。
- 本仓库**不携带 `.tl` 源文件**：Java 侧是预生成的 `TLRPC.java`（3 MB）+ `org/telegram/tgnet/tl/TL_*.java`（18 个命名空间，如 `TL_account`、`TL_messages`）；原生侧是生成好的 `ApiScheme.{h,cpp}`（28,705 B / 73,109 B）与 `MTProtoScheme.{h,cpp}`。
- Go 侧应引入 `schema/*.tl` 源 + 生成器（`cmd/tlgen`），而不是手写结构体：schema 每 1–2 周随 Telegram 更新，手写必然腐化。

### 2.1 已核对的协议层构造器 ID（`jni/tgnet/MTProtoScheme.h`）

| 构造器 | ID | 位置 |
| --- | --- | --- |
| `msg_container` | `0x73f1f8dc` | :488 |
| `message`（TL_message） | `0x5bb8e511` | :298 |
| `rpc_result` | `0xf35c6d01` | :429 |
| `rpc_error` | `0x2144ca19` | :531 |
| `gzip_packed` | `0x3072cfa1` | :684 |
| `msgs_ack` | `0x62d6b459` | :476 |
| `bad_server_salt` | `0xedab447b` | :358 |
| `resPQ` | `0x05162463` | :99 |
| `future_salt` | `0x0949d9dc` | :46 |
| `msg_resend_req` | `0x7d861a08` | :500 |

> Go 实现必须在解包入口按这 10 个 ID 分流，其余 ID 一律交给 `tl` 注册表；**未知 ID 不得 panic**，应记 `unparsed` 并让上层发 `msgs_ack`。

---

## 3. 传输层（TCP 帧）

`jni/tgnet/Connection.h:64-69` 定义四种帧格式，`Connection.cpp:464-471` 选择：

| 枚举 | 线上首字节/头 | 说明 |
| --- | --- | --- |
| `ProtocolTypeEF` | `0xef` + 1 字节长度 | abridged |
| `ProtocolTypeEE` | `0xeeeeeeee` + 4 字节长度 | intermediate（默认） |
| `ProtocolTypeDD` | `0xdddddddd` + 4 字节长度 + 4 字节对齐填充 | padded intermediate |
| `ProtocolTypeTLS` | TLS ClientHello 外形 | MTProxy fake-TLS 混淆 |

实现要点（`Connection.cpp:216,476-487,527`）：
- 单包上限校验 `currentPacketLength > 2*1024*1024` 直接断连（Go 侧同样要上限，防 OOM）。
- EE/EF 要求 `packetLength % 4 == 0`，DD/TLS 不要求。
- 首包要做协议嗅探：`val` 命中 `0x44414548("HEAD")`、`0x54534f50("POST")`、`0x20544547("GET ")`、`0x4954504f("OPTI")` 等视为 HTTP 探测，直接丢弃/断开（`Connection.cpp:527`）。Go 侧建议照做，避免被中间盒识别。

---

## 4. 握手（auth_key 生成）

`Handshake.cpp` 状态机四步，全部已核对：

| 步骤 | 代码位置 | 报文 |
| --- | --- | --- |
| 1 | `:59` | `req_pq_multi(nonce)` |
| 2 | `:437` + `:454-493` | `req_DH_params`，内部数据 `p_q_inner_data_dc` / `p_q_inner_data_temp_dc` |
| 3 | `:785` | `set_client_DH_params` |
| 4 | `:841` | `dh_gen_ok` / `dh_gen_retry` / `dh_gen_fail` |

关键细节：

1. **服务器 RSA 公钥是硬编码的**（`Handshake.cpp:365-390`），生产指纹 `0xd09d1d85de64fd85`，测试后端另有 `0xb25898df208d2603`。Go 侧必须 pin 公钥并校验 `server_public_key_fingerprints` 命中，**不允许"信任任意指纹"**。
2. **临时密钥（PFS）**：`p_q_inner_data_temp_dc` 的 `dc` 字段为负数（`-1..-5`，`:479-487`），`expires_in = TEMP_AUTH_KEY_EXPIRE_TIME`（`Defines.h:26` = 24 小时）。之后必须 `auth.bindTempAuthKey` 绑定主密钥，绑定失败要重新握手（`ConnectionsManager.cpp:1445,1649`）。
3. **DC 地址表**（`ConnectionsManager.cpp:1841-1891`）为内置预设 + `help.getConfig` 动态更新：生产 DC1–5 均为 `*.154.*:443`，IPv6 为 `2001:67c:4e8:f002::a` 一类；测试 DC 为 `149.154.175.40 / 167.40 / 167.51` 等。Go 侧应把"预设地址"与"配置下发地址"分层，配置层可覆盖。
4. 握手阶段使用 `mtProtoVersion = 1`（`Datacenter.cpp` `createRequestsData`，`pfsInit == true` 分支），正式消息用 `2`。

---

## 5. 加密消息封装（MTProto 2.0）

`Datacenter::createRequestsData`（`Datacenter.cpp:1113`）展示了完整组包顺序：

1. 取 auth_key 与 `auth_key_id`；
2. 单条消息若"时间戳偏移 > 30s 或强制容器"，包一层 `msg_container`；多条必然容器化；
3. 计算 `messageSize`，`additionalSize = (32 + messageSize) % 16`，补零到 **16 字节整数倍**（MTProto 2.0 规定）；
4. 写 `salt`、`session_id`、`msg_id`、`seq_no`、`message length`，再写 `message body`，再填充 `1..15` 字节随机/零填充；
5. 用 `msg_key = SHA256(auth_key[88:120] + plaintext)[8:24]`、再派生 `key/iv`（规范：`sha256_a = SHA256(msg_key + auth_key[x:36])`、`sha256_b = SHA256(auth_key[40+x:36] + msg_key)`，`x=0` 客户端发、`x=8` 服务端发）做 AES-256-IGE 加密；
6. 前置 `auth_key_id(8) + msg_key(16)` 写入传输层。

Go 侧注意事项：
- 标准库无 AES-IGE，需自实现（IGE 不是 CBC，块间异或取前一块密文，首块异或 IV）。
- **MTProto 2.0 已废除 msg_key 校验的宽松模式**，msg_key 不匹配即视为攻击，直接断连（1.0 兼容模式可选，默认关）。
- `gzip_packed` 只在 >512 B 且压缩后更小时使用；解包必须限流（防 zip bomb）。

---

## 6. 会话与序号

- `msg_id`：`ConnectionsManager.cpp:603` — `(unixtime_ms + timeDifference*1000) * 2^32 / 1000`，**严格单调递增**且 **4 字节对齐**；客户端 msg_id 必须为偶数（服务端奇偶相反）。
- `seq_no`：`ConnectionSession.cpp:47` — `value = nextSeqNo; nextSeqNo += increment ? 1 : 0; return value * 2 + (increment ? 1 : 0)`。即"内容消息 `2n`、需确认消息 `2n+1`"。
- 去重窗口：`ConnectionSession.cpp:62-80`，拒绝偶数 msg_id、维护最近 300 条已处理 msg_id（超过则裁掉 100 条）。Go 侧用环形缓冲 + map 实现同样语义。
- `salt`：通过 `future_salts` 预取，收到 `bad_server_salt` 时用 `TL_bad_server_salt.new_server_salt` 重发；`new_session_created` 要落盘（含 `first_msg_id`、`unique_id`）。
- 错误恢复：`bad_msg_notification` 按 `error_code` 分派（16/17/18/19/20/32/34/35/48），Go 侧须逐条实现，不能一把梭重连。

---

## 7. 更新（updates）系统

- 落点：`MessagesController.processUpdates`（`MessagesController.java:17917`）；补齐缺口走 `updates.getDifference`（`:16921`）。
- 状态四元组 `pts / qts / seq / date` 必须与消息**同事务**落库，否则重启后必然重复或丢消息。
- 处理顺序：`updatesTooLong` → 强制 `getDifference`；`updateShort*` 与 `updates` 容器统一进队列串行处理；`seq` 出现空洞时暂停应用并拉 difference。
- 结论：Go 侧必须有一个**单写者更新泵（single-writer update pump）**，DB 写入与状态推进在同一事务内，禁止并发写。

---

## 8. 登录与账号

`LoginActivity.java` 的调用点：`sendCode`(:3193) → `signIn`(:5104, :7114, :9721) → `checkPassword`(:5878，两步验证 SRP) → `signUp`(:8541)。

Go 侧登录状态机：`phone → code → (password?) → ready`，每步失败类型要区分：
- `PHONE_NUMBER_INVALID` / `PHONE_CODE_INVALID` / `PHONE_CODE_EXPIRED` / `SESSION_PASSWORD_NEEDED`；
- `PHONE_MIGRATE_X` / `NETWORK_MIGRATE_X`：迁移 DC 后**重放同一请求**；
- `FLOOD_WAIT_X`：按秒退避，且必须落盘（重启后继续等待，避免连环封禁）。

---

## 9. 文件与媒体

- 上传：`upload.saveFilePart`（≤512 KB/片）/ `upload.saveBigFilePart`（>10 MB 文件）；并发片数受服务端限制，需令牌桶。
- 下载：`upload.getFile`（offset/limit，1 MB 对齐更省流）；`FILE_MIGRATE_X` 切 DC；大文件走 CDN（`upload.getCdnFile` + `CDN` 公钥）。
- 本仓库参考：`FileLoadOperation` / `FileLoader`（Java）与 `ConnectionsManager` 的 `ConnectionTypeDownload`（`Defines.h`）—— 即**下载与 RPC 分连接**，Go 侧同样要分连接池，否则大文件会阻塞消息收发。

---

## 10. 代理

- `ConnectionsManager.setProxySettings`（Java 侧）→ `ConnectionSocket::setOverrideProxy`（`ConnectionSocket.h:37`），支持 `socks5://`、`http://`、MTProxy（`secret` 形如 `dd<hex>` / `ee<hex>`）。
- Go 标准库只有 `http.ProxyFromEnvironment` 与 `net`；SOCKS5 需自实现或引 `golang.org/x/net/proxy`；MTProxy fake-TLS 需自写（与 §3 的 `ProtocolTypeTLS` 对应）。

---

## 11. 错误与限流（Go 侧必需）

| 类别 | 例子 | 处理 |
| --- | --- | --- |
| 业务错误 | `PEER_ID_INVALID`、`MESSAGE_TOO_LONG` | 原样上抛给 UI，不重试 |
| 迁移 | `PHONE_MIGRATE_X`、`FILE_MIGRATE_X`、`NETWORK_MIGRATE_X` | 切 DC 后重放一次 |
| 限流 | `FLOOD_WAIT_X`、`-503` | 令牌桶 + 持久化退避，禁止热重试 |
| 会话失效 | `AUTH_KEY_UNREGISTERED`、`SESSION_REVOKED` | 清理本地 key，回登录页 |
| 协议异常 | `msg_key` 不匹配、seq 空洞 | 断连重连 / 拉 difference |

---

## 12. Android 源码对照地图（Go 实现时按此跳转）

| 关注点 | 参考文件:行 |
| --- | --- |
| 握手主流程 | `TMessagesProj/jni/tgnet/Handshake.cpp:59,437,785,841` |
| 服务器公钥与指纹 | `TMessagesProj/jni/tgnet/Handshake.cpp:365-390` |
| 组包/填充/容器 | `TMessagesProj/jni/tgnet/Datacenter.cpp:1113` |
| MTProto 构造器 | `TMessagesProj/jni/tgnet/MTProtoScheme.h` |
| 帧格式选择与嗅探 | `TMessagesProj/jni/tgnet/Connection.cpp:216,464-471,527` |
| msg_id / seq_no | `ConnectionsManager.cpp:603`、`ConnectionSession.cpp:47` |
| 去重窗口 | `ConnectionSession.cpp:62-80` |
| quick ack | `ConnectionsManager.cpp:818-841` |
| PFS 绑定与重试 | `ConnectionsManager.cpp:1445,1649`；`Defines.h:26` |
| DC 地址表 | `ConnectionsManager.cpp:1841-1891` |
| JNI 桥与回调 | `TMessagesProj/jni/TgNetWrapper.cpp`（747 行） |
| Java 连接层 API | `org/telegram/tgnet/ConnectionsManager.java`（sendRequest/cancelRequest/getConnectionState/setProxySettings…） |
| 更新处理 | `MessagesController.java:17917`；`getDifference` `:16921` |
| 登录 | `LoginActivity.java:3193,5104,5878,8541` |

---

## 13. Go 实现与 Android 实现的差异清单

| 维度 | Android | Go（本设计） |
| --- | --- | --- |
| 协议实现 | C++ + BoringSSL/OpenSSL | 纯 Go（`crypto/*` + 自写 AES-IGE / DH / RSA-PKCS1） |
| 加密库 | BoringSSL，JNI 调用 | 标准库；**禁止 cgo**，保证单文件 exe |
| 并发模型 | Java 线程池 + C++ 事件循环 | 每 DC 单写者 goroutine + RPC 请求表 |
| TL 类型 | 预生成 `TLRPC.java` | `cmd/tlgen` 生成 `internal/tl/*_gen.go` |
| 存储 | SQLite + SharedPreferences | SQLite（`modernc.org/sqlite`）+ DPAPI 加密密钥文件 |
| UI | Android View | WebView2（HTML/TS）+ Win32 宿主窗口 |
| 体积 | APK + 多 ABI so | 单 exe ≈ 20–30 MB（不含 WebView2 运行时） |

> 许可：仓库为 **GPLv2**。`TelegramWindows/` 作为同仓库子项目，同样以 GPLv2 发布；不得复制上游 Java/C++ 代码，只能按协议文档与上述语义独立实现。上游 README 要求：自备 `api_id/api_hash`、不得使用 "Telegram" 作为应用名、不得使用官方 logo。
