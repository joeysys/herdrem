package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Client represents a connected WebSocket client.
type Client struct {
	conn  *websocket.Conn
	send  chan []byte
	token string
	// incremental marks a client that declared support for incremental
	// terminal deltas (append/rev/base_rev). Deltas are only delivered to
	// such clients; older clients receive full snapshots only.
	incremental bool
}

// broadcastExceptMsg carries a broadcast with an excluded connection.
type broadcastExceptMsg struct {
	data   []byte
	except *websocket.Conn
}

// Hub manages all connected WS clients and broadcasts.
type Hub struct {
	mu                   sync.RWMutex
	clients              map[*Client]struct{}
	register             chan *Client
	unregister           chan *Client
	broadcast            chan []byte
	broadcastIncremental chan []byte
	broadcastExcept      chan broadcastExceptMsg
	logger               *slog.Logger
	onConnect            func() // invoked after each client registers (e.g. poll.TriggerPoll)
}

// SetOnConnect registers a callback invoked by the hub loop immediately after
// a client is added — so anything it triggers (an immediate poll) already
// observes the new client in Count(). Must be non-blocking; set before Run.
func (h *Hub) SetOnConnect(fn func()) {
	h.mu.Lock()
	h.onConnect = fn
	h.mu.Unlock()
}

// New creates a Hub.
func New(logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	return &Hub{
		clients:              make(map[*Client]struct{}),
		register:             make(chan *Client, 8),
		unregister:           make(chan *Client, 8),
		broadcast:            make(chan []byte, 64),
		broadcastIncremental: make(chan []byte, 64),
		broadcastExcept:      make(chan broadcastExceptMsg, 64),
		logger:               logger,
	}
}

// Run starts the hub event loop. Blocks until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = struct{}{}
			onConnect := h.onConnect
			h.mu.Unlock()
			h.logger.Info("client connected", "count", h.Count())
			if onConnect != nil {
				onConnect()
			}

		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			h.mu.Unlock()
			h.logger.Info("client disconnected", "count", h.Count())

		case msg := <-h.broadcast:
			h.mu.RLock()
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					// Client buffer full — kick it
					go func(cl *Client) {
						h.unregister <- cl
					}(c)
				}
			}
			h.mu.RUnlock()

		case msg := <-h.broadcastIncremental:
			// Incremental terminal deltas are only sent to clients that declared
			// support; older clients would splice the delta as a full
			// snapshot and corrupt their view.
			h.mu.RLock()
			for c := range h.clients {
				if !c.incremental {
					continue
				}
				select {
				case c.send <- msg:
				default:
					go func(cl *Client) {
						h.unregister <- cl
					}(c)
				}
			}
			h.mu.RUnlock()

		case bem := <-h.broadcastExcept:
			h.mu.RLock()
			for c := range h.clients {
				if c.conn == bem.except {
					continue
				}
				select {
				case c.send <- bem.data:
				default:
					go func(cl *Client) {
						h.unregister <- cl
					}(c)
				}
			}
			h.mu.RUnlock()

		case <-ctx.Done():
			h.mu.Lock()
			for c := range h.clients {
				close(c.send)
				_ = c.conn.Close(websocket.StatusNormalClosure, "shutdown")
			}
			h.clients = make(map[*Client]struct{})
			h.mu.Unlock()
			return
		}
	}
}

// Broadcast sends a message to all connected clients.
func (h *Hub) Broadcast(msg []byte) {
	select {
	case h.broadcast <- msg:
	default:
		h.logger.Warn("broadcast channel full, dropping message")
	}
}

// BroadcastJSON marshals and broadcasts a message.
func (h *Hub) BroadcastJSON(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		h.logger.Error("broadcast marshal error", "err", err)
		return
	}
	h.Broadcast(data)
}

// BroadcastJSONIncremental broadcasts a message only to clients that declared
// support for incremental terminal deltas. Never send deltas to clients that
// would misinterpret them as full snapshots.
func (h *Hub) BroadcastJSONIncremental(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		h.logger.Error("broadcast marshal error", "err", err)
		return
	}
	select {
	case h.broadcastIncremental <- data:
	default:
		h.logger.Warn("incremental broadcast channel full, dropping message")
	}
}

// MarkIncremental flags the connection as understanding incremental terminal
// deltas. Called when a client declares support (e.g. read_pane with an
// explicit incremental capability).
func (h *Hub) MarkIncremental(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.conn == conn {
			c.incremental = true
			return
		}
	}
}

// BroadcastJSONExcept marshals and broadcasts a message to all clients
// except the one on the given connection (prevents duplicate delivery).
func (h *Hub) BroadcastJSONExcept(v interface{}, except *websocket.Conn) {
	data, err := json.Marshal(v)
	if err != nil {
		h.logger.Error("broadcast marshal error", "err", err)
		return
	}
	h.broadcastExcept <- broadcastExceptMsg{data: data, except: except}
}

// Count returns the number of connected clients.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// MessageHandler processes an incoming WS message from a client.
type MessageHandler func(ctx context.Context, conn *websocket.Conn, data []byte)

// HandleConn takes an already-upgraded websocket.Conn and manages its lifecycle.
// onMessage is called for each incoming message; if nil, messages are discarded.
func (h *Hub) HandleConn(ctx context.Context, conn *websocket.Conn, token string, onMessage MessageHandler) {
	c := &Client{
		conn:  conn,
		send:  make(chan []byte, 32),
		token: token,
	}

	h.register <- c

	// Write pump
	go func() {
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			select {
			case msg, ok := <-c.send:
				if !ok {
					return
				}
				writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Write(writeCtx, websocket.MessageText, msg)
				cancel()
				if err != nil {
					h.logger.Error("write error", "err", err)
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Read pump — dispatch to handler or discard
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		if onMessage != nil {
			onMessage(ctx, conn, data)
		}
	}

	h.unregister <- c
}
