package subscription

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yaodao0yaodao/proxylens/internal/model"
)

func TestParseSubscriptionUserInfo(t *testing.T) {
	now := time.Unix(100, 0)
	m := ParseUserInfo("upload=10; download=20, total=100; expire=200", now)
	if m.Upload != 10 || m.Download != 20 || m.Total != 100 || m.Expire.Unix() != 200 || !m.CollectedAt.Equal(now) {
		t.Fatalf("metadata=%+v", m)
	}
	m = ParseUserInfo("upload=-1; download=nope; total=50; broken", now)
	if m.Upload != 0 || m.Download != 0 || m.Total != 50 || !m.Expire.IsZero() {
		t.Fatalf("malformed metadata=%+v", m)
	}
}

func TestEffectiveNoticesUsesAccountMetadataOnlyForEmptyPlaceholder(t *testing.T) {
	metadata := ParseUserInfo("upload=1048576; download=2097152; total=1073741824; expire=2000000000; reset_day=15", time.Now())
	notices := EffectiveNotices([]model.Notice{{Name: "套餐通知：暂无"}}, metadata)
	joined := notices[0].Name
	for _, notice := range notices[1:] {
		joined += ";" + notice.Name
	}
	if !strings.Contains(joined, "机场账号：3.00 MiB / 1.00 GiB") || !strings.Contains(joined, "到期时间") || !strings.Contains(joined, "每月 15 日重置") {
		t.Fatalf("notices=%#v", notices)
	}
	kept := EffectiveNotices([]model.Notice{{Name: "维护通知"}}, metadata)
	if len(kept) != 1 || kept[0].Name != "维护通知" {
		t.Fatalf("real notice replaced: %#v", kept)
	}
}

func TestFetcherReturnsBodyAndMetadata(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.UserAgent() != "clash.meta" {
			t.Errorf("ua=%q", r.UserAgent())
		}
		w.Header().Set("Subscription-Userinfo", "upload=1; download=2; total=3")
		_, _ = w.Write([]byte("proxies: []"))
	}))
	defer ts.Close()
	r, err := NewFetcher().FetchWithMetadata(context.Background(), ts.URL, "clash.meta")
	if err != nil {
		t.Fatal(err)
	}
	if string(r.Body) != "proxies: []" || r.Metadata.Total != 3 {
		t.Fatalf("result=%+v", r)
	}
}

func TestParseFiltersNoticesAndKeepsIdentityAcrossKeyRotation(t *testing.T) {
	a := []byte("proxies:\n  - {name: 日本 01 0.5x, type: ss, server: jp.example, port: 443, cipher: aes-128-gcm, password: old}\n  - {name: 剩余流量 100 GB, type: ss, server: info, port: 1, cipher: x, password: x}\n")
	b := []byte("proxies:\n  - {name: 日本 01 0.5x, type: ss, server: jp.example, port: 443, cipher: aes-128-gcm, password: new}\n")
	nodes, notices, e := Parse("task", a)
	if e != nil {
		t.Fatal(e)
	}
	if len(nodes) != 1 || len(notices) != 1 {
		t.Fatalf("nodes=%d notices=%d", len(nodes), len(notices))
	}
	if nodes[0].Multiplier != 0.5 {
		t.Fatalf("multiplier=%v", nodes[0].Multiplier)
	}
	nodes2, _, e := Parse("task", b)
	if e != nil {
		t.Fatal(e)
	}
	if nodes[0].ID != nodes2[0].ID {
		t.Fatal("credential rotation changed node ID")
	}
}

func TestServerAndSNIRotationDoNotChangeNodeID(t *testing.T) {
	a := []byte("proxies:\n  - {name: 香港 01, type: anytls, server: old.example, port: 27002, sni: old.example, password: shared}\n")
	b := []byte("proxies:\n  - {name: 香港 01, type: anytls, server: new.example, port: 27002, sni: new.example, password: shared}\n")
	first, _, err := Parse("task", a)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := Parse("task", b)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].ID != second[0].ID {
		t.Fatalf("rotating server changed logical node ID: %s != %s", first[0].ID, second[0].ID)
	}
}

func TestDuplicateRealityIdentityUsesStableNameDiscriminator(t *testing.T) {
	data := []byte("proxies:\n  - {name: 美国 01, type: vless, server: a.example, port: 443, uuid: shared, reality-opts: {short-id: aa, public-key: pk}}\n  - {name: 美国 02, type: vless, server: b.example, port: 443, uuid: shared, reality-opts: {short-id: aa, public-key: pk}}\n")
	nodes, _, err := Parse("task", data)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].ID == nodes[1].ID {
		t.Fatalf("duplicate Reality slots were merged: %+v", nodes)
	}
}

func TestMultiplierProviderNameFormats(t *testing.T) {
	tests := []struct {
		name string
		want float64
	}{
		{"🇺🇸US01 / 1.5x🌟", 1.5},
		{"🇭🇰HK08 / 5.0x🏠", 5},
		{"🇯🇵JP08 / 4.0X🌟", 4},
		{"🇦🇺AU01 / 0.5×🌟", 0.5},
		{"🇩🇪 [随便用] 德国 0.1倍率", 0.1},
		{"日本 倍率：2.5", 2.5},
		{"新加坡 ０．２Ｘ✨", 0.2},
		{"US01 1000Mbps", 1},
		{"剩余流量 100 GB", 1},
		{"x25519 日本", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Multiplier(tt.name); got != tt.want {
				t.Fatalf("Multiplier(%q)=%v want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestDetectNameUsesMetadataThenHostname(t *testing.T) {
	if got := DetectName([]byte("title: 测试机场\nproxies: []\n"), "https://fallback.example/sub"); got != "测试机场" {
		t.Fatalf("metadata name=%q", got)
	}
	if got := DetectName([]byte("proxies: []\n"), "https://www.popcornlab.de/api/sub"); got != "popcornlab.de" {
		t.Fatalf("hostname name=%q", got)
	}
	if got := DetectName([]byte("name: Config\nproxies: []\n"), "https://cn-sub.example/sub", "Config.yaml"); got != "cn-sub.example" {
		t.Fatalf("generic filename hid provider hostname: %q", got)
	}
}
