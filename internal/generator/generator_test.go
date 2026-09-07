package generator

import (
	"encoding/json"
	"fmt"
	"github.com/yaodao0yaodao/proxylens/internal/model"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateHasNoNullOutboundsAndClientSpecificDownloadRule(t *testing.T) {
	nodes := []model.Node{{ID: "a", Number: 1, Protocol: "ss", Server: "example.com", Port: 443, Country: "日本", CountryCode: "JP", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "secret"}}}
	q := map[string]model.Quality{"a": {NodeID: "a", Priority: 80, Availability: .9}}
	sfa, e := Generate(SFA, nodes, []model.Notice{{Name: "剩余 10 GB"}}, q)
	if e != nil {
		t.Fatal(e)
	}
	carton, e := Generate(Carton, nodes, nil, q)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(sfa.Content), `"outbounds": [\n    null`) {
		t.Fatal("null outbound generated")
	}
	if strings.Contains(string(sfa.Content), "qbittorrent") || !strings.Contains(string(carton.Content), "qbittorrent") {
		t.Fatal("download process rule is not Carton-only")
	}
	for _, process := range []string{"steam", "steamcmd", "steamwebhelper", "Steam.exe", "transmission-gtk", "fdm.bin", "webtorrent-desktop"} {
		if process == "steamwebhelper" {
			if strings.Contains(string(carton.Content), process) {
				t.Fatal("Steam process-level DIRECT would bypass login/store/community proxy routing")
			}
			continue
		}
		if process == "Steam.exe" {
			if strings.Contains(string(carton.Content), process) {
				t.Fatal("Windows Steam process must be handled by the Windows system-proxy integration")
			}
			continue
		}
		if !strings.Contains(string(carton.Content), process) {
			t.Fatalf("Carton Linux download process %q missing", process)
		}
	}
	if strings.Count(string(carton.Content), `"download_detour": "DIRECT"`) != 16 {
		t.Fatal("Carton 1.13 rule-sets must explicitly bootstrap through DIRECT")
	}
	if strings.Contains(string(sfa.Content), `"download_detour"`) ||
		!strings.Contains(string(sfa.Content), `"default_http_client": "rule-set-http"`) ||
		!strings.Contains(string(sfa.Content), `"http_clients"`) {
		t.Fatal("SFA 1.14 must use HTTP clients instead of legacy download_detour")
	}
	if strings.Contains(string(carton.Content), `"http_clients"`) ||
		strings.Contains(string(carton.Content), `"default_http_client"`) {
		t.Fatal("Carton 1.13 must not receive sing-box 1.14 HTTP client fields")
	}
	if strings.Contains(string(sfa.Content), `"independent_cache"`) || strings.Contains(string(carton.Content), `"independent_cache"`) {
		t.Fatal("deprecated independent_cache must not be generated")
	}
	if strings.Contains(string(sfa.Content), `"ip_version": 6`) ||
		strings.Count(string(sfa.Content), `"strategy": "prefer_ipv4"`) != 1 ||
		strings.Count(string(carton.Content), `"strategy": "prefer_ipv4"`) != 1 {
		t.Fatal("both clients must prefer IPv4 without forcing all IPv6 through an overseas proxy")
	}
	if strings.Count(string(sfa.Content), `"listen": "0.0.0.0"`) != 1 ||
		strings.Count(string(carton.Content), `"listen": "0.0.0.0"`) != 1 ||
		!strings.Contains(string(sfa.Content), `"listen_port": 2080`) ||
		!strings.Contains(string(carton.Content), `"listen_port": 2080`) {
		t.Fatal("SFA and Carton must publish the mixed proxy on LAN port 2080")
	}
	var v map[string]any
	if e = json.Unmarshal(sfa.Content, &v); e != nil {
		t.Fatal(e)
	}
	rules := v["route"].(map[string]any)["rules"].([]any)
	positions := map[string]int{}
	combinedCN := false
	for i, raw := range rules {
		sets, _ := raw.(map[string]any)["rule_set"].([]any)
		hasGeoCN, hasIPCN := false, false
		for _, item := range sets {
			tag := item.(string)
			if _, exists := positions[tag]; !exists {
				positions[tag] = i
			}
			hasGeoCN = hasGeoCN || tag == "geosite-cn"
			hasIPCN = hasIPCN || tag == "geoip-cn"
		}
		combinedCN = combinedCN || (hasGeoCN && hasIPCN)
	}
	nonCN, hasNonCN := positions["geosite-geolocation-not-cn"]
	geoCN, hasGeoCN := positions["geosite-cn"]
	ipCN, hasIPCN := positions["geoip-cn"]
	if !hasNonCN || !hasGeoCN || !hasIPCN || !(nonCN < geoCN && geoCN < ipCN) || combinedCN {
		t.Fatal("foreign domains must be routed before separate China domain and IP fallbacks")
	}
	if !strings.Contains(string(sfa.Content), `"tag": "日本 001"`) || strings.Contains(string(sfa.Content), `[1]`) {
		t.Fatal("node and notice names were not normalized")
	}
	if strings.Contains(string(carton.Content), "raw.githubusercontent.com") ||
		strings.Count(string(carton.Content), "cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@sing/") != 16 {
		t.Fatal("remote rule-sets must use the non-poisoned CDN endpoint")
	}
	gameDownload := strings.Index(string(carton.Content), `"geosite-game-download"`)
	steam := strings.Index(string(carton.Content), `"geosite-steam"`)
	if gameDownload < 0 || steam < 0 || gameDownload > steam {
		t.Fatal("game downloads must be DIRECT before the broader Steam proxy rule")
	}
	if strings.Contains(string(sfa.Content), "steamserver.net") {
		t.Fatal("Steam session DIRECT rule must stay desktop-only")
	}
	assertSteamDepotLocalityRules(t, v)
	var desktopConfig map[string]any
	if e = json.Unmarshal(carton.Content, &desktopConfig); e != nil {
		t.Fatal(e)
	}
	assertSteamSessionLocalityRules(t, desktopConfig)
	assertMicrosoftStoreDirectRules(t, desktopConfig)
}

