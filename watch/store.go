package watch

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var serviceIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]{2,31}$`)

const (
	StatusUnknown = "unknown"
	StatusOK      = "ok"
	StatusWarn    = "warn"
	StatusFail    = "fail"
	StatusMissed  = "missed"

	IncidentOpen     = "open"
	IncidentResolved = "resolved"
)

var ErrNotFound = errors.New("not found")

type ValidationError struct {
	Code    string `json:"code"`
	Message string `json:"error"`
}

func (e ValidationError) Error() string { return e.Message }

type ConflictError struct {
	Code    string `json:"code"`
	Message string `json:"error"`
}

func (e ConflictError) Error() string { return e.Message }

type Service struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Owner           string     `json:"owner"`
	Tags            []string   `json:"tags"`
	IntervalSeconds int        `json:"interval_seconds"`
	TimeoutSeconds  int        `json:"timeout_seconds"`
	Status          string     `json:"status"`
	Enabled         bool       `json:"enabled"`
	LastSeenAt      *time.Time `json:"last_seen_at,omitempty"`
	LastMessage     string     `json:"last_message,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type ServiceInput struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Owner           string   `json:"owner"`
	Tags            []string `json:"tags"`
	IntervalSeconds int      `json:"interval_seconds"`
	TimeoutSeconds  int      `json:"timeout_seconds"`
	Enabled         *bool    `json:"enabled,omitempty"`
}

