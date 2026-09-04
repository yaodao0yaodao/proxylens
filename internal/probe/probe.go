package probe

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yaodao0yaodao/proxylens/internal/generator"
	"github.com/yaodao0yaodao/proxylens/internal/model"
)

type Engine struct {
	SingBoxPath, DataDir string
	PortStart            int
	Log                  *slog.Logger
	gate                 chan struct{}
	gateOnce             sync.Once
}

type BatchOptions struct {
	Kind    string
	Timeout time.Duration
}

func (e *Engine) acquire(ctx context.Context) error {
	e.gateOnce.Do(func() { e.gate = make(chan struct{}, 1); e.gate <- struct{}{} })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.gate:
		return nil
	}
}

func (e *Engine) release() { e.gate <- struct{}{} }

// WithExclusive serializes operations that inspect or replace the sing-box
// executable with probe processes and internal proxy helpers.
func (e *Engine) WithExclusive(ctx context.Context, fn func() error) error {
	if err := e.acquire(ctx); err != nil {
		return err
	}
	defer e.release()
	return fn()
}

// WithHTTPClient starts one short-lived sing-box proxy through node and gives
// the caller an HTTP client using it. The same gate as quality probes prevents
// config/port collisions and keeps only one auxiliary core alive at a time.
func (e *Engine) WithHTTPClient(ctx context.Context, node model.Node, fn func(*http.Client) error) error {
	if err := e.acquire(ctx); err != nil {
		return err
	}
	defer e.release()
	config, active, err := e.config([]model.Node{node})
	if err != nil || len(active) != 1 {
		if err == nil {
			err = errors.New("selected internal proxy node is unsupported")
		}
		return err
	}
	path := filepath.Join(e.DataDir, "internal-http-proxy.json")
	defer os.Remove(path)
	raw, _ := json.Marshal(config)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		return err
	}
	check := exec.CommandContext(ctx, e.SingBoxPath, "check", "-c", path)
	if output, checkErr := check.CombinedOutput(); checkErr != nil {
		return fmt.Errorf("sing-box rejected internal proxy: %w: %s", checkErr, strings.TrimSpace(string(output)))
	}
	cmd := exec.CommandContext(ctx, e.SingBoxPath, "run", "-c", path)
	configureCommand(cmd)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	processDone := make(chan struct{})
	var processErr error
	go func() { processErr = cmd.Wait(); close(processDone) }()
	defer func() {
		if cmd.Process != nil {
			terminateCommand(cmd)
		}
		<-processDone
	}()
	go e.pipeLogs(stderr)
	if err = e.waitPorts(ctx, []int{e.PortStart}, 10*time.Second, processDone, func() error { return processErr }); err != nil {
		return err
	}
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", e.PortStart))
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), ForceAttemptHTTP2: true, TLSHandshakeTimeout: 10 * time.Second}
	defer transport.CloseIdleConnections()
	return fn(&http.Client{Transport: transport, Timeout: 5 * time.Minute})
}

func (e *Engine) Batch(ctx context.Context, nodes []model.Node, opt BatchOptions) ([]model.Measurement, error) {
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	defer e.release()
	results, err := e.batchOnce(ctx, nodes, opt)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]model.Node, len(nodes))
	for _, node := range nodes {
		byID[node.ID] = node
	}
	// A shared probe process can disappear after its ports initially opened. A
	// local 127.0.0.1 refusal says nothing about the remote proxy, so retry that
	// node in an isolated sing-box process before returning a result.
	for i := range results {
		if !isLocalProxyFailure(results[i].Error) {
			continue
		}
		node, ok := byID[results[i].NodeID]
		if !ok {
			continue
		}
		retry, retryErr := e.batchOnce(ctx, []model.Node{node}, opt)
		if retryErr != nil || len(retry) != 1 {
			results[i].Available = nil
			results[i].Error = engineFailurePrefix + firstError(retryErr, results[i].Error)
			continue
		}
		results[i] = retry[0]
		if isLocalProxyFailure(results[i].Error) {
			results[i].Available = nil
			results[i].Error = engineFailurePrefix + results[i].Error
		}
	}
	return results, nil
}

const engineFailurePrefix = "probe engine failure: "

func IsEngineFailure(message string) bool { return strings.HasPrefix(message, engineFailurePrefix) }

func isLocalProxyFailure(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "proxyconnect tcp") && strings.Contains(lower, "127.0.0.1") && strings.Contains(lower, "connection refused")
}

func firstError(err error, fallback string) string {
	if err != nil {
		return err.Error()
	}
	return fallback
}

