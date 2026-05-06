// Package handler exposes the HTTP endpoints (/check, /healthz).
package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/kawaz/port-peeker/internal/cache"
	"github.com/kawaz/port-peeker/internal/checker"
)

// Inspector is the subset of *checker.Checker the handler needs.
type Inspector interface {
	Inspect(port int, wantProcesses bool) (checker.Status, error)
}

type Result struct {
	Status int
	Body   string
}

type Check struct {
	Insp  Inspector
	Cache *cache.Cache[Result]
}

func (h *Check) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	portStr := q.Get("port")
	if portStr == "" {
		writeResult(w, Result{Status: http.StatusBadRequest, Body: "missing port parameter"})
		return
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		writeResult(w, Result{Status: http.StatusBadRequest, Body: fmt.Sprintf("invalid port: %s", portStr)})
		return
	}
	processName := q.Get("process")

	key := portStr + "|" + processName
	if h.Cache != nil {
		if res, ok := h.Cache.Get(key); ok {
			writeResult(w, res)
			return
		}
	}
	res := h.evaluate(port, processName)
	if h.Cache != nil {
		h.Cache.Set(key, res)
	}
	writeResult(w, res)
}

func (h *Check) evaluate(port int, processName string) Result {
	s, err := h.Insp.Inspect(port, processName != "")
	if err != nil {
		return Result{Status: http.StatusServiceUnavailable, Body: fmt.Sprintf("check error: %v", err)}
	}
	if !s.Listening {
		return Result{Status: http.StatusServiceUnavailable, Body: fmt.Sprintf("port %d not listening", port)}
	}
	if processName == "" {
		return Result{Status: http.StatusOK, Body: "OK"}
	}
	for _, n := range s.Processes {
		if n == processName {
			return Result{Status: http.StatusOK, Body: "OK"}
		}
	}
	got := "(none)"
	if len(s.Processes) > 0 {
		got = strings.Join(s.Processes, ",")
	}
	return Result{Status: http.StatusServiceUnavailable, Body: fmt.Sprintf("process mismatch (expected %s, got %s)", processName, got)}
}

func writeResult(w http.ResponseWriter, r Result) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(r.Status)
	fmt.Fprintln(w, r.Body)
}

func Healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "OK")
}
