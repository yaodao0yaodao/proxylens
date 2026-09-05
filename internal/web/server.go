package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/yaodao0yaodao/proxylens/internal/dependency"
	"github.com/yaodao0yaodao/proxylens/internal/model"
	"github.com/yaodao0yaodao/proxylens/internal/service"
)

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	Service                            *service.Service
	AdminToken, LogPath, PublicBaseURL string
	Version                            string
	Log                                *slog.Logger
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	assets, _ := fs.Sub(staticFiles, "static")
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/local-token", s.localToken)
	mux.HandleFunc("GET /sub/{token}", s.subscription)
	mux.HandleFunc("GET /rules/{token}/{tag}", s.ruleSet)
	mux.Handle("/api/", s.requireAdmin(http.HandlerFunc(s.api)))
	return recoverer(s.Log, securityHeaders(mux))
}
func (s *Server) localToken(w http.ResponseWriter, r *http.Request) {
	remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteHost = r.RemoteAddr
	}
	remoteIP := net.ParseIP(strings.Trim(remoteHost, "[]"))
	local := remoteIP != nil && remoteIP.IsLoopback()
	if !local {
		if addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
			localHost, _, splitErr := net.SplitHostPort(addr.String())
			if splitErr == nil {
				localIP := net.ParseIP(strings.Trim(localHost, "[]"))
				local = localIP != nil && remoteIP != nil && localIP.Equal(remoteIP)
			}
		}
	}
	if !local {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, 200, map[string]string{"token": s.AdminToken})
}
func (s *Server) ruleSet(w http.ResponseWriter, r *http.Request) {
	if s.Service.Rules == nil {
		http.NotFound(w, r)
		return
	}
	if _, e := s.Service.Store.TaskByToken(r.Context(), r.PathValue("token")); e != nil {
		http.NotFound(w, r)
		return
	}
	b, st, e := s.Service.Rules.Read(r.PathValue("tag"))
	if e != nil {
		writeJSON(w, 503, map[string]string{"error": "rule set unavailable"})
		return
	}
	etag := `"` + st.SHA256 + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("Content-Type", "application/octet-stream")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(304)
		return
	}
	w.Write(b)
}
func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, _ := staticFiles.ReadFile("static/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b)
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "version": s.Version, "time": time.Now()})
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(s.AdminToken)) != 1 {
			writeJSON(w, 401, map[string]string{"error": "admin token required"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/api/")
	parts := strings.Split(clean, "/")
	ctx := r.Context()
	if clean == "tasks" && r.Method == http.MethodGet {
		s.listTasks(ctx, w)
		return
	}
	if clean == "tasks" && r.Method == http.MethodPost {
		s.createTask(ctx, w, r)
		return
	}
	if clean == "logs" && r.Method == http.MethodGet {
		s.logs(w, r)
		return
	}
	if clean == "backup" && r.Method == http.MethodGet {
		b, e := s.Service.Store.Backup()
		if e != nil {
			writeError(w, e)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.sqlite3")
		w.Header().Set("Content-Disposition", `attachment; filename="proxylens-backup.sqlite"`)
		w.Write(b)
		return
	}
	if clean == "storage" && r.Method == http.MethodGet {
		writeJSON(w, 200, databaseStorage(s.Service.Store.Path))
		return
	}
	if clean == "overview" && r.Method == http.MethodGet {
		s.overview(ctx, w)
		return
	}
	if clean == "version-info" && r.Method == http.MethodGet {
		s.versionInfo(ctx, w)
		return
	}
	if clean == "settings" && r.Method == http.MethodGet {
		writeJSON(w, 200, s.Service.GetSettings())
		return
	}
	if clean == "settings" && r.Method == http.MethodPut {
		var settings service.Settings
		if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&settings); e != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		if e := s.Service.UpdateSettings(ctx, settings); e != nil {
			writeJSON(w, 400, map[string]string{"error": e.Error()})
			return
		}
		if tasks, e := s.Service.Store.Tasks(ctx); e == nil {
			for _, task := range tasks {
				if task.Enabled {
					s.Service.CancelCurrent(task.ID)
					s.startTaskRun(task.ID)
				}
			}
		}
		writeJSON(w, 200, s.Service.GetSettings())
		return
	}
	if len(parts) >= 2 && parts[0] == "tasks" {
		id := parts[1]
		if len(parts) == 2 && r.Method == http.MethodPut {
			var request struct {
				Name            string `json:"name"`
				SubscriptionURL string `json:"subscription_url"`
				SubscriptionUA  string `json:"subscription_ua"`
			}
			if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); e != nil {
				writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
				return
			}
			task, e := s.Service.UpdateTaskDetails(ctx, id, request.Name, request.SubscriptionURL, request.SubscriptionUA)
			if e != nil {
				writeJSON(w, 400, map[string]string{"error": e.Error()})
				return
			}
			s.startTaskRun(id)
			writeJSON(w, 200, task)
			return
		}
		if len(parts) == 2 && r.Method == http.MethodDelete {
			_ = s.Service.Pause(ctx, id)
			if e := s.Service.Store.DeleteTask(ctx, id); e != nil {
				writeError(w, e)
				return
			}
			w.WriteHeader(204)
			return
		}
		if len(parts) == 3 && parts[2] == "run" && r.Method == http.MethodPost {
			_ = s.Service.Start(ctx, id)
			s.startTaskRun(id)
			writeJSON(w, 202, map[string]string{"status": "started"})
			return
		}
		if len(parts) == 3 && parts[2] == "continuous" && r.Method == http.MethodPost {
			go func() {
				runCtx, cancel := context.WithTimeout(context.Background(), 4*time.Hour+15*time.Minute)
				defer cancel()
				_ = s.Service.ContinuousRun(runCtx, id, 4*time.Hour)
			}()
			writeJSON(w, 202, map[string]string{"status": "started"})
			return
		}
		if len(parts) == 3 && parts[2] == "continuous" && r.Method == http.MethodDelete {
			if !s.Service.StopContinuousAfterCurrent(id) {
				writeJSON(w, 409, map[string]string{"error": "continuous detection is not running"})
				return
			}
			writeJSON(w, 200, map[string]string{"status": "stopping_after_current"})
			return
		}
		if len(parts) == 3 && parts[2] == "rotate-token" && r.Method == http.MethodPost {
			token, e := s.Service.RotatePublishToken(ctx, id)
			if e != nil {
				writeError(w, e)
				return
			}
			writeJSON(w, 200, map[string]string{"publish_token": token})
			return
		}
		if len(parts) == 3 && parts[2] == "pause" && r.Method == http.MethodPost {
			if e := s.Service.Pause(ctx, id); e != nil {
				writeError(w, e)
				return
			}
			writeJSON(w, 200, map[string]string{"status": "paused"})
			return
		}
		if len(parts) == 3 && parts[2] == "start" && r.Method == http.MethodPost {
			if e := s.Service.Start(ctx, id); e != nil {
				writeError(w, e)
				return
			}
			s.startTaskRun(id)
			writeJSON(w, 202, map[string]string{"status": "started"})
			return
		}
		if len(parts) == 3 && parts[2] == "nodes" && r.Method == http.MethodGet {
			s.nodes(ctx, w, id)
			return
		}
		if len(parts) == 3 && parts[2] == "identity-conflicts" && r.Method == http.MethodGet {
			items, e := s.Service.Store.IdentityConflicts(ctx, id)
			if e != nil {
				writeError(w, e)
				return
			}
			writeJSON(w, 200, items)
			return
		}
		if len(parts) == 5 && parts[2] == "identity-conflicts" && parts[4] == "keep-new" && r.Method == http.MethodPost {
			conflictID, e := strconv.ParseInt(parts[3], 10, 64)
			if e != nil {
				writeJSON(w, 400, map[string]string{"error": "invalid conflict id"})
				return
			}
			if e = s.Service.Store.ResolveIdentityConflict(ctx, id, conflictID); e != nil {
				writeJSON(w, 400, map[string]string{"error": e.Error()})
				return
			}
			writeJSON(w, 200, map[string]string{"status": "resolved"})
			return
		}
		if len(parts) == 3 && parts[2] == "merge-nodes" && r.Method == http.MethodPost {
			var request struct {
				TargetID string `json:"target_id"`
				SourceID string `json:"source_id"`
			}
			if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); e != nil {
				writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
				return
			}
			if e := s.Service.MergeNodes(ctx, id, request.TargetID, request.SourceID); e != nil {
				writeJSON(w, 400, map[string]string{"error": e.Error()})
				return
			}
			writeJSON(w, 200, map[string]string{"status": "merged"})
			return
		}
		if len(parts) == 5 && parts[2] == "nodes" && parts[4] == "measurements" && r.Method == http.MethodGet {
			nodeID := parts[3]
			nodes, e := s.Service.Store.Nodes(ctx, id, false)
			if e != nil {
				writeError(w, e)
				return
			}
			allowed := false
			for _, n := range nodes {
				if n.ID == nodeID {
					allowed = true
					break
				}
			}
			if !allowed {
				writeJSON(w, 404, map[string]string{"error": "node not found"})
				return
			}
			m, e := s.Service.Store.Measurements(ctx, nodeID, time.Now().Add(-90*24*time.Hour))
			if e != nil {
				writeError(w, e)
				return
			}
			writeJSON(w, 200, m)
			return
		}
		if len(parts) == 3 && parts[2] == "settings" && r.Method == http.MethodGet {
			v, e := s.Service.TaskSettings(ctx, id)
			if e != nil {
				writeError(w, e)
				return
			}
			writeJSON(w, 200, v)
			return
		}
		if len(parts) == 3 && parts[2] == "settings" && r.Method == http.MethodPut {
			var request struct {
				service.TaskSettings
				Name            string `json:"name"`
				SubscriptionURL string `json:"subscription_url"`
				SubscriptionUA  string `json:"subscription_ua"`
			}
			if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); e != nil {
				writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
				return
			}
			if strings.TrimSpace(request.SubscriptionURL) != "" {
				if _, e := s.Service.UpdateTaskDetails(ctx, id, request.Name, request.SubscriptionURL, request.SubscriptionUA); e != nil {
					writeJSON(w, 400, map[string]string{"error": e.Error()})
					return
				}
			}
			// These legacy advanced fields are intentionally hidden from the
			// simplified form; preserve them instead of silently clearing them.
			if current, currentErr := s.Service.TaskSettings(ctx, id); currentErr == nil {
				request.LANListen = current.LANListen
				request.LANUsername = current.LANUsername
				request.LANPassword = current.LANPassword
			}
			if e := s.Service.UpdateTaskSettings(ctx, id, request.TaskSettings); e != nil {
				writeJSON(w, 400, map[string]string{"error": e.Error()})
				return
			}
			_ = s.Service.Start(ctx, id)
			s.Service.CancelCurrent(id)
			s.startTaskRun(id)
			writeJSON(w, 200, request.TaskSettings)
			return
		}
	}
	writeJSON(w, 404, map[string]string{"error": "not found"})
}
func (s *Server) listTasks(ctx context.Context, w http.ResponseWriter) {
	tasks, e := s.Service.Store.Tasks(ctx)
	if e != nil {
		writeError(w, e)
		return
	}
	type view struct {
		model.Task
		Runtime       service.Runtime `json:"runtime"`
		SingBoxURL    string          `json:"sing_box_url"`
		Notices       []model.Notice  `json:"notices"`
		SourceURL     string          `json:"source_url"`
		LastSingBoxAt time.Time       `json:"last_sing_box_at,omitempty"`
	}
	out := make([]view, 0, len(tasks))
	for _, t := range tasks {
		sourceURL := t.SubscriptionURL
		t.SubscriptionURL = redactURL(t.SubscriptionURL)
		base := strings.TrimSuffix(s.PublicBaseURL, "/")
		notices, _ := s.Service.Store.Notices(ctx, t.ID)
		lastConfig := t.LastSFAAt
		if t.LastCartonAt.After(lastConfig) {
			lastConfig = t.LastCartonAt
		}
		out = append(out, view{Task: t, Runtime: s.Service.Runtime(t), SingBoxURL: base + "/sub/" + t.PublishToken, Notices: notices, SourceURL: sourceURL, LastSingBoxAt: lastConfig})
	}
	writeJSON(w, 200, out)
}

