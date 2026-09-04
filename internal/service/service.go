package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/yaodao0yaodao/proxylens/internal/dependency"
	"github.com/yaodao0yaodao/proxylens/internal/generator"
	"github.com/yaodao0yaodao/proxylens/internal/model"
	"github.com/yaodao0yaodao/proxylens/internal/naming"
	"github.com/yaodao0yaodao/proxylens/internal/probe"
	"github.com/yaodao0yaodao/proxylens/internal/quality"
	"github.com/yaodao0yaodao/proxylens/internal/rules"
	"github.com/yaodao0yaodao/proxylens/internal/store"
	"github.com/yaodao0yaodao/proxylens/internal/subscription"
)

type Service struct {
	Store           *store.Store
	Fetch           *subscription.Fetcher
	Probe           *probe.Engine
	Geo             *probe.Geo
	Schedule        model.Schedule
	Log             *slog.Logger
	Rules           *rules.Manager
	PublicBaseURL   string
	ManageSingBox   bool
	mu              sync.Mutex
	running         map[string]*runtimeState
	queued          map[string]*queuedState
	continuous      map[string]*continuousState
	scheduleMu      sync.RWMutex
	globalSettings  Settings
	maintenanceMu   sync.Mutex
	lastMaintenance time.Time
}

type runtimeState struct {
	Stage     string
	StartedAt time.Time
	Cancel    context.CancelFunc
	Done      chan struct{}
}
type queuedState struct {
	Cancel context.CancelFunc
}
type continuousState struct {
	Deadline         time.Time
	StopAfterCurrent bool
}
type Runtime struct {
	Status           string    `json:"status"`
	Stage            string    `json:"stage"`
	StartedAt        time.Time `json:"started_at,omitempty"`
	Continuous       bool      `json:"continuous,omitempty"`
	ContinuousUntil  time.Time `json:"continuous_until,omitempty"`
	RemainingSeconds int64     `json:"remaining_seconds,omitempty"`
	StopAfterCurrent bool      `json:"stop_after_current,omitempty"`
}
type Settings struct {
	DetectionMinutes int    `json:"detection_minutes"`
	GooglePlayMode   string `json:"google_play_mode"`
	LANPort          int    `json:"lan_port"`
	LANEnabled       bool   `json:"lan_enabled"`
}
type TaskSettings struct {
	DetectionMinutes *int   `json:"detection_minutes,omitempty"`
	GooglePlayMode   string `json:"google_play_mode,omitempty"`
	LANEnabled       *bool  `json:"lan_enabled,omitempty"`
	LANListen        string `json:"lan_listen,omitempty"`
	LANPort          *int   `json:"lan_port,omitempty"`
	LANUsername      string `json:"lan_username,omitempty"`
	LANPassword      string `json:"lan_password,omitempty"`
}

var errTaskAlreadyRunning = errors.New("task is already running")

func New(st *store.Store, p *probe.Engine, s model.Schedule, log *slog.Logger) *Service {
	defaults := Settings{DetectionMinutes: int(s.DetectionInterval / time.Minute), GooglePlayMode: "stable", LANPort: 2080, LANEnabled: true}
	service := &Service{Store: st, Fetch: subscription.NewFetcher(), Probe: p, Geo: probe.NewGeo(), Schedule: s, Log: log, running: map[string]*runtimeState{}, queued: map[string]*queuedState{}, continuous: map[string]*continuousState{}, globalSettings: defaults}
	if p != nil {
		base := "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@sing/geo/"
		var sources []rules.Source
		for _, x := range [][2]string{{"geosite-private", "geosite/private.srs"}, {"geoip-private", "geoip/private.srs"}, {"geosite-geolocation-not-cn", "geosite/geolocation-!cn.srs"}, {"geosite-cn", "geosite/cn.srs"}, {"geoip-cn", "geoip/cn.srs"}, {"geosite-google-play", "geosite/google-play.srs"}, {"geosite-google-play-cn", "geosite/google-play@cn.srs"}, {"geosite-steam", "geosite/steam.srs"}, {"geosite-game-download", "geosite/category-game-platforms-download.srs"}, {"geosite-abema", "geosite/abema.srs"}, {"geosite-dmm", "geosite/dmm.srs"}, {"geosite-niconico", "geosite/niconico.srs"}, {"geosite-pixiv", "geosite/pixiv.srs"}, {"geosite-tver", "geosite/tver.srs"}, {"geosite-radiko", "geosite/radiko.srs"}, {"geosite-nhk", "geosite/nhk.srs"}} {
			sources = append(sources, rules.Source{Tag: x[0], URL: base + x[1], License: "GPL-3.0-or-later", Version: "MetaCubeX sing branch", MaxBytes: 8 << 20})
		}
		service.Rules = rules.New(filepath.Join(p.DataDir, "rules"), sources)
	}
	if raw, e := st.Setting(context.Background(), "schedule"); e == nil {
		settings := defaults
		if json.Unmarshal([]byte(raw), &settings) == nil {
			var legacy struct {
				AvailabilityMinutes int `json:"availability_minutes"`
			}
			_ = json.Unmarshal([]byte(raw), &legacy)
			if settings.DetectionMinutes == 0 {
				settings.DetectionMinutes = legacy.AvailabilityMinutes
			}
			if settings.DetectionMinutes < 15 {
				settings.DetectionMinutes = 60
			}
			if service.applySettings(settings) == nil {
				migrated, _ := json.Marshal(settings)
				_ = st.PutSetting(context.Background(), "schedule", string(migrated))
			}
		}
	}
	return service
}