func (e *Engine) batchOnce(ctx context.Context, nodes []model.Node, opt BatchOptions) ([]model.Measurement, error) {
	if len(nodes) == 0 {
		return nil, nil
	}
	if _, err := os.Stat(e.SingBoxPath); err != nil {
		return nil, fmt.Errorf("sing-box probe unavailable: %w", err)
	}
	path := filepath.Join(e.DataDir, "probe.json")
	defer os.Remove(path)
	active := append([]model.Node(nil), nodes...)
	var config map[string]any
	for {
		var err error
		config, active, err = e.config(active)
		if err != nil {
			return nil, err
		}
		if len(active) == 0 {
			return nil, errors.New("no node protocol supported by sing-box converter")
		}
		raw, _ := json.Marshal(config)
		if err = os.WriteFile(path, raw, 0600); err != nil {
			return nil, err
		}
		check := exec.CommandContext(ctx, e.SingBoxPath, "check", "-c", path)
		out, checkErr := check.CombinedOutput()
		if checkErr == nil {
			break
		}
		index := outboundErrorIndex(string(out))
		if index < 0 || index >= len(active) {
			return nil, fmt.Errorf("sing-box rejected probe config: %w: %s", checkErr, strings.TrimSpace(string(out)))
		}
		e.Log.Warn("skip node rejected by sing-box", "node", active[index].ID, "error", strings.TrimSpace(string(out)))
		active = append(active[:index], active[index+1:]...)
	}
	cmd := exec.CommandContext(ctx, e.SingBoxPath, "run", "-c", path)
	configureCommand(cmd)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	processDone := make(chan struct{})
	var processErr error
	go func() {
		processErr = cmd.Wait()
		close(processDone)
	}()
	defer func() {
		if cmd.Process != nil {
			terminateCommand(cmd)
		}
		<-processDone
	}()
	go e.pipeLogs(stderr)
	ports := make([]int, len(active))
	for i := range active {
		ports[i] = e.PortStart + i
	}
	if err = e.waitPorts(ctx, ports, 10*time.Second, processDone, func() error { return processErr }); err != nil {
		return nil, err
	}
	var directLatency *float64
	if opt.Kind == "latency" || opt.Kind == "cycle" {
		transport := &http.Transport{ForceAttemptHTTP2: true, TLSHandshakeTimeout: 10 * time.Second}
		client := &http.Client{Transport: transport, Timeout: opt.Timeout}
		value, _, baselineErr := steadyLatency(ctx, client)
		transport.CloseIdleConnections()
		if baselineErr != nil {
			return nil, fmt.Errorf("direct latency baseline unavailable: %w", baselineErr)
		}
		if value >= 800 {
			return nil, fmt.Errorf("direct latency baseline unhealthy: %.0f ms", value)
		}
		directLatency = &value
		e.Log.Info("direct steady latency baseline", "latency_ms", value)
	}
	results := make([]model.Measurement, len(active))
	jobs := make(chan int)
	var workers sync.WaitGroup
	workerCount := 4
	if len(active) < workerCount {
		workerCount = len(active)
	}
	for w := 0; w < workerCount; w++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				results[i] = e.one(ctx, active[i], e.PortStart+i, opt, directLatency)
			}
		}()
	}
	for i := range active {
		select {
		case jobs <- i:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return results, ctx.Err()
		}
	}
	close(jobs)
	workers.Wait()
	return results, nil
}

var outboundIndexRE = regexp.MustCompile(`outbounds\[([0-9]+)\]`)

func outboundErrorIndex(message string) int {
	m := outboundIndexRE.FindStringSubmatch(message)
	if len(m) != 2 {
		return -1
	}
	i, _ := strconv.Atoi(m[1])
	return i
}

func (e *Engine) config(nodes []model.Node) (map[string]any, []model.Node, error) {
	var inbounds, outbounds, rules []any
	var active []model.Node
	for _, n := range nodes {
		idx := len(active)
		tag := "node-" + n.ID
		out, err := generator.ConvertNode(n, tag)
		if err != nil {
			e.Log.Warn("skip unsupported node", "node", n.ID, "error", err)
			continue
		}
		inTag := "probe-" + strconv.Itoa(idx)
		inbounds = append(inbounds, map[string]any{"type": "mixed", "tag": inTag, "listen": "127.0.0.1", "listen_port": e.PortStart + idx})
		outbounds = append(outbounds, out)
		rules = append(rules, map[string]any{"inbound": inTag, "action": "route", "outbound": tag})
		active = append(active, n)
	}
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "direct"})
	return map[string]any{"log": map[string]any{"level": "warn"}, "inbounds": inbounds, "outbounds": outbounds, "route": map[string]any{"rules": rules, "final": "direct", "auto_detect_interface": true}}, active, nil
}

