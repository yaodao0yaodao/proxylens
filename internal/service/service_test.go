package service

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yaodao0yaodao/proxylens/internal/model"
	"github.com/yaodao0yaodao/proxylens/internal/probe"
	"github.com/yaodao0yaodao/proxylens/internal/store"
)

type serviceRoundTripFunc func(*http.Request) (*http.Response, error)

func (f serviceRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func testSchedule() model.Schedule {
	return model.Schedule{DetectionInterval: time.Hour, RulesInterval: 24 * time.Hour}
}

func TestApplyTaskSettingsOverridesUnifiedCycle(t *testing.T) {
	schedule := testSchedule()
	detectionMinutes := 30
	applyTaskSettings(&schedule, TaskSettings{DetectionMinutes: &detectionMinutes})
	if schedule.DetectionInterval != 30*time.Minute {
		t.Fatalf("task schedule overrides not applied: %+v", schedule)
	}
}

func TestCompleteDetectionLooksUpOnlyUnknownCountriesAndRuleRefreshLooksUpAll(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	geo := probe.NewGeo()
	geo.Client = &http.Client{Transport: serviceRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		ip := strings.TrimPrefix(request.URL.Path, "/")
		mu.Lock()
		calls[ip]++
		mu.Unlock()
		body := `{"success":true,"ip":"` + ip + `","country_code":"JP","country":"Japan","connection":{"asn":1,"org":"test"}}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	svc := &Service{Geo: geo}
	nodes := map[string]model.Node{
		"known":   {ID: "known", Server: "known.example", ASN: "AS1", ASNServer: "known.example", CountryCode: "JP", Country: "Japan"},
		"unknown": {ID: "unknown", Server: "unknown.example", ASN: "AS1", ASNServer: "unknown.example"},
	}
	results := []model.Measurement{{NodeID: "known", ExitIP: "192.0.2.1"}, {NodeID: "unknown", ExitIP: "192.0.2.2"}}
	exits, _ := svc.prefetchProbeGeography(t.Context(), nodes, results, false)
	if len(exits) != 1 || exits["192.0.2.2"].err != nil {
		t.Fatalf("complete detection lookups=%+v", exits)
	}
	exits, _ = svc.prefetchProbeGeography(t.Context(), nodes, results, true)
	if len(exits) != 2 {
		t.Fatalf("rule refresh lookups=%+v", exits)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls["192.0.2.1"] != 1 || calls["192.0.2.2"] != 2 {
		t.Fatalf("country provider calls=%v", calls)
	}
}
func TestSettingsPersistAndTaskPauseCancels(t *testing.T) {
	st, e := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(st, nil, testSchedule(), log)
	settings := Settings{DetectionMinutes: 30, GooglePlayMode: "economy", LANPort: 2088, LANEnabled: false}
	if e = svc.UpdateSettings(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	reloaded := New(st, nil, testSchedule(), log)
	if got := reloaded.GetSettings(); got != settings {
		t.Fatalf("settings=%+v want=%+v", got, settings)
	}
	task, e := svc.CreateTask(context.Background(), "", "https://example.com/sub", "clash.meta")
	if e != nil {
		t.Fatal(e)
	}
	if task.Name != "" {
		t.Fatalf("blank name replaced with %q", task.Name)
	}
	runCtx, ok := svc.begin(context.Background(), task.ID, "正在测试")
	if !ok {
		t.Fatal("begin failed")
	}
	if e = svc.Pause(context.Background(), task.ID); e != nil {
		t.Fatal(e)
	}
	select {
	case <-runCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("pause did not cancel context")
	}
	stored, e := st.Task(context.Background(), task.ID)
	if e != nil {
		t.Fatal(e)
	}
	if stored.Enabled {
		t.Fatal("task was not disabled")
	}
	if svc.Runtime(stored).Status != "paused" {
		t.Fatal("runtime did not report paused")
	}
	svc.end(task.ID)
}
func TestSettingsValidation(t *testing.T) {
	st, e := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	svc := New(st, nil, testSchedule(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if e = svc.UpdateSettings(context.Background(), Settings{}); e == nil {
		t.Fatal("accepted zero settings")
	}
}

func TestBeginWhenIdleWakesWhenCurrentRunEnds(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := New(st, nil, testSchedule(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, ok := svc.begin(context.Background(), "task", "first"); !ok {
		t.Fatal("first run did not start")
	}
	type result struct {
		ctx context.Context
		ok  bool
	}
	ready := make(chan result, 1)
	go func() {
		ctx, ok := svc.beginWhenIdle(context.Background(), "task", "second")
		ready <- result{ctx: ctx, ok: ok}
	}()
	select {
	case <-ready:
		t.Fatal("waiter started before current run ended")
	case <-time.After(20 * time.Millisecond):
	}
	svc.end("task")
	select {
	case got := <-ready:
		if !got.ok || got.ctx.Err() != nil {
			t.Fatalf("wait result ok=%v err=%v", got.ok, got.ctx.Err())
		}
		svc.end("task")
	case <-time.After(time.Second):
		t.Fatal("waiter was not awakened")
	}
}

func TestMaintenanceDueIsHourly(t *testing.T) {
	svc := &Service{}
	now := time.Now()
	if !svc.maintenanceDue(now) {
		t.Fatal("first maintenance was not due")
	}
	if svc.maintenanceDue(now.Add(59 * time.Minute)) {
		t.Fatal("maintenance repeated within an hour")
	}
	if !svc.maintenanceDue(now.Add(time.Hour)) {
		t.Fatal("hourly maintenance did not become due")
	}
}

func TestQueueRunCoalescesAndPauseCancelsPendingRun(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := New(st, nil, testSchedule(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	task, err := svc.CreateTask(context.Background(), "queued", "https://example.com/sub", "clash.meta")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.begin(context.Background(), task.ID, "current"); !ok {
		t.Fatal("current run did not start")
	}
	if !svc.QueueRun(context.Background(), task.ID, time.Minute) {
		t.Fatal("first queued run was rejected")
	}
	if svc.QueueRun(context.Background(), task.ID, time.Minute) {
		t.Fatal("duplicate queued run was accepted")
	}
	if err = svc.Pause(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	svc.end(task.ID)
	deadline := time.Now().Add(time.Second)
	for svc.Busy(task.ID) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if svc.Busy(task.ID) {
		t.Fatal("pause left a queued run behind")
	}
}