func (s *Service) GetSettings() Settings {
	s.scheduleMu.RLock()
	defer s.scheduleMu.RUnlock()
	v := s.globalSettings
	v.DetectionMinutes = int(s.Schedule.DetectionInterval / time.Minute)
	return v
}
func (s *Service) UpdateSettings(ctx context.Context, v Settings) error {
	if e := s.applySettings(v); e != nil {
		return e
	}
	raw, _ := json.Marshal(v)
	return s.Store.PutSetting(ctx, "schedule", string(raw))
}

func (s *Service) MergeNodes(ctx context.Context, taskID, targetID, sourceID string) error {
	if err := s.Store.MergeNodes(ctx, taskID, targetID, sourceID); err != nil {
		return err
	}
	if err := s.Calculate(ctx, taskID); err != nil {
		return err
	}
	return s.Generate(ctx, taskID)
}
func (s *Service) applySettings(v Settings) error {
	if v.DetectionMinutes < 15 || v.DetectionMinutes > 10080 {
		return errors.New("test interval is outside the allowed range")
	}
	if v.GooglePlayMode != "stable" && v.GooglePlayMode != "economy" {
		return errors.New("google_play_mode must be stable or economy")
	}
	if v.LANPort < 1 || v.LANPort > 65535 {
		return errors.New("LAN port outside range")
	}
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	s.Schedule.DetectionInterval = time.Duration(v.DetectionMinutes) * time.Minute
	s.globalSettings = v
	return nil
}
func (s *Service) schedule() model.Schedule {
	s.scheduleMu.RLock()
	defer s.scheduleMu.RUnlock()
	return s.Schedule
}
func (s *Service) TaskSettings(ctx context.Context, id string) (TaskSettings, error) {
	var v TaskSettings
	raw, err := s.Store.Setting(ctx, "task_settings:"+id)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	err = json.Unmarshal([]byte(raw), &v)
	return v, err
}
func (s *Service) UpdateTaskSettings(ctx context.Context, id string, v TaskSettings) error {
	sc := s.schedule()
	applyTaskSettings(&sc, v)
	if sc.DetectionInterval < 15*time.Minute || sc.DetectionInterval > 7*24*time.Hour {
		return errors.New("task override outside allowed range")
	}
	if v.GooglePlayMode != "" && v.GooglePlayMode != "stable" && v.GooglePlayMode != "economy" {
		return errors.New("google_play_mode must be stable or economy")
	}
	if v.LANPort != nil && (*v.LANPort < 1 || *v.LANPort > 65535) {
		return errors.New("LAN port outside range")
	}
	if (v.LANUsername == "") != (v.LANPassword == "") {
		return errors.New("LAN username and password must be configured together")
	}
	raw, _ := json.Marshal(v)
	return s.Store.PutSetting(ctx, "task_settings:"+id, string(raw))
}
func applyTaskSettings(sc *model.Schedule, v TaskSettings) {
	if v.DetectionMinutes != nil {
		sc.DetectionInterval = time.Duration(*v.DetectionMinutes) * time.Minute
	}
}
func (s *Service) taskSchedule(ctx context.Context, id string) model.Schedule {
	sc := s.schedule()
	if v, err := s.TaskSettings(ctx, id); err == nil {
		applyTaskSettings(&sc, v)
	}
	return sc
}

func (s *Service) CreateTask(ctx context.Context, name, subURL, ua string) (model.Task, error) {
	if _, e := url.ParseRequestURI(subURL); e != nil {
		return model.Task{}, fmt.Errorf("invalid subscription URL: %w", e)
	}
	t := model.Task{ID: uuid.NewString(), Name: strings.TrimSpace(name), SubscriptionURL: subURL, SubscriptionUA: ua, PublishToken: randomToken(24)}
	e := s.Store.CreateTask(ctx, &t)
	return t, e
}

func (s *Service) UpdateTaskDetails(ctx context.Context, id, name, subURL, ua string) (model.Task, error) {
	if _, err := url.ParseRequestURI(subURL); err != nil {
		return model.Task{}, fmt.Errorf("invalid subscription URL: %w", err)
	}
	task, err := s.Store.Task(ctx, id)
	if err != nil {
		return task, err
	}
	task.Name = s.Store.UniqueTaskName(ctx, name, id)
	task.SubscriptionURL = strings.TrimSpace(subURL)
	if strings.TrimSpace(ua) != "" {
		task.SubscriptionUA = strings.TrimSpace(ua)
	}
	err = s.Store.UpdateTask(ctx, task)
	return task, err
}
func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
func (s *Service) RotatePublishToken(ctx context.Context, id string) (string, error) {
	token := randomToken(24)
	return token, s.Store.RotatePublishToken(ctx, id, token)
}

func (s *Service) FullRun(ctx context.Context, taskID string) error {
	ctx, ok := s.begin(ctx, taskID, "正在准备")
	if !ok {
		return errTaskAlreadyRunning
	}
	defer s.end(taskID)
	t, e := s.Store.Task(ctx, taskID)
	if e != nil {
		return e
	}
	// Persist the attempt before network I/O so a broken subscription cannot
	// cause the one-minute scheduler tick to hammer it continuously.
	_ = s.Store.SetTaskRunKeepError(ctx, t.ID, "detection")
	s.Log.Info("full task run started", "task", taskID)
	s.setStage(taskID, "正在获取订阅")
	_ = s.Store.MarkSubscriptionAttempt(ctx, t.ID)
	if e = s.updateSubscription(ctx, t); e != nil {
		if errors.Is(e, context.Canceled) {
			s.Log.Info("task run cancelled", "task", taskID)
			return e
		}
		_ = s.Store.MarkSubscriptionFailure(ctx, t.ID, e.Error())
		if failedTask, taskErr := s.Store.Task(ctx, t.ID); taskErr == nil && !failedTask.SubscriptionFailedSince.IsZero() && time.Since(failedTask.SubscriptionFailedSince) >= 6*time.Hour {
			_ = s.Generate(context.Background(), t.ID)
		}
		return s.fail(ctx, t.ID, "subscription", e)
	}
	nodes, e := s.Store.Nodes(ctx, t.ID, false)
	if e != nil {
		return e
	}
	s.setStage(taskID, "正在更新出口并检测延迟")
	available, e := s.runProbe(ctx, t, nodes, "cycle")
	if e != nil {
		return s.fail(ctx, t.ID, "detection", e)
	}
	refreshed, _ := s.Store.Nodes(ctx, t.ID, false)
	okIDs := map[string]bool{}
	for _, n := range available {
		okIDs[n.ID] = true
	}
	available = available[:0]
	for _, n := range refreshed {
		if okIDs[n.ID] {
			available = append(available, n)
		}
	}
	s.setStage(taskID, "正在计算质量")
	if e = s.Calculate(ctx, t.ID); e != nil {
		return s.fail(ctx, t.ID, "rules", e)
	}
	s.setStage(taskID, "正在生成配置")
	if e = s.Generate(ctx, t.ID); e != nil {
		return s.fail(ctx, t.ID, "rules", e)
	}
	_ = s.Store.SetTaskRun(ctx, t.ID, "detection", "")
	s.Log.Info("full task run completed", "task", taskID)
	return nil
}

