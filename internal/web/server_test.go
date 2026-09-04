package web

import (
	"context"
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

func TestVersionDescriptionsCoverCustomRoutingAndRawCore(t *testing.T) {
	custom := fmt.Sprint(routingCustomizations())
	for _, want := range []string{"TUN 本地下载应用", "TUN 与局域网代理入站", "Google Play", "Steam", "规则启动与缓存"} {
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
