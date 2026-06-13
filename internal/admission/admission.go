// Package admission turns the P3 accounting engine and quota store into P4
// admission decisions: it rejects over-quota create/config/resize (and denies
// pool-membership edits) before forwarding.
//
// Concurrency: each user's enforced writes are serialized by a per-user lock
// that is held across check + forward + settle. "Settle" waits until the
// change is observable in live accounting before releasing the lock — a freshly
// created guest until its VMID joins the pool, a config/resize until the
// guest's config actually changes. PVE applies these via async tasks (the API
// returns a UPID before the config/membership updates), so without settling a
// same-user flood would read stale usage and overshoot.
//
// Every quota-relevant write is still audit-logged (P2 behaviour).
package admission

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/audit"
	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/classify"
	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/identity"
	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/quota"
	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/usage"
)

const gib = int64(1) << 30

// settleTimeout caps how long the per-user lock is held waiting for a change to
// become observable. A var so tests can shorten it.
var settleTimeout = 10 * time.Second

// Enforcer applies quota admission to quota-relevant writes.
type Enforcer struct {
	store   *quota.Store
	engine  *usage.Engine
	admins  map[string]bool
	enforce bool
	logger  *slog.Logger
	locks   locks
}

// New builds an Enforcer. When enforce is false (or store/engine are nil) it
// only audits — identical to P2 — so it is always safe to install.
func New(store *quota.Store, engine *usage.Engine, admins []string, enforce bool, logger *slog.Logger) *Enforcer {
	am := make(map[string]bool, len(admins))
	for _, a := range admins {
		am[a] = true
	}
	return &Enforcer{store: store, engine: engine, admins: am, enforce: enforce, logger: logger}
}

type settleKind int

const (
	settleNone settleKind = iota
	settleCreate
	settleGuestMod
)

type decision struct {
	allowed bool
	reason  string
	settle  settleKind
	vmid    int
	pool    string
	node    string
	kind    string
	preCfg  map[string]string
}

// Middleware audits every quota-relevant write and, when enforcing, admits or
// rejects the core write endpoints.
func (e *Enforcer) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res := classify.Classify(r.Method, r.URL.Path)
		if !res.QuotaRelevant {
			next.ServeHTTP(w, r)
			return
		}
		id := identity.FromRequest(r)
		params := audit.Params(r, res)
		audit.Emit(e.logger, id, res, params)

		if !e.enforce || e.store == nil || e.engine == nil ||
			e.admins[id.User] || !enforceable(res.Action) {
			next.ServeHTTP(w, r)
			return
		}
		if res.Action == classify.ActionPoolMembership {
			e.deny(w, res, id, "pool membership changes are not permitted for users")
			return
		}
		q, ok := e.store.Get(id.User)
		if !ok {
			e.deny(w, res, id, "no quota record for "+id.User+" (default deny)")
			return
		}

		// Hold the per-user lock across decide + forward + settle so a
		// same-user flood is strictly serialized and each request sees the
		// prior one's effect before it is checked.
		lock := e.locks.get(id.User)
		lock.Lock()
		defer lock.Unlock()

		dec := e.decide(res, q, params)
		if !dec.allowed {
			e.deny(w, res, id, dec.reason)
			return
		}
		sc := &statusCapture{ResponseWriter: w}
		next.ServeHTTP(sc, r)
		if sc.status >= 300 {
			return // nothing was applied; no need to wait
		}
		switch dec.settle {
		case settleCreate:
			e.waitMember(dec.pool, dec.vmid)
		case settleGuestMod:
			e.waitConfigChanged(dec.node, dec.kind, dec.vmid, dec.preCfg)
		}
	})
}

func enforceable(a classify.Action) bool {
	switch a {
	case classify.ActionGuestCreate, classify.ActionGuestConfig, classify.ActionResize,
		classify.ActionClone, classify.ActionMoveDisk, classify.ActionRollback,
		classify.ActionStorageAlloc, classify.ActionPoolMembership:
		return true
	}
	return false
}