func (s *Service) begin(parent context.Context, id, stage string) (context.Context, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.running[id]; ok {
		return parent, false
	}
	ctx, cancel := context.WithCancel(parent)
	s.running[id] = &runtimeState{Stage: stage, StartedAt: time.Now(), Cancel: cancel, Done: make(chan struct{})}
	return ctx, true
}
func (s *Service) end(id string) {
	s.mu.Lock()
	if state, ok := s.running[id]; ok {
		state.Cancel()
		close(state.Done)
	}
	delete(s.running, id)
	s.mu.Unlock()
}
func (s *Service) Running(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.running[id]
	return ok
}
func (s *Service) setStage(id, stage string) {
	s.mu.Lock()
	if state := s.running[id]; state != nil {
		state.Stage = stage
	}
	s.mu.Unlock()
}
func (s *Service) Runtime(t model.Task) Runtime {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !t.Enabled {
		return Runtime{Status: "paused", Stage: "已暂停"}
	}
	if campaign := s.continuous[t.ID]; campaign != nil {
		stage := "正在进行4小时不间断完整检测"
		started := time.Now()
		if state := s.running[t.ID]; state != nil {
			stage, started = state.Stage, state.StartedAt
		}
		remaining := int64(time.Until(campaign.Deadline).Seconds())
		if remaining < 0 {
			remaining = 0
		}
		return Runtime{Status: "running", Stage: stage, StartedAt: started, Continuous: true, ContinuousUntil: campaign.Deadline, RemainingSeconds: remaining, StopAfterCurrent: campaign.StopAfterCurrent}
	}
	if state := s.running[t.ID]; state != nil {
		return Runtime{Status: "running", Stage: state.Stage, StartedAt: state.StartedAt}
	}
	if s.queued[t.ID] != nil {
		return Runtime{Status: "running", Stage: "排队等待当前检测结束"}
	}
	return Runtime{Status: "idle", Stage: "空闲"}
}
func (s *Service) Pause(ctx context.Context, id string) error {
	s.mu.Lock()
	delete(s.continuous, id)
	if queued := s.queued[id]; queued != nil {
		queued.Cancel()
		delete(s.queued, id)
	}
	if state := s.running[id]; state != nil {
		state.Cancel()
	}
	s.mu.Unlock()
	return s.Store.SetTaskEnabled(ctx, id, false)
}

// CancelCurrent stops the current/campaign run without changing whether the
// task is enabled. A caller can then queue one fresh run with new settings.
func (s *Service) CancelCurrent(id string) {
	s.mu.Lock()
	delete(s.continuous, id)
	if queued := s.queued[id]; queued != nil {
		queued.Cancel()
		delete(s.queued, id)
	}
	if state := s.running[id]; state != nil {
		state.Cancel()
	}
	s.mu.Unlock()
}

func (s *Service) ContinuousRun(ctx context.Context, id string, duration time.Duration) error {
	if duration <= 0 {
		duration = 4 * time.Hour
	}
	if err := s.Store.SetTaskEnabled(ctx, id, true); err != nil {
		return err
	}
	s.mu.Lock()
	if s.continuous[id] != nil {
		s.mu.Unlock()
		return errors.New("continuous detection is already running")
	}
	state := &continuousState{Deadline: time.Now().Add(duration)}
	s.continuous[id] = state
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.continuous, id)
		s.mu.Unlock()
	}()
	for {
		if err := s.RunWhenIdle(ctx, id); err != nil && errors.Is(err, context.Canceled) {
			return err
		}
		s.mu.Lock()
		stop := state.StopAfterCurrent || time.Now().After(state.Deadline)
		s.mu.Unlock()
		if stop {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Minute):
		}
	}
}

func (s *Service) StopContinuousAfterCurrent(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state := s.continuous[id]; state != nil {
		state.StopAfterCurrent = true
		return true
	}
	return false
}

func (s *Service) Busy(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[id] != nil || s.queued[id] != nil || s.continuous[id] != nil
}
func (s *Service) Start(ctx context.Context, id string) error {
	return s.Store.SetTaskEnabled(ctx, id, true)
}
func (s *Service) RunWhenIdle(ctx context.Context, id string) error {
	for {
		s.mu.Lock()
		state := s.running[id]
		var done <-chan struct{}
		if state != nil {
			done = state.Done
		}
		s.mu.Unlock()
		if state == nil {
			if err := s.FullRun(ctx, id); err == nil || !errors.Is(err, errTaskAlreadyRunning) {
				return err
			}
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
		}
	}
}

