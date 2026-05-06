package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kawaz/port-peeker/internal/checker"
)

type fakeInspector struct {
	byPort map[int]checker.Status
	err    error
}

func (f *fakeInspector) Inspect(port int, _ bool) (checker.Status, error) {
	if f.err != nil {
		return checker.Status{}, f.err
	}
	return f.byPort[port], nil
}

func do(h http.Handler, target string) (int, string) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	h.ServeHTTP(rec, req)
	return rec.Code, strings.TrimSpace(rec.Body.String())
}

func TestCheck_MissingPort(t *testing.T) {
	h := &Check{Insp: &fakeInspector{}}
	code, body := do(h, "/check")
	if code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%q want 400", code, body)
	}
}

func TestCheck_InvalidPort(t *testing.T) {
	h := &Check{Insp: &fakeInspector{}}
	for _, v := range []string{"abc", "0", "65536", "-1"} {
		code, body := do(h, "/check?port="+v)
		if code != http.StatusBadRequest {
			t.Fatalf("port=%s status=%d body=%q want 400", v, code, body)
		}
	}
}

func TestCheck_ListeningNoProcess(t *testing.T) {
	h := &Check{Insp: &fakeInspector{byPort: map[int]checker.Status{22: {Listening: true}}}}
	code, body := do(h, "/check?port=22")
	if code != http.StatusOK || body != "OK" {
		t.Fatalf("status=%d body=%q", code, body)
	}
}

func TestCheck_NotListening(t *testing.T) {
	h := &Check{Insp: &fakeInspector{byPort: map[int]checker.Status{}}}
	code, body := do(h, "/check?port=22")
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "not listening") {
		t.Fatalf("status=%d body=%q", code, body)
	}
}

func TestCheck_ProcessMatch(t *testing.T) {
	h := &Check{Insp: &fakeInspector{byPort: map[int]checker.Status{
		993: {Listening: true, Processes: []string{"dovecot"}},
	}}}
	code, body := do(h, "/check?port=993&process=dovecot")
	if code != http.StatusOK || body != "OK" {
		t.Fatalf("status=%d body=%q", code, body)
	}
}

func TestCheck_ProcessMismatch(t *testing.T) {
	h := &Check{Insp: &fakeInspector{byPort: map[int]checker.Status{
		22: {Listening: true, Processes: []string{"sshd"}},
	}}}
	code, body := do(h, "/check?port=22&process=dovecot")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", code)
	}
	if !strings.Contains(body, "process mismatch") || !strings.Contains(body, "expected dovecot") || !strings.Contains(body, "got sshd") {
		t.Fatalf("body=%q", body)
	}
}

func TestCheck_ListeningButNoResolvableProcess(t *testing.T) {
	// 他ユーザのソケットでプロセス名が取れないケース。
	h := &Check{Insp: &fakeInspector{byPort: map[int]checker.Status{
		25: {Listening: true, Processes: nil},
	}}}
	code, body := do(h, "/check?port=25&process=master")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", code)
	}
	if !strings.Contains(body, "got (none)") {
		t.Fatalf("body=%q", body)
	}
}

func TestCheck_InspectorError(t *testing.T) {
	h := &Check{Insp: &fakeInspector{err: errors.New("boom")}}
	code, body := do(h, "/check?port=22")
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "check error") {
		t.Fatalf("status=%d body=%q", code, body)
	}
}

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	http.HandlerFunc(Healthz).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "OK" {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
}
