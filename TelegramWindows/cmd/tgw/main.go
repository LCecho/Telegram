// Command tgw 是 TelegramWindows 的 GUI 入口。
//
// M0 骨架：初始化平台层与应用层，打开原生窗口展示运行状态。
// M2 起窗口内嵌 WebView2，加载 web/ 前端，通过 JSON-RPC 调用 app.Gateway。
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/LCecho/Telegram/TelegramWindows/internal/app"
	"github.com/LCecho/Telegram/TelegramWindows/internal/platform"
	"github.com/LCecho/Telegram/TelegramWindows/internal/ui/win"
)

// 由 -ldflags -X 注入。
var (
	version   = "0.0.0-m0"
	buildTime = "unknown"
)

func main() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "tgw: 致命错误: %v\n", r)
			os.Exit(2)
		}
	}()

	smoke := flag.Duration("smoke", 0, "自检模式：窗口显示指定时长后自动退出（如 800ms）")
	printVersion := flag.Bool("version", false, "打印版本后退出")
	flag.Parse()

	if *printVersion {
		fmt.Printf("tgw %s (%s)\n", version, buildTime)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	gw := app.NewGateway(version, buildTime)
	bus := app.NewBus()
	defer func() { _ = bus }()

	cfg := win.Config{
		Title:  "TGW " + version + " · TelegramWindows",
		Lines:  statusLines(gw),
		Width:  960,
		Height: 640,
		Smoke:  *smoke,
	}
	if err := win.Run(cfg); err != nil {
		logger.Error("窗口运行失败", "err", err)
		os.Exit(1)
	}
}

// statusLines 汇总启动状态，直接渲染在 M0 的原生窗口里。
func statusLines(gw *app.Gateway) []string {
	lines := []string{
		"TelegramWindows · M0 骨架（原生窗口，无 cgo）",
		"版本：" + version + "    构建：" + buildTime,
	}

	if dir, err := platform.DataDir(); err != nil {
		lines = append(lines, "数据目录：不可用（"+err.Error()+"）")
	} else {
		lines = append(lines, "数据目录："+dir)
	}

	cfg, err := platform.LoadConfig()
	switch {
	case err == nil && cfg.HasCredentials():
		lines = append(lines, "凭据：已配置 api_id / api_hash（内容不显示）")
	case err != nil:
		lines = append(lines, "凭据：未配置（设置 TGW_API_ID / TGW_API_HASH 或写入 config.json）")
	default:
		lines = append(lines, "凭据：不完整")
	}

	lines = append(lines, "RPC 方法："+joinMethods(gw.Methods()))
	lines = append(lines, "下一步：M1 实现 mtproto 握手（tgw-cli selftest），M2 接入 WebView2 前端")
	lines = append(lines, "按 Esc 关闭窗口")
	return lines
}

func joinMethods(methods []string) string {
	out := ""
	for i, m := range methods {
		if i > 0 {
			out += " · "
		}
		out += m
	}
	if out == "" {
		return "（无）"
	}
	return out
}
