package watch

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func decode(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	return m
}
func req(method, path string, body string) *http.Request {
	if body == "" {
		return httptest.NewRequest(method, path, nil)
	}
	return httptest.NewRequest(method, path, bytes.NewBufferString(body))
}

func TestServerHealth(t *testing.T) {
	rr := httptest.NewRecorder()
	NewServer(NewStore()).Handler().ServeHTTP(rr, req(http.MethodGet, "/health", ""))
	if rr.Code != 200 || decode(t, rr)["ok"] != true {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestServerListDefaultServices(t *testing.T) {
	rr := httptest.NewRecorder()
	NewServer(DefaultStore()).Handler().ServeHTTP(rr, req(http.MethodGet, "/services", ""))
	body := decode(t, rr)
	if rr.Code != 200 || len(body["services"].([]any)) != 2 {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestServerCreateGetPatchDeleteService(t *testing.T) {
	server := NewServer(NewStore()).Handler()
	create := httptest.NewRecorder()
	server.ServeHTTP(create, req(http.MethodPost, "/services", `{"id":"api-test","name":"API Test","owner":"qa","tags":["edge"],"interval_seconds":30}`))
	if create.Code != 201 {
		t.Fatalf("create=%d %s", create.Code, create.Body.String())
	}
	get := httptest.NewRecorder()
	server.ServeHTTP(get, req(http.MethodGet, "/services/api-test", ""))
	if get.Code != 200 {
		t.Fatalf("get=%d", get.Code)
	}
	patch := httptest.NewRecorder()
	server.ServeHTTP(patch, req(http.MethodPatch, "/services/api-test", `{"enabled":false}`))
	if patch.Code != 200 || decode(t, patch)["service"].(map[string]any)["enabled"] != false {
		t.Fatalf("patch=%d %s", patch.Code, patch.Body.String())
	}
	del := httptest.NewRecorder()
	server.ServeHTTP(del, req(http.MethodDelete, "/services/api-test", ""))
	if del.Code != 204 {
		t.Fatalf("delete=%d", del.Code)
	}
}

func TestServerCreateRejectsDuplicate(t *testing.T) {
	server := NewServer(NewStore()).Handler()
	server.ServeHTTP(httptest.NewRecorder(), req(http.MethodPost, "/services", `{"id":"api-test","name":"API Test"}`))
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, req(http.MethodPost, "/services", `{"id":"api-test","name":"Again"}`))
	if rr.Code != 409 || decode(t, rr)["code"] != "duplicate_service" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestServerHeartbeatOKAndList(t *testing.T) {
	store := newTestStore()
	store.CreateService(ServiceInput{ID: "api-test", Name: "API Test", IntervalSeconds: 30, TimeoutSeconds: 60})
	server := NewServer(store).Handler()
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, req(http.MethodPost, "/services/api-test/heartbeat", `{"status":"ok","message":"green","latency_ms":10}`))
	if rr.Code != 201 || decode(t, rr)["heartbeat"].(map[string]any)["status"] != StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	list := httptest.NewRecorder()
	server.ServeHTTP(list, req(http.MethodGet, "/services/api-test/heartbeats?limit=1", ""))
	if list.Code != 200 || len(decode(t, list)["heartbeats"].([]any)) != 1 {
		t.Fatalf("list=%d %s", list.Code, list.Body.String())
	}
}

func TestServerHeartbeatFailReturnsIncident(t *testing.T) {
	store := newTestStore()
	store.CreateService(ServiceInput{ID: "api-test", Name: "API Test"})
	server := NewServer(store).Handler()
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, req(http.MethodPost, "/services/api-test/heartbeat", `{"status":"fail","message":"down"}`))
	body := decode(t, rr)
	if rr.Code != 201 || body["incident"] == nil {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestServerInvalidHeartbeatStatus(t *testing.T) {
	store := newTestStore()
	store.CreateService(ServiceInput{ID: "api-test", Name: "API Test"})
	rr := httptest.NewRecorder()
	NewServer(store).Handler().ServeHTTP(rr, req(http.MethodPost, "/services/api-test/heartbeat", `{"status":"bad"}`))
	if rr.Code != 400 || decode(t, rr)["code"] != "invalid_heartbeat_status" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestServerDashboardMarksOverdue(t *testing.T) {
	store := newTestStore()
	store.CreateService(ServiceInput{ID: "api-test", Name: "API Test", IntervalSeconds: 30, TimeoutSeconds: 60})
	store.RecordHeartbeat("api-test", HeartbeatInput{Status: StatusOK})
	store.SetNowFn(func() time.Time { return fixedNow.Add(91 * time.Second) })
	rr := httptest.NewRecorder()
	NewServer(store).Handler().ServeHTTP(rr, req(http.MethodGet, "/dashboard", ""))
	body := decode(t, rr)["dashboard"].(map[string]any)
	if rr.Code != 200 || len(body["overdue"].([]any)) != 1 {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestServerListAndResolveIncidents(t *testing.T) {
	store := newTestStore()
	store.CreateService(ServiceInput{ID: "api-test", Name: "API Test"})
	_, _, inc, _ := store.RecordHeartbeat("api-test", HeartbeatInput{Status: StatusFail})
	server := NewServer(store).Handler()
	list := httptest.NewRecorder()
	server.ServeHTTP(list, req(http.MethodGet, "/incidents?status=open", ""))
	if list.Code != 200 || len(decode(t, list)["incidents"].([]any)) != 1 {
		t.Fatalf("list=%d %s", list.Code, list.Body.String())
	}
	resolve := httptest.NewRecorder()
	server.ServeHTTP(resolve, req(http.MethodPost, "/incidents/"+inc.ID+"/resolve", "{}"))
	if resolve.Code != 200 || decode(t, resolve)["incident"].(map[string]any)["status"] != IncidentResolved {
		t.Fatalf("resolve=%d %s", resolve.Code, resolve.Body.String())
	}
}

func TestServerBadServiceStatus(t *testing.T) {
	rr := httptest.NewRecorder()
	NewServer(NewStore()).Handler().ServeHTTP(rr, req(http.MethodGet, "/services?status=dead", ""))
	if rr.Code != 400 || decode(t, rr)["code"] != "invalid_status" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestServerInvalidJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	NewServer(NewStore()).Handler().ServeHTTP(rr, req(http.MethodPost, "/services", `{`))
	if rr.Code != 400 || decode(t, rr)["code"] != "invalid_json" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestServerUnknownPath(t *testing.T) {
	rr := httptest.NewRecorder()
	NewServer(NewStore()).Handler().ServeHTTP(rr, req(http.MethodGet, "/nope", ""))
	if rr.Code != 404 || decode(t, rr)["code"] != "route_not_found" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}
