package service

import (
	"fmt"
	"regexp"
	"testing"
	"time"
)

func TestOpCacheReplaysAndExpires(t *testing.T) {
	c := newOpCache()
	start := time.Now()
	c.now = func() time.Time { return start }
	slot, owner := c.begin("respond\x00op")
	if !owner {
		t.Fatal("first caller should own the slot")
	}
	c.finish("respond\x00op", []byte(`{"ok":true}`))
	if !slot.done || string(slot.payload) != `{"ok":true}` {
		t.Fatalf("slot = %+v", slot)
	}
	again, owner := c.begin("respond\x00op")
	if owner || again != slot || string(again.payload) != `{"ok":true}` {
		t.Fatal("replay should return the finished slot")
	}
	c.now = func() time.Time { return start.Add(opTTL + time.Second) }
	_, owner = c.begin("respond\x00op")
	if !owner {
		t.Fatal("expired id should run again")
	}
}

func TestOpCacheSingleFlight(t *testing.T) {
	c := newOpCache()
	_, owner := c.begin("pane_create\x00op")
	if !owner {
		t.Fatal("owner")
	}
	waited := make(chan struct{})
	go func() {
		slot, owner := c.begin("pane_create\x00op")
		if owner {
			t.Error("second caller ran the operation")
		}
		<-slot.ready
		if string(slot.payload) != "one" {
			t.Errorf("payload = %s", slot.payload)
		}
		close(waited)
	}()
	time.Sleep(20 * time.Millisecond)
	c.finish("pane_create\x00op", []byte("one"))
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("waiter did not receive the first result")
	}
}

func TestOpCacheEvictsOldestDoneAndKeepsInFlight(t *testing.T) {
	c := newOpCache()
	for i := 0; i < opCap; i++ {
		key := fmt.Sprintf("k%d", i)
		if _, owner := c.begin(key); !owner {
			t.Fatalf("owner %d", i)
		}
		c.finish(key, []byte("x"))
	}
	if _, owner := c.begin("inflight"); !owner {
		t.Fatal("inflight owner")
	}
	if _, ok := c.slots["inflight"]; !ok {
		t.Fatal("in-flight row missing")
	}
	if _, ok := c.slots["k0"]; ok {
		t.Fatal("adding an in-flight row should evict the oldest done row")
	}
	if _, owner := c.begin("extra"); !owner {
		t.Fatal("extra owner")
	}
	if _, ok := c.slots["inflight"]; !ok {
		t.Fatal("eviction removed an in-flight row")
	}
	if _, ok := c.slots["k1"]; ok {
		t.Fatal("oldest remaining done row should have been evicted")
	}
}

func TestNewBranchShape(t *testing.T) {
	got, err := newBranch()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^worktree/(brave|calm|clear|green|lucky|quiet|rapid|silver)-(river|cloud|field|forest|harbor|meadow|stone|valley)-[0-9a-f]{4}$`).MatchString(got) {
		t.Fatalf("branch = %s", got)
	}
	if _, err := cleanBranch("feature/../x"); err == nil {
		t.Fatal("branch with .. should be rejected")
	}
	if _, err := cleanBranch("-secret"); err == nil {
		t.Fatal("leading dash should be rejected")
	}
}
