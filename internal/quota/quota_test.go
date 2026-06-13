package quota

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

const sample = `
version: 0
defaults: {}
users:
  alice@pve:
    pool: uq-alice
    cores: 16
    memory-mib: 32768
    instances: 8
    disk-gib:
      tank: 200
    nodes:
      n1:
        cores: 8
  bob@pve:
    pool: uq-bob
    cores: 4
    memory-mib: 8192
    instances: 2
    disk-gib:
      tank: 50
`

func TestParseValid(t *testing.T) {
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	a, ok := c.Users["alice@pve"]
	if !ok || a.Pool != "uq-alice" || a.Cores != 16 ||
		a.MemoryMiB != 32768 || a.Instances != 8 || a.DiskGiB["tank"] != 200 {
		t.Fatalf("alice=%+v", a)
	}
	if a.Nodes["n1"].Cores == nil || *a.Nodes["n1"].Cores != 8 {
		t.Error("node override cores not parsed")
	}
}

func TestValidateDuplicatePool(t *testing.T) {
	y := "version: 0\nusers:\n  a@pve:\n    pool: uq-x\n  b@pve:\n    pool: uq-x\n"
	if _, err := Parse([]byte(y)); err == nil {
		t.Error("expected duplicate-pool error")
	}
}

func TestValidateBadVersion(t *testing.T) {
	if _, err := Parse([]byte("version: 9\n")); err == nil {
		t.Error("expected version error")
	}
}

func TestValidateBadPoolID(t *testing.T) {
	y := "version: 0\nusers:\n  a@pve:\n    pool: \"bad pool!\"\n"
	if _, err := Parse([]byte(y)); err == nil {
		t.Error("expected pool-id error")
	}
}

func TestStoreDefaultDeny(t *testing.T) {
	p := filepath.Join(t.TempDir(), "quotas.yaml")
	if err := os.WriteFile(p, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(p, discard())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("alice@pve"); !ok {
		t.Error("alice should be present")
	}
	if _, ok := s.Get("nobody@pve"); ok {
		t.Error("unknown user must be default-deny")
	}
	if len(s.Users()) != 2 {
		t.Errorf("users=%d want 2", len(s.Users()))
	}
}
