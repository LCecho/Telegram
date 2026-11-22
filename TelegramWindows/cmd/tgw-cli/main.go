// Command tgw-cli 是无界面入口：协议自检、RPC 调试、版本查询。
//
// 设计目标：让协议层能在 CI 里被验证，不依赖任何窗口或网络环境。
//
//	tgw-cli version           打印版本
//	tgw-cli selftest          跑本地自检（加密、msg_id、RPC 网关）
//	tgw-cli rpc               从 stdin 逐行读 JSON-RPC 请求并回写响应
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/LCecho/Telegram/TelegramWindows/internal/app"
	"github.com/LCecho/Telegram/TelegramWindows/internal/mtproto/crypto"
	"github.com/LCecho/Telegram/TelegramWindows/internal/mtproto/session"
	"github.com/LCecho/Telegram/TelegramWindows/internal/platform"
)

var (
	version   = "0.0.0-m0"
	buildTime = "unknown"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Printf("tgw-cli %s (%s)\n", version, buildTime)
	case "selftest":
		if code := selftest(); code != 0 {
			os.Exit(code)
		}
	case "rpc":
		if code := serveRPC(); code != 0 {
			os.Exit(code)
		}
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知子命令：%s\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `tgw-cli <命令>
  version    打印版本
  selftest   本地自检：AES-IGE / msg_id / JSON-RPC 网关
  rpc        从 stdin 逐行处理 JSON-RPC 请求（UI 联调用）
`)
}

func selftest() int {
	failures := 0
	check := func(name string, fn func() error) {
		if err := fn(); err != nil {
			failures++
			fmt.Printf("FAIL %-22s %v\n", name, err)
			return
		}
		fmt.Printf("PASS %-22s\n", name)
	}

	check("crypto/aes-ige", func() error {
		key := make([]byte, 32)
		iv := make([]byte, 32)
		plain := make([]byte, 64)
		if _, err := rand.Read(plain); err != nil {
			return err
		}
		enc, err := crypto.IGEEncrypt(key, iv, plain)
		if err != nil {
			return err
		}
		dec, err := crypto.IGEDecrypt(key, iv, enc)
		if err != nil {
			return err
		}
		for i := range plain {
			if plain[i] != dec[i] {
				return fmt.Errorf("第 %d 字节不一致", i)
			}
		}
		return nil
	})

	check("session/msg-id", func() error {
		now := time.Now().UnixMilli()
		var last int64
		for i := 0; i < 100; i++ {
			id := session.NextMessageID(now+int64(i), 0, last)
			if id%4 != 0 {
				return fmt.Errorf("msg_id 未 4 字节对齐: %d", id)
			}
			if id <= last {
				return fmt.Errorf("msg_id 未递增: %d <= %d", id, last)
			}
			last = id
		}
		return nil
	})

	check("app/gateway", func() error {
		gw := app.NewGateway(version, buildTime)
		raw := gw.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"app.health"}`))
		var resp app.Response
		if err := json.Unmarshal(raw, &resp); err != nil {
			return err
		}
		if resp.Error != nil {
			return resp.Error
		}
		return nil
	})

	check("platform/datadir", func() error {
		_, err := platform.DataDir()
		return err
	})

	if failures > 0 {
		fmt.Printf("\n自检失败：%d 项\n", failures)
		return 1
	}
	fmt.Println("\n自检通过：4/4")
	return 0
}

// serveRPC 从 stdin 逐行读 JSON-RPC 请求，输出响应。
// 用于在没有 WebView2 的情况下联调前端逻辑。
func serveRPC() int {
	gw := app.NewGateway(version, buildTime)
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		if resp := gw.Handle(context.Background(), line); resp != nil {
			if _, err := out.Write(resp); err != nil {
				fmt.Fprintf(os.Stderr, "写响应失败: %v\n", err)
				return 1
			}
			if err := out.WriteByte('\n'); err != nil {
				return 1
			}
			if err := out.Flush(); err != nil {
				return 1
			}
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "读 stdin 失败: %v\n", err)
		return 1
	}
	return 0
}