func (s *Server) startTaskRun(id string) {
	s.Service.QueueRun(context.Background(), id, 6*time.Hour)
}

func (s *Server) overview(ctx context.Context, w http.ResponseWriter) {
	var alerts []string
	seen := map[string]bool{}
	for _, key := range []string{"rules_important_alert", "process_rules_important_alert"} {
		if value, err := s.Service.Store.Setting(ctx, key); err == nil {
			value = strings.TrimSpace(value)
			if value != "" && !seen[value] {
				seen[value] = true
				alerts = append(alerts, value)
			}
		}
	}
	writeJSON(w, 200, map[string]any{"storage": databaseStorage(s.Service.Store.Path), "alerts": alerts})
}

func (s *Server) versionInfo(ctx context.Context, w http.ResponseWriter) {
	fileTime := func(filename string) time.Time {
		if info, err := os.Stat(filename); err == nil {
			return info.ModTime()
		}
		return time.Time{}
	}
	executable, _ := os.Executable()
	coreVersion := ""
	if s.Service.Probe != nil {
		if value, err := dependency.Version(ctx, s.Service.Probe.SingBoxPath); err == nil {
			coreVersion = value
		}
	}
	ruleItems := []any{}
	if s.Service.Rules != nil {
		for _, status := range s.Service.Rules.Statuses() {
			version := status.Source.Version
			if len(status.SHA256) >= 12 {
				version += " · " + status.SHA256[:12]
			}
			ruleItems = append(ruleItems, map[string]any{"tag": status.Source.Tag, "version": version, "updated_at": status.UpdatedAt, "error": status.LastError, "source_type": "上游原版规则文件"})
		}
	}
	if updated, err := s.Service.Store.Setting(ctx, "download_processes_last_update"); err == nil {
		at, _ := time.Parse(time.RFC3339Nano, updated)
		version := "blackmatrix7/ios_rule_script"
		if s.Service.Rules != nil {
			if content, readErr := os.ReadFile(path.Join(s.Service.Rules.Dir, "download-processes.json")); readErr == nil {
				sum := sha256.Sum256(content)
				version += " · " + hex.EncodeToString(sum[:6])
			}
		}
		ruleItems = append(ruleItems, map[string]any{"tag": "download-processes", "version": version, "updated_at": at, "source_type": "上游原版应用列表"})
	}
	databaseSchema := 0
	_ = s.Service.Store.DB.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&databaseSchema)
	build := map[string]any{"go_version": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "database_schema": databaseSchema}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				build["revision"] = setting.Value
			case "vcs.time":
				build["revision_time"] = setting.Value
			case "vcs.modified":
				build["modified"] = setting.Value == "true"
			}
		}
	}
	settingTime := func(key string) time.Time {
		value, err := s.Service.Store.Setting(ctx, key)
		if err != nil {
			return time.Time{}
		}
		at, _ := time.Parse(time.RFC3339Nano, value)
		return at
	}
	writeJSON(w, 200, map[string]any{
		"software":               map[string]any{"version": s.Version, "updated_at": fileTime(executable)},
		"sing_box":               map[string]any{"version": coreVersion},
		"rule_maintenance":       map[string]any{"last_success_at": settingTime("rules_last_update"), "last_attempt_at": settingTime("rules_last_attempt")},
		"build":                  build,
		"rules":                  ruleItems,
		"routing_customizations": routingCustomizations(),
		"compatibility":          clientCompatibility(),
		"input_compatibility": map[string]any{
			"format":     "Clash/Mihomo YAML 订阅",
			"fetch_ua":   "clash.meta（任务记录可指定其他 UA）",
			"protocols":  []string{"Shadowsocks", "VMess", "VLESS", "Trojan", "Hysteria2", "TUIC", "AnyTLS", "WireGuard", "SOCKS", "HTTP"},
			"transports": []string{"TCP", "WebSocket", "gRPC", "HTTPUpgrade", "HTTP/2"},
		},
	})
}

