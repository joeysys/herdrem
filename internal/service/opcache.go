package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
	"herdrem/internal/protocol"
)

const (
	opTTL = 10 * time.Minute
	opCap = 1024
)

// opError is a client error with a machine-readable key. A plain error is
// still cached and replayed, with an empty key.
type opError struct {
	key string
	msg string
}

func (e *opError) Error() string { return e.msg }

type opSlot struct {
	done    bool
	at      time.Time
	payload []byte
	ready   chan struct{}
}

// opCache is the process-wide idempotency table. The key is
// (op type, operation_id), not the connection. In-flight calls share one
// result. Finished results stay for opTTL, and only finished rows are
// evicted when the table passes opCap.
type opCache struct {
	mu    sync.Mutex
	slots map[string]*opSlot
	order []string // completion order of done keys
	now   func() time.Time
}

func newOpCache() *opCache {
	return &opCache{slots: make(map[string]*opSlot), now: time.Now}
}

func newBootID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("boot-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// BootID is the id hello advertises for this process.
func (s *Service) BootID() string { return s.bootID }

// begin returns the slot and whether the caller owns the execution.
// A finished slot older than opTTL is replaced. A live slot is not.
func (c *opCache) begin(key string) (*opSlot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if sl, ok := c.slots[key]; ok {
		if sl.done && now.Sub(sl.at) >= opTTL {
			c.dropLocked(key)
		} else {
			return sl, false
		}
	}
	sl := &opSlot{ready: make(chan struct{})}
	c.slots[key] = sl
	c.evictLocked()
	return sl, true
}

func (c *opCache) finish(key string, payload []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sl, ok := c.slots[key]
	if !ok || sl.done {
		return
	}
	sl.payload = append([]byte(nil), payload...)
	sl.done = true
	sl.at = c.now()
	c.order = append(c.order, key)
	close(sl.ready)
	c.evictLocked()
}

func (c *opCache) dropLocked(key string) {
	delete(c.slots, key)
	kept := c.order[:0]
	for _, k := range c.order {
		if k != key {
			kept = append(kept, k)
		}
	}
	c.order = kept
}

// evictLocked drops the oldest finished rows until the table is within opCap.
// An in-flight row is not in order, so it stays even if the table is over cap.
func (c *opCache) evictLocked() {
	for len(c.slots) > opCap && len(c.order) > 0 {
		key := c.order[0]
		c.order = c.order[1:]
		sl, ok := c.slots[key]
		if !ok || !sl.done {
			continue
		}
		delete(c.slots, key)
	}
}

// once runs fn at most once for (op, operationID) inside this process.
// requireID rejects an empty operation_id without calling fn. A boot_id that
// is missing or not this process's id returns stale_boot and does not call fn
// or store a result. An empty operation_id with requireID false is the old
// path: run and reply, with no cache.
func (s *Service) once(ctx context.Context, conn *websocket.Conn, op, operationID, bootID, requestID string, requireID bool, fn func() (any, error)) {
	if operationID == "" {
		if requireID {
			s.writeRaw(ctx, conn, s.opPayload(op, requestID, nil, &opError{key: "invalid", msg: "missing operation_id"}))
			return
		}
		v, err := fn()
		s.writeRaw(ctx, conn, s.opPayload(op, requestID, v, err))
		return
	}
	if bootID == "" || bootID != s.bootID {
		s.writeRaw(ctx, conn, s.opPayload(op, requestID, nil, &opError{key: "stale_boot", msg: "relay restarted; refresh and try again"}))
		return
	}
	if s.ops == nil {
		s.writeRaw(ctx, conn, s.opPayload(op, requestID, nil, &opError{key: "invalid", msg: "operation cache unavailable"}))
		return
	}
	key := op + "\x00" + operationID
	slot, owner := s.ops.begin(key)
	if !owner {
		select {
		case <-slot.ready:
			s.writeRaw(ctx, conn, slot.payload)
		case <-ctx.Done():
		}
		return
	}
	finished := false
	defer func() {
		if !finished {
			s.ops.finish(key, s.opPayload(op, requestID, nil, errors.New("interrupted")))
		}
	}()
	v, err := fn()
	payload := s.opPayload(op, requestID, v, err)
	s.ops.finish(key, payload)
	finished = true
	s.writeRaw(ctx, conn, payload)
}

func (s *Service) opPayload(op, requestID string, v any, err error) []byte {
	if err != nil {
		key := ""
		msg := err.Error()
		var oe *opError
		if errors.As(err, &oe) {
			key = oe.key
			msg = oe.msg
		}
		b, mErr := json.Marshal(protocol.ErrorMsg{
			Type: "error", Op: op, Message: msg, Key: key, RequestID: requestID,
		})
		if mErr != nil {
			return nil
		}
		return b
	}
	if v == nil {
		return nil
	}
	b, mErr := json.Marshal(v)
	if mErr != nil {
		return nil
	}
	return b
}

func (s *Service) writeRaw(ctx context.Context, conn *websocket.Conn, payload []byte) {
	if conn == nil || len(payload) == 0 {
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := conn.Write(writeCtx, websocket.MessageText, payload); err != nil {
		s.logger.Error("write error", "err", err)
	}
}
