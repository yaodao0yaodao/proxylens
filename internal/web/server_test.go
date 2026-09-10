package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArtifactForUA(t *testing.T) {
	cases := map[string]string{"SFA/1.13.21": "sfa-1.13", "SFA/1.14.0-rc.2": "sfa", "Carton/0.5.2": "carton", "Carton/0.5.2 sing-box/1.14.1": "carton-1.14", "Carton/0.5.2 sing-box/1.12.9": "", "Carton/0.5.2 sing-box/1.15.0": "", "sing-box/1.13.9": "carton", "sing-box/1.14.2": "carton-1.14", "sing-box/1.14.2 Android": "sfa", "sing-box/1.12.9": "", "sing-box/1.15.0": "", "SFA/1.12.0": "", "Mozilla/5.0": ""}
	for ua, want := range cases {
		if got := artifactForUA(ua); got != want {
			t.Errorf("ua %q got %q want %q", ua, got, want)
		}
	}
}

func TestLinuxDesktopSubscriptionEnablesAutoRedirect(t *testing.T) {
	input := []byte(`{"inbounds":[{"type":"tun","tag":"tun-in","auto_route":true},{"type":"mixed"}]}`)
	for _, tc := range []struct {
		name, kind, ua string
		want           bool
	}{
		{"linux core", "carton-1.14", "sing-box/1.14.2 Linux", true},
		{"cachyos core", "carton", "sing-box/1.13.19 CachyOS", true},
		{"linux carton", "carton-1.14", "Carton/0.5 sing-box/1.14.2 Linux", true},
		{"windows core", "carton-1.14", "sing-box/1.14.2 Windows", false},
		{"unknown desktop", "carton-1.14", "sing-box/1.14.2", false},
		{"android linux token", "sfa", "sing-box/1.14.2 Linux Android", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := enableLinuxAutoRedirect(input, tc.kind, tc.ua)
			if err != nil {
				t.Fatal(err)
			}
			var config struct {
				Inbounds []map[string]any `json:"inbounds"`
			}
			if err = json.Unmarshal(got, &config); err != nil {
				t.Fatal(err)
			}
			enabled, _ := config.Inbounds[0]["auto_redirect"].(bool)
			if enabled != tc.want {
				t.Fatalf("auto_redirect=%v want %v: %s", enabled, tc.want, got)
			}
			excluded, _ := config.Inbounds[0]["route_exclude_address_set"].([]any)
			if tc.want && (len(excluded) != 1 || excluded[0] != "geoip-cn") {
				t.Fatalf("Linux profile must exclude geoip-cn from the TUN: %#v", config.Inbounds[0]["route_exclude_address_set"])
			}
			if !tc.want && config.Inbounds[0]["route_exclude_address_set"] != nil {
				t.Fatalf("non-Linux profile unexpectedly received route exclusion: %#v", config.Inbounds[0]["route_exclude_address_set"])
			}
		})
	}
}

func TestVersionDescriptionsCoverCustomRoutingAndRawCore(t *testing.T) {
	custom := fmt.Sprint(routingCustomizations())
	for _, want := range []string{"TUN 本地下载应用", "TUN 与局域网代理入站", "auto_redirect", "Google Play", "Steam", "规则启动与缓存"} {
		if !strings.Contains(custom, want) {
			t.Fatalf("custom routing description is missing %q", want)
		}
	}
	compatibility := fmt.Sprint(clientCompatibility())
	if !strings.Contains(compatibility, "原生 sing-box CLI") || !strings.Contains(compatibility, "sing-box/1.14") || !strings.Contains(compatibility, "Apple") || !strings.Contains(compatibility, "Mihomo") {
		t.Fatal("raw sing-box compatibility or UA is missing")
	}
}

func TestRewriteRuleBaseForLAN(t *testing.T) {
	content := []byte(`{"url":"http://public.example:19686/rules/token/geosite-cn"}`)
	request := httptest.NewRequest(http.MethodGet, "http://public.example:19686/sub/token", nil)
	request.RemoteAddr = "192.168.10.210:54321"
	ctx := context.WithValue(request.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("192.168.10.1"), Port: 9099})
	request = request.WithContext(ctx)
	got := string(rewriteRuleBaseForLAN(content, "http://public.example:19686", request))
	if !strings.Contains(got, "http://192.168.10.1:9099/rules/token/geosite-cn") {
		t.Fatalf("LAN rule base was not rewritten: %s", got)
	}

	request.RemoteAddr = "203.0.113.10:54321"
	got = string(rewriteRuleBaseForLAN(content, "http://public.example:19686", request))
	if got != string(content) {
		t.Fatalf("public client rule base was unexpectedly rewritten: %s", got)
	}
}

func TestLocalTokenIsOnlyReturnedToTheHostMachine(t *testing.T) {
	s := &Server{AdminToken: "secret-token"}
	h := s.Handler()
	local := httptest.NewRequest(http.MethodGet, "http://localhost/api/local-token", nil)
	local.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, local)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "secret-token") {
		t.Fatalf("local response=%d %s", w.Code, w.Body.String())
	}
	remote := httptest.NewRequest(http.MethodGet, "http://router/api/local-token", nil)
	remote.RemoteAddr = "192.168.10.20:12345"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, remote)
	if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "secret-token") {
		t.Fatalf("remote response leaked token: %d %s", w.Code, w.Body.String())
	}
}
