package admission

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/pve"
	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/quota"
	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/usage"
)

// Keep the settle wait short so tests with a fake API (where the guest never
// "appears") don't block for the production timeout.
func init() { settleTimeout = 150 * time.Millisecond }

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestCheckQuotaCoresAtLimitAndOver(t *testing.T) {
	q := &quota.UserQuota{Cores: 8, MemoryMiB: 16384, Instances: 4,
		DiskGiB: map[string]int64{"pool": 100}}
	u := usage.Usage{Cores: 6, MemoryMiB: 8192, Instances: 2,
		DiskBytes: map[string]int64{"pool": 50 << 30}}
	if reason, ok := checkQuota(u, Delta{Cores: 2}, q, ""); !ok {
		t.Errorf("exactly-at-limit must pass: %s", reason)
	}
	if _, ok := checkQuota(u, Delta{Cores: 3}, q, ""); ok {
		t.Error("one-over must be denied")
	}
}

func TestCheckQuotaStorageNotAllowed(t *testing.T) {
	q := &quota.UserQuota{DiskGiB: map[string]int64{"pool": 100}}
	u := usage.Usage{DiskBytes: map[string]int64{}}
	if _, ok := checkQuota(u, Delta{DiskBytes: map[string]int64{"other": 1 << 30}}, q, ""); ok {
		t.Error("disk on an unlisted storage must be denied")
	}
}

func TestCheckQuotaNodeOverride(t *testing.T) {
	eight := 8
	q := &quota.UserQuota{Cores: 32,
		Nodes: map[string]quota.NodeOverride{"n1": {Cores: &eight}}}
	u := usage.Usage{Cores: 6}
	if _, ok := checkQuota(u, Delta{Cores: 3}, q, "n1"); ok {
		t.Error("node override should cap cores at 8 (6+3>8)")
	}
	if _, ok := checkQuota(u, Delta{Cores: 3}, q, ""); !ok {
		t.Error("base limit 32 should allow 6+3")
	}
}

// fakeAPI implements usage.APIClient with an empty pool (usage 0) and fixed
// guest/snapshot/archive configs.
type fakeAPI struct {
	configs  map[int]map[string]string
	snaps    map[string]map[string]string
	archives map[string]map[string]string
}

func (f *fakeAPI) PoolMembers(string) ([]pve.Member, error) { return nil, nil }
func (f *fakeAPI) GuestConfig(_, _ string, vmid int) (map[string]string, error) {
	if c, ok := f.configs[vmid]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("no config %d", vmid)
}
func (f *fakeAPI) StorageContent(string, string) (map[string]int64, error) {
	return map[string]int64{}, nil
}
func (f *fakeAPI) SnapshotConfig(_, _ string, _ int, snap string) (map[string]string, error) {
	if c, ok := f.snaps[snap]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("no snapshot %s", snap)
}
func (f *fakeAPI) ExtractConfig(_, vol string) (map[string]string, error) {
	if c, ok := f.archives[vol]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("no archive %s", vol)
}

func newStore(t *testing.T) *quota.Store {
	t.Helper()
	p := filepath.Join(t.TempDir(), "q.yaml")
	body := "version: 0\nusers:\n  u@pve:\n    pool: uq-u\n    cores: 4\n" +
		"    memory-mib: 4096\n    instances: 2\n    disk-gib:\n      pool: 50\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := quota.Open(p, discard())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func createReq(envelope, body string) *http.Request {
	r, _ := http.NewRequest("POST", "/api2/"+envelope+"/nodes/n1/qemu",
		io.NopCloser(strings.NewReader(body)))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Cookie", "PVEAuthCookie=PVE:u@pve:6A::sig")
	return r
}

func TestMiddlewareDeniesOverQuota(t *testing.T) {
	eng := usage.NewEngine(&fakeAPI{configs: map[int]map[string]string{}})
	e := New(Options{Store: newStore(t), Engine: eng, Enforce: true, Logger: discard()})
	called := false
	h := e.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createReq("extjs", "vmid=999&cores=8&memory=2048"))

	if called {
		t.Fatal("over-quota create must not be forwarded")
	}
	if w.Code != http.StatusOK {
		t.Errorf("extjs deny should be HTTP 200, got %d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["success"] != float64(0) {
		t.Errorf("expected extjs success:0, got %v (%s)", resp["success"], w.Body.String())
	}
}

func TestMiddlewareAllowsWithinQuota(t *testing.T) {
	// vmid 999 config present so awaitGuest returns immediately.
	eng := usage.NewEngine(&fakeAPI{configs: map[int]map[string]string{
		999: {"cores": "2", "memory": "2048"},
	}})
	e := New(Options{Store: newStore(t), Engine: eng, Enforce: true, Logger: discard()})
	called := false
	h := e.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createReq("json", "vmid=999&cores=2&memory=2048"))

	if !called {
		t.Fatal("within-quota create must be forwarded")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status=%d want 200", w.Code)
	}
}

func TestMiddlewareAdminBypass(t *testing.T) {
	eng := usage.NewEngine(&fakeAPI{configs: map[int]map[string]string{}})
	e := New(Options{Store: newStore(t), Engine: eng, Admins: []string{"u@pve"}, Enforce: true, Logger: discard()})
	called := false
	h := e.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	w := httptest.NewRecorder()
	// way over quota, but u@pve is an admin -> bypass
	h.ServeHTTP(w, createReq("json", "vmid=999&cores=64&memory=999999"))
	if !called {
		t.Error("admin user must bypass enforcement")
	}
}

