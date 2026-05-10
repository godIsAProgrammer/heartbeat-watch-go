package watch

import (
	"errors"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)

func newTestStore() *Store {
	s := NewStore()
	s.SetNowFn(func() time.Time { return fixedNow })
	return s
}
func boolPtr(v bool) *bool    { return &v }
func intPtr(v int) *int       { return &v }
func strPtr(v string) *string { return &v }

func seedService(t *testing.T) *Store {
	t.Helper()
	s := newTestStore()
	_, err := s.CreateService(ServiceInput{ID: "api-test", Name: "API Test", Owner: "qa", Tags: []string{"edge", "edge"}, IntervalSeconds: 30, TimeoutSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCreateServiceDefaults(t *testing.T) {
	s := newTestStore()
	svc, err := s.CreateService(ServiceInput{ID: "api-test", Name: " API Test "})
	if err != nil {
		t.Fatal(err)
	}
	if svc.Status != StatusUnknown || !svc.Enabled || svc.IntervalSeconds != 60 || svc.TimeoutSeconds != 120 {
		t.Fatalf("bad defaults: %+v", svc)
	}
	if svc.Owner != "unowned" {
		t.Fatalf("owner=%s", svc.Owner)
	}
}

func TestCreateServiceRejectsBadID(t *testing.T) {
	s := newTestStore()
	_, err := s.CreateService(ServiceInput{ID: "Bad", Name: "Bad"})
	var ve ValidationError
	if !errors.As(err, &ve) || ve.Code != "invalid_service_id" {
		t.Fatalf("err=%v", err)
	}
}

func TestCreateServiceRejectsDuplicate(t *testing.T) {
	s := seedService(t)
	_, err := s.CreateService(ServiceInput{ID: "api-test", Name: "Again"})
	var ce ConflictError
	if !errors.As(err, &ce) || ce.Code != "duplicate_service" {
		t.Fatalf("err=%v", err)
	}
}

func TestListServicesFiltersStatusAndTag(t *testing.T) {
	s := seedService(t)
	_, _, _, err := s.RecordHeartbeat("api-test", HeartbeatInput{Status: StatusOK, Message: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	byStatus, _ := s.ListServices(StatusOK, "")
	if len(byStatus) != 1 || byStatus[0].ID != "api-test" {
		t.Fatalf("byStatus=%+v", byStatus)
	}
	byTag, _ := s.ListServices("", "edge")
	if len(byTag) != 1 {
		t.Fatalf("byTag=%+v", byTag)
	}
}

func TestPatchService(t *testing.T) {
	s := seedService(t)
	svc, err := s.PatchService("api-test", ServicePatch{Name: strPtr("Renamed"), Enabled: boolPtr(false), IntervalSeconds: intPtr(45)})
	if err != nil {
		t.Fatal(err)
	}
	if svc.Name != "Renamed" || svc.Enabled || svc.IntervalSeconds != 45 {
		t.Fatalf("svc=%+v", svc)
	}
}

func TestDeleteServiceRemovesHeartbeats(t *testing.T) {
	s := seedService(t)
	_, _, _, err := s.RecordHeartbeat("api-test", HeartbeatInput{Status: StatusOK})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteService("api-test"); err != nil {
		t.Fatal(err)
	}
	_, err = s.ListHeartbeats("api-test", 0)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestRecordHeartbeatUpdatesService(t *testing.T) {
	s := seedService(t)
	hb, svc, inc, err := s.RecordHeartbeat("api-test", HeartbeatInput{Status: StatusOK, Message: "green", LatencyMS: 12})
	if err != nil {
		t.Fatal(err)
	}
	if hb.ID != "hb_000001" || svc.Status != StatusOK || svc.LastSeenAt == nil || inc != nil {
		t.Fatalf("hb=%+v svc=%+v inc=%+v", hb, svc, inc)
	}
}

func TestRecordHeartbeatFailOpensIncident(t *testing.T) {
	s := seedService(t)
	_, svc, inc, err := s.RecordHeartbeat("api-test", HeartbeatInput{Status: StatusFail, Message: "down"})
	if err != nil {
		t.Fatal(err)
	}
	if svc.Status != StatusFail || inc == nil || inc.Reason != "heartbeat_failed" {
		t.Fatalf("svc=%+v inc=%+v", svc, inc)
	}
	incidents, _ := s.ListIncidents(IncidentOpen)
	if len(incidents) != 1 {
		t.Fatalf("incidents=%+v", incidents)
	}
}

func TestRecordHeartbeatRejectsDisabled(t *testing.T) {
	s := seedService(t)
	_, err := s.PatchService("api-test", ServicePatch{Enabled: boolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = s.RecordHeartbeat("api-test", HeartbeatInput{Status: StatusOK})
	var ce ConflictError
	if !errors.As(err, &ce) || ce.Code != "service_disabled" {
		t.Fatalf("err=%v", err)
	}
}

func TestListHeartbeatsLimitNewestFirst(t *testing.T) {
	s := seedService(t)
	s.RecordHeartbeat("api-test", HeartbeatInput{Status: StatusOK, Message: "a"})
	s.SetNowFn(func() time.Time { return fixedNow.Add(time.Minute) })
	s.RecordHeartbeat("api-test", HeartbeatInput{Status: StatusWarn, Message: "b"})
	items, err := s.ListHeartbeats("api-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Message != "b" {
		t.Fatalf("items=%+v", items)
	}
}

func TestEvaluateOverdueMarksMissed(t *testing.T) {
	s := seedService(t)
	s.RecordHeartbeat("api-test", HeartbeatInput{Status: StatusOK})
	s.SetNowFn(func() time.Time { return fixedNow.Add(91 * time.Second) })
	overdue := s.EvaluateOverdue()
	if len(overdue) != 1 || overdue[0] != "api-test" {
		t.Fatalf("overdue=%+v", overdue)
	}
	svc, _ := s.GetService("api-test")
	if svc.Status != StatusMissed {
		t.Fatalf("status=%s", svc.Status)
	}
}

func TestEvaluateOverdueDoesNotDuplicateIncident(t *testing.T) {
	s := seedService(t)
	s.RecordHeartbeat("api-test", HeartbeatInput{Status: StatusOK})
	s.SetNowFn(func() time.Time { return fixedNow.Add(91 * time.Second) })
	s.EvaluateOverdue()
	s.EvaluateOverdue()
	incidents, _ := s.ListIncidents(IncidentOpen)
	if len(incidents) != 1 {
		t.Fatalf("incidents=%+v", incidents)
	}
}

func TestResolveIncidentIdempotent(t *testing.T) {
	s := seedService(t)
	_, _, inc, _ := s.RecordHeartbeat("api-test", HeartbeatInput{Status: StatusFail})
	resolved, err := s.ResolveIncident(inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.ResolveIncident(inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != IncidentResolved || again.Status != IncidentResolved {
		t.Fatalf("resolved=%+v again=%+v", resolved, again)
	}
}

func TestDashboardCounts(t *testing.T) {
	s := seedService(t)
	s.RecordHeartbeat("api-test", HeartbeatInput{Status: StatusFail})
	d := s.Dashboard()
	if d.Services["total"] != 1 || d.Services[StatusFail] != 1 || d.Incidents[IncidentOpen] != 1 {
		t.Fatalf("dashboard=%+v", d)
	}
}

func TestDefaultStore(t *testing.T) {
	s := DefaultStore()
	services, err := s.ListServices("", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 2 {
		t.Fatalf("services=%+v", services)
	}
	hb, err := s.ListHeartbeats("api-gateway", 0)
	if err != nil || len(hb) != 1 {
		t.Fatalf("hb=%+v err=%v", hb, err)
	}
}
