package httpserver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"herdrem/internal/store"
	"herdrem/internal/webpush"
)

const maxPushSubs = 32

type pushStatusBody struct {
	Enabled    bool   `json:"enabled"`
	Subscribed bool   `json:"subscribed"`
	Stable     bool   `json:"stable"`
	Reason     string `json:"reason,omitempty"`
	PublicKey  string `json:"publicKey,omitempty"`
	Input      *bool  `json:"input,omitempty"`
	Done       string `json:"done,omitempty"`
}

func (s *Server) handleWebPushStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if !s.webPushGate(w, r) {
		return
	}
	s.writePushStatus(w, r)
}

func (s *Server) handleWebPushEnabled(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if !s.webPushGate(w, r) {
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	st := s.webPush.Store()
	if body.Enabled {
		if ok, reason := s.pushStable(r); !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(pushStatusBody{Enabled: false, Stable: false, Reason: reason})
			return
		}
		if _, _, err := st.EnsureKeys(webpush.GenerateKeys); err != nil {
			http.Error(w, "key generation failed", http.StatusInternalServerError)
			return
		}
	}
	if err := st.SetEnabled(body.Enabled); err != nil {
		http.Error(w, "store failed", http.StatusInternalServerError)
		return
	}
	s.writePushStatus(w, r)
}

func (s *Server) handleWebPushSubscribe(w http.ResponseWriter, r *http.Request) {
	if !s.webPushWritable(w, r) {
		return
	}
	if ok, reason := s.pushStable(r); !ok {
		http.Error(w, reason, http.StatusForbidden)
		return
	}
	var body struct {
		Endpoint string `json:"endpoint"`
		P256DH   string `json:"p256dh"`
		Auth     string `json:"auth"`
		Input    *bool  `json:"input"`
		Done     string `json:"done"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := allowedPushEndpoint(body.Endpoint); err != nil {
		http.Error(w, "bad endpoint", http.StatusBadRequest)
		return
	}
	if _, err := decodePushKey(body.P256DH, 65); err != nil {
		http.Error(w, "bad p256dh", http.StatusBadRequest)
		return
	}
	if _, err := decodePushKey(body.Auth, 16); err != nil {
		http.Error(w, "bad auth", http.StatusBadRequest)
		return
	}
	done := body.Done
	if done == "" {
		done = "long"
	}
	if done != "off" && done != "long" && done != "always" {
		http.Error(w, "bad done preference", http.StatusBadRequest)
		return
	}
	input := true
	if body.Input != nil {
		input = *body.Input
	}
	st := s.webPush.Store()
	replacing := false
	for _, sub := range st.Subscriptions() {
		if sub.Endpoint == body.Endpoint {
			replacing = true
			break
		}
	}
	if !replacing && len(st.Subscriptions()) >= maxPushSubs {
		http.Error(w, "too many subscriptions", http.StatusBadRequest)
		return
	}
	if err := st.Upsert(store.PushSub{
		Endpoint: body.Endpoint,
		P256DH:   body.P256DH,
		Auth:     body.Auth,
		Input:    input,
		Done:     done,
	}); err != nil {
		http.Error(w, "store failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleWebPushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if !s.webPushWritable(w, r) {
		return
	}
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.webPush.Store().Delete(body.Endpoint); err != nil {
		http.Error(w, "store failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleWebPushPrefs(w http.ResponseWriter, r *http.Request) {
	if !s.webPushWritable(w, r) {
		return
	}
	var body struct {
		Endpoint string `json:"endpoint"`
		Input    bool   `json:"input"`
		Done     string `json:"done"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if body.Done != "off" && body.Done != "long" && body.Done != "always" {
		http.Error(w, "bad done preference", http.StatusBadRequest)
		return
	}
	if err := s.webPush.Store().UpdatePrefs(body.Endpoint, body.Input, body.Done); err != nil {
		http.Error(w, "no subscription", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleWebPushTest(w http.ResponseWriter, r *http.Request) {
	if !s.webPushWritable(w, r) {
		return
	}
	if err := s.webPush.SendTest(r.Context()); err != nil {
		if s.logger != nil {
			s.logger.Info("webpush test failed", "err", err)
		}
		http.Error(w, "push was not delivered", http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) webPushWritable(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return false
	}
	if !s.webPushGate(w, r) {
		return false
	}
	if s.webPush == nil || !s.webPush.Store().Enabled() {
		http.NotFound(w, r)
		return false
	}
	return true
}

func (s *Server) webPushGate(w http.ResponseWriter, r *http.Request) bool {
	if s.webPush == nil {
		http.NotFound(w, r)
		return false
	}
	if !s.relayAuthGate(w, r) {
		return false
	}
	if !isSameOrigin(r) {
		http.Error(w, "same-origin required", http.StatusForbidden)
		return false
	}
	return true
}

func (s *Server) writePushStatus(w http.ResponseWriter, r *http.Request) {
	st := s.webPush.Store()
	stable, reason := s.pushStable(r)
	body := pushStatusBody{Enabled: st.Enabled(), Stable: stable, Reason: reason}
	if body.Enabled {
		body.PublicKey, _ = st.Keys()
		if ep := r.Header.Get("X-WebPush-Endpoint"); ep != "" {
			for _, sub := range st.Subscriptions() {
				if sub.Endpoint != ep {
					continue
				}
				body.Subscribed = true
				input := sub.Input
				body.Input = &input
				body.Done = sub.Done
				break
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func (s *Server) pushStable(r *http.Request) (bool, string) {
	return webpush.OriginStable(pushScheme(r), pushHost(r))
}

func pushScheme(r *http.Request) string {
	if o := r.Header.Get("Origin"); o != "" {
		if u, err := url.Parse(o); err == nil && u.Scheme != "" {
			return u.Scheme
		}
	}
	if r.TLS != nil {
		return "https"
	}
	if xf := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))); xf == "https" || xf == "http" {
		return xf
	}
	return "http"
}

func pushHost(r *http.Request) string {
	if o := r.Header.Get("Origin"); o != "" {
		if u, err := url.Parse(o); err == nil && u.Host != "" {
			return u.Host
		}
	}
	return r.Host
}

func allowedPushEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return fmt.Errorf("bad endpoint")
	}
	host := strings.Trim(u.Hostname(), "[]")
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return nil
		}
	}
	return fmt.Errorf("bad endpoint")
}

func decodePushKey(s string, n int) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != n {
		return nil, fmt.Errorf("bad key")
	}
	return b, nil
}
