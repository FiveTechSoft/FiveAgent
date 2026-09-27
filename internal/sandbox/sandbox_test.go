package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
)

func TestUserDirSanitizesKey(t *testing.T) {
	root := t.TempDir()
	dir, err := userDir(root, "whatsapp/34600123456")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "whatsapp_34600123456" {
		t.Fatalf("bad dir: %s", dir)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("dir not created: %v", err)
	}
	if _, err := userDir(root, ""); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestNewDefaults(t *testing.T) {
	sb, err := New(config.Sandbox{Backend: "docker"})
	if err != nil {
		t.Fatal(err)
	}
	if sb.Name() != "docker" {
		t.Fatalf("unexpected backend: %s", sb.Name())
	}
	if _, err := New(config.Sandbox{Backend: "nope"}); err == nil {
		t.Fatal("expected error for unknown backend")
	}
}