func TestMiddlewareDeniesOverQuotaClone(t *testing.T) {
	// Source guest 100 has 8 cores; cloning it would exceed the 4-core quota.
	eng := usage.NewEngine(&fakeAPI{configs: map[int]map[string]string{
		100: {"cores": "8", "memory": "2048"},
	}})
	e := New(Options{Store: newStore(t), Engine: eng, Enforce: true, Logger: discard()})
	called := false
	h := e.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	r, _ := http.NewRequest("POST", "/api2/json/nodes/n1/qemu/100/clone",
		io.NopCloser(strings.NewReader("newid=200&pool=uq-u")))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Cookie", "PVEAuthCookie=PVE:u@pve:6A::sig")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if called {
		t.Error("over-quota clone must be denied")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("json deny should be 403, got %d", w.Code)
	}
}

func TestMiddlewareDeniesOverQuotaRollback(t *testing.T) {
	// Rolling back to a snapshot configured with 8 cores exceeds the 4 quota.
	eng := usage.NewEngine(&fakeAPI{
		configs: map[int]map[string]string{100: {"cores": "2", "memory": "2048"}},
		snaps:   map[string]map[string]string{"snap1": {"cores": "8", "memory": "2048"}},
	})
	e := New(Options{Store: newStore(t), Engine: eng, Enforce: true, Logger: discard()})
	called := false
	h := e.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	r, _ := http.NewRequest("POST",
		"/api2/json/nodes/n1/qemu/100/snapshot/snap1/rollback",
		io.NopCloser(strings.NewReader("")))
	r.Header.Set("Cookie", "PVEAuthCookie=PVE:u@pve:6A::sig")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if called {
		t.Error("over-quota rollback must be denied")
	}
}

func TestMiddlewareFailClosed(t *testing.T) {
	// Config change on a guest whose current config can't be read.
	eng := usage.NewEngine(&fakeAPI{configs: map[int]map[string]string{}})
	e := New(Options{Store: newStore(t), Engine: eng, Enforce: true,
		FailClosed: true, Logger: discard()})
	called := false
	h := e.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	r, _ := http.NewRequest("PUT", "/api2/json/nodes/n1/qemu/404/config",
		io.NopCloser(strings.NewReader("memory=8192")))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Cookie", "PVEAuthCookie=PVE:u@pve:6A::sig")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if called {
		t.Error("fail-closed must deny when accounting reads fail")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestMiddlewareFailOpen(t *testing.T) {
	// Same scenario, default (fail-open): the write is allowed through.
	eng := usage.NewEngine(&fakeAPI{configs: map[int]map[string]string{}})
	e := New(Options{Store: newStore(t), Engine: eng, Enforce: true, Logger: discard()})
	called := false
	h := e.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	r, _ := http.NewRequest("PUT", "/api2/json/nodes/n1/qemu/404/config",
		io.NopCloser(strings.NewReader("memory=8192")))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Cookie", "PVEAuthCookie=PVE:u@pve:6A::sig")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !called {
		t.Error("fail-open (default) should allow when accounting reads fail")
	}
}

func TestMiddlewareDefaultDeny(t *testing.T) {
	eng := usage.NewEngine(&fakeAPI{configs: map[int]map[string]string{}})
	e := New(Options{Store: newStore(t), Engine: eng, Enforce: true,
		DefaultDeny: true, Logger: discard()})
	called := false
	h := e.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	// migrate is in neither table -> unknown write
	r, _ := http.NewRequest("POST", "/api2/json/nodes/n1/qemu/100/migrate",
		io.NopCloser(strings.NewReader("target=n2")))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Cookie", "PVEAuthCookie=PVE:u@pve:6A::sig")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if called {
		t.Error("default-deny must block unknown write endpoints")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestMiddlewareUnknownWritePassesWithoutDefaultDeny(t *testing.T) {
	eng := usage.NewEngine(&fakeAPI{configs: map[int]map[string]string{}})
	e := New(Options{Store: newStore(t), Engine: eng, Enforce: true, Logger: discard()})
	called := false
	h := e.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	r, _ := http.NewRequest("POST", "/api2/json/nodes/n1/qemu/100/migrate",
		io.NopCloser(strings.NewReader("target=n2")))
	r.Header.Set("Cookie", "PVEAuthCookie=PVE:u@pve:6A::sig")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !called {
		t.Error("unknown write must pass when default-deny is off")
	}
}

func TestMiddlewarePoolMembershipDenied(t *testing.T) {
	eng := usage.NewEngine(&fakeAPI{configs: map[int]map[string]string{}})
	e := New(Options{Store: newStore(t), Engine: eng, Enforce: true, Logger: discard()})
	called := false
	h := e.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	r, _ := http.NewRequest("PUT", "/api2/json/pools/uq-u",
		io.NopCloser(strings.NewReader("vms=999")))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Cookie", "PVEAuthCookie=PVE:u@pve:6A::sig")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if called {
		t.Error("pool membership edits must be denied for users")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("json deny should be 403, got %d", w.Code)
	}
}