func routingCustomizations() []map[string]string {
	return []map[string]string{
		{"name": "中国大陆与私有网络", "scope": "全端", "based_on": "MetaCubeX geosite/geoip", "behavior": "私有地址和中国大陆域名/IP 直连；已知非中国域名优先代理，避免被错误 GeoIP 结果覆盖。"},
		{"name": "日本限定服务", "scope": "全端", "based_on": "MetaCubeX Abema、DMM、Niconico、Pixiv、TVer、Radiko、NHK", "behavior": "置于最高业务优先级，统一交给日本自动选择。"},
		{"name": "Google Play", "scope": "SFA/Android 为主，全端可用", "based_on": "MetaCubeX Google Play 与 Google Play@cn", "behavior": "稳定模式全部代理；节省模式仅中国 CDN 直连，其余代理；services.googleapis.cn 强制代理。"},
		{"name": "Steam", "scope": "桌面端", "based_on": "MetaCubeX Steam 与游戏平台下载", "behavior": "登录、商店、社区代理；steamcontent.com 和游戏下载域名直连并使用本地 DNS，让 CDN 地点跟随用户网络。"},
		{"name": "Microsoft Store 与 Windows Update", "scope": "桌面端", "based_on": "Microsoft 官方必需端点，ProxyLens 修正", "behavior": "商店区域/下载位置接口、安装包 CDN、Delivery Optimization 和 Windows Update 直连并使用本地 DNS；账号登录、购买、授权、商品目录及其他商店服务继续代理。"},
		{"name": "TUN 本地下载应用", "scope": "Carton、原生 sing-box Windows/Linux", "based_on": "内置跨平台进程名 + blackmatrix7/ios_rule_script Download.list", "behavior": "TUN 模式下，迅雷、qBittorrent、aria2、Transmission、µTorrent、BitComet、FDM、WebTorrent 等本地下载程序按进程直连。Android 不应用进程名规则。"},
		{"name": "TUN 与局域网代理入站", "scope": "全端", "based_on": "ProxyLens 自定义", "behavior": "始终生成 IPv4/IPv6 TUN；桌面配置启用 strict_route，明确的 Linux/CachyOS UA 额外启用 auto_redirect。按全局或任务设置生成可选 mixed 局域网代理入站、监听端口和账号认证。"},
		{"name": "DNS 分流", "scope": "全端", "based_on": "ProxyLens 自定义", "behavior": "国内/私有/下载 CDN 使用本地 DNS，境外与代理业务使用远程 DNS；优先 IPv4，避免错误 IPv6 路径影响体验。"},
		{"name": "订阅自访问防回环", "scope": "全端", "based_on": "ProxyLens 自定义", "behavior": "ProxyLens 公网/DDNS 订阅域名直连并用本地 DNS；局域网访问订阅时自动改写为路由器局域网规则地址。"},
		{"name": "规则启动与缓存", "scope": "全端", "based_on": "MetaCubeX 原版 SRS，由 ProxyLens 缓存/转发", "behavior": "避免 raw.githubusercontent.com 在大陆网络被错误解析；1.13 使用 DIRECT download_detour，1.14+ 使用显式 HTTP Client，防止首次启动死循环。"},
		{"name": "节点分组与质量选择", "scope": "全端", "based_on": "ProxyLens 自定义", "behavior": "生成代理选择、普通/中费/高费/随便用/全部节点、自动选择、日本自动选择及提示组；按出口国家、倍率、可用率与延迟动态更新。"},
		{"name": "运行故障提示", "scope": "全端", "based_on": "ProxyLens 自定义", "behavior": "订阅长期更新失败等重要故障会变成置顶提示组；套餐通知使用不可误触的直通假节点展示。"},
	}
}

