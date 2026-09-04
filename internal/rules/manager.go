package rules

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Source struct {
	Tag, URL, License, Version string
	MaxBytes                   int64
}
type Status struct {
	Source    Source    `json:"source"`
	SHA256    string    `json:"sha256"`
	UpdatedAt time.Time `json:"updated_at"`
	LastError string    `json:"last_error,omitempty"`
}
type Manager struct {
	Dir        string
	Client     *http.Client
	Sources    []Source
	Validate   func([]byte) error
	ProcessURL string
}

func New(dir string, s []Source) *Manager {
	return &Manager{Dir: dir, Sources: s, Client: &http.Client{Timeout: 30 * time.Second}, Validate: ValidateSRS, ProcessURL: "https://cdn.jsdelivr.net/gh/blackmatrix7/ios_rule_script@master/rule/Surge/Download/Download.list"}
}

func (m *Manager) UpdateProcesses(ctx context.Context) error {
	return m.UpdateProcessesWithClient(ctx, m.Client)
}

func (m *Manager) UpdateProcessesWithClient(ctx context.Context, client *http.Client) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.ProcessURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download process list HTTP %d", resp.StatusCode)
	}
	var names []string
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 1<<20))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(strings.ToUpper(line), "PROCESS-NAME,") {
			name := strings.TrimSpace(strings.SplitN(line, ",", 3)[1])
			if name != "" {
				names = append(names, name)
			}
		}
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	if len(names) == 0 {
		return errors.New("download process list contains no process names")
	}
	b, _ := json.Marshal(names)
	if err = os.MkdirAll(m.Dir, 0700); err != nil {
		return err
	}
	tmp := filepath.Join(m.Dir, "download-processes.json.tmp")
	if err = os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return replaceAtomic(tmp, filepath.Join(m.Dir, "download-processes.json"))
}

func (m *Manager) ProcessNames() []string {
	b, err := os.ReadFile(filepath.Join(m.Dir, "download-processes.json"))
	if err != nil {
		return nil
	}
	var names []string
	_ = json.Unmarshal(b, &names)
	return names
}
func ValidateSRS(b []byte) error {
	if len(b) < 4 || string(b[:3]) != "SRS" {
		return fmt.Errorf("invalid SRS magic")
	}
	if b[3] > 3 {
		return fmt.Errorf("unsupported SRS version %d", b[3])
	}
	return nil
}
func (m *Manager) Update(ctx context.Context) []Status {
	return m.UpdateWithClient(ctx, m.Client)
}

func (m *Manager) UpdateWithClient(ctx context.Context, client *http.Client) []Status {
	return m.updateSources(ctx, client, m.Sources)
}

// RetryFailedWithClient retries only providers that failed in the preceding
// direct pass. Successful rule files are left untouched, avoiding a second
// download of every rule when only one host/request had a transient problem.
func (m *Manager) RetryFailedWithClient(ctx context.Context, previous []Status, client *http.Client) []Status {
	failed := make([]Source, 0, len(previous))
	for _, status := range previous {
		if status.LastError != "" {
			failed = append(failed, status.Source)
		}
	}
	if len(failed) == 0 {
		return previous
	}
	retried := m.updateSources(ctx, client, failed)
	byTag := make(map[string]Status, len(retried))
	for _, status := range retried {
		byTag[status.Source.Tag] = status
	}
	merged := append([]Status(nil), previous...)
	for i, status := range merged {
		if retry, ok := byTag[status.Source.Tag]; ok {
			merged[i] = retry
		}
	}
	return merged
}

func (m *Manager) updateSources(ctx context.Context, client *http.Client, sources []Source) []Status {
	_ = os.MkdirAll(m.Dir, 0700)
	out := make([]Status, 0, len(sources))
	for _, src := range sources {
		st := Status{Source: src}
		old, _ := m.status(src.Tag)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode != 200 {
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		var b []byte
		if err == nil {
			limit := src.MaxBytes
			if limit <= 0 {
				limit = 8 << 20
			}
			b, err = io.ReadAll(io.LimitReader(resp.Body, limit+1))
			resp.Body.Close()
			if err == nil && int64(len(b)) > limit {
				err = fmt.Errorf("rule set exceeds %d bytes", limit)
			}
		}
		if err == nil {
			err = m.Validate(b)
		}
		if err != nil {
			st = old
			st.Source = src
			st.LastError = err.Error()
			out = append(out, st)
			_ = m.writeStatus(st)
			continue
		}
		sum := sha256.Sum256(b)
		st.SHA256 = hex.EncodeToString(sum[:])
		st.UpdatedAt = time.Now()
		tmp := filepath.Join(m.Dir, src.Tag+".tmp")
		if err = os.WriteFile(tmp, b, 0600); err == nil {
			err = replaceAtomic(tmp, filepath.Join(m.Dir, src.Tag+".srs"))
		}
		if err != nil {
			st = old
			st.Source = src
			st.LastError = err.Error()
		} else {
			st.LastError = ""
		}
		_ = m.writeStatus(st)
		out = append(out, st)
	}
	return out
}

// Statuses returns the last durable state for every configured rule source.
func (m *Manager) Statuses() []Status {
	out := make([]Status, 0, len(m.Sources))
	for _, source := range m.Sources {
		status, _ := m.status(source.Tag)
		status.Source = source
		out = append(out, status)
	}
	return out
}

// Complete reports whether every configured source has a readable, validated
// cache entry. This checks the current source list rather than trusting only a
// timestamp, since an upgrade can add sources while that timestamp is fresh.
func (m *Manager) Complete() bool {
	for _, src := range m.Sources {
		if _, _, err := m.Read(src.Tag); err != nil {
			return false
		}
	}
	return true
}
func replaceAtomic(tmp, target string) error {
	bak := target + ".bak"
	_ = os.Remove(bak)
	had := false
	if _, err := os.Stat(target); err == nil {
		if err = os.Rename(target, bak); err != nil {
			return err
		}
		had = true
	}
	if err := os.Rename(tmp, target); err != nil {
		if had {
			_ = os.Rename(bak, target)
		}
		return err
	}
	_ = os.Remove(bak)
	return nil
}
func (m *Manager) Read(tag string) ([]byte, Status, error) {
	st, err := m.status(tag)
	if err != nil {
		return nil, st, err
	}
	b, err := os.ReadFile(filepath.Join(m.Dir, tag+".srs"))
	if err != nil {
		return nil, st, err
	}
	if err = m.Validate(b); err != nil {
		return nil, st, err
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != st.SHA256 {
		return nil, st, fmt.Errorf("cached rule SHA mismatch")
	}
	return b, st, nil
}
func (m *Manager) status(tag string) (Status, error) {
	var s Status
	b, err := os.ReadFile(filepath.Join(m.Dir, tag+".json"))
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(b, &s)
	return s, err
}
func (m *Manager) writeStatus(s Status) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	tmp := filepath.Join(m.Dir, s.Source.Tag+".json.tmp")
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return replaceAtomic(tmp, filepath.Join(m.Dir, s.Source.Tag+".json"))
}