// decide computes the request's delta and checks it against the user's live
// usage. It fails open (allows) on accounting errors; P6 makes it fail closed.
func (e *Enforcer) decide(res classify.Result, q *quota.UserQuota, params map[string]string) decision {
	vmid := atoi(res.VMID)
	switch res.Action {
	case classify.ActionGuestCreate:
		if audit.IsRestore(params) {
			archive := params["archive"]
			if archive == "" {
				archive = params["ostemplate"] // CT restore carries the backup here
			}
			u, err := e.engine.RestoreUsage(res.Node, archive, res.GuestKind)
			if err != nil {
				return e.failOpen("restore config unreadable", err)
			}
			d := usageToDelta(u)
			return e.finalizeCreate(e.check(q, res.Node, d), d, atoi(params["vmid"]), q.Pool)
		}
		d := CreateDelta(res.GuestKind, params)
		return e.finalizeCreate(e.check(q, res.Node, d), d, atoi(params["vmid"]), q.Pool)

	case classify.ActionClone:
		src, err := e.engine.GuestUsage(res.Node, res.GuestKind, vmid)
		if err != nil {
			return e.failOpen("clone source unreadable", err)
		}
		d := CloneDelta(src, params)
		return e.finalizeCreate(e.check(q, res.Node, d), d, atoi(params["newid"]), q.Pool)

	case classify.ActionGuestConfig, classify.ActionResize, classify.ActionMoveDisk:
		cur, err := e.engine.GuestConfig(res.Node, res.GuestKind, vmid)
		if err != nil {
			return e.failOpen("current config unreadable", err)
		}
		var d Delta
		switch res.Action {
		case classify.ActionResize:
			d = ResizeDelta(params, cur)
		case classify.ActionMoveDisk:
			d = MoveDelta(res.GuestKind, params, cur)
		default:
			d = ConfigDelta(res.GuestKind, params, cur)
		}
		return e.finalizeGuestMod(e.check(q, res.Node, d), d, res.Node, res.GuestKind, vmid, cur)

	case classify.ActionRollback:
		cur, err := e.engine.GuestConfig(res.Node, res.GuestKind, vmid)
		if err != nil {
			return e.failOpen("current config unreadable", err)
		}
		snapU, err := e.engine.SnapshotUsage(res.Node, res.GuestKind, vmid, res.Snapshot)
		if err != nil {
			return e.failOpen("snapshot config unreadable", err)
		}
		d := IncreaseDelta(snapU, usage.ConfigUsage(res.GuestKind, cur))
		return e.finalizeGuestMod(e.check(q, res.Node, d), d, res.Node, res.GuestKind, vmid, cur)

	case classify.ActionStorageAlloc:
		// Raw volume allocation is not attached to a guest config, so it is not
		// counted by config-based accounting and cannot be settled. Best-effort
		// check against current usage; P6 reconciliation backstops drift.
		d := StorageAllocDelta(res.Storage, params)
		return e.check(q, res.Node, d)
	}
	return decision{allowed: true}
}

func (e *Enforcer) failOpen(msg string, err error) decision {
	e.logger.Warn("admission allow: "+msg+" (fail-open)", "err", err)
	return decision{allowed: true}
}

func (e *Enforcer) finalizeCreate(dec decision, d Delta, vmid int, pool string) decision {
	if dec.allowed && d.positive() {
		dec.settle = settleCreate
		dec.vmid = vmid
		dec.pool = pool
	}
	return dec
}

func (e *Enforcer) finalizeGuestMod(dec decision, d Delta, node, kind string, vmid int, cur map[string]string) decision {
	if dec.allowed && d.positive() {
		dec.settle = settleGuestMod
		dec.vmid = vmid
		dec.node = node
		dec.kind = kind
		dec.preCfg = cur
	}
	return dec
}

// check returns an allow/deny decision for a delta against the user's live
// usage. A non-positive delta always passes.
func (e *Enforcer) check(q *quota.UserQuota, node string, d Delta) decision {
	if !d.positive() {
		return decision{allowed: true}
	}
	u, err := e.engine.UserUsage(q.Pool)
	if err != nil {
		e.logger.Warn("admission allow: usage computation failed (P4 fail-open)",
			"pool", q.Pool, "err", err)
		return decision{allowed: true}
	}
	reason, ok := checkQuota(u, d, q, node)
	return decision{allowed: ok, reason: reason}
}