func clientCompatibility() []map[string]string {
	return []map[string]string{
		{"client": "SFA / sing-box for Android 1.14.x", "ua": "SFA/1.14… 或 sing-box/1.14… Android", "output": "SFA 1.14 配置", "notes": "Android VPN/TUN；不含桌面进程名分流。"},
		{"client": "SFA / sing-box for Android 1.13", "ua": "SFA/1.13… 或 sing-box/1.13… Android", "output": "SFA 1.13 配置", "notes": "使用 1.13 规则下载字段。"},
		{"client": "Carton（Windows/Linux）1.14.x", "ua": "Carton/… sing-box/1.14…", "output": "桌面 1.14 配置", "notes": "含 TUN 本地下载应用直连。"},
		{"client": "Carton（未报告核心版本或 1.13）", "ua": "Carton/…", "output": "桌面 1.13 配置", "notes": "为兼容 Carton 默认 UA，安全回退到 1.13。"},
		{"client": "原生 sing-box CLI（Windows/Linux）1.14.x", "ua": "sing-box/1.14… Linux 或 Windows", "output": "复用桌面 1.14 配置", "notes": "需管理员/root 权限运行 TUN；Linux/CachyOS UA 自动启用 auto_redirect。"},
		{"client": "原生 sing-box CLI（Windows/Linux）1.13", "ua": "sing-box/1.13… Linux 或 Windows", "output": "复用桌面 1.13 配置", "notes": "需管理员/root 权限运行 TUN；Linux/CachyOS UA 自动启用 auto_redirect。"},
		{"client": "Mihomo / Clash 客户端", "ua": "任意", "output": "不提供输出配置", "notes": "Mihomo/Clash YAML 仅作为输入订阅格式。"},
		{"client": "sing-box for Apple platforms", "ua": "SFI/SFM 等", "output": "当前不提供", "notes": "尚未生成 Apple 平台专用配置，也未做实机验收。"},
		{"client": "无法识别或未经验证的版本", "ua": "缺少 UA/版本，版本 < 1.13 或 > 1.14", "output": "HTTP 406，不发送配置", "notes": "避免客户端下载到不兼容格式；新版本经验证后再放行。"},
	}
}