func assertSteamSessionLocalityRules(t *testing.T, config map[string]any) {
	t.Helper()
	check := func(rules []any, dns bool) {
		sessionDirect, steamProxy := -1, -1
		for i, raw := range rules {
			rule := raw.(map[string]any)
			for _, suffix := range anyStrings(rule["domain_suffix"]) {
				if suffix == "steamserver.net" {
					if dns && rule["server"] == "dns-cn" || !dns && rule["outbound"] == "DIRECT" {
						sessionDirect = i
					}
				}
			}
			sets, _ := rule["rule_set"].([]any)
			for _, set := range sets {
				if set == "geosite-steam" {
					steamProxy = i
				}
			}
		}
		if sessionDirect < 0 || steamProxy < 0 || sessionDirect > steamProxy {
			t.Fatalf("Steam session locality rule missing or ordered after broad Steam rule: session=%d steam=%d dns=%v", sessionDirect, steamProxy, dns)
		}
	}
	check(config["dns"].(map[string]any)["rules"].([]any), true)
	check(config["route"].(map[string]any)["rules"].([]any), false)
}

func assertMicrosoftStoreDirectRules(t *testing.T, config map[string]any) {
	t.Helper()
	check := func(rules []any, dns bool) {
		storeDirect, foreignProxy := -1, -1
		proxyOnly := map[string]bool{
			"displaycatalog.mp.microsoft.com": true,
			"purchase.md.mp.microsoft.com":    true,
			"licensing.mp.microsoft.com":      true,
		}
		for i, raw := range rules {
			rule := raw.(map[string]any)
			for _, suffix := range anyStrings(rule["domain_suffix"]) {
				if proxyOnly[suffix] {
					t.Fatalf("Microsoft Store account/catalog endpoint must not be in the DIRECT rule: %s", suffix)
				}
				if suffix == "storeedge.microsoft.com" {
					if dns && rule["server"] == "dns-cn" || !dns && rule["outbound"] == "DIRECT" {
						storeDirect = i
					}
				}
			}
			for _, set := range anyStrings(rule["rule_set"]) {
				if set == "geosite-geolocation-not-cn" {
					foreignProxy = i
				}
			}
		}
		if storeDirect < 0 || !dns && (foreignProxy < 0 || storeDirect > foreignProxy) {
			t.Fatalf("Microsoft Store DIRECT rule missing or ordered too late: store=%d foreign=%d dns=%v", storeDirect, foreignProxy, dns)
		}
	}
	check(config["dns"].(map[string]any)["rules"].([]any), true)
	check(config["route"].(map[string]any)["rules"].([]any), false)
}

