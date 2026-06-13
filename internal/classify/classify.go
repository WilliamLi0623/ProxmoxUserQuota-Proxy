// Package classify maps an HTTP method+path to a PVE API action, following
// the endpoint tables in ProxmoxUserQuota-Docs/endpoints.md.
//
// It is the shared data source for P2 (audit), P4/P5 (enforcement) and P6
// (default-deny). It is path/method-only and has no side effects; body-level
// refinements (e.g. create-with-archive => restore) happen in the audit layer.
package classify

import "strings"

// Action is the semantic class of a request.
type Action string

const (
	ActionRead           Action = "read"              // GET/HEAD/OPTIONS
	ActionPassthrough    Action = "passthrough"       // a write that is not quota-relevant
	ActionGuestCreate    Action = "guest.create"      // POST nodes/+/{qemu,lxc}
	ActionGuestConfig    Action = "guest.config"      // PUT|POST .../{vmid}/config
	ActionResize         Action = "disk.resize"       // PUT .../{vmid}/resize
	ActionClone          Action = "guest.clone"       // POST .../{vmid}/clone
	ActionMoveDisk       Action = "disk.move"         // POST .../move_disk|move_volume
	ActionRollback       Action = "snapshot.rollback" // POST .../snapshot/+/rollback
	ActionStorageAlloc   Action = "storage.alloc"     // POST storage/+/content
	ActionStorageUpload  Action = "storage.upload"    // POST storage/+/upload|download-url
	ActionPoolMembership Action = "pool.membership"   // PUT|POST /pools
	ActionUnknownWrite   Action = "unknown.write"     // write absent from both tables (P6 default-deny)
)

// Result is the classification outcome. Empty string fields are not applicable.
type Result struct {
	Method        string // original HTTP method
	Path          string // original raw request path (with /api2/... prefix)
	Envelope      string // "json" | "extjs" | "other"
	Action        Action
	GuestKind     string // "qemu" | "lxc" | ""
	Node          string
	VMID          string
	Storage       string
	Snapshot      string // snapshot name, for rollback
	QuotaRelevant bool
	Phase         string // enforcement phase: "P4" | "P5" | ""
}

// Classify returns the action for an HTTP method and request path. The path
// is the raw URL path including the /api2/{json,extjs} prefix.
func Classify(method, rawPath string) Result {
	env, rest := splitEnvelope(rawPath)
	res := Result{Method: method, Path: rawPath, Envelope: env}
	method = strings.ToUpper(method)

	if method == "GET" || method == "HEAD" || method == "OPTIONS" {
		res.Action = ActionRead
		return res
	}

	segs := splitSegs(rest)

	if len(segs) >= 1 && segs[0] == "pools" {
		if method == "PUT" || method == "POST" {
			res.Action = ActionPoolMembership
			res.QuotaRelevant = true
			res.Phase = "P4"
			return res
		}
		return passOrUnknown(method, segs, res)
	}

	if len(segs) >= 3 && segs[0] == "nodes" {
		res.Node = segs[1]
		switch segs[2] {
		case "qemu", "lxc":
			res.GuestKind = segs[2]
			return classifyGuest(method, segs, res)
		case "storage":
			return classifyStorage(method, segs, res)
		}
	}

	return passOrUnknown(method, segs, res)
}

func classifyGuest(method string, segs []string, res Result) Result {
	// segs: nodes/{node}/{kind}[/{vmid}/{op}/...]
	if len(segs) == 3 {
		if method == "POST" {
			res.Action = ActionGuestCreate
			res.QuotaRelevant = true
			res.Phase = "P4"
			return res
		}
		return passOrUnknown(method, segs, res)
	}
	res.VMID = segs[3]
	if len(segs) >= 5 {
		switch segs[4] {
		case "config":
			if method == "PUT" || method == "POST" {
				return quota(res, ActionGuestConfig, "P4")
			}
		case "resize":
			if method == "PUT" {
				return quota(res, ActionResize, "P4")
			}
		case "clone":
			if method == "POST" {
				return quota(res, ActionClone, "P5")
			}
		case "move_disk", "move_volume":
			if method == "POST" {
				return quota(res, ActionMoveDisk, "P5")
			}
		case "snapshot":
			// .../snapshot/{snap}/rollback is quota-relevant; create/delete pass.
			if method == "POST" && len(segs) >= 7 && segs[6] == "rollback" {
				res.Snapshot = segs[5]
				return quota(res, ActionRollback, "P5")
			}
		}
	}
	return passOrUnknown(method, segs, res)
}

func classifyStorage(method string, segs []string, res Result) Result {
	// segs: nodes/{node}/storage/{storage}/{op}
	if len(segs) >= 4 {
		res.Storage = segs[3]
	}
	if method == "POST" && len(segs) >= 5 {
		switch segs[4] {
		case "content":
			return quota(res, ActionStorageAlloc, "P5")
		case "upload", "download-url":
			return quota(res, ActionStorageUpload, "P5")
		}
	}
	return passOrUnknown(method, segs, res)
}

// passOrUnknown classifies a non-quota write as a known-safe pass-through or,
// failing that, an unknown write (which P6 default-deny rejects).
func passOrUnknown(method string, segs []string, res Result) Result {
	if knownPassthrough(method, segs) {
		res.Action = ActionPassthrough
	} else {
		res.Action = ActionUnknownWrite
	}
	return res
}

// knownPassthrough is the allowlist of write endpoints that change no quota
// dimension and must always be forwarded (endpoints.md pass-through table).
func knownPassthrough(method string, segs []string) bool {
	if len(segs) == 0 {
		return false
	}
	switch segs[0] {
	case "access":
		// login / self-service flows
		return len(segs) >= 2 && (segs[1] == "ticket" || segs[1] == "password" ||
			segs[1] == "tfa" || segs[1] == "openid")
	case "nodes":
		return nodePassthrough(method, segs)
	}
	return false
}

func nodePassthrough(method string, segs []string) bool {
	if len(segs) < 3 {
		return false
	}
	switch segs[2] {
	case "vzdump": // backups
		return true
	case "qemu", "lxc":
		return guestPassthrough(method, segs)
	case "storage":
		// deleting stored content frees space
		return method == "DELETE" && len(segs) >= 5 && segs[4] == "content"
	}
	return false
}

func guestPassthrough(method string, segs []string) bool {
	if len(segs) == 4 {
		return method == "DELETE" // destroying a guest frees quota
	}
	if len(segs) >= 5 {
		switch segs[4] {
		case "status", "vncproxy", "termproxy", "spiceproxy", "vncwebsocket",
			"agent", "firewall", "sendkey", "cloudinit", "unlink", "snapshot",
			"rrd", "rrddata", "feature", "spiceshell":
			return true
		}
	}
	return false
}

func quota(res Result, a Action, phase string) Result {
	res.Action = a
	res.QuotaRelevant = true
	res.Phase = phase
	return res
}

func splitEnvelope(p string) (env, rest string) {
	p = strings.TrimPrefix(p, "/")
	switch {
	case p == "api2/json" || strings.HasPrefix(p, "api2/json/"):
		return "json", strings.TrimPrefix(strings.TrimPrefix(p, "api2/json"), "/")
	case p == "api2/extjs" || strings.HasPrefix(p, "api2/extjs/"):
		return "extjs", strings.TrimPrefix(strings.TrimPrefix(p, "api2/extjs"), "/")
	}
	return "other", p
}

func splitSegs(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}
