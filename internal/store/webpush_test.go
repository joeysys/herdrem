package store

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebPushStoreSurvivesReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webpush.json")
	s, err := OpenWebPush(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Enabled() {
		t.Fatal("new store is enabled")
	}
	if err := s.SetKeys("pub", "priv"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(PushSub{Endpoint: "https://push.example/a", P256DH: "k", Auth: "a", Input: true, Done: "long"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	again, err := OpenWebPush(path)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Enabled() {
		t.Fatal("enabled flag was not reloaded")
	}
	pub, priv := again.Keys()
	if pub != "pub" || priv != "priv" {
		t.Fatalf("keys = %s %s", pub, priv)
	}
	subs := again.Subscriptions()
	if len(subs) != 1 || subs[0].Done != "long" {
		t.Fatalf("subs = %+v", subs)
	}
	if err := again.Delete(subs[0].Endpoint); err != nil {
		t.Fatal(err)
	}
	if len(again.Subscriptions()) != 0 {
		t.Fatal("delete did not stick")
	}
}

func TestEnsureKeysGeneratesOnceUnderLock(t *testing.T) {
	s, err := OpenWebPush(filepath.Join(t.TempDir(), "webpush.json"))
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	start := make(chan struct{})
	var wg sync.WaitGroup
	pubs := make([]string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			pub, priv, err := s.EnsureKeys(func() (string, string, error) {
				calls.Add(1)
				time.Sleep(40 * time.Millisecond)
				return "pub", "priv", nil
			})
			if err != nil {
				t.Errorf("ensure: %v", err)
				return
			}
			if pub != "pub" || priv != "priv" {
				t.Errorf("keys = %s %s", pub, priv)
			}
			pubs[i] = pub
		}(i)
	}
	close(start)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("generator calls = %d, want 1", calls.Load())
	}
	if pubs[0] != "pub" || pubs[1] != "pub" {
		t.Fatalf("pubs = %q %q", pubs[0], pubs[1])
	}
	pub, priv := s.Keys()
	if pub != "pub" || priv != "priv" {
		t.Fatalf("stored = %s %s", pub, priv)
	}
}