func anyStrings(value any) []string {
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func assertSteamDepotLocalityRules(t *testing.T, config map[string]any) {
	t.Helper()
	check := func(rules []any, dns bool) {
		depotDirect, steamProxy := -1, -1
		for i, raw := range rules {
			rule := raw.(map[string]any)
			if suffix, _ := rule["domain_suffix"].([]any); len(suffix) == 1 && suffix[0] == "steamcontent.com" {
				if dns && rule["server"] == "dns-cn" || !dns && rule["outbound"] == "DIRECT" {
					depotDirect = i
				}
			}
			sets, _ := rule["rule_set"].([]any)
			for _, set := range sets {
				if set == "geosite-steam" {
					steamProxy = i
				}
			}
		}
		if depotDirect < 0 || steamProxy < 0 || depotDirect > steamProxy {
			t.Fatalf("Steam depot locality rule missing or ordered after broad Steam rule: depot=%d steam=%d dns=%v", depotDirect, steamProxy, dns)
		}
	}
	check(config["dns"].(map[string]any)["rules"].([]any), true)
	check(config["route"].(map[string]any)["rules"].([]any), false)
}

func TestHysteria2BandwidthStringsAreNormalized(t *testing.T) {
	n := model.Node{Protocol: "hysteria2", Server: "example.com", Port: 443, Config: map[string]any{"password": "x", "up": "100 Mbps", "down": "1 Gbps"}}
	out, err := ConvertNode(n, "n")
	if err != nil {
		t.Fatal(err)
	}
	if out["up_mbps"] != 100 || out["down_mbps"] != 1000 {
		t.Fatalf("bandwidth not normalized: %#v", out)
	}
}

func TestChinaMultiplierUnknownAndDirectSelection(t *testing.T) {
	nodes := []model.Node{
		{ID: "ordinary", Number: 1, Protocol: "ss", Server: "us.example", Port: 1, CountryCode: "US", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}},
		{ID: "cn", Number: 1, Protocol: "ss", Server: "cn.example", Port: 1, CountryCode: "CN", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}},
		{ID: "cn-slow", Number: 3, Protocol: "ss", Server: "cn3.example", Port: 1, CountryCode: "CN", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}},
		{ID: "cn-expensive", Number: 2, Protocol: "ss", Server: "cn2.example", Port: 1, CountryCode: "HK", Multiplier: 2, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}},
		{ID: "unknown", Protocol: "ss", Server: "u.example", Port: 2, Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}},
	}
	ready := func(priority float64) model.Quality {
		return model.Quality{Priority: priority, Samples: 30, Availability: .9, AvailabilityRaw: .98, AverageLatencyMS: 80}
	}
	a, err := Generate(Carton, nodes, nil, map[string]model.Quality{
		"ordinary": ready(50), "cn": ready(60), "cn-slow": ready(40), "cn-expensive": ready(99),
	})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err = json.Unmarshal(a.Content, &cfg); err != nil {
		t.Fatal(err)
	}
	groups := map[string][]string{}
	for _, raw := range cfg["outbounds"].([]any) {
		m := raw.(map[string]any)
		tag, _ := m["tag"].(string)
		for _, v := range toStrings(m["outbounds"]) {
			groups[tag] = append(groups[tag], v)
		}
	}
	if len(groups["中国节点"]) != 0 || len(groups["待检测节点"]) != 0 || len(groups["全部节点"]) != 4 {
		t.Fatalf("removed/complete groups are incorrect: %#v", groups)
	}
	if values := groups["代理选择"]; len(values) == 0 || values[len(values)-1] != "DIRECT" {
		t.Fatalf("DIRECT missing from proxy selector: %#v", values)
	}
	for _, tag := range groups["自动选择"] {
		if strings.Contains(tag, "中国") || strings.Contains(tag, "未知") {
			t.Fatalf("isolated node entered automatic group: %s", tag)
		}
	}
}