// QueueRun coalesces ordinary triggers for one task. Repeated clicks or closely
// spaced setting changes produce one pending full run; deliberate repetition is
// handled by ContinuousRun instead.
func (s *Service) QueueRun(parent context.Context, id string, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = 6 * time.Hour
	}
	runCtx, cancel := context.WithTimeout(parent, timeout)
	state := &queuedState{Cancel: cancel}
	s.mu.Lock()
	if s.queued[id] != nil {
		s.mu.Unlock()
		cancel()
		return false
	}
	s.queued[id] = state
	s.mu.Unlock()
	go func() {
		defer cancel()
		err := s.RunWhenIdle(runCtx, id)
		s.mu.Lock()
		if s.queued[id] == state {
			delete(s.queued, id)
		}
		s.mu.Unlock()
		if err != nil && !errors.Is(err, context.Canceled) {
			s.Log.Warn("queued task run ended", "task", id, "error", err)
		}
	}()
	return true
}

func (s *Service) fail(ctx context.Context, id, kind string, e error) error {
	if kind != "subscription" {
		_ = s.Store.SetTaskError(ctx, id, e.Error())
		// A detection-stage failure exits before the normal generation step.
		// Regenerate from the last valid data so the important warning becomes
		// visible in clients immediately instead of waiting for the next cycle.
		if kind == "detection" {
			generateCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = s.Generate(generateCtx, id)
			cancel()
		}
	}
	s.Log.Error("task stage failed", "task", id, "stage", kind, "error", e)
	return e
}

func (s *Service) updateSubscription(ctx context.Context, t model.Task) error {
	fetched, e := s.Fetch.FetchWithMetadata(ctx, t.SubscriptionURL, t.SubscriptionUA)
	if e != nil && s.Probe != nil {
		if fallback, ok := s.bestInternalProxy(ctx); ok {
			directErr := e
			proxyErr := s.Probe.WithHTTPClient(ctx, fallback, func(client *http.Client) error {
				fetched, e = s.Fetch.FetchWithClient(ctx, t.SubscriptionURL, t.SubscriptionUA, client)
				return e
			})
			if proxyErr == nil && e == nil {
				s.Log.Info("subscription fetched through best ordinary node after direct failure", "task", t.ID, "node", fallback.ID, "direct_error", directErr)
			} else if proxyErr != nil {
				e = fmt.Errorf("direct fetch failed: %v; proxy fallback failed: %w", directErr, proxyErr)
			}
		}
	}
	if e != nil {
		return e
	}
	b := fetched.Body
	nodes, notices, e := subscription.Parse(t.ID, b)
	if e != nil {
		return e
	}
	notices = subscription.EffectiveNotices(notices, fetched.Metadata)
	if e = s.Store.UpsertNodes(ctx, t.ID, nodes, notices); e != nil {
		return e
	}
	if e = s.Store.UpdateSubscriptionInfo(ctx, t.ID, fetched.Metadata); e != nil {
		return e
	}
	if strings.TrimSpace(t.Name) == "" {
		name := subscription.DetectName(b, t.SubscriptionURL, fetched.NameHints...)
		if e = s.Store.UpdateTaskName(ctx, t.ID, name); e != nil {
			return e
		}
		t.Name = name
	}
	s.Log.Info("subscription updated", "task", t.ID, "nodes", len(nodes), "notices", len(notices), "user_agent", t.SubscriptionUA)
	return s.Store.SetTaskRun(ctx, t.ID, "subscription", "")
}

func (s *Service) bestInternalProxy(ctx context.Context) (model.Node, bool) {
	tasks, err := s.Store.Tasks(ctx)
	if err != nil {
		return model.Node{}, false
	}
	bestScore := -1.0
	var best model.Node
	for _, task := range tasks {
		nodes, nodeErr := s.Store.Nodes(ctx, task.ID, false)
		qualities, qualityErr := s.Store.Qualities(ctx, task.ID)
		if nodeErr != nil || qualityErr != nil {
			continue
		}
		for _, node := range nodes {
			code := strings.ToUpper(strings.TrimSpace(node.CountryCode))
			if node.Multiplier > 1 || code == "" || code == "CN" || code == "HK" || code == "MO" || code == "TW" {
				continue
			}
			q := qualities[node.ID]
			if q.Samples > 0 && q.Priority > bestScore {
				best, bestScore = node, q.Priority
			}
		}
	}
	return best, bestScore >= 0
}

func statusesHealthy(statuses []rules.Status) bool {
	for _, status := range statuses {
		if status.LastError != "" {
			return false
		}
	}
	return true
}

type geoLookupResult struct {
	value probe.GeoResult
	err   error
}

// prefetchProbeGeography overlaps independent metadata lookups while keeping
// all SQLite writes in the deterministic result loop below. The small bound is
// deliberate: it speeds up large subscriptions without hammering the public
// geolocation service from an OpenWrt device.
func (s *Service) prefetchProbeGeography(ctx context.Context, nodes map[string]model.Node, results []model.Measurement) (map[string]geoLookupResult, map[string]geoLookupResult) {
	exits := make(map[string]geoLookupResult)
	servers := make(map[string]geoLookupResult)
	if s.Geo == nil {
		return exits, servers
	}
	type job struct {
		key      string
		isServer bool
	}
	var jobs []job
	seenExit, seenServer := map[string]bool{}, map[string]bool{}
	for _, measurement := range results {
		if measurement.ExitIP == "" {
			continue
		}
		if !seenExit[measurement.ExitIP] {
			seenExit[measurement.ExitIP] = true
			jobs = append(jobs, job{key: measurement.ExitIP})
		}
		node := nodes[measurement.NodeID]
		if node.Server != "" && (node.ASN == "" || node.ASNServer != node.Server) && !seenServer[node.Server] {
			seenServer[node.Server] = true
			jobs = append(jobs, job{key: node.Server, isServer: true})
		}
	}
	queue := make(chan job)
	var workers sync.WaitGroup
	var resultMu sync.Mutex
	workerCount := 3
	if len(jobs) < workerCount {
		workerCount = len(jobs)
	}
	for i := 0; i < workerCount; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range queue {
				var value probe.GeoResult
				var err error
				if item.isServer {
					value, err = s.Geo.ResolveServer(ctx, item.key)
				} else {
					value, err = s.Geo.Lookup(ctx, item.key)
				}
				resultMu.Lock()
				if item.isServer {
					servers[item.key] = geoLookupResult{value: value, err: err}
				} else {
					exits[item.key] = geoLookupResult{value: value, err: err}
				}
				resultMu.Unlock()
			}
		}()
	}
	for _, item := range jobs {
		select {
		case queue <- item:
		case <-ctx.Done():
			close(queue)
			workers.Wait()
			return exits, servers
		}
	}
	close(queue)
	workers.Wait()
	return exits, servers
}

