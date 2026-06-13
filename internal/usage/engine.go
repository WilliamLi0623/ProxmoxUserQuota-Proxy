package usage

import (
	"fmt"

	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/pve"
)

// APIClient is the read-only PVE surface the engine needs. *pve.Client
// satisfies it; tests use a fake.
type APIClient interface {
	PoolMembers(poolid string) ([]pve.Member, error)
	GuestConfig(node, kind string, vmid int) (map[string]string, error)
	StorageContent(node, storage string) (map[string]int64, error)
}

// Engine computes live usage by reading pool membership and guest configs.
type Engine struct {
	api APIClient
}

func NewEngine(api APIClient) *Engine { return &Engine{api: api} }

// UserUsage sums the configured resources of every qemu/lxc guest in poolid.
// Storage content listings are cached for the duration of the call so each
// (node, storage) is fetched at most once.
func (e *Engine) UserUsage(poolid string) (Usage, error) {
	u := Usage{DiskBytes: map[string]int64{}}
	members, err := e.api.PoolMembers(poolid)
	if err != nil {
		return u, fmt.Errorf("pool %s: %w", poolid, err)
	}

	contentCache := map[string]map[string]int64{}
	for _, m := range members {
		if m.Type != "qemu" && m.Type != "lxc" {
			continue
		}
		cfg, err := e.api.GuestConfig(m.Node, m.Type, m.VMID)
		if err != nil {
			return u, fmt.Errorf("config %s/%d: %w", m.Type, m.VMID, err)
		}
		r := GuestResources(m.Type, cfg)
		u.Cores += r.Cores
		u.MemoryMiB += r.MemoryMiB
		u.Instances++
		for st, b := range r.Disks {
			u.DiskBytes[st] += b
		}
		for _, ref := range r.Unused {
			u.DiskBytes[ref.Storage] += e.volumeSize(contentCache, m.Node, ref)
		}
	}
	return u, nil
}

func (e *Engine) volumeSize(cache map[string]map[string]int64, node string, ref UnusedRef) int64 {
	key := node + "/" + ref.Storage
	cont, ok := cache[key]
	if !ok {
		var err error
		cont, err = e.api.StorageContent(node, ref.Storage)
		if err != nil {
			cont = map[string]int64{}
		}
		cache[key] = cont
	}
	return cont[ref.Volid]
}