func (s *Server) createTask(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var in struct {
		service.TaskSettings
		Name            string `json:"name"`
		SubscriptionURL string `json:"subscription_url"`
		SubscriptionUA  string `json:"subscription_ua"`
	}
	if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); e != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
		return
	}
	t, e := s.Service.CreateTask(ctx, in.Name, in.SubscriptionURL, in.SubscriptionUA)
	if e != nil {
		writeError(w, e)
		return
	}
	if e = s.Service.UpdateTaskSettings(ctx, t.ID, in.TaskSettings); e != nil {
		_ = s.Service.Store.DeleteTask(ctx, t.ID)
		writeJSON(w, 400, map[string]string{"error": e.Error()})
		return
	}
	s.startTaskRun(t.ID)
	t.SubscriptionURL = redactURL(t.SubscriptionURL)
	writeJSON(w, 201, t)
}
func (s *Server) nodes(ctx context.Context, w http.ResponseWriter, id string) {
	nodes, e := s.Service.Store.Nodes(ctx, id, false)
	if e != nil {
		writeError(w, e)
		return
	}
	q, e := s.Service.Store.Qualities(ctx, id)
	if e != nil {
		writeError(w, e)
		return
	}
	type item struct {
		Node    model.Node    `json:"node"`
		Quality model.Quality `json:"quality"`
	}
	out := make([]item, 0, len(nodes))
	for _, n := range nodes {
		n.Config = nil
		out = append(out, item{n, q[n.ID]})
	}
	writeJSON(w, 200, out)
}
func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="proxylens-logs.txt"`)
	wrote := false
	for _, filename := range []string{s.LogPath + ".1", s.LogPath} {
		content, e := os.ReadFile(filename)
		if e != nil {
			if errors.Is(e, os.ErrNotExist) {
				continue
			}
			writeError(w, e)
			return
		}
		fmt.Fprintf(w, "===== %s =====\n", path.Base(filename))
		_, _ = w.Write(content)
		_, _ = w.Write([]byte("\n"))
		wrote = true
	}
	if !wrote {
		writeJSON(w, 404, map[string]string{"error": "no log file"})
	}
}

func databaseStorage(dbPath string) map[string]any {
	result := map[string]any{"database_path": dbPath}
	var used int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if info, err := os.Stat(dbPath + suffix); err == nil {
			used += info.Size()
		}
	}
	result["database_bytes"] = used
	free, err := partitionFree(dbPath)
	if err != nil {
		result["free_error"] = err.Error()
	} else {
		result["partition_free_bytes"] = free
	}
	return result
}
func (s *Server) subscription(w http.ResponseWriter, r *http.Request) {
	t, e := s.Service.Store.TaskByToken(r.Context(), r.PathValue("token"))
	if e != nil {
		http.NotFound(w, r)
		return
	}
	kind := artifactForUA(r.UserAgent())
	if kind == "" {
		w.Header().Set("Accept-User-Agent", "Carton/*, SFA/1.13.x or 1.14.x, sing-box/1.13.x or 1.14.x (Android or Windows/Linux CLI)")
		writeJSON(w, 406, map[string]string{"error": "unsupported or missing client User-Agent"})
		return
	}
	b, updated, e := s.Service.Store.Artifact(r.Context(), t.ID, kind)
	if e != nil {
		writeJSON(w, 503, map[string]string{"error": "configuration has not been generated yet"})
		return
	}
	b = rewriteRuleBaseForLAN(b, s.PublicBaseURL, r)
	b, e = enableLinuxAutoRedirect(b, kind, r.UserAgent())
	if e != nil {
		if s.Log != nil {
			s.Log.Error("prepare linux subscription", "task", t.ID, "error", e)
		}
		writeJSON(w, 500, map[string]string{"error": "failed to prepare Linux configuration"})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s-%s.json"`, safeFilename(t.Name), kind))
	w.Header().Set("Last-Modified", updated.UTC().Format(http.TimeFormat))
	sum := sha256.Sum256(b)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Vary", "User-Agent")
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(b)
}