func (s *Service) runProbe(ctx context.Context, t model.Task, nodes []model.Node, kind string) ([]model.Node, error) {
	if len(nodes) == 0 {
		return nil, nil
	}
	timeout := 30 * time.Second
	if kind == "cycle" {
		timeout = 5 * time.Second
	}
	started := time.Now()
	options := probe.BatchOptions{Kind: kind, Timeout: timeout}
	results, e := s.Probe.Batch(ctx, nodes, options)
	if e != nil {
		if strings.Contains(e.Error(), "probe config") || strings.Contains(e.Error(), "protocol supported") {
			_ = s.Store.SetTaskError(ctx, t.ID, "节点配置转换失败："+e.Error())
		}
		return nil, e
	}
	if len(results) < len(nodes) {
		_ = s.Store.SetTaskError(ctx, t.ID, fmt.Sprintf("%d 个节点配置不受 sing-box 支持，已跳过且不计入可用率", len(nodes)-len(results)))
	}
	available, failed, engineFailures, downloaded := 0, 0, 0, int64(0)
	for _, m := range results {
		if probe.IsEngineFailure(m.Error) {
			engineFailures++
			continue
		}
		if m.Available != nil && *m.Available {
			available++
		} else {
			failed++
		}
		downloaded += m.DownloadBytes
	}
	s.Log.Info("probe batch completed", "task", t.ID, "kind", kind, "nodes", len(results), "available", available, "failed", failed, "engine_failures", engineFailures, "download_bytes", downloaded, "duration", time.Since(started).Round(time.Millisecond))
	nodeByID := map[string]model.Node{}
	for _, n := range nodes {
		nodeByID[n.ID] = n
	}
	exitGeos, serverGeos := s.prefetchProbeGeography(ctx, nodeByID, results)
	var succeeded []model.Node
	for _, m := range results {
		n := nodeByID[m.NodeID]
		if probe.IsEngineFailure(m.Error) {
			s.Log.Warn("node probe skipped after local engine failure", "task", t.ID, "node_id", n.ID, "node", n.DisplayName, "kind", kind, "error", m.Error)
			continue
		}
		probeSucceeded := m.Available != nil && *m.Available
		if kind != "availability" && kind != "cycle" {
			m.Available = nil
		}
		m.ConfigRevision = n.ConfigRevision
		if e = s.Store.AddMeasurement(ctx, m); e != nil {
			return succeeded, e
		}
		if m.Error != "" {
			s.Log.Warn("node probe failed", "task", t.ID, "node_id", n.ID, "node", n.DisplayName, "source_name", n.OriginalName, "kind", kind, "error", m.Error)
		}
		if m.ExitError != "" {
			s.Log.Warn("node exit discovery failed", "task", t.ID, "node_id", n.ID, "node", n.DisplayName, "source_name", n.OriginalName, "error", m.ExitError)
		}
		if probeSucceeded {
			succeeded = append(succeeded, n)
		}
		if (kind == "exit" || kind == "cycle") && m.ExitIP != "" {
			exitLookup, found := exitGeos[m.ExitIP]
			if !found || exitLookup.err != nil {
				s.Log.Warn("exit geo lookup failed", "ip", m.ExitIP, "error", exitLookup.err)
				continue
			}
			exitGeo := exitLookup.value
			asn, asnServer := n.ASN, n.ASNServer
			if asn == "" || asnServer != n.Server {
				if serverLookup, found := serverGeos[n.Server]; found && serverLookup.err == nil {
					asn, asnServer = serverLookup.value.ASN, n.Server
				} else {
					s.Log.Warn("server ASN lookup failed", "node_id", n.ID, "server", n.Server, "error", serverLookup.err)
				}
			}
			display := naming.DisplayName(exitGeo.CountryCode, exitGeo.Country, n.Number, n.Multiplier)
			if e = s.Store.UpdateNodeGeo(ctx, n.ID, asn, asnServer, m.ExitIP, exitGeo.CountryCode, exitGeo.Country, display); e != nil {
				return succeeded, e
			}
		}
	}
	return succeeded, nil
}

func DisplayName(country string, number int64, multiplier float64) string {
	return naming.DisplayName("", country, number, multiplier)
}

