# TelegramWindows（Go 版 Windows 客户端）

Go 实现的 Telegram 桌面客户端，作为本仓库的独立子项目（嵌套 `go.mod`，不参与 Gradle 构建）。
协议细节见 [`docs/API-ANALYSIS.md`](docs/API-ANALYSIS.md)，架构见 [`docs/DESIGN.md`](docs/DESIGN.md)。

## 当前进度

| 里程碑 | 状态 |
| --- | --- |
| M0 骨架（模块、目录、原生窗口、自检 CLI） | ✅ |
| M1 协议层（AES-IGE、握手、会话、传输、RPC） | 🚧 进行中（已完成 AES-IGE、msg_id） |
| M2 可用聊天（登录、会话、收发、SQLite、通知） | ⬜ |
| M3 媒体与体验 | ⬜ |

## 构建

需要 Go ≥ 1.24，且**必须** `CGO_ENABLED=0`（保证单文件分发）。

```bash
go build ./...
go build -o bin/tgw.exe ./cmd/tgw        # GUI
go build -o bin/tgw-cli.exe ./cmd/tgw-cli # 无界面工具
```

或使用 Makefile（MSYS2 的 make 可用）：

```bash
make build      # bin/tgw.exe
make test       # go test ./...
make race       # go test -race ./...
make lint       # gofmt 检查 + go vet
```

## 运行

```bash
./bin/tgw.exe               # 打开窗口（Esc 关闭）
./bin/tgw.exe -smoke 800ms  # 自检模式：窗口自动关闭，便于 CI
./bin/tgw-cli selftest      # 本地自检：AES-IGE / msg_id / RPC 网关 / 数据目录
./bin/tgw-cli rpc           # 从 stdin 逐行处理 JSON-RPC（调前端用）
```

## 配置

数据目录按以下优先级确定（`TGW_HOME` → 便携 `data/` → `%LOCALAPPDATA%\TGW`），
便携模式下把 `tgw.exe` 拷到任意位置即可，配置与缓存跟随可执行文件。

`<数据目录>\config.json`：

```json
{
  "api_id": 12345,
  "api_hash": "0123456789abcdef0123456789abcdef",
  "proxy": { "type": "socks5", "host": "127.0.0.1", "port": 1080 }
}
```

也可用环境变量 `TGW_API_ID` / `TGW_API_HASH` 覆盖，或用 `TGW_HOME` 指定数据目录。
**凭据不入库、不打印**；`api_id` 请自行在
<https://core.telegram.org/api/obtaining_api_id> 申请，不要复用 Android 版的。

## 目录

```
cmd/tgw          GUI 入口（原生窗口宿主）
cmd/tgw-cli      自检 / RPC 调试
internal/app     JSON-RPC 网关 + 事件总线（用例编排）
internal/mtproto 协议层（crypto / handshake / session / transport / rpc）
internal/tl      TL 生成代码（`make gen` 产出，禁手改）
internal/tg      高层客户端（登录、会话、消息、文件）
internal/store   SQLite 仓储
internal/ui/win  Win32 窗口（M2 起内嵌 WebView2）
internal/platform 路径 / 配置 / 时钟
internal/sec     DPAPI、密钥内存清理、日志脱敏
web/             前端资源
```

## 许可

GPLv2，与仓库一致。未复制 `TMessagesProj` 下的任何 Java/C++ 代码。
