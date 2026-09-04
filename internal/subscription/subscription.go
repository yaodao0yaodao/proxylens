package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/yaodao0yaodao/proxylens/internal/model"
	"github.com/yaodao0yaodao/proxylens/internal/naming"
	"gopkg.in/yaml.v3"
)

const maxSubscriptionBytes = 16 << 20

var (
	multiplierSuffixRE = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*(?:x|×|倍(?:率)?)`)
	multiplierPrefixRE = regexp.MustCompile(`倍(?:率)?\s*[:：=]?\s*([0-9]+(?:\.[0-9]+)?)`)
	countryRE          = regexp.MustCompile(`(?i)(🇦🇺|🇨🇦|🇨🇳|🇩🇪|🇫🇷|🇬🇧|🇭🇰|🇮🇳|🇮🇩|🇯🇵|🇰🇷|🇲🇾|🇳🇱|🇵🇭|🇷🇺|🇸🇬|🇹🇭|🇹🇼|🇺🇸|🇻🇳|香港|日本|美国|美國|新加坡|台湾|台灣|韩国|韓國|英国|英國|德国|德國|法国|法國|澳大利亚|澳洲|加拿大|印度|印度尼西亚|印尼|马来西亚|菲律賓|菲律宾|泰国|越南|俄罗斯|荷兰|Hong\s*Kong|Japan|United\s*States|USA|Singapore|Taiwan|Korea|Germany|France|Canada|Australia|India|Malaysia|Thailand|Vietnam|Russia|Netherlands)`)
)

type Fetcher struct{ Client *http.Client }

type Metadata struct {
	Upload, Download, Total int64
	Expire                  time.Time
	Reset                   time.Time
	ResetDay                int
	CollectedAt             time.Time
}
type FetchResult struct {
	Body      []byte
	Metadata  Metadata
	NameHints []string
}

func NewFetcher() *Fetcher {
	return &Fetcher{Client: &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("too many redirects")
		}
		if len(via) > 0 {
			req.Header.Set("User-Agent", via[0].Header.Get("User-Agent"))
		}
		return nil
	}}}
}

func (f *Fetcher) Fetch(ctx context.Context, url, userAgent string) ([]byte, error) {
	r, err := f.FetchWithMetadata(ctx, url, userAgent)
	return r.Body, err
}
func (f *Fetcher) FetchWithMetadata(ctx context.Context, url, userAgent string) (FetchResult, error) {
	return f.FetchWithClient(ctx, url, userAgent, f.Client)
}

func (f *Fetcher) FetchWithClient(ctx context.Context, url, userAgent string, client *http.Client) (FetchResult, error) {
	if userAgent == "" {
		userAgent = "clash.meta"
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if e != nil {
		return FetchResult{}, e
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/yaml,text/yaml,text/plain,application/octet-stream;q=0.9,*/*;q=0.1")
	resp, e := client.Do(req)
	if e != nil {
		return FetchResult{}, e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return FetchResult{}, fmt.Errorf("subscription returned HTTP %d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, maxSubscriptionBytes+1))
	if e != nil {
		return FetchResult{}, e
	}
	if len(b) > maxSubscriptionBytes {
		return FetchResult{}, errors.New("subscription exceeds 16 MiB limit")
	}
	var hints []string
	for _, key := range []string{"Profile-Title", "Profile-Name", "X-Profile-Title"} {
		if value := decodeNameHint(resp.Header.Get(key)); value != "" {
			hints = append(hints, value)
		}
	}
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		if value := decodeNameHint(params["filename"]); value != "" {
			lower := strings.ToLower(value)
			if strings.HasSuffix(lower, ".yaml") {
				value = value[:len(value)-len(".yaml")]
			} else if strings.HasSuffix(lower, ".yml") {
				value = value[:len(value)-len(".yml")]
			}
			hints = append(hints, value)
		}
	}
	return FetchResult{Body: b, Metadata: ParseUserInfo(resp.Header.Get("Subscription-Userinfo"), time.Now()), NameHints: hints}, nil
}

func decodeNameHint(value string) string {
	value = strings.Trim(strings.TrimSpace(value), "\"")
	if decoded, err := url.QueryUnescape(value); err == nil {
		value = decoded
	}
	return SanitizeName(value)
}

func ParseUserInfo(raw string, now time.Time) Metadata {
	m := Metadata{CollectedAt: now}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ';' || r == ',' }) {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimSpace(kv[1]), 10, 64)
		if err != nil || v < 0 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(kv[0])) {
		case "upload":
			m.Upload = v
		case "download":
			m.Download = v
		case "total":
			m.Total = v
		case "expire":
			if v > 0 {
				m.Expire = time.Unix(v, 0)
			}
		case "reset", "reset_at", "reset-time", "reset_time":
			if v >= 1_000_000_000 {
				m.Reset = time.Unix(v, 0)
			} else if v >= 1 && v <= 31 {
				m.ResetDay = int(v)
			}
		case "reset_day", "reset-day":
			if v >= 1 && v <= 31 {
				m.ResetDay = int(v)
			}
		}
	}
	return m
}

// EffectiveNotices keeps real provider notices, but replaces empty placeholder
// notices with the standardized Subscription-Userinfo account information.
func EffectiveNotices(notices []model.Notice, metadata Metadata) []model.Notice {
	out := make([]model.Notice, 0, len(notices)+2)
	for _, notice := range notices {
		name := strings.TrimSpace(notice.Name)
		compact := strings.NewReplacer(" ", "", "：", ":").Replace(name)
		if name == "" || compact == "暂无" || compact == "套餐通知:暂无" || compact == "套餐信息:暂无" {
			continue
		}
		out = append(out, model.Notice{Name: name})
	}
	if len(out) > 0 || metadata.Total <= 0 {
		return out
	}
	used := metadata.Upload + metadata.Download
	remaining := metadata.Total - used
	if remaining < 0 {
		remaining = 0
	}
	out = append(out, model.Notice{Name: fmt.Sprintf("机场账号：%s / %s，剩余 %s", formatBytes(used), formatBytes(metadata.Total), formatBytes(remaining))})
	if !metadata.Expire.IsZero() {
		out = append(out, model.Notice{Name: "到期时间：" + metadata.Expire.Local().Format("2006-01-02 15:04")})
	}
	if !metadata.Reset.IsZero() {
		out = append(out, model.Notice{Name: "重置时间：" + metadata.Reset.Local().Format("2006-01-02 15:04")})
	} else if metadata.ResetDay > 0 {
		out = append(out, model.Notice{Name: fmt.Sprintf("每月 %d 日重置", metadata.ResetDay)})
	}
	return out
}

func formatBytes(value int64) string {
	const mib, gib = int64(1 << 20), int64(1 << 30)
	if value >= gib {
		return fmt.Sprintf("%.2f GiB", float64(value)/float64(gib))
	}
	return fmt.Sprintf("%.2f MiB", float64(value)/float64(mib))
}

func Parse(taskID string, data []byte) ([]model.Node, []model.Notice, error) {
	var root struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if e := yaml.Unmarshal(data, &root); e != nil {
		return nil, nil, fmt.Errorf("parse Clash YAML: %w", e)
	}
	if len(root.Proxies) == 0 {
		return nil, nil, errors.New("subscription contains no Clash/Mihomo proxies")
	}
	var nodes []model.Node
	var notices []model.Notice
	baseCount := map[string]int{}
	credentialCount := map[string]int{}
	for _, p := range root.Proxies {
		base := ContinuityKey(p)
		baseCount[base]++
		credentialCount[base+"\x00"+credentialKey(p)]++
	}
	for _, p := range root.Proxies {
		name := text(p["name"])
		typ := strings.ToLower(text(p["type"]))
		server := text(p["server"])
		port := integer(p["port"])
		if name == "" {
			continue
		}
		if !HasCountry(name) {
			notices = append(notices, model.Notice{Name: name})
			continue
		}
		if typ == "" || server == "" || port < 1 || port > 65535 {
			continue
		}
		identity := ContinuityKey(p)
		if baseCount[identity] > 1 {
			withCredential := identity + "\x00" + credentialKey(p)
			if credentialKey(p) != "" && credentialCount[withCredential] == 1 {
				identity = withCredential
			} else {
				identity = withCredential + "\x00name:" + strings.ToLower(SanitizeName(name))
			}
		}
		id := stableID(taskID, identity)
		nodes = append(nodes, model.Node{ID: id, TaskID: taskID, Protocol: typ, Server: server, Port: port, OriginalName: name, Multiplier: Multiplier(name), Config: cloneMap(p)})
	}
	if len(nodes) == 0 {
		return nil, notices, errors.New("all proxy entries were notices or invalid")
	}
	return nodes, notices, nil
}

func DetectName(data []byte, rawURL string, hints ...string) string {
	for _, hint := range hints {
		if value := SanitizeName(hint); meaningfulProfileName(value) {
			return value
		}
	}
	var root map[string]any
	if yaml.Unmarshal(data, &root) == nil {
		for _, key := range []string{"name", "title", "profile-title"} {
			if value := SanitizeName(text(root[key])); meaningfulProfileName(value) {
				return value
			}
		}
		if profile, ok := root["profile"].(map[string]any); ok {
			if value := SanitizeName(text(profile["name"])); meaningfulProfileName(value) {
				return value
			}
		}
	}
	if parsed, e := url.Parse(rawURL); e == nil {
		host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
		if host != "" {
			return host
		}
	}
	return "未命名订阅"
}

func meaningfulProfileName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || value == "<nil>" {
		return false
	}
	normalized := strings.ToLower(value)
	for _, extension := range []string{".yaml", ".yml", ".json"} {
		normalized = strings.TrimSuffix(normalized, extension)
	}
	switch normalized {
	case "config", "configuration", "clash", "mihomo", "profile", "subscription", "subscribe", "default":
		return false
	}
	return true
}

func stableID(taskID, identity string) string {
	h := sha256.Sum256([]byte(taskID + "\x00" + identity))
	raw := h[:16]
	raw[6] = (raw[6] & 0x0f) | 0x50
	raw[8] = (raw[8] & 0x3f) | 0x80
	id, _ := uuid.FromBytes(raw)
	return id.String()
}

// ContinuityKey identifies a logical provider slot without using rotating
// connection locations such as server, IP or TLS SNI. Reality identifiers and
// transport host/path data are retained because providers commonly use them as
// the stable slot identifier. A caller must treat duplicate keys as ambiguous.
func ContinuityKey(p map[string]any) string {
	typ := strings.ToLower(text(p["type"]))
	parts := []string{"type:" + typ, "port:" + strconv.Itoa(integer(p["port"]))}
	if reality := nestedMap(p, "reality-opts", "reality_opts", "reality"); reality != nil {
		appendIdentityPart(&parts, "short-id", firstValue(reality, "short-id", "short_id"))
		appendIdentityPart(&parts, "public-key", firstValue(reality, "public-key", "public_key"))
	}
	appendIdentityPart(&parts, "network", p["network"])
	for _, spec := range []struct {
		keys   []string
		fields []string
	}{
		{[]string{"xhttp-opts", "xhttp_opts"}, []string{"host", "path", "mode"}},
		{[]string{"ws-opts", "ws_opts"}, []string{"path"}},
		{[]string{"grpc-opts", "grpc_opts"}, []string{"grpc-service-name", "service-name", "service_name"}},
		{[]string{"http-opts", "http_opts", "h2-opts", "h2_opts"}, []string{"host", "path"}},
		{[]string{"httpupgrade-opts", "httpupgrade_opts"}, []string{"host", "path"}},
	} {
		if opts := nestedMap(p, spec.keys...); opts != nil {
			for _, field := range spec.fields {
				appendIdentityPart(&parts, spec.keys[0]+"."+field, opts[field])
			}
			if headers := nestedMap(opts, "headers"); headers != nil {
				appendIdentityPart(&parts, spec.keys[0]+".host", firstValue(headers, "Host", "host"))
			}
		}
	}
	appendIdentityPart(&parts, "peer-public-key", firstValue(p, "peer-public-key", "peer_public_key", "public-key", "public_key"))
	return strings.Join(parts, "\x00")
}

func credentialKey(p map[string]any) string {
	parts := []string{}
	for _, field := range []string{"uuid", "password", "username", "private-key", "private_key", "pre-shared-key", "pre_shared_key"} {
		appendIdentityPart(&parts, field, p[field])
	}
	return strings.Join(parts, "\x00")
}

func appendIdentityPart(parts *[]string, label string, value any) {
	if valueText := strings.ToLower(text(value)); valueText != "" && valueText != "<nil>" {
		*parts = append(*parts, label+":"+valueText)
	}
}

func nestedMap(p map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value, ok := p[key].(map[string]any); ok {
			return value
		}
	}
	return nil
}

func firstValue(p map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := p[key]; ok {
			return value
		}
	}
	return nil
}

func HasCountry(name string) bool {
	if countryRE.FindStringIndex(name) != nil || naming.HasCountryHint(name) {
		return true
	}
	runes := []rune(name)
	for i := 1; i < len(runes); i++ {
		if runes[i-1] >= 0x1F1E6 && runes[i-1] <= 0x1F1FF && runes[i] >= 0x1F1E6 && runes[i] <= 0x1F1FF {
			return true
		}
	}
	return false
}
func Multiplier(name string) float64 {
	// A multiplier marker is required, so ordinary node numbers and bandwidths
	// are never treated as traffic ratios. Do not require a delimiter after the
	// marker: providers commonly append emoji, for example "1.5x🌟".
	normalized := strings.NewReplacer(
		"０", "0", "１", "1", "２", "2", "３", "3", "４", "4",
		"５", "5", "６", "6", "７", "7", "８", "8", "９", "9",
		"．", ".", "Ｘ", "x", "ｘ", "x",
	).Replace(name)
	m := multiplierSuffixRE.FindStringSubmatch(normalized)
	if len(m) < 2 {
		m = multiplierPrefixRE.FindStringSubmatch(normalized)
	}
	if len(m) < 2 {
		return 1
	}
	v, e := strconv.ParseFloat(m[1], 64)
	if e != nil || v <= 0 {
		return 1
	}
	return v
}
func text(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case fmt.Stringer:
		return strings.TrimSpace(x.String())
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}
func integer(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case uint64:
		return int(x)
	case float64:
		return int(x)
	case string:
		i, _ := strconv.Atoi(x)
		return i
	default:
		i, _ := strconv.Atoi(fmt.Sprint(v))
		return i
	}
}
func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if strings.EqualFold(k, "name") {
			continue
		}
		out[k] = normalize(v)
	}
	return out
}
func normalize(v any) any {
	switch x := v.(type) {
	case map[any]any:
		m := map[string]any{}
		for k, v := range x {
			m[fmt.Sprint(k)] = normalize(v)
		}
		return m
	case map[string]any:
		m := map[string]any{}
		for k, v := range x {
			m[k] = normalize(v)
		}
		return m
	case []any:
		a := make([]any, len(x))
		for i, v := range x {
			a[i] = normalize(v)
		}
		return a
	default:
		return v
	}
}

func SecretFingerprint(config map[string]any) string {
	h := sha256.New()
	for _, k := range []string{"password", "uuid", "token", "private-key", "psk"} {
		if v, ok := config[k]; ok {
			io.WriteString(h, k+"="+fmt.Sprint(v)+"\n")
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func SanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