func (s *Service) Calculate(ctx context.Context, taskID string) error {
	nodes, e := s.Store.Nodes(ctx, taskID, false)
	if e != nil {
		return e
	}
	now := time.Now()
	for _, n := range nodes {
		samples, e := s.Store.Measurements(ctx, n.ID, now.Add(-30*24*time.Hour))
		if e != nil {
			return e
		}
		// Quality belongs to the permanent node identity. Providers commonly
		// rotate credentials or other connection fields on every subscription
		// fetch; a configuration revision must not erase that node's history.
		if e = s.Store.PutQuality(ctx, quality.Calculate(n, samples, now)); e != nil {
			return e
		}
		var daily []model.Measurement
		for _, sample := range samples {
			if !sample.TestedAt.Before(now.Add(-24 * time.Hour)) {
				daily = append(daily, sample)
			}
		}
		if e = s.Store.PutDailyQuality(ctx, quality.Calculate(n, daily, now), now); e != nil {
			return e
		}
	}
	return s.Store.SetTaskRunKeepError(ctx, taskID, "rules")
}
func (s *Service) Generate(ctx context.Context, taskID string) error {
	nodes, e := s.Store.Nodes(ctx, taskID, false)
	if e != nil {
		return e
	}
	notices, e := s.Store.Notices(ctx, taskID)
	if e != nil {
		return e
	}
	q, e := s.Store.Qualities(ctx, taskID)
	if e != nil {
		return e
	}
	var skipped []string
	settings, _ := s.TaskSettings(ctx, taskID)
	global := s.GetSettings()
	opt := generator.Options{LANEnabled: global.LANEnabled, LANListen: "0.0.0.0", LANPort: global.LANPort, GooglePlayMode: global.GooglePlayMode}
	if task, taskErr := s.Store.Task(ctx, taskID); taskErr == nil {
		if !task.SubscriptionFailedSince.IsZero() && time.Since(task.SubscriptionFailedSince) >= 6*time.Hour {
			hours := int(time.Since(task.LastSubscriptionAt).Hours())
			if task.LastSubscriptionAt.IsZero() {
				hours = int(time.Since(task.SubscriptionFailedSince).Hours())
			}
			if hours < 6 {
				hours = 6
			}
			opt.Alerts = append(opt.Alerts, fmt.Sprintf("源订阅更新失败 · %d小时未更新", hours))
		} else if strings.TrimSpace(task.LastError) != "" {
			opt.Alerts = append(opt.Alerts, "重要提示 · "+task.LastError)
		}
	}
	for _, key := range []string{"rules_important_alert", "process_rules_important_alert", "dependency_important_alert"} {
		if alert, alertErr := s.Store.Setting(ctx, key); alertErr == nil && strings.TrimSpace(alert) != "" {
			opt.Alerts = append(opt.Alerts, "重要提示 · "+alert)
		}
	}
	if s.Rules != nil {
		opt.DownloadProcesses = s.Rules.ProcessNames()
	}
	if s.PublicBaseURL != "" {
		if publicURL, parseErr := url.Parse(s.PublicBaseURL); parseErr == nil && publicURL.Hostname() != "" {
			opt.DirectDomains = []string{publicURL.Hostname()}
		}
		if task, e := s.Store.Task(ctx, taskID); e == nil {
			opt.RuleBaseURL = strings.TrimSuffix(s.PublicBaseURL, "/") + "/rules/" + task.PublishToken
		}
	}
	if settings.LANEnabled != nil {
		opt.LANEnabled = *settings.LANEnabled
	}
	if settings.LANListen != "" {
		opt.LANListen = settings.LANListen
	}
	if settings.LANPort != nil {
		opt.LANPort = *settings.LANPort
	}
	opt.LANUsername = settings.LANUsername
	opt.LANPassword = settings.LANPassword
	if settings.GooglePlayMode != "" {
		opt.GooglePlayMode = settings.GooglePlayMode
	}
	for _, client := range []generator.Client{generator.SFA, generator.SFA113, generator.Carton, generator.Carton114} {
		a, e := generator.GenerateWithOptions(client, nodes, notices, q, opt)
		if e != nil {
			return e
		}
		if len(a.Skipped) > 0 && len(skipped) == 0 {
			skipped = a.Skipped
		}
		if e = s.Store.PutArtifact(ctx, taskID, string(client), a.SHA256, a.Content); e != nil {
			return e
		}
		if e = s.Store.SetTaskRunKeepError(ctx, taskID, string(client)); e != nil {
			return e
		}
		s.Log.Info("configuration generated", "task", taskID, "client", client, "bytes", len(a.Content), "sha256", a.SHA256)
	}
	if len(skipped) > 0 {
		_ = s.Store.SetTaskError(ctx, taskID, fmt.Sprintf("%d 个节点配置不受支持，已跳过且不计入可用率：%s", len(skipped), strings.Join(skipped, "；")))
	}
	return nil
}

// RegenerateStored rebuilds client artifacts from durable node and quality
// state. It is intentionally probe-free, so a program or routing-rule upgrade
// is reflected immediately after restart without spending subscription traffic.
func (s *Service) RegenerateStored(ctx context.Context) {
	tasks, err := s.Store.Tasks(ctx)
	if err != nil {
		s.Log.Error("startup regeneration list tasks", "error", err)
		return
	}
	for _, task := range tasks {
		taskCtx, ok := s.beginWhenIdle(ctx, task.ID, "正在更新本地配置")
		if !ok {
			return
		}
		s.regenerateStoredTask(taskCtx, task)
		s.end(task.ID)
	}
}

func (s *Service) beginWhenIdle(ctx context.Context, id, stage string) (context.Context, bool) {
	for {
		if runCtx, ok := s.begin(ctx, id, stage); ok {
			return runCtx, true
		}
		s.mu.Lock()
		state := s.running[id]
		var done <-chan struct{}
		if state != nil {
			done = state.Done
		}
		s.mu.Unlock()
		if done == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx, false
		case <-done:
		}
	}
}