type ServicePatch struct {
	Name            *string  `json:"name,omitempty"`
	Owner           *string  `json:"owner,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	IntervalSeconds *int     `json:"interval_seconds,omitempty"`
	TimeoutSeconds  *int     `json:"timeout_seconds,omitempty"`
	Enabled         *bool    `json:"enabled,omitempty"`
}

type HeartbeatInput struct {
	Status    string         `json:"status"`
	Message   string         `json:"message"`
	LatencyMS int            `json:"latency_ms"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type Heartbeat struct {
	ID         string         `json:"id"`
	ServiceID  string         `json:"service_id"`
	Status     string         `json:"status"`
	Message    string         `json:"message"`
	LatencyMS  int            `json:"latency_ms"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	ReceivedAt time.Time      `json:"received_at"`
}

type Incident struct {
	ID         string     `json:"id"`
	ServiceID  string     `json:"service_id"`
	Status     string     `json:"status"`
	Reason     string     `json:"reason"`
	OpenedAt   time.Time  `json:"opened_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

type Dashboard struct {
	Services  map[string]int `json:"services"`
	Incidents map[string]int `json:"incidents"`
	Overdue   []string       `json:"overdue"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type Store struct {
	mu         sync.RWMutex
	services   map[string]Service
	heartbeats map[string][]Heartbeat
	incidents  map[string]Incident
	nowFn      func() time.Time
	nextHB     int
	nextInc    int
}

func NewStore() *Store {
	return &Store{services: map[string]Service{}, heartbeats: map[string][]Heartbeat{}, incidents: map[string]Incident{}, nowFn: func() time.Time { return time.Now().UTC() }}
}

func (s *Store) SetNowFn(f func() time.Time) { s.mu.Lock(); defer s.mu.Unlock(); s.nowFn = f }
func (s *Store) now() time.Time              { return s.nowFn().UTC() }

func (s *Store) CreateService(in ServiceInput) (Service, error) {
	id := strings.TrimSpace(in.ID)
	if !serviceIDRe.MatchString(id) {
		return Service{}, ValidationError{"invalid_service_id", "service id must be 3-32 chars: lowercase letters, digits or -"}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Service{}, ValidationError{"invalid_name", "name is required"}
	}
	owner := strings.TrimSpace(in.Owner)
	if owner == "" {
		owner = "unowned"
	}
	interval := in.IntervalSeconds
	if interval == 0 {
		interval = 60
	}
	if interval < 5 || interval > 86400 {
		return Service{}, ValidationError{"invalid_interval", "interval_seconds must be 5..86400"}
	}
	timeout := in.TimeoutSeconds
	if timeout == 0 {
		timeout = interval * 2
	}
	if timeout < 1 || timeout > 86400 {
		return Service{}, ValidationError{"invalid_timeout", "timeout_seconds must be 1..86400"}
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	tags, err := normalizeTags(in.Tags)
	if err != nil {
		return Service{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.services[id]; ok {
		return Service{}, ConflictError{"duplicate_service", fmt.Sprintf("service %s already exists", id)}
	}
	now := s.now()
	svc := Service{ID: id, Name: name, Owner: owner, Tags: tags, IntervalSeconds: interval, TimeoutSeconds: timeout, Status: StatusUnknown, Enabled: enabled, CreatedAt: now, UpdatedAt: now}
	s.services[id] = svc
	return svc, nil
}

func (s *Store) GetService(id string) (Service, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	svc, ok := s.services[id]
	if !ok {
		return Service{}, ErrNotFound
	}
	return svc, nil
}

func (s *Store) ListServices(status, tag string) ([]Service, error) {
	if status != "" && !validServiceStatus(status) {
		return nil, ValidationError{"invalid_status", "invalid service status"}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Service{}
	for _, svc := range s.services {
		if status != "" && svc.Status != status {
			continue
		}
		if tag != "" && !contains(svc.Tags, tag) {
			continue
		}
		out = append(out, svc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Store) PatchService(id string, p ServicePatch) (Service, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[id]
	if !ok {
		return Service{}, ErrNotFound
	}
	if p.Name != nil {
		v := strings.TrimSpace(*p.Name)
		if v == "" {
			return Service{}, ValidationError{"invalid_name", "name is required"}
		}
		svc.Name = v
	}
	if p.Owner != nil {
		svc.Owner = strings.TrimSpace(*p.Owner)
		if svc.Owner == "" {
			svc.Owner = "unowned"
		}
	}
	if p.Tags != nil {
		tags, err := normalizeTags(p.Tags)
		if err != nil {
			return Service{}, err
		}
		svc.Tags = tags
	}
	if p.IntervalSeconds != nil {
		if *p.IntervalSeconds < 5 || *p.IntervalSeconds > 86400 {
			return Service{}, ValidationError{"invalid_interval", "interval_seconds must be 5..86400"}
		}
		svc.IntervalSeconds = *p.IntervalSeconds
	}
	if p.TimeoutSeconds != nil {
		if *p.TimeoutSeconds < 1 || *p.TimeoutSeconds > 86400 {
			return Service{}, ValidationError{"invalid_timeout", "timeout_seconds must be 1..86400"}
		}
		svc.TimeoutSeconds = *p.TimeoutSeconds
	}
	if p.Enabled != nil {
		svc.Enabled = *p.Enabled
	}
	svc.UpdatedAt = s.now()
	s.services[id] = svc
	return svc, nil
}

func (s *Store) DeleteService(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.services[id]; !ok {
		return ErrNotFound
	}
	delete(s.services, id)
	delete(s.heartbeats, id)
	return nil
}

func (s *Store) RecordHeartbeat(id string, in HeartbeatInput) (Heartbeat, Service, *Incident, error) {
	status := in.Status
	if status == "" {
		status = StatusOK
	}
	if status != StatusOK && status != StatusWarn && status != StatusFail {
		return Heartbeat{}, Service{}, nil, ValidationError{"invalid_heartbeat_status", "heartbeat status must be ok, warn or fail"}
	}
	if in.LatencyMS < 0 {
		return Heartbeat{}, Service{}, nil, ValidationError{"invalid_latency", "latency_ms must be >= 0"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[id]
	if !ok {
		return Heartbeat{}, Service{}, nil, ErrNotFound
	}
	if !svc.Enabled {
		return Heartbeat{}, Service{}, nil, ConflictError{"service_disabled", fmt.Sprintf("service %s disabled", id)}
	}
	s.nextHB++
	now := s.now()
	hb := Heartbeat{ID: fmt.Sprintf("hb_%06d", s.nextHB), ServiceID: id, Status: status, Message: strings.TrimSpace(in.Message), LatencyMS: in.LatencyMS, Metadata: in.Metadata, ReceivedAt: now}
	s.heartbeats[id] = append(s.heartbeats[id], hb)
	svc.Status = status
	svc.LastSeenAt = &now
	svc.LastMessage = hb.Message
	svc.UpdatedAt = now
	s.services[id] = svc
	var inc *Incident
	if status == StatusFail {
		created := s.openIncidentLocked(id, "heartbeat_failed", now)
		inc = &created
	}
	return hb, svc, inc, nil
}

func (s *Store) ListHeartbeats(id string, limit int) ([]Heartbeat, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.services[id]; !ok {
		return nil, ErrNotFound
	}
	items := append([]Heartbeat(nil), s.heartbeats[id]...)
	sort.Slice(items, func(i, j int) bool { return items[i].ReceivedAt.After(items[j].ReceivedAt) })
	if limit > 0 && limit < len(items) {
		items = items[:limit]
	}
	return items, nil
}

func (s *Store) EvaluateOverdue() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	overdue := []string{}
	for id, svc := range s.services {
		if !svc.Enabled || svc.LastSeenAt == nil {
			continue
		}
		deadline := svc.LastSeenAt.Add(time.Duration(svc.IntervalSeconds+svc.TimeoutSeconds) * time.Second)
		if !now.Before(deadline) {
			svc.Status = StatusMissed
			svc.UpdatedAt = now
			s.services[id] = svc
			overdue = append(overdue, id)
			s.openIncidentLocked(id, "heartbeat_missed", now)
		}
	}
	sort.Strings(overdue)
	return overdue
}

func (s *Store) Dashboard() Dashboard {
	overdue := s.EvaluateOverdue()
	s.mu.RLock()
	defer s.mu.RUnlock()
	d := Dashboard{Services: map[string]int{"total": 0, StatusUnknown: 0, StatusOK: 0, StatusWarn: 0, StatusFail: 0, StatusMissed: 0}, Incidents: map[string]int{"open": 0, "resolved": 0}, Overdue: overdue, UpdatedAt: s.now()}
	for _, svc := range s.services {
		d.Services["total"]++
		d.Services[svc.Status]++
	}
	for _, inc := range s.incidents {
		d.Incidents[inc.Status]++
	}
	return d
}

func (s *Store) ListIncidents(status string) ([]Incident, error) {
	if status != "" && status != IncidentOpen && status != IncidentResolved {
		return nil, ValidationError{"invalid_incident_status", "invalid incident status"}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Incident{}
	for _, inc := range s.incidents {
		if status == "" || inc.Status == status {
			out = append(out, inc)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].OpenedAt.After(out[j].OpenedAt) || (out[i].OpenedAt.Equal(out[j].OpenedAt) && out[i].ID < out[j].ID)
	})
	return out, nil
}

func (s *Store) ResolveIncident(id string) (Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inc, ok := s.incidents[id]
	if !ok {
		return Incident{}, ErrNotFound
	}
	if inc.Status == IncidentResolved {
		return inc, nil
	}
	now := s.now()
	inc.Status = IncidentResolved
	inc.ResolvedAt = &now
	s.incidents[id] = inc
	return inc, nil
}

func (s *Store) openIncidentLocked(serviceID, reason string, now time.Time) Incident {
	for _, inc := range s.incidents {
		if inc.ServiceID == serviceID && inc.Status == IncidentOpen && inc.Reason == reason {
			return inc
		}
	}
	s.nextInc++
	inc := Incident{ID: fmt.Sprintf("inc_%06d", s.nextInc), ServiceID: serviceID, Status: IncidentOpen, Reason: reason, OpenedAt: now}
	s.incidents[inc.ID] = inc
	return inc
}

func normalizeTags(in []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range in {
		tag := strings.TrimSpace(strings.ToLower(raw))
		if tag == "" {
			continue
		}
		if !regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`).MatchString(tag) {
			return nil, ValidationError{"invalid_tag", "invalid tag"}
		}
		if !seen[tag] {
			seen[tag] = true
			out = append(out, tag)
		}
	}
	return out, nil
}

func validServiceStatus(status string) bool {
	return status == StatusUnknown || status == StatusOK || status == StatusWarn || status == StatusFail || status == StatusMissed
}
func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func DefaultStore() *Store {
	s := NewStore()
	s.CreateService(ServiceInput{ID: "api-gateway", Name: "API Gateway", Owner: "platform@example.test", Tags: []string{"edge", "critical"}, IntervalSeconds: 30, TimeoutSeconds: 90})
	s.CreateService(ServiceInput{ID: "billing-worker", Name: "Billing Worker", Owner: "billing@example.test", Tags: []string{"billing"}, IntervalSeconds: 60, TimeoutSeconds: 180})
	s.RecordHeartbeat("api-gateway", HeartbeatInput{Status: StatusOK, Message: "edge healthy", LatencyMS: 12})
	s.RecordHeartbeat("billing-worker", HeartbeatInput{Status: StatusWarn, Message: "queue lag high", LatencyMS: 88})
	return s
}
