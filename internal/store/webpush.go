package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// PushSub is one browser's Web Push subscription. Endpoint is the key.
// Input and Done are that browser's alert preferences.
type PushSub struct {
	Endpoint string `json:"endpoint"`
	P256DH   string `json:"p256dh"`
	Auth     string `json:"auth"`
	Input    bool   `json:"input"`
	Done     string `json:"done"`
}

type pushFile struct {
	Enabled    bool      `json:"enabled"`
	PublicKey  string    `json:"public_key,omitempty"`
	PrivateKey string    `json:"private_key,omitempty"`
	Subs       []PushSub `json:"subscriptions,omitempty"`
}

// WebPushStore persists the Web Push switch, the VAPID key pair, and
// per-browser subscriptions. The file holds a private key, so it is mode 0600.
type WebPushStore struct {
	mu   sync.Mutex
	path string
	data pushFile
}

// OpenWebPush loads path. A missing file is a disabled store with no keys.
func OpenWebPush(path string) (*WebPushStore, error) {
	s := &WebPushStore{path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *WebPushStore) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Enabled
}

func (s *WebPushStore) SetEnabled(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Enabled = on
	return s.saveLocked()
}

func (s *WebPushStore) Keys() (public, private string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.PublicKey, s.data.PrivateKey
}

func (s *WebPushStore) SetKeys(public, private string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.PublicKey = public
	s.data.PrivateKey = private
	return s.saveLocked()
}

// EnsureKeys returns the stored VAPID pair. When either half is missing, gen
// runs while the store lock is held, so two first-time enables cannot each
// write a key and leave the earlier subscription permanently unverifiable.
func (s *WebPushStore) EnsureKeys(gen func() (public, private string, err error)) (string, string, error) {
	if gen == nil {
		return "", "", errors.New("webpush: key generator is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.PublicKey != "" && s.data.PrivateKey != "" {
		return s.data.PublicKey, s.data.PrivateKey, nil
	}
	pub, priv, err := gen()
	if err != nil {
		return "", "", err
	}
	if pub == "" || priv == "" {
		return "", "", errors.New("webpush: empty vapid key")
	}
	s.data.PublicKey = pub
	s.data.PrivateKey = priv
	if err := s.saveLocked(); err != nil {
		s.data.PublicKey = ""
		s.data.PrivateKey = ""
		return "", "", err
	}
	return pub, priv, nil
}

func (s *WebPushStore) Subscriptions() []PushSub {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PushSub, len(s.data.Subs))
	copy(out, s.data.Subs)
	return out
}

// Upsert inserts or replaces the subscription with the same endpoint.
func (s *WebPushStore) Upsert(sub PushSub) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Subs {
		if s.data.Subs[i].Endpoint == sub.Endpoint {
			s.data.Subs[i] = sub
			return s.saveLocked()
		}
	}
	s.data.Subs = append(s.data.Subs, sub)
	return s.saveLocked()
}

func (s *WebPushStore) UpdatePrefs(endpoint string, input bool, done string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Subs {
		if s.data.Subs[i].Endpoint != endpoint {
			continue
		}
		s.data.Subs[i].Input = input
		s.data.Subs[i].Done = done
		return s.saveLocked()
	}
	return os.ErrNotExist
}

func (s *WebPushStore) Delete(endpoint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.data.Subs[:0]
	found := false
	for _, sub := range s.data.Subs {
		if sub.Endpoint == endpoint {
			found = true
			continue
		}
		next = append(next, sub)
	}
	if !found {
		return nil
	}
	s.data.Subs = next
	return s.saveLocked()
}

func (s *WebPushStore) saveLocked() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-webpush-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Chmod(s.path, 0o600)
}
