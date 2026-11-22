package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("TGW_HOME", t.TempDir())
	t.Setenv("TGW_API_ID", "12345")
	t.Setenv("TGW_API_HASH", "0123456789abcdef0123456789abcdef")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.HasCredentials() {
		t.Fatalf("环境变量未被读取: %+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestLoadConfigMissing(t *testing.T) {
	t.Setenv("TGW_HOME", t.TempDir())
	t.Setenv("TGW_API_ID", "")
	t.Setenv("TGW_API_HASH", "")

	if _, err := LoadConfig(); err != ErrConfigMissing {
		t.Fatalf("期望 ErrConfigMissing，实际 %v", err)
	}
}

func TestLoadConfigFromFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TGW_HOME", home)
	t.Setenv("TGW_API_ID", "")
	t.Setenv("TGW_API_HASH", "")

	body := `{"api_id":777,"api_hash":"0123456789abcdef0123456789abcdef","proxy":{"type":"socks5","host":"127.0.0.1","port":1080}}`
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.APIID != 777 || cfg.Proxy == nil || cfg.Proxy.Port != 1080 {
		t.Fatalf("配置解析不符: %+v", cfg)
	}
}

func TestLoadConfigBadJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TGW_HOME", home)
	t.Setenv("TGW_API_ID", "")
	t.Setenv("TGW_API_HASH", "")

	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte("{oops"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadConfig(); err == nil {
		t.Fatal("坏 JSON 应报错")
	}
}

func TestValidateRejectsBadHash(t *testing.T) {
	cfg := Config{APIID: 1, APIHash: "tooshort"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("短 api_hash 应校验失败")
	}
	cfg = Config{APIID: 0, APIHash: "0123456789abcdef0123456789abcdef"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("零 api_id 应校验失败")
	}
}

func TestDataDirHonoursTGWHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "tgw-home")
	t.Setenv("TGW_HOME", home)

	got, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	if got != home {
		t.Fatalf("未使用 TGW_HOME: got=%s want=%s", got, home)
	}
	if info, err := os.Stat(got); err != nil || !info.IsDir() {
		t.Fatalf("数据目录未创建: %v", err)
	}
}

// 没有 TGW_HOME 时应回退到便携目录或 LOCALAPPDATA，且目录可写。
func TestDataDirFallbackWritable(t *testing.T) {
	t.Setenv("TGW_HOME", "")
	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	probe := filepath.Join(dir, "probe.tmp")
	if err := os.WriteFile(probe, []byte("x"), 0o600); err != nil {
		t.Fatalf("数据目录不可写: %v", err)
	}
	_ = os.Remove(probe)
}
