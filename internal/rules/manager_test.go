package rules

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestLastKnownGoodSurvivesBadUpdate(t *testing.T) {
	good := true
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if good {
			w.Write([]byte{'S', 'R', 'S', 2, 1})
		} else {
			w.Write([]byte("broken"))
		}
	}))
	defer ts.Close()
	m := New(filepath.Join(t.TempDir(), "rules"), []Source{{Tag: "x", URL: ts.URL, License: "GPL-3.0-or-later", Version: "fixture", MaxBytes: 32}})
	s := m.Update(context.Background())
	if len(s) != 1 || s[0].SHA256 == "" || s[0].LastError != "" {
		t.Fatalf("first=%+v", s)
	}
	first := s[0].SHA256
	good = false
	s = m.Update(context.Background())
	if s[0].SHA256 != first || s[0].LastError == "" {
		t.Fatalf("bad update replaced LKG: %+v", s)
	}
	b, _, err := m.Read("x")
	if err != nil || string(b[:3]) != "SRS" {
		t.Fatalf("read LKG: %v %q", err, b)
	}
}

func TestRetryFailedWithClientDoesNotRedownloadSuccessfulRules(t *testing.T) {
	var goodCalls, flakyCalls atomic.Int32
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		goodCalls.Add(1)
		_, _ = w.Write([]byte{'S', 'R', 'S', 2, 1})
	}))
	defer good.Close()
	recovered := false
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flakyCalls.Add(1)
		if !recovered {
			http.Error(w, "temporary", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte{'S', 'R', 'S', 2, 2})
	}))
	defer flaky.Close()
	m := New(filepath.Join(t.TempDir(), "rules"), []Source{
		{Tag: "good", URL: good.URL, MaxBytes: 32},
		{Tag: "flaky", URL: flaky.URL, MaxBytes: 32},
	})
	statuses := m.Update(context.Background())
	if statusesHealthyForTest(statuses) {
		t.Fatal("initial flaky update unexpectedly succeeded")
	}
	recovered = true
	statuses = m.RetryFailedWithClient(context.Background(), statuses, http.DefaultClient)
	if !statusesHealthyForTest(statuses) {
		t.Fatalf("retry did not recover: %+v", statuses)
	}
	if goodCalls.Load() != 1 || flakyCalls.Load() != 2 {
		t.Fatalf("calls good=%d flaky=%d", goodCalls.Load(), flakyCalls.Load())
	}
}

func statusesHealthyForTest(statuses []Status) bool {
	for _, status := range statuses {
		if status.LastError != "" {
			return false
		}
	}
	return true
}