func TestCasualAndAllNodeGroups(t *testing.T) {
	nodes := []model.Node{
		{ID: "us1", Number: 1, Protocol: "ss", Server: "a", Port: 1, CountryCode: "US", Multiplier: .1, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}},
		{ID: "us2", Number: 2, Protocol: "ss", Server: "b", Port: 1, CountryCode: "US", Multiplier: .1, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}},
		{ID: "us3", Number: 3, Protocol: "ss", Server: "c", Port: 1, CountryCode: "US", Multiplier: .1, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}},
		{ID: "hk", Number: 1, Protocol: "ss", Server: "d", Port: 1, CountryCode: "HK", Multiplier: 2, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}},
		{ID: "tw", Number: 1, Protocol: "ss", Server: "e", Port: 1, CountryCode: "TW", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}},
		{ID: "mo", Number: 1, Protocol: "ss", Server: "f", Port: 1, CountryCode: "MO", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}},
	}
	qualities := map[string]model.Quality{}
	for i, node := range nodes {
		qualities[node.ID] = model.Quality{Priority: float64(100 - i*6), Samples: 10, AverageLatencyMS: 100}
	}
	artifact, err := Generate(SFA, nodes, nil, qualities)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(artifact.Content, &config); err != nil {
		t.Fatal(err)
	}
	groups := map[string][]string{}
	var order []string
	for _, raw := range config["outbounds"].([]any) {
		outbound := raw.(map[string]any)
		if values := toStrings(outbound["outbounds"]); len(values) > 0 {
			tag := outbound["tag"].(string)
			groups[tag] = values
			order = append(order, tag)
		}
	}
	if len(groups["随便用组"]) != 3 || len(groups["全部节点"]) != len(nodes) {
		t.Fatalf("casual/all group membership incorrect: %#v", groups)
	}
	all := groups["全部节点"]
	if !strings.HasPrefix(all[0], "美国") || !strings.HasPrefix(all[1], "美国") || !strings.HasPrefix(all[2], "美国") || !strings.HasPrefix(all[3], "中国香港") {
		t.Fatalf("locations are not ordered by their best node priority: %#v", all)
	}
	joined := strings.Join(order, ",")
	if strings.Contains(joined, "下载组") || strings.Contains(joined, "游戏组") || !strings.Contains(joined, "代理选择,普通组") || !strings.Contains(joined, "随便用组,全部节点,自动选择,日本自动选择,套餐信息") {
		t.Fatalf("group order incorrect: %s", joined)
	}
}

func TestImportantAlertSelectorPrecedesProxySelection(t *testing.T) {
	nodes := []model.Node{{ID: "a", Number: 1, Protocol: "ss", Server: "a", Port: 1, CountryCode: "US", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}}}
	qualities := map[string]model.Quality{"a": {Priority: 80, Samples: 1, AverageLatencyMS: 100}}
	artifact, err := GenerateWithOptions(SFA, nodes, nil, qualities, Options{Alerts: []string{"源订阅更新失败 · 6小时未更新"}})
	if err != nil {
		t.Fatal(err)
	}
	alertAt := strings.Index(string(artifact.Content), `"tag": "⚠ 源订阅更新失败`)
	proxyAt := strings.Index(string(artifact.Content), `"tag": "代理选择"`)
	if alertAt < 0 || proxyAt < 0 || alertAt > proxyAt {
		t.Fatalf("important alert is not the first selector: alert=%d proxy=%d", alertAt, proxyAt)
	}
}

func TestAutomaticGroupsUseRelativeQualityAndKeepAtLeastTwo(t *testing.T) {
	nodes := []model.Node{
		{ID: "best", Number: 1, Protocol: "ss", Server: "a.example", Port: 1, CountryCode: "JP", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}},
		{ID: "second", Number: 2, Protocol: "ss", Server: "b.example", Port: 1, CountryCode: "JP", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}},
		{ID: "third", Number: 3, Protocol: "ss", Server: "c.example", Port: 1, CountryCode: "JP", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}},
	}
	poor := func(priority float64) model.Quality {
		return model.Quality{Priority: priority, Samples: 10, Availability: .4, AvailabilityRaw: .6, AverageLatencyMS: 600}
	}
	artifact, err := Generate(SFA, nodes, nil, map[string]model.Quality{"best": poor(100), "second": poor(50), "third": poor(49)})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(artifact.Content, &config); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"自动选择", "日本自动选择"} {
		found := false
		for _, raw := range config["outbounds"].([]any) {
			outbound := raw.(map[string]any)
			if outbound["tag"] != name {
				continue
			}
			found = true
			if outbound["type"] != "urltest" || len(outbound["outbounds"].([]any)) < 2 {
				t.Fatalf("%s did not retain the two-node minimum: %#v", name, outbound)
			}
		}
		if !found {
			t.Fatalf("%s missing", name)
		}
	}
}

