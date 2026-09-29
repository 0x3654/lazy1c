package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteDefault: шаблон создаётся при отсутствии и не трогается при наличии.
func TestWriteDefault(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lazy1c.toml")
	if err := WriteDefault(p); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil || len(data) == 0 {
		t.Fatalf("шаблон не записан: %v", err)
	}
	// перезапись существующего запрещена
	if err := os.WriteFile(p, []byte("mine = true"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = WriteDefault(p)
	data, _ = os.ReadFile(p)
	if string(data) != "mine = true" {
		t.Fatal("WriteDefault перезаписал существующий конфиг")
	}
}