func (s *Service) regenerateStoredTask(ctx context.Context, task model.Task) {
	var err error
	if resolved, resolveErr := s.Store.ResolveInitialBatchIdentityConflicts(ctx, task.ID); resolveErr != nil {
		s.Log.Warn("startup false identity conflict cleanup failed", "task", task.ID, "error", resolveErr)
	} else if resolved > 0 {
		s.Log.Info("startup false identity conflicts resolved", "task", task.ID, "conflicts", resolved)
	}
	moved, mergeErr := s.Store.MergeRotatedNodeHistory(ctx, task.ID)
	qualityComplete, qualityCoverageErr := s.Store.QualityCoverageComplete(ctx, task.ID)
	if mergeErr != nil {
		s.Log.Warn("startup rotated-node history migration failed", "task", task.ID, "error", mergeErr)
	}
	if qualityCoverageErr != nil {
		s.Log.Warn("startup quality coverage check failed", "task", task.ID, "error", qualityCoverageErr)
	}
	if moved > 0 || !qualityComplete {
		if moved > 0 {
			s.Log.Info("startup rotated-node history merged", "task", task.ID, "measurements", moved)
		}
		if err = s.Calculate(ctx, task.ID); err != nil {
			s.Log.Warn("startup quality recalculation after identity migration failed", "task", task.ID, "error", err)
		}
	}
	nodes, nodeErr := s.Store.Nodes(ctx, task.ID, false)
	if nodeErr == nil {
		for _, node := range nodes {
			multiplier := subscription.Multiplier(node.OriginalName)
			if multiplier != node.Multiplier {
				node.Multiplier = multiplier
				display := node.DisplayName
				if node.Number > 0 {
					display = naming.DisplayName(node.CountryCode, node.Country, node.Number, multiplier)
				}
				if err = s.Store.UpdateNodeMultiplier(ctx, node.ID, multiplier, display); err != nil {
					s.Log.Warn("startup node multiplier repair failed", "node", node.ID, "error", err)
					continue
				}
				node.DisplayName = display
				s.Log.Info("startup node multiplier repaired", "node", node.ID, "source_name", node.OriginalName, "multiplier", multiplier)
			}
			if node.Number <= 0 {
				continue
			}
			display := naming.DisplayName(node.CountryCode, node.Country, node.Number, node.Multiplier)
			if display != node.DisplayName {
				if err = s.Store.UpdateNodeDisplayName(ctx, node.ID, display); err != nil {
					s.Log.Warn("startup node name normalization failed", "node", node.ID, "error", err)
				}
			}
		}
	}
	// Recalculate from stored measurements on every upgrade/restart so score
	// formula and identity-semantics fixes take effect without a network run.
	if err = s.Calculate(ctx, task.ID); err != nil {
		s.Log.Warn("startup quality recalculation failed", "task", task.ID, "error", err)
	}
	if err = s.Generate(ctx, task.ID); err != nil {
		s.Log.Warn("startup regeneration skipped", "task", task.ID, "error", err)
	}
}