// checkQuota returns ("", true) when the delta fits, else a human reason.
func checkQuota(used usage.Usage, d Delta, q *quota.UserQuota, node string) (string, bool) {
	cores, mem, inst, disk := effectiveLimits(q, node)
	if d.Cores > 0 && used.Cores+d.Cores > cores {
		return fmt.Sprintf("cores: %d in use + %d requested > %d limit",
			used.Cores, d.Cores, cores), false
	}
	if d.MemoryMiB > 0 && used.MemoryMiB+d.MemoryMiB > mem {
		return fmt.Sprintf("memory: %d MiB in use + %d > %d MiB limit",
			used.MemoryMiB, d.MemoryMiB, mem), false
	}
	if d.Instances > 0 && used.Instances+d.Instances > inst {
		return fmt.Sprintf("instances: %d in use + %d > %d limit",
			used.Instances, d.Instances, inst), false
	}
	for st, db := range d.DiskBytes {
		if db <= 0 {
			continue
		}
		lim, ok := disk[st]
		if !ok {
			return fmt.Sprintf("storage %q is not in your quota", st), false
		}
		if combined := ceilGiB(used.DiskBytes[st] + db); combined > lim {
			return fmt.Sprintf("disk %s: %d GiB used+requested > %d GiB limit",
				st, combined, lim), false
		}
	}
	return "", true
}

func effectiveLimits(q *quota.UserQuota, node string) (cores int, mem int64, inst int, disk map[string]int64) {
	cores, mem, inst, disk = q.Cores, q.MemoryMiB, q.Instances, q.DiskGiB
	o, ok := q.Nodes[node]
	if node == "" || !ok {
		return
	}
	if o.Cores != nil {
		cores = *o.Cores
	}
	if o.MemoryMiB != nil {
		mem = *o.MemoryMiB
	}
	if o.Instances != nil {
		inst = *o.Instances
	}
	if o.DiskGiB != nil {
		disk = o.DiskGiB
	}
	return
}

// waitMember blocks until a freshly created guest's VMID joins the pool (so it
// is counted in live usage) or until settleTimeout.
func (e *Enforcer) waitMember(pool string, vmid int) {
	if vmid == 0 {
		return
	}
	deadline := time.Now().Add(settleTimeout)
	for time.Now().Before(deadline) {
		if set, err := e.engine.PoolMemberSet(pool); err == nil && set[vmid] {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// waitConfigChanged blocks until a guest's config differs from its pre-change
// snapshot (the async config/resize task has landed) or until settleTimeout.
func (e *Enforcer) waitConfigChanged(node, kind string, vmid int, pre map[string]string) {
	if vmid == 0 {
		return
	}
	deadline := time.Now().Add(settleTimeout)
	for time.Now().Before(deadline) {
		if cur, err := e.engine.GuestConfig(node, kind, vmid); err == nil && !sameConfig(pre, cur) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func sameConfig(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// deny writes a PVE-compatible error so the native GUI shows the reason.
func (e *Enforcer) deny(w http.ResponseWriter, res classify.Result, id identity.Identity, reason string) {
	e.logger.Warn("quota denied", "user", id.User, "action", string(res.Action),
		"path", res.Path, "reason", reason)
	msg := "uq-proxy quota: " + reason
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	if res.Envelope == "extjs" {
		// ExtJS reads {success, message}; HTTP 200 with success:0 surfaces the
		// message inline in the GUI dialog.
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": 0, "message": msg, "data": nil})
		return
	}
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": nil, "message": msg})
}

func ceilGiB(b int64) int64 {
	if b <= 0 {
		return 0
	}
	return (b + gib - 1) / gib
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// locks hands out one mutex per user id.
type locks struct {
	mu sync.Mutex
	m  map[string]*sync.Mutex
}

func (l *locks) get(user string) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]*sync.Mutex{}
	}
	m, ok := l.m[user]
	if !ok {
		m = &sync.Mutex{}
		l.m[user] = m
	}
	return m
}

// statusCapture records the upstream status code while staying transparent.
type statusCapture struct {
	http.ResponseWriter
	status int
}

func (s *statusCapture) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusCapture) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer (flush etc.).
func (s *statusCapture) Unwrap() http.ResponseWriter { return s.ResponseWriter }
