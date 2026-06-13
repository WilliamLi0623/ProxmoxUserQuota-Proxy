package usage

import (
	"testing"

	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/pve"
)

type fakeAPI struct {
	members []pve.Member
	configs map[int]map[string]string
	content map[string]map[string]int64
}

func (f *fakeAPI) PoolMembers(string) ([]pve.Member, error) { return f.members, nil }

func (f *fakeAPI) GuestConfig(_, _ string, vmid int) (map[string]string, error) {
	return f.configs[vmid], nil
}

func (f *fakeAPI) StorageContent(node, storage string) (map[string]int64, error) {
	return f.content[node+"/"+storage], nil
}

func TestEngineUserUsage(t *testing.T) {
	f := &fakeAPI{
		members: []pve.Member{
			{VMID: 100, Node: "n1", Type: "qemu"},
			{VMID: 101, Node: "n1", Type: "lxc"},
			{VMID: 0, Node: "n1", Type: "storage"}, // skipped
		},
		configs: map[int]map[string]string{
			100: {
				"sockets": "1", "cores": "4", "memory": "4096",
				"scsi0":   "tank:vm-100-disk-0,size=50G",
				"unused0": "tank:vm-100-disk-9", // resolved via storage content
			},
			101: {"cores": "2", "memory": "2048", "rootfs": "tank:subvol-101-disk-0,size=10G"},
		},
		content: map[string]map[string]int64{
			"n1/tank": {"tank:vm-100-disk-9": 20 << 30},
		},
	}
	u, err := NewEngine(f).UserUsage("uq-x")
	if err != nil {
		t.Fatal(err)
	}
	if u.Cores != 6 {
		t.Errorf("cores=%d want 6", u.Cores)
	}
	if u.MemoryMiB != 6144 {
		t.Errorf("memory=%d want 6144", u.MemoryMiB)
	}
	if u.Instances != 2 {
		t.Errorf("instances=%d want 2", u.Instances)
	}
	if want := int64(50<<30 + 10<<30 + 20<<30); u.DiskBytes["tank"] != want {
		t.Errorf("disk=%d want %d", u.DiskBytes["tank"], want)
	}
}