func TestHighCostGroupFallsBackToOrdinaryBenchmark(t *testing.T) {
	nodes := []model.Node{
		{ID: "ordinary", Number: 1, Protocol: "ss", Server: "a.example", Port: 1, CountryCode: "US", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}},
		{ID: "high", Number: 2, Protocol: "ss", Server: "b.example", Port: 1, CountryCode: "JP", Multiplier: 5, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}},
	}
	ready := func(priority float64) model.Quality {
		return model.Quality{Priority: priority, Samples: 30, Availability: .9, AvailabilityRaw: .98, AverageLatencyMS: 80}
	}
	artifact, err := Generate(SFA, nodes, nil, map[string]model.Quality{"ordinary": ready(50), "high": ready(80)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(artifact.Content), `"tag": "高费组"`) || strings.Contains(string(artifact.Content), `"tag": "中费组"`) {
		t.Fatalf("high group did not use the ordinary fallback benchmark: %s", artifact.Content)
	}
}

func TestJapanAutomaticFallsBackWithoutSelectorCycle(t *testing.T) {
	nodes := []model.Node{{ID: "ordinary", Number: 1, Protocol: "ss", Server: "a.example", Port: 1, CountryCode: "US", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}}}
	qualities := map[string]model.Quality{"ordinary": {Priority: 80, Samples: 10, Availability: .9, AverageLatencyMS: 80}}
	artifact, err := Generate(SFA, nodes, nil, qualities)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(artifact.Content, &config); err != nil {
		t.Fatal(err)
	}
	for _, raw := range config["outbounds"].([]any) {
		outbound := raw.(map[string]any)
		if outbound["tag"] != "日本自动选择" {
			continue
		}
		members := toStrings(outbound["outbounds"])
		if outbound["type"] != "selector" || len(members) != 1 || members[0] != "自动选择" {
			t.Fatalf("invalid no-Japan fallback: %#v", outbound)
		}
		return
	}
	t.Fatal("Japan automatic group missing")
}

func TestOrdinaryIsBroaderThanAutomaticAndRespectsMinima(t *testing.T) {
	var nodes []model.Node
	qualities := map[string]model.Quality{}
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("node-%d", i)
		nodes = append(nodes, model.Node{ID: id, Number: int64(i + 1), Protocol: "ss", Server: id + ".example", Port: 1, CountryCode: "US", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}})
		qualities[id] = model.Quality{Priority: float64(100 - i*20), Samples: 10, AverageLatencyMS: 500}
	}
	artifact, err := Generate(SFA, nodes, nil, qualities)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(artifact.Content, &config); err != nil {
		t.Fatal(err)
	}
	ordinaryCount, automaticCount := 0, 0
	for _, raw := range config["outbounds"].([]any) {
		outbound := raw.(map[string]any)
		if outbound["tag"] == "普通组" {
			ordinaryCount = len(outbound["outbounds"].([]any))
		}
		if outbound["tag"] == "自动选择" && outbound["type"] == "urltest" {
			automaticCount = len(outbound["outbounds"].([]any))
		}
	}
	if ordinaryCount < 5 || automaticCount < 2 || automaticCount > ordinaryCount {
		t.Fatalf("invalid relative group sizes: ordinary=%d automatic=%d", ordinaryCount, automaticCount)
	}
}

func TestFirstSuccessfulCycleCanGenerateRelativeGroups(t *testing.T) {
	var nodes []model.Node
	qualities := map[string]model.Quality{}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("fresh-%d", i)
		nodes = append(nodes, model.Node{ID: id, Number: int64(i + 1), Protocol: "ss", Server: id + ".example", Port: 1, CountryCode: "US", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}})
		qualities[id] = model.Quality{Priority: float64(50 - i), Samples: 1, AverageLatencyMS: 100}
	}
	artifact, err := Generate(SFA, nodes, nil, qualities)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(artifact.Content), `"tag": "普通组"`) || !strings.Contains(string(artifact.Content), `"tag": "自动选择"`) {
		t.Fatal("one successful cycle incorrectly left the profile without usable groups")
	}
}

func toStrings(v any) []string {
	var out []string
	if a, ok := v.([]any); ok {
		for _, x := range a {
			out = append(out, x.(string))
		}
	}
	return out
}