func (e *Engine) one(ctx context.Context, n model.Node, port int, opt BatchOptions, directLatency *float64) model.Measurement {
	m := model.Measurement{NodeID: n.ID, Kind: opt.Kind, TestedAt: time.Now()}
	available := false
	m.Available = &available
	timeout := opt.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	if opt.Kind == "latency" || opt.Kind == "cycle" {
		transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), ForceAttemptHTTP2: true, TLSHandshakeTimeout: 10 * time.Second}
		client := &http.Client{Transport: transport, Timeout: timeout}
		if opt.Kind == "cycle" {
			exit := e.exit(ctx, n, client, timeout)
			m.DownloadBytes += exit.DownloadBytes
			m.ExitIP = exit.ExitIP
			m.ExitError = exit.Error
		}
		value, downloaded, err := steadyLatency(ctx, client)
		transport.CloseIdleConnections()
		m.DownloadBytes += downloaded
		if err != nil {
			m.Error = err.Error()
			return m
		}
		if value >= 800 {
			m.Error = fmt.Sprintf("latency threshold exceeded: %.0f ms >= 800 ms", value)
			return m
		}
		available = true
		m.Available = &available
		m.LatencyMS = &value
		m.DirectLatencyMS = directLatency
		return m
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), ForceAttemptHTTP2: true, DisableKeepAlives: true, TLSHandshakeTimeout: 10 * time.Second}
	client := &http.Client{Transport: transport, Timeout: timeout}
	if opt.Kind == "exit" {
		return e.exit(ctx, n, client, timeout)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, latencyTarget, nil)
	req.Header.Set("User-Agent", "ProxyLens/1.0 exit-quality-probe")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		m.Error = err.Error()
		return m
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	elapsed := time.Since(start)
	m.DownloadBytes = int64(len(body))
	if err != nil {
		m.Error = err.Error()
		return m
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		m.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return m
	}
	available = true
	m.Available = &available
	if opt.Kind == "latency" {
		v := float64(elapsed.Microseconds()) / 1000
		m.LatencyMS = &v
	}
	return m
}

const latencyTarget = "https://www.gstatic.com/generate_204"

// steadyLatency deliberately excludes the first cold transaction, then uses
// P75 of three requests over the established transport. This reflects the
// interactive latency of the node exit without letting one lucky request win.
func steadyLatency(ctx context.Context, client *http.Client) (float64, int64, error) {
	values := make([]float64, 0, 3)
	var downloaded int64
	var failures []string
	for attempt := 0; attempt < 4; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, latencyTarget, nil)
		req.Header.Set("User-Agent", "ProxyLens/1.0 exit-quality-probe")
		started := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return 0, downloaded, ctx.Err()
			}
			failures = append(failures, err.Error())
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		downloaded += int64(len(body))
		if readErr != nil {
			failures = append(failures, readErr.Error())
			continue
		}
		if resp.StatusCode != http.StatusNoContent {
			failures = append(failures, fmt.Sprintf("HTTP %d, expected 204", resp.StatusCode))
			continue
		}
		if attempt > 0 {
			values = append(values, float64(time.Since(started).Microseconds())/1000)
		}
	}
	if len(values) < 2 {
		return 0, downloaded, fmt.Errorf("only %d/3 valid latency requests: %s", len(values), strings.Join(failures, " | "))
	}
	sort.Float64s(values)
	if len(values) == 2 {
		return values[1], downloaded, nil
	}
	// Linear P75 for three values lies halfway between the median and maximum.
	return (values[1] + values[2]) / 2, downloaded, nil
}

