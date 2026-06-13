package classify

import "testing"

func TestClassify(t *testing.T) {
	tests := []struct {
		method, path string
		action       Action
		quota        bool
		env          string
		guest        string
		vmid         string
		storage      string
	}{
		{"GET", "/api2/json/nodes/n1/qemu/100/config", ActionRead, false, "json", "", "", ""},
		{"POST", "/api2/json/nodes/n1/qemu", ActionGuestCreate, true, "json", "qemu", "", ""},
		{"POST", "/api2/json/nodes/n1/lxc", ActionGuestCreate, true, "json", "lxc", "", ""},
		{"PUT", "/api2/json/nodes/n1/qemu/100/config", ActionGuestConfig, true, "json", "qemu", "100", ""},
		{"POST", "/api2/extjs/nodes/n1/qemu/100/config", ActionGuestConfig, true, "extjs", "qemu", "100", ""},
		{"PUT", "/api2/json/nodes/n1/lxc/101/config", ActionGuestConfig, true, "json", "lxc", "101", ""},
		{"PUT", "/api2/json/nodes/n1/qemu/100/resize", ActionResize, true, "json", "qemu", "100", ""},
		{"POST", "/api2/json/nodes/n1/qemu/100/clone", ActionClone, true, "json", "qemu", "100", ""},
		{"POST", "/api2/json/nodes/n1/qemu/100/move_disk", ActionMoveDisk, true, "json", "qemu", "100", ""},
		{"POST", "/api2/json/nodes/n1/lxc/101/move_volume", ActionMoveDisk, true, "json", "lxc", "101", ""},
		{"POST", "/api2/json/nodes/n1/qemu/100/snapshot/s1/rollback", ActionRollback, true, "json", "qemu", "100", ""},
		{"POST", "/api2/json/nodes/n1/storage/local/content", ActionStorageAlloc, true, "json", "", "", "local"},
		{"POST", "/api2/json/nodes/n1/storage/local/upload", ActionStorageUpload, true, "json", "", "", "local"},
		{"POST", "/api2/json/nodes/n1/storage/local/download-url", ActionStorageUpload, true, "json", "", "", "local"},
		{"PUT", "/api2/json/pools", ActionPoolMembership, true, "json", "", "", ""},
		{"PUT", "/api2/json/pools/mypool", ActionPoolMembership, true, "json", "", "", ""},
		// pass-through writes (must not be quota-relevant)
		{"POST", "/api2/json/nodes/n1/qemu/100/status/start", ActionPassthrough, false, "json", "qemu", "100", ""},
		{"POST", "/api2/json/nodes/n1/qemu/100/vncproxy", ActionPassthrough, false, "json", "qemu", "100", ""},
		{"POST", "/api2/json/nodes/n1/qemu/100/snapshot", ActionPassthrough, false, "json", "qemu", "100", ""},
		{"POST", "/api2/json/nodes/n1/vzdump", ActionPassthrough, false, "json", "", "", ""},
		{"DELETE", "/api2/json/nodes/n1/qemu/100", ActionPassthrough, false, "json", "qemu", "100", ""},
		{"POST", "/api2/json/access/ticket", ActionPassthrough, false, "json", "", "", ""},
		{"GET", "/api2/json/nodes/n1/vncwebsocket", ActionRead, false, "json", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			r := Classify(tt.method, tt.path)
			if r.Action != tt.action || r.QuotaRelevant != tt.quota {
				t.Errorf("action=%s quota=%v, want %s/%v", r.Action, r.QuotaRelevant, tt.action, tt.quota)
			}
			if r.Envelope != tt.env {
				t.Errorf("envelope=%s want %s", r.Envelope, tt.env)
			}
			if r.GuestKind != tt.guest || r.VMID != tt.vmid || r.Storage != tt.storage {
				t.Errorf("guest=%q vmid=%q storage=%q; want %q/%q/%q",
					r.GuestKind, r.VMID, r.Storage, tt.guest, tt.vmid, tt.storage)
			}
		})
	}
}
