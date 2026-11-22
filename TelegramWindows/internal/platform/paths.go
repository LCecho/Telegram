// Package platform 提供与操作系统相关的路径、配置与时钟。
//
// 数据目录优先级（从高到低）：
//  1. 环境变量 TGW_HOME；
//  2. 便携模式：可执行文件同级的 data/ 目录（可写时启用，便于整包拷走即用）；
//  3. %LOCALAPPDATA%\TGW。
//
// 安全约束：api_id/api_hash 属于用户凭据，只从配置文件或环境变量读取，
// 绝不写入仓库、日志或数据库。
package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AppDirName 是回退目录名（%LOCALAPPDATA% 下）。
const AppDirName = "TGW"

// PortableDirName 是便携模式的数据目录名（与 exe 同级）。
const PortableDirName = "data"

var (
	// ErrNoAppData 表示无法确定用户数据目录。
	ErrNoAppData = errors.New("platform: 无法确定数据目录（未设置 TGW_HOME 且 LOCALAPPDATA 为空）")
	// ErrConfigMissing 表示配置文件不存在。
	ErrConfigMissing = errors.New("platform: 配置文件不存在")
)

// Proxy 是代理配置。
type Proxy struct {
	Type   string `json:"type"` // socks5 | http | mtproxy
	Host   string `json:"host"`
	Port   int    `json:"port"`
	User   string `json:"user,omitempty"`
	Pass   string `json:"pass,omitempty"`
	Secret string `json:"secret,omitempty"` // 仅 mtproxy
}

// Config 是用户级配置（不含会话密钥）。
type Config struct {
	APIID   int    `json:"api_id"`
	APIHash string `json:"api_hash"`
	Proxy   *Proxy `json:"proxy,omitempty"`
}

// DataDir 返回并确保数据目录存在。
func DataDir() (string, error) {
	if home := strings.TrimSpace(os.Getenv("TGW_HOME")); home != "" {
		if err := os.MkdirAll(home, 0o700); err != nil {
			return "", fmt.Errorf("platform: 创建 TGW_HOME 失败: %w", err)
		}
		return home, nil
	}

	if dir, ok := portableDir(); ok {
		return dir, nil
	}

	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return "", ErrNoAppData
	}
	dir := filepath.Join(base, AppDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("platform: 创建数据目录失败: %w", err)
	}
	return dir, nil
}

// portableDir 尝试使用 exe 同级的 data/ 目录，并做可写性探测。
func portableDir() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	dir := filepath.Join(filepath.Dir(exe), PortableDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false
	}
	probe := filepath.Join(dir, ".write-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", false
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return dir, true
}

// ConfigPath 返回配置文件路径。
func ConfigPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// LoadConfig 读取配置。
//
// 优先级：环境变量 TGW_API_ID / TGW_API_HASH > 配置文件。
// 两者都缺失时返回 ErrConfigMissing，便于上层给出引导而非硬失败。
func LoadConfig() (Config, error) {
	var cfg Config

	if p, err := ConfigPath(); err == nil {
		data, readErr := os.ReadFile(p)
		switch {
		case readErr == nil:
			if err := json.Unmarshal(data, &cfg); err != nil {
				return Config{}, fmt.Errorf("platform: 配置文件格式错误: %w", err)
			}
		case errors.Is(readErr, os.ErrNotExist):
			// 保留零值，最后统一判断
		default:
			return Config{}, fmt.Errorf("platform: 读取配置失败: %w", readErr)
		}
	}

	if v := strings.TrimSpace(os.Getenv("TGW_API_ID")); v != "" {
		var id int
		if _, err := fmt.Sscanf(v, "%d", &id); err == nil {
			cfg.APIID = id
		}
	}
	if v := strings.TrimSpace(os.Getenv("TGW_API_HASH")); v != "" {
		cfg.APIHash = v
	}

	if cfg.APIID == 0 && cfg.APIHash == "" {
		return cfg, ErrConfigMissing
	}
	return cfg, nil
}

// Validate 校验凭据基本形态（不联网）。
func (c Config) Validate() error {
	if c.APIID <= 0 {
		return errors.New("platform: api_id 必须为正整数")
	}
	if len(c.APIHash) != 32 {
		return errors.New("platform: api_hash 应为 32 位十六进制字符串")
	}
	return nil
}

// HasCredentials 用于启动时给出提示，不打印任何凭据内容。
func (c Config) HasCredentials() bool {
	return c.APIID > 0 && c.APIHash != ""
}