func (e *Engine) exit(ctx context.Context, node model.Node, client *http.Client, timeout time.Duration) model.Measurement {
	measurement := model.Measurement{NodeID: node.ID, Kind: "exit", TestedAt: time.Now()}
	available := false
	measurement.Available = &available
	type endpoint struct {
		url   string
		parse func([]byte) string
	}
	jsonIP := func(body []byte) string {
		var value struct {
			IP string `json:"ip"`
		}
		_ = json.Unmarshal(body, &value)
		return strings.TrimSpace(value.IP)
	}
	traceIP := func(body []byte) string {
		for _, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(line, "ip=") {
				return strings.TrimSpace(strings.TrimPrefix(line, "ip="))
			}
		}
		return ""
	}
	endpoints := []endpoint{
		{"https://api64.ipify.org?format=json", jsonIP},
		{"https://www.cloudflare.com/cdn-cgi/trace", traceIP},
		{"https://api.ipify.org?format=json", jsonIP},
	}
	perAttempt := timeout / time.Duration(len(endpoints))
	if perAttempt < 2*time.Second {
		perAttempt = 2 * time.Second
	}
	var failures []string
	for _, candidate := range endpoints {
		attemptCtx, cancel := context.WithTimeout(ctx, perAttempt)
		request, _ := http.NewRequestWithContext(attemptCtx, http.MethodGet, candidate.url, nil)
		request.Header.Set("User-Agent", "ProxyLens/1.0 exit-quality-probe")
		response, err := client.Do(request)
		if err != nil {
			cancel()
			failures = append(failures, err.Error())
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		_ = response.Body.Close()
		cancel()
		measurement.DownloadBytes += int64(len(body))
		if readErr != nil || response.StatusCode < 200 || response.StatusCode >= 400 {
			failures = append(failures, fmt.Sprintf("%s: HTTP %d: %v", candidate.url, response.StatusCode, readErr))
			continue
		}
		ip := candidate.parse(body)
		if net.ParseIP(ip) == nil {
			failures = append(failures, candidate.url+": response did not contain an IP address")
			continue
		}
		available = true
		measurement.Available = &available
		measurement.ExitIP = ip
		return measurement
	}
	measurement.Error = strings.Join(failures, " | ")
	return measurement
}

func (e *Engine) waitPort(ctx context.Context, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("timed out waiting for sing-box probe")
}

func (e *Engine) waitPorts(ctx context.Context, ports []int, timeout time.Duration, processDone <-chan struct{}, processError func() error) error {
	readyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	errorsByPort := make(chan error, len(ports))
	for _, port := range ports {
		port := port
		go func() {
			for {
				connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
				if err == nil {
					_ = connection.Close()
					errorsByPort <- nil
					return
				}
				select {
				case <-readyCtx.Done():
					errorsByPort <- fmt.Errorf("timed out waiting for sing-box probe port %d", port)
					return
				case <-processDone:
					errorsByPort <- fmt.Errorf("sing-box probe exited before port %d became ready: %w", port, processError())
					return
				case <-time.After(100 * time.Millisecond):
				}
			}
		}()
	}
	for range ports {
		if err := <-errorsByPort; err != nil {
			return err
		}
	}
	return nil
}
func (e *Engine) pipeLogs(r io.Reader) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		line := s.Text()
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error") || strings.Contains(lower, "fatal") || strings.Contains(lower, "warn") {
			e.Log.Warn("sing-box probe", "line", line)
		} else {
			e.Log.Debug("sing-box probe", "line", line)
		}
	}
}

type Geo struct {
	Client *http.Client
	mu     sync.Mutex
	cache  map[string]GeoResult
}
type GeoResult struct{ IP, CountryCode, Country, ASN string }

func NewGeo() *Geo {
	return &Geo{Client: &http.Client{Timeout: 15 * time.Second}, cache: map[string]GeoResult{}}
}
func (g *Geo) Lookup(ctx context.Context, ip string) (GeoResult, error) {
	g.mu.Lock()
	if v, ok := g.cache[ip]; ok {
		g.mu.Unlock()
		return v, nil
	}
	g.mu.Unlock()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://ipwho.is/"+url.PathEscape(ip), nil)
	req.Header.Set("User-Agent", "ProxyLens/1.0")
	resp, e := g.Client.Do(req)
	if e != nil {
		return GeoResult{}, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return GeoResult{}, fmt.Errorf("geo lookup HTTP %d", resp.StatusCode)
	}
	var raw struct {
		Success     bool   `json:"success"`
		IP          string `json:"ip"`
		CountryCode string `json:"country_code"`
		Country     string `json:"country"`
		Connection  struct {
			ASN int    `json:"asn"`
			Org string `json:"org"`
		} `json:"connection"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); e != nil {
		return GeoResult{}, e
	}
	if !raw.Success {
		return GeoResult{}, errors.New("geo lookup failed")
	}
	v := GeoResult{IP: raw.IP, CountryCode: strings.ToUpper(raw.CountryCode), Country: raw.Country}
	if raw.Connection.ASN > 0 {
		v.ASN = fmt.Sprintf("AS%d %s", raw.Connection.ASN, raw.Connection.Org)
	}
	g.mu.Lock()
	g.cache[ip] = v
	g.mu.Unlock()
	return v, nil
}
func (g *Geo) ResolveServer(ctx context.Context, server string) (GeoResult, error) {
	ip := net.ParseIP(server)
	if ip == nil {
		ips, e := net.DefaultResolver.LookupIP(ctx, "ip", server)
		if e != nil || len(ips) == 0 {
			return GeoResult{}, fmt.Errorf("resolve %s: %w", server, e)
		}
		ip = ips[0]
	}
	return g.Lookup(ctx, ip.String())
}
