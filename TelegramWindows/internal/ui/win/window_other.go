//go:build !windows

package win

import (
	"errors"
	"time"
)

// ErrUnsupported 表示当前平台没有原生窗口宿主。
var ErrUnsupported = errors.New("win: 仅支持 Windows 平台")

// Config 是窗口参数（非 Windows 平台仅用于保持编译通过）。
type Config struct {
	Title  string
	Lines  []string
	Width  int32
	Height int32
	Smoke  time.Duration
}

// Run 在非 Windows 平台直接返回 ErrUnsupported。
func Run(Config) error { return ErrUnsupported }