func (s *Service) Scheduler(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	s.runDue(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.runDue(ctx)
		}
	}
}
func (s *Service) runDue(ctx context.Context) {
	schedule := s.schedule()
	if s.Rules != nil && !s.Running("__rules__") {
		raw, _ := s.Store.Setting(ctx, "rules_last_update")
		last, _ := time.Parse(time.RFC3339Nano, raw)
		rawRulesAttempt, _ := s.Store.Setting(ctx, "rules_last_attempt")
		rulesAttempt, _ := time.Parse(time.RFC3339Nano, rawRulesAttempt)
		rawProcesses, _ := s.Store.Setting(ctx, "download_processes_last_update")
		processesLast, _ := time.Parse(time.RFC3339Nano, rawProcesses)
		rawAttempt, _ := s.Store.Setting(ctx, "download_processes_last_attempt")
		processesAttempt, _ := time.Parse(time.RFC3339Nano, rawAttempt)
		dependencyRaw, _ := s.Store.Setting(ctx, "singbox_last_update_check")
		dependencyLast, _ := time.Parse(time.RFC3339Nano, dependencyRaw)
		dependencyAttemptRaw, _ := s.Store.Setting(ctx, "singbox_last_update_attempt")
		dependencyAttempt, _ := time.Parse(time.RFC3339Nano, dependencyAttemptRaw)
		rulesDue := last.IsZero() || time.Since(last) >= schedule.RulesInterval || !s.Rules.Complete()
		processesDue := processesLast.IsZero() || time.Since(processesLast) >= schedule.RulesInterval || len(s.Rules.ProcessNames()) == 0
		dependencyDue := s.ManageSingBox && (dependencyLast.IsZero() || time.Since(dependencyLast) >= schedule.RulesInterval)
		if rulesDue && !rulesAttempt.IsZero() && time.Since(rulesAttempt) < 15*time.Minute {
			rulesDue = false
		}
		if processesDue && !processesAttempt.IsZero() && time.Since(processesAttempt) < 15*time.Minute {
			processesDue = false
		}
		// A core archive is tens of MiB. Do not repeatedly consume bandwidth on
		// a broken route; rules/process lists are small and may retry sooner.
		if dependencyDue && !dependencyAttempt.IsZero() && time.Since(dependencyAttempt) < 6*time.Hour {
			dependencyDue = false
		}
		if rulesDue || processesDue || dependencyDue {
			go func() {
				runCtx, ok := s.begin(ctx, "__rules__", "正在更新规则")
				if !ok {
					return
				}
				defer s.end("__rules__")
				var statuses []rules.Status
				alertsChanged := false
				if rulesDue {
					_ = s.Store.PutSetting(runCtx, "rules_last_attempt", time.Now().UTC().Format(time.RFC3339Nano))
					statuses = s.Rules.Update(runCtx)
					if !statusesHealthy(statuses) && s.Probe != nil {
						if fallback, found := s.bestInternalProxy(runCtx); found {
							_ = s.Probe.WithHTTPClient(runCtx, fallback, func(client *http.Client) error {
								statuses = s.Rules.RetryFailedWithClient(runCtx, statuses, client)
								if !statusesHealthy(statuses) {
									return errors.New("one or more rule providers still failed")
								}
								return nil
							})
						}
					}
				}
				processesUpdated := false
				if processesDue {
					now := time.Now().UTC().Format(time.RFC3339Nano)
					_ = s.Store.PutSetting(runCtx, "download_processes_last_attempt", now)
					if err := s.Rules.UpdateProcesses(runCtx); err != nil {
						directErr := err
						if fallback, found := s.bestInternalProxy(runCtx); found && s.Probe != nil {
							err = s.Probe.WithHTTPClient(runCtx, fallback, func(client *http.Client) error { return s.Rules.UpdateProcessesWithClient(runCtx, client) })
						}
						if err != nil {
							_ = s.Store.PutSetting(runCtx, "process_rules_important_alert", "下载类应用规则更新失败，继续使用已有缓存")
							alertsChanged = true
							s.Log.Warn("download process rule update failed; keeping cached list", "direct_error", directErr, "error", err)
						} else {
							_ = s.Store.PutSetting(runCtx, "process_rules_important_alert", "")
							alertsChanged = true
							processesUpdated = true
							_ = s.Store.PutSetting(runCtx, "download_processes_last_update", now)
						}
					} else {
						_ = s.Store.PutSetting(runCtx, "process_rules_important_alert", "")
						alertsChanged = true
						processesUpdated = true
						_ = s.Store.PutSetting(runCtx, "download_processes_last_update", now)
					}
				}
				dependencyUpdated := false
				if dependencyDue && s.Probe != nil {
					now := time.Now().UTC().Format(time.RFC3339Nano)
					_ = s.Store.PutSetting(runCtx, "singbox_last_update_attempt", now)
					client := &http.Client{Timeout: 5 * time.Minute}
					var result dependency.Result
					updateErr := s.Probe.WithExclusive(runCtx, func() error {
						var err error
						result, err = dependency.UpdateSingBox(runCtx, client, s.Probe.SingBoxPath)
						return err
					})
					if updateErr != nil {
						directErr := updateErr
						if fallback, found := s.bestInternalProxy(runCtx); found {
							updateErr = s.Probe.WithHTTPClient(runCtx, fallback, func(proxyClient *http.Client) error {
								result, updateErr = dependency.UpdateSingBox(runCtx, proxyClient, s.Probe.SingBoxPath)
								return updateErr
							})
						}
						if updateErr != nil {
							message := "sing-box依赖更新失败，继续使用当前版本"
							_ = s.Store.PutSetting(runCtx, "dependency_important_alert", message)
							alertsChanged = true
							s.Log.Warn("sing-box dependency update failed; keeping current binary", "direct_error", directErr, "error", updateErr)
						}
					}
					if updateErr == nil {
						_ = s.Store.PutSetting(runCtx, "singbox_last_update_check", now)
						_ = s.Store.PutSetting(runCtx, "dependency_important_alert", "")
						alertsChanged = true
						dependencyUpdated = result.Updated
						if result.Updated {
							s.Log.Info("sing-box dependency updated", "from", result.Current, "to", result.Latest)
						}
					}
				}
				okAll := true
				for _, st := range statuses {
					if st.LastError != "" {
						okAll = false
						s.Log.Warn("rule provider update failed", "tag", st.Source.Tag, "error", st.LastError)
					}
				}
				if rulesDue && okAll {
					_ = s.Store.PutSetting(runCtx, "rules_last_update", time.Now().UTC().Format(time.RFC3339Nano))
				}
				if rulesDue {
					if okAll {
						_ = s.Store.PutSetting(runCtx, "rules_important_alert", "")
					} else {
						_ = s.Store.PutSetting(runCtx, "rules_important_alert", "分流规则更新失败，继续使用已有缓存")
					}
					alertsChanged = true
				}
				if processesUpdated || dependencyUpdated || alertsChanged {
					if tasks, err := s.Store.Tasks(runCtx); err == nil {
						for _, task := range tasks {
							if s.Running(task.ID) {
								continue
							}
							if err = s.Generate(runCtx, task.ID); err != nil {
								s.Log.Warn("regenerate after process rule update failed", "task", task.ID, "error", err)
							}
						}
					}
				}
			}()
		}
	}
	// Retention is independent maintenance. The scheduler wakes every minute for
	// task precision, but rewriting the same old rows every minute only creates
	// SQLite/WAL churn. Hourly is comfortably inside the 48-hour retention edge.
	if now := time.Now(); s.maintenanceDue(now) {
		if err := s.Store.RollupMeasurements(ctx, now.Add(-48*time.Hour), now.Add(-90*24*time.Hour)); err != nil {
			s.Log.Warn("measurement rollup failed", "error", err)
		}
		if err := s.Store.PruneQualityHistory(ctx, now.Add(-48*time.Hour), now.Add(-365*24*time.Hour)); err != nil {
			s.Log.Warn("quality history prune failed", "error", err)
		}
	}
	tasks, e := s.Store.Tasks(ctx)
	if e != nil {
		s.Log.Error("scheduler list tasks", "error", e)
		return
	}
	for _, t := range tasks {
		if !t.Enabled || s.Busy(t.ID) {
			continue
		}
		taskSchedule := s.taskSchedule(ctx, t.ID)
		due := t.LastDetectionAt.IsZero() || time.Since(t.LastDetectionAt) >= taskSchedule.DetectionInterval
		if due {
			go func(id string) {
				runCtx, cancel := context.WithTimeout(ctx, 6*time.Hour)
				defer cancel()
				_ = s.FullRun(runCtx, id)
			}(t.ID)
		}
	}
}

func (s *Service) maintenanceDue(now time.Time) bool {
	s.maintenanceMu.Lock()
	defer s.maintenanceMu.Unlock()
	if !s.lastMaintenance.IsZero() && now.Sub(s.lastMaintenance) < time.Hour {
		return false
	}
	s.lastMaintenance = now
	return true
}

func (s *Service) incremental(ctx context.Context, t model.Task) {
	_ = s.FullRun(ctx, t.ID)
}
