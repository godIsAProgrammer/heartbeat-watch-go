package watch

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

type Server struct{ Store *Store }

func NewServer(store *Store) *Server {
	if store == nil {
		store = DefaultStore()
	}
	return &Server{Store: store}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/services", s.handleServices)
	mux.HandleFunc("/services/", s.handleServiceItem)
	mux.HandleFunc("/incidents", s.handleIncidents)
	mux.HandleFunc("/incidents/", s.handleIncidentItem)
	mux.HandleFunc("/dashboard", s.handleDashboard)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		sendJSON(w, 404, map[string]any{"error": "not found", "code": "route_not_found"})
	})
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendMethod(w)
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.Store.ListServices(r.URL.Query().Get("status"), r.URL.Query().Get("tag"))
		if err != nil {
			s.handleError(w, err)
			return
		}
		sendJSON(w, 200, map[string]any{"services": items})
	case http.MethodPost:
		var in ServiceInput
		if !decodeJSON(w, r, &in) {
			return
		}
		svc, err := s.Store.CreateService(in)
		if err != nil {
			s.handleError(w, err)
			return
		}
		sendJSON(w, 201, map[string]any{"service": svc})
	default:
		sendMethod(w)
	}
}

func (s *Server) handleServiceItem(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/services/"), "/")
	if len(parts) == 1 && parts[0] != "" {
		id := parts[0]
		switch r.Method {
		case http.MethodGet:
			svc, err := s.Store.GetService(id)
			if err != nil {
				s.handleError(w, err)
				return
			}
			sendJSON(w, 200, map[string]any{"service": svc})
		case http.MethodPatch:
			var p ServicePatch
			if !decodeJSON(w, r, &p) {
				return
			}
			svc, err := s.Store.PatchService(id, p)
			if err != nil {
				s.handleError(w, err)
				return
			}
			sendJSON(w, 200, map[string]any{"service": svc})
		case http.MethodDelete:
			if err := s.Store.DeleteService(id); err != nil {
				s.handleError(w, err)
				return
			}
			w.WriteHeader(204)
		default:
			sendMethod(w)
		}
		return
	}
	if len(parts) == 2 && parts[1] == "heartbeat" {
		if r.Method != http.MethodPost {
			sendMethod(w)
			return
		}
		var in HeartbeatInput
		if !decodeJSON(w, r, &in) {
			return
		}
		hb, svc, inc, err := s.Store.RecordHeartbeat(parts[0], in)
		if err != nil {
			s.handleError(w, err)
			return
		}
		body := map[string]any{"heartbeat": hb, "service": svc}
		if inc != nil {
			body["incident"] = inc
		}
		sendJSON(w, 201, body)
		return
	}
	if len(parts) == 2 && parts[1] == "heartbeats" {
		if r.Method != http.MethodGet {
			sendMethod(w)
			return
		}
		limit := 0
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 1000 {
				sendJSON(w, 400, map[string]any{"error": "invalid limit", "code": "invalid_limit"})
				return
			}
			limit = n
		}
		items, err := s.Store.ListHeartbeats(parts[0], limit)
		if err != nil {
			s.handleError(w, err)
			return
		}
		sendJSON(w, 200, map[string]any{"heartbeats": items})
		return
	}
	sendJSON(w, 404, map[string]any{"error": "not found", "code": "route_not_found"})
}

func (s *Server) handleIncidents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendMethod(w)
		return
	}
	items, err := s.Store.ListIncidents(r.URL.Query().Get("status"))
	if err != nil {
		s.handleError(w, err)
		return
	}
	sendJSON(w, 200, map[string]any{"incidents": items})
}

func (s *Server) handleIncidentItem(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/incidents/"), "/")
	if len(parts) == 2 && parts[1] == "resolve" {
		if r.Method != http.MethodPost {
			sendMethod(w)
			return
		}
		inc, err := s.Store.ResolveIncident(parts[0])
		if err != nil {
			s.handleError(w, err)
			return
		}
		sendJSON(w, 200, map[string]any{"incident": inc})
		return
	}
	sendJSON(w, 404, map[string]any{"error": "not found", "code": "route_not_found"})
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendMethod(w)
		return
	}
	sendJSON(w, 200, map[string]any{"dashboard": s.Store.Dashboard()})
}

func (s *Server) handleError(w http.ResponseWriter, err error) {
	var ve ValidationError
	if errors.As(err, &ve) {
		sendJSON(w, 400, ve)
		return
	}
	var ce ConflictError
	if errors.As(err, &ce) {
		sendJSON(w, 409, ce)
		return
	}
	if errors.Is(err, ErrNotFound) {
		sendJSON(w, 404, map[string]any{"error": "not found", "code": "not_found"})
		return
	}
	sendJSON(w, 500, map[string]any{"error": "internal server error", "code": "internal_error"})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 200000))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		sendJSON(w, 400, map[string]any{"error": "invalid json", "code": "invalid_json"})
		return false
	}
	return true
}

func sendMethod(w http.ResponseWriter) {
	sendJSON(w, 405, map[string]any{"error": "method not allowed", "code": "method_not_allowed"})
}
func sendJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}
