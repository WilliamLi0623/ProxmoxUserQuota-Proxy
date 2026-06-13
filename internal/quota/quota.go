// Package quota loads, validates and hot-reloads the declarative quota store
// (quotas.yaml, schema in ProxmoxUserQuota-Docs/quota-model.md).
//
// Policy is default-deny: a user with no record has zero quota. A broken file
// on reload is rejected and the previous good config is kept.
package quota

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// NodeOverride narrows limits on a specific node (pointers distinguish
// "unset" from "zero").
type NodeOverride struct {
	Cores     *int             `yaml:"cores"`
	MemoryMiB *int64           `yaml:"memory-mib"`
	Instances *int             `yaml:"instances"`
	DiskGiB   map[string]int64 `yaml:"disk-gib"`
}

// UserQuota is one user's limits. A dimension absent here counts as 0 (deny).
type UserQuota struct {
	Pool      string                  `yaml:"pool"`
	Cores     int                     `yaml:"cores"`
	MemoryMiB int64                   `yaml:"memory-mib"`
	Instances int                     `yaml:"instances"`
	DiskGiB   map[string]int64        `yaml:"disk-gib"`
	Nodes     map[string]NodeOverride `yaml:"nodes"`
}

// Config is the whole quota file.
type Config struct {
	Version  int                   `yaml:"version"`
	Defaults map[string]any        `yaml:"defaults"`
	Users    map[string]*UserQuota `yaml:"users"`
}

var poolIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Parse unmarshals and validates raw YAML.
func Parse(data []byte) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("yaml: %w", err)
	}
	if err := Validate(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate checks the schema invariants, including the injective user->pool
// mapping that the whole accounting model depends on.
func Validate(c *Config) error {
	if c.Version != 0 {
		return fmt.Errorf("unsupported version %d (want 0)", c.Version)
	}
	pools := map[string]string{}
	for user, q := range c.Users {
		if q == nil {
			return fmt.Errorf("user %q: empty record", user)
		}
		if q.Pool == "" {
			return fmt.Errorf("user %q: missing pool", user)
		}
		if !poolIDRe.MatchString(q.Pool) {
			return fmt.Errorf("user %q: invalid pool id %q", user, q.Pool)
		}
		if other, dup := pools[q.Pool]; dup {
			return fmt.Errorf("pool %q claimed by both %q and %q", q.Pool, other, user)
		}
		pools[q.Pool] = user
		if q.Cores < 0 || q.MemoryMiB < 0 || q.Instances < 0 {
			return fmt.Errorf("user %q: negative limit", user)
		}
		for st, g := range q.DiskGiB {
			if g < 0 {
				return fmt.Errorf("user %q: negative disk-gib for %q", user, st)
			}
		}
	}
	return nil
}

// Store holds the current config and reloads it when the file changes.
type Store struct {
	path string
	log  *slog.Logger

	mu    sync.RWMutex
	cfg   *Config
	mtime time.Time
}

// Open loads the initial config; an invalid file is a hard startup error.
func Open(path string, log *slog.Logger) (*Store, error) {
	s := &Store{path: path, log: log}
	cfg, mt, err := s.read()
	if err != nil {
		return nil, err
	}
	s.cfg = cfg
	s.mtime = mt
	return s, nil
}

func (s *Store) read() (*Config, time.Time, error) {
	fi, err := os.Stat(s.path)
	if err != nil {
		return nil, time.Time{}, err
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, time.Time{}, err
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fi.ModTime(), err
	}
	return cfg, fi.ModTime(), nil
}

// Get returns a copy-free pointer to the user's quota, or false if the user
// has no record (default deny).
func (s *Store) Get(user string) (*UserQuota, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	q, ok := s.cfg.Users[user]
	return q, ok
}

// Users returns the set of configured user ids.
func (s *Store) Users() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.cfg.Users))
	for u := range s.cfg.Users {
		out = append(out, u)
	}
	return out
}

// Watch polls the file mtime and hot-reloads on change. A reload that fails
// validation is logged and the previous config is kept. Returns when ctx ends.
func (s *Store) Watch(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fi, err := os.Stat(s.path)
			if err != nil {
				s.log.Error("quota stat", "path", s.path, "err", err)
				continue
			}
			s.mu.RLock()
			unchanged := fi.ModTime().Equal(s.mtime)
			s.mu.RUnlock()
			if unchanged {
				continue
			}
			cfg, mt, err := s.read()
			if err != nil {
				s.log.Error("quota reload rejected, keeping previous",
					"path", s.path, "err", err)
				s.mu.Lock()
				s.mtime = mt // avoid re-reporting the same broken file
				s.mu.Unlock()
				continue
			}
			s.mu.Lock()
			s.cfg = cfg
			s.mtime = mt
			s.mu.Unlock()
			s.log.Info("quota reloaded", "path", s.path, "users", len(cfg.Users))
		}
	}
}