// enableLinuxAutoRedirect applies the sing-box recommendation only when the
// requesting desktop client explicitly identifies Linux. auto_redirect is not
// a harmless no-op on Windows: official Windows cores reject it during TUN
// initialization. Android UAs may also contain "Linux", so the selected
// artifact must be a desktop artifact as an additional guard.
func enableLinuxAutoRedirect(content []byte, kind, ua string) ([]byte, error) {
	lower := strings.ToLower(ua)
	if !strings.HasPrefix(kind, "carton") || strings.Contains(lower, "android") ||
		(!strings.Contains(lower, "linux") && !strings.Contains(lower, "cachyos")) {
		return content, nil
	}
	var config map[string]any
	if err := json.Unmarshal(content, &config); err != nil {
		return nil, err
	}
	inbounds, ok := config["inbounds"].([]any)
	if !ok {
		return content, nil
	}
	changed := false
	for _, value := range inbounds {
		inbound, ok := value.(map[string]any)
		if !ok || inbound["type"] != "tun" {
			continue
		}
		inbound["auto_route"] = true
		inbound["auto_redirect"] = true
		changed = true
	}
	if !changed {
		return content, nil
	}
	result, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(result, '\n'), nil
}

func rewriteRuleBaseForLAN(content []byte, publicBase string, r *http.Request) []byte {
	publicBase = strings.TrimSuffix(strings.TrimSpace(publicBase), "/")
	if publicBase == "" || !requestFromPrivateNetwork(r.RemoteAddr) {
		return content
	}
	localAddr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok || localAddr == nil {
		return content
	}
	host, port, err := net.SplitHostPort(localAddr.String())
	if err != nil {
		return content
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() {
		return content
	}
	localBase := "http://" + net.JoinHostPort(host, port)
	return bytes.ReplaceAll(content, []byte(publicBase+"/rules/"), []byte(localBase+"/rules/"))
}

func requestFromPrivateNetwork(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback())
}