func TestCartonConfigChecksWithBundledSingBox113(t *testing.T) {
	bin := filepath.Join("..", "..", "work", "singbox", "windows", "sing-box-1.13.19-windows-amd64", "sing-box.exe")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("local sing-box 1.13.19 not present")
	}
	nodes := []model.Node{{ID: "a", Number: 1, Protocol: "ss", Server: "example.com", Port: 443, CountryCode: "JP", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "secret"}}}
	a, err := Generate(Carton, nodes, nil, map[string]model.Quality{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "carton.json")
	if err = os.WriteFile(path, a.Content, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(bin, "check", "-c", path).CombinedOutput(); err != nil {
		t.Fatalf("sing-box 1.13.19 rejected config: %v\n%s", err, out)
	}
}

func TestCapabilityRendering113And114(t *testing.T) {
	nodes := []model.Node{{ID: "a", Number: 1, Protocol: "ss", Server: "example.com", Port: 443, CountryCode: "JP", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "secret"}}}
	legacy, err := Generate(SFA113, nodes, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	modern, err := Generate(Carton114, nodes, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacy.Content), "http_clients") || strings.Count(string(legacy.Content), `"download_detour": "DIRECT"`) != 16 {
		t.Fatal("1.13 capability rendering incorrect")
	}
	if !strings.Contains(string(modern.Content), "http_clients") || strings.Contains(string(modern.Content), "download_detour") {
		t.Fatal("1.14 capability rendering incorrect")
	}
	var modernConfig map[string]any
	if err = json.Unmarshal(modern.Content, &modernConfig); err != nil {
		t.Fatal(err)
	}
	httpClient := modernConfig["http_clients"].([]any)[0].(map[string]any)
	if _, hasDetour := httpClient["detour"]; hasDetour {
		t.Fatal("1.14 direct HTTP client must omit detour")
	}
	if !strings.Contains(string(modern.Content), "qbittorrent") {
		t.Fatal("Carton process rules lost in 1.14 mode")
	}
}

func TestLANAndGooglePlayOptions(t *testing.T) {
	nodes := []model.Node{{ID: "a", Number: 1, Protocol: "ss", Server: "example.com", Port: 443, CountryCode: "JP", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "secret"}}}
	a, err := GenerateWithOptions(Carton, nodes, nil, nil, Options{LANEnabled: true, LANListen: "192.168.1.1", LANPort: 2081, LANUsername: "u", LANPassword: "p", GooglePlayMode: "economy"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(a.Content)
	if !strings.Contains(text, `"listen": "192.168.1.1"`) || !strings.Contains(text, `"username": "u"`) || !strings.Contains(text, `"geosite-google-play-cn"`) {
		t.Fatal("LAN/economy options not rendered")
	}
	var economy map[string]any
	if err = json.Unmarshal(a.Content, &economy); err != nil {
		t.Fatal(err)
	}
	rules := economy["route"].(map[string]any)["rules"].([]any)
	servicesProxy, cdnDirect := false, false
	for _, raw := range rules {
		rule := raw.(map[string]any)
		for _, domain := range testStrings(rule["domain"]) {
			servicesProxy = servicesProxy || domain == "services.googleapis.cn" && rule["outbound"] == "代理选择"
		}
		for _, set := range testStrings(rule["rule_set"]) {
			cdnDirect = cdnDirect || set == "geosite-google-play-cn" && rule["outbound"] == "DIRECT"
		}
	}
	if !servicesProxy || !cdnDirect {
		t.Fatal("Google Play economy mode must proxy control API and direct only mainland CDN")
	}
	a, err = GenerateWithOptions(Carton, nodes, nil, nil, Options{LANEnabled: false, GooglePlayMode: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(a.Content), `"tag": "mixed-in"`) {
		t.Fatal("disabled LAN mixed inbound rendered")
	}
}

func TestPublicSubscriptionDomainIsDirect(t *testing.T) {
	nodes := []model.Node{{ID: "a", Number: 1, Protocol: "ss", Server: "example.com", Port: 443, CountryCode: "JP", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "secret"}}}
	a, err := GenerateWithOptions(SFA, nodes, nil, nil, Options{GooglePlayMode: "stable", DirectDomains: []string{"router.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(a.Content, &config); err != nil {
		t.Fatal(err)
	}
	hasDirectRoute := false
	for _, raw := range config["route"].(map[string]any)["rules"].([]any) {
		rule := raw.(map[string]any)
		for _, domain := range testStrings(rule["domain"]) {
			hasDirectRoute = hasDirectRoute || domain == "router.example.test" && rule["outbound"] == "DIRECT"
		}
	}
	hasLocalDNS := false
	for _, raw := range config["dns"].(map[string]any)["rules"].([]any) {
		rule := raw.(map[string]any)
		for _, domain := range testStrings(rule["domain"]) {
			hasLocalDNS = hasLocalDNS || domain == "router.example.test" && rule["server"] == "dns-cn"
		}
	}
	if !hasDirectRoute || !hasLocalDNS {
		t.Fatal("public subscription domain must use DIRECT and local DNS")
	}
}

func testStrings(value any) []string {
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}