var coreVersionRE = regexp.MustCompile(`(?i)(?:sing-box|sfa)[ /]v?([0-9]+)\.([0-9]+)`)

func artifactForUA(ua string) string {
	lower := strings.ToLower(ua)
	m := coreVersionRE.FindStringSubmatch(ua)
	major, minor := 0, 0
	if len(m) == 3 {
		major, _ = strconv.Atoi(m[1])
		minor, _ = strconv.Atoi(m[2])
	}
	modern := major == 1 && minor == 14
	supported := major == 1 && (minor == 13 || minor == 14)
	if strings.Contains(lower, "carton/") {
		if len(m) == 3 && !supported {
			return ""
		}
		if modern {
			return "carton-1.14"
		}
		return "carton"
	} // Carton's safe default bundled core is 1.13.
	if strings.Contains(lower, "sfa") || strings.Contains(lower, "sing-box") && strings.Contains(lower, "android") {
		if !supported {
			return ""
		}
		if modern {
			return "sfa"
		}
		return "sfa-1.13"
	}
	if strings.Contains(lower, "sing-box") && supported {
		if modern {
			return "carton-1.14"
		}
		return "carton"
	}
	return ""
}

func redactURL(raw string) string {
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		return raw[:i] + "?[已隐藏]"
	}
	return raw
}
func safeFilename(s string) string {
	return strings.Map(func(r rune) rune {
		if r > 127 || r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return r
		}
		return '_'
	}, s)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, e error) {
	writeJSON(w, 500, map[string]string{"error": e.Error()})
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}
func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Error("http panic", "panic", v)
				writeJSON(w, 500, map[string]string{"error": "internal error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
