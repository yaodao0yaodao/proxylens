package generator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/yaodao0yaodao/proxylens/internal/model"
	"github.com/yaodao0yaodao/proxylens/internal/naming"
	"github.com/yaodao0yaodao/proxylens/internal/quality"
)

type Client string

const (
	SFA       Client = "sfa"
	Carton    Client = "carton"
	SFA113    Client = "sfa-1.13"
	Carton114 Client = "carton-1.14"
)

func modern(c Client) bool { return c == SFA || c == Carton114 }
func carton(c Client) bool { return c == Carton || c == Carton114 }

type Artifact struct {
	Content []byte
	SHA256  string
	Skipped []string
}
type Options struct {
	LANEnabled               bool
	LANListen                string
	LANPort                  int
	LANUsername, LANPassword string
	GooglePlayMode           string
	RuleBaseURL              string
	DirectDomains            []string
	DownloadProcesses        []string
	Alerts                   []string
}

func Generate(client Client, nodes []model.Node, notices []model.Notice, qualities map[string]model.Quality) (Artifact, error) {
	return GenerateWithOptions(client, nodes, notices, qualities, Options{LANEnabled: true, LANListen: "0.0.0.0", LANPort: 2080, GooglePlayMode: "stable"})
}
func GenerateWithOptions(client Client, nodes []model.Node, notices []model.Notice, qualities map[string]model.Quality, opt Options) (Artifact, error) {
	if len(nodes) == 0 {
		return Artifact{}, fmt.Errorf("no active nodes")
	}
	sort.SliceStable(nodes, func(i, j int) bool { return qualities[nodes[i].ID].Priority > qualities[nodes[j].ID].Priority })
	var outbounds []any
	nodeTags := map[string]string{}
	usedNodeTags := map[string]bool{}
	var usable []model.Node
	var skipped []string
	for _, n := range nodes {
		// Nodes without a confirmed exit country/number are kept in storage and
		// continue to be probed, but must not leak into a client selector.
		if strings.TrimSpace(n.CountryCode) == "" || n.Number <= 0 {
			continue
		}
		tag := uniqueNodeTag(nodeTag(n), n.ID, usedNodeTags)
		out, e := convertNode(n, tag)
		if e != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", n.OriginalName, e))
			continue
		}
		nodeTags[n.ID] = tag
		outbounds = append(outbounds, out)
		usable = append(usable, n)
	}
	if len(usable) == 0 {
		return Artifact{}, fmt.Errorf("no supported nodes in subscription")
	}
	priority := func(n model.Node) float64 { return qualities[n.ID].Priority }
	isChina := func(n model.Node) bool {
		switch strings.ToUpper(n.CountryCode) {
		case "CN", "HK", "MO", "TW":
			return true
		}
		return false
	}
	isKnown := func(n model.Node) bool { return strings.TrimSpace(n.CountryCode) != "" && n.Number > 0 }
	isJP := func(n model.Node) bool {
		return strings.EqualFold(n.CountryCode, "JP") || strings.Contains(n.Country, "日本")
	}
	rankable := func(n model.Node) bool {
		q := qualities[n.ID]
		return q.Samples > 0 && q.AverageLatencyMS > 0
	}
	ordinaryAccept := func(n model.Node) bool {
		return isKnown(n) && !isChina(n) && n.Multiplier <= 1 && rankable(n)
	}
	priorityScore := func(n model.Node) float64 { return qualities[n.ID].Priority }
	ordinary := rankedTop(usable, .90, 5, 8, ordinaryAccept, priorityScore)
	casual := rankedTop(usable, .90, 3, 0, func(n model.Node) bool {
		return isKnown(n) && n.Multiplier <= .1 && rankable(n)
	}, priorityScore)
	allNodes := append([]model.Node(nil), usable...)
	locationBest := make(map[string]float64)
	for _, node := range allNodes {
		location := locationKey(node)
		if score := priority(node); score > locationBest[location] {
			locationBest[location] = score
		}
	}
	sort.SliceStable(allNodes, func(i, j int) bool {
		li, lj := locationKey(allNodes[i]), locationKey(allNodes[j])
		if li != lj {
			if locationBest[li] != locationBest[lj] {
				return locationBest[li] > locationBest[lj]
			}
			return li < lj
		}
		if allNodes[i].Multiplier != allNodes[j].Multiplier {
			return allNodes[i].Multiplier < allNodes[j].Multiplier
		}
		if priority(allNodes[i]) != priority(allNodes[j]) {
			return priority(allNodes[i]) > priority(allNodes[j])
		}
		return allNodes[i].Number < allNodes[j].Number
	})
	base := math.Inf(1)
	if len(ordinary) > 0 {
		base = priority(ordinary[0])
	}
	var medium []model.Node
	if len(ordinary) > 0 {
		medium = rankedTop(usable, .90, 0, 5, func(n model.Node) bool {
			return isKnown(n) && !isChina(n) && n.Multiplier > 1 && n.Multiplier <= 3 && rankable(n) && priority(n) > base
		}, priorityScore)
	}
	// A missing medium group must not suppress an exceptional high-cost node.
	// Compare high-cost nodes with the best visible medium node when present;
	// otherwise fall back to the best ordinary node.
	mediumBase := base
	if len(medium) > 0 {
		mediumBase = priority(medium[0])
	}
	var high []model.Node
	if !math.IsInf(mediumBase, 1) {
		high = rankedTop(usable, .90, 0, 5, func(n model.Node) bool {
			return isKnown(n) && !isChina(n) && n.Multiplier > 3 && rankable(n) && priority(n) > mediumBase
		}, priorityScore)
	}
	autoAccept := ordinaryAccept
	jpAccept := func(n model.Node) bool {
		return isKnown(n) && !isChina(n) && isJP(n) && n.Multiplier <= 1 && rankable(n)
	}
	autoRanked := rankedTop(usable, .90, 2, 0, autoAccept, priorityScore)
	jpRanked := rankedTop(usable, .90, 2, 0, jpAccept, priorityScore)
	auto := quality.DiverseCoverageTop(autoRanked, qualities, 2, 0)
	jp := quality.DiverseCoverageTop(jpRanked, qualities, 2, 0)
	var autoOutbound map[string]any
	if len(auto) == 0 {
		fallback := []string{"DIRECT"}
		if len(ordinary) > 0 {
			fallback = []string{"普通组"}
		}
		autoOutbound = selector("自动选择", fallback)
	} else {
		autoOutbound = urltest("自动选择", tags(auto, nodeTags), 100)
	}
	var jpOutbound map[string]any
	if len(jp) == 0 {
		// Keep Japan-only routing functional when the subscription has no
		// Japanese node, without creating a selector cycle back to 代理选择.
		jpOutbound = selector("日本自动选择", []string{"自动选择"})
	} else {
		jpOutbound = urltest("日本自动选择", tags(jp, nodeTags), 100)
	}
	selectorGroups := []string{"自动选择"}
	if len(ordinary) > 0 {
		selectorGroups = append(selectorGroups, "普通组")
	}
	if len(medium) > 0 {
		selectorGroups = append(selectorGroups, "中费组")
	}
	if len(high) > 0 {
		selectorGroups = append(selectorGroups, "高费组")
	}
	if len(casual) > 0 {
		selectorGroups = append(selectorGroups, "随便用组")
	}
	selectorGroups = append(selectorGroups, "全部节点")
	selectorGroups = append(selectorGroups, "日本自动选择")
	selectorGroups = append(selectorGroups, "DIRECT")
	for _, message := range opt.Alerts {
		if message = cleanTag(message); message != "" {
			outbounds = append(outbounds, selector("⚠ "+message, []string{"自动选择"}))
		}
	}
	outbounds = append(outbounds, selector("代理选择", selectorGroups))
	if len(ordinary) > 0 {
		outbounds = append(outbounds, selector("普通组", tags(ordinary, nodeTags)))
	}
	if len(medium) > 0 {
		outbounds = append(outbounds, selector("中费组", tags(medium, nodeTags)))
	}
	if len(high) > 0 {
		outbounds = append(outbounds, selector("高费组", tags(high, nodeTags)))
	}
	if len(casual) > 0 {
		outbounds = append(outbounds, selector("随便用组", tags(casual, nodeTags)))
	}
	outbounds = append(outbounds, selector("全部节点", tags(allNodes, nodeTags)))
	outbounds = append(outbounds, autoOutbound, jpOutbound)
	if len(notices) == 0 {
		notices = []model.Notice{{Name: "套餐信息：暂无"}}
	}
	noticeTags := make([]string, 0, len(notices))
	seenNotices := map[string]bool{}
	for i, n := range notices {
		tag := cleanTag(n.Name)
		if tag == "" {
			tag = fmt.Sprintf("套餐信息 %d", i+1)
		}
		tag = "ℹ " + tag
		if seenNotices[tag] {
			continue
		}
		seenNotices[tag] = true
		noticeTags = append(noticeTags, tag)
		outbounds = append(outbounds, map[string]any{"type": "direct", "tag": tag})
	}
	outbounds = append(outbounds, urltest("套餐信息", noticeTags, 1000), map[string]any{"type": "direct", "tag": "DIRECT"})

	inbounds := []any{tunInbound(client)}
	if opt.LANEnabled {
		listen := opt.LANListen
		if listen == "" {
			listen = "127.0.0.1"
		}
		port := opt.LANPort
		if port == 0 {
			port = 2080
		}
		mixed := map[string]any{"type": "mixed", "tag": "mixed-in", "listen": listen, "listen_port": port}
		if opt.LANUsername != "" && opt.LANPassword != "" {
			mixed["users"] = []any{map[string]any{"username": opt.LANUsername, "password": opt.LANPassword}}
		}
		inbounds = append(inbounds, mixed)
	}
	config := map[string]any{
		"log":          map[string]any{"level": "warn", "timestamp": true},
		"dns":          dnsConfig(opt.GooglePlayMode, opt.DirectDomains),
		"inbounds":     inbounds,
		"outbounds":    outbounds,
		"route":        routeConfig(client, opt.GooglePlayMode, opt.RuleBaseURL, opt.DirectDomains, opt.DownloadProcesses),
		"experimental": map[string]any{"cache_file": map[string]any{"enabled": true}, "clash_api": map[string]any{"external_controller": "127.0.0.1:9090", "secret": ""}},
	}
	if modern(client) {
		// In sing-box 1.14 an HTTP client with no detour is the direct client.
		// Pointing it at a direct outbound is rejected as an empty detour.
		config["http_clients"] = []any{map[string]any{"tag": "rule-set-http"}}
	}
	b, e := json.MarshalIndent(config, "", "  ")
	if e != nil {
		return Artifact{}, e
	}
	b = append(b, '\n')
	h := sha256.Sum256(b)
	return Artifact{Content: b, SHA256: hex.EncodeToString(h[:]), Skipped: skipped}, nil
}

func nodeTag(n model.Node) string {
	country := naming.Country(n.CountryCode, n.Country)
	m := ""
	if n.Multiplier != 1 {
		m = fmt.Sprintf(" %.2gx", n.Multiplier)
	}
	return cleanTag(fmt.Sprintf("%s %03d%s", country, n.Number, m))
}

func uniqueNodeTag(base, id string, used map[string]bool) string {
	if base == "" {
		base = "节点"
	}
	if !used[base] {
		used[base] = true
		return base
	}
	compactID := strings.ReplaceAll(id, "-", "")
	if compactID == "" {
		compactID = fmt.Sprintf("%x", sha256.Sum256([]byte(base)))
	}
	for length := 8; length <= len(compactID); length += 4 {
		if length > len(compactID) {
			length = len(compactID)
		}
		suffix := " ·" + compactID[:length]
		runes := []rune(base)
		maxBase := 80 - len([]rune(suffix))
		if len(runes) > maxBase {
			runes = runes[:maxBase]
		}
		candidate := string(runes) + suffix
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
		if length == len(compactID) {
			break
		}
	}
	// IDs are expected to be unique, but keep the config valid even if corrupt
	// imported data contains repeated IDs.
	for ordinal := 2; ; ordinal++ {
		suffix := fmt.Sprintf(" ·%d", ordinal)
		runes := []rune(base)
		maxBase := 80 - len([]rune(suffix))
		if len(runes) > maxBase {
			runes = runes[:maxBase]
		}
		candidate := string(runes) + suffix
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}
func cleanTag(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s))
	if len([]rune(s)) > 80 {
		s = string([]rune(s)[:80])
	}
	return s
}
func tags(nodes []model.Node, m map[string]string) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if t := m[n.ID]; t != "" {
			out = append(out, t)
		}
	}
	return out
}

// relativeTop has no fixed group size. It rejects nodes that fall too far
// behind the best eligible node, so a weak subscription does not fill groups
// merely to reach an arbitrary count.
func relativeTop(nodes []model.Node, q map[string]model.Quality, ratio float64, accept func(model.Node) bool) []model.Node {
	return relativeTopMin(nodes, q, ratio, 0, accept)
}

func relativeTopMin(nodes []model.Node, q map[string]model.Quality, ratio float64, minimum int, accept func(model.Node) bool) []model.Node {
	return rankedTop(nodes, ratio, minimum, 0, accept, func(n model.Node) float64 { return q[n.ID].Priority })
}

func rankedTop(nodes []model.Node, ratio float64, minimum, maximum int, accept func(model.Node) bool, score func(model.Node) float64) []model.Node {
	var candidates []model.Node
	for _, n := range nodes {
		if accept(n) {
			candidates = append(candidates, n)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return score(candidates[i]) > score(candidates[j]) })
	if len(candidates) == 0 {
		return nil
	}
	cutoff := score(candidates[0]) * ratio
	end := len(candidates)
	for i, n := range candidates {
		if score(n) < cutoff {
			end = i
			break
		}
	}
	if end < minimum {
		end = minimum
		if end > len(candidates) {
			end = len(candidates)
		}
	}
	if maximum > 0 && end > maximum {
		end = maximum
	}
	return candidates[:end]
}

func locationKey(n model.Node) string {
	if code := strings.ToUpper(strings.TrimSpace(n.CountryCode)); code != "" {
		return code
	}
	return strings.TrimSpace(n.Country)
}

// rankedTopPerLocation applies one global quality threshold, limits each
// country/region independently, then groups the result by location. CN, HK,
// TW and MO remain distinct because their ISO-style codes are distinct.
func rankedTopPerLocation(nodes []model.Node, ratio float64, perLocation int, accept func(model.Node) bool, score func(model.Node) float64) []model.Node {
	ranked := rankedTop(nodes, ratio, 0, 0, accept, score)
	counts := map[string]int{}
	selected := make([]model.Node, 0, len(ranked))
	for _, node := range ranked {
		key := locationKey(node)
		if counts[key] >= perLocation {
			continue
		}
		counts[key]++
		selected = append(selected, node)
	}
	sort.SliceStable(selected, func(i, j int) bool {
		li, lj := locationKey(selected[i]), locationKey(selected[j])
		if li != lj {
			return li < lj
		}
		return score(selected[i]) > score(selected[j])
	})
	return selected
}

func ensureMinimumCandidates(chosen, ranked []model.Node, minimum int) []model.Node {
	if minimum > len(ranked) {
		minimum = len(ranked)
	}
	seen := make(map[string]bool, len(chosen))
	for _, node := range chosen {
		seen[node.ID] = true
	}
	for _, node := range ranked {
		if len(chosen) >= minimum {
			break
		}
		if !seen[node.ID] {
			chosen = append(chosen, node)
			seen[node.ID] = true
		}
	}
	return chosen
}
func urltest(tag string, out []string, tolerance int) map[string]any {
	return map[string]any{"type": "urltest", "tag": tag, "outbounds": out, "url": "https://www.gstatic.com/generate_204", "interval": "10m", "tolerance": tolerance, "idle_timeout": "30m", "interrupt_exist_connections": false}
}
func selector(tag string, out []string) map[string]any {
	return map[string]any{"type": "selector", "tag": tag, "outbounds": out, "default": out[0], "interrupt_exist_connections": false}
}

func tunInbound(client Client) map[string]any {
	m := map[string]any{"type": "tun", "tag": "tun-in", "address": []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"}, "mtu": 9000, "auto_route": true, "stack": "mixed"}
	if carton(client) {
		m["strict_route"] = true
	}
	return m
}
func dnsConfig(mode string, directDomains []string) map[string]any {
	// Game download CDNs use the local resolver for a nearby mainland edge;
	// Steam account/store/community traffic uses consistent remote DNS+egress.
	rules := []any{
		// The maintained game-download set enumerates many cache hostnames and
		// inevitably lags newly allocated cache numbers. Cover the whole depot
		// namespace before the broader Steam rule sends it to remote DNS. Local
		// DNS lets CDN locality follow the client network and Steam setting.
		map[string]any{"domain_suffix": []string{"steamcontent.com"}, "action": "route", "server": "dns-cn"},
		map[string]any{"rule_set": []string{"geosite-game-download"}, "action": "route", "server": "dns-cn"},
		map[string]any{"rule_set": []string{"geosite-steam"}, "action": "route", "server": "dns-remote"},
	}
	if len(directDomains) > 0 {
		// The subscription publisher can resolve to the router's public address.
		// Resolve it locally so a client on the LAN can use NAT reflection without
		// depending on the very proxy configuration it is trying to update.
		rules = append([]any{map[string]any{"domain": directDomains, "action": "route", "server": "dns-cn"}}, rules...)
	}
	rules = append(rules, map[string]any{"domain": []string{"services.googleapis.cn"}, "action": "route", "server": "dns-remote"})
	if mode == "economy" {
		rules = append(rules, map[string]any{"rule_set": []string{"geosite-google-play-cn"}, "action": "route", "server": "dns-cn"})
	}
	rules = append(rules, map[string]any{"rule_set": []string{"geosite-google-play"}, "action": "route", "server": "dns-remote"})
	rules = append(rules, map[string]any{"rule_set": []string{"geosite-cn", "geosite-private"}, "action": "route", "server": "dns-cn"})
	return map[string]any{"servers": []any{map[string]any{"type": "udp", "tag": "dns-cn", "server": "223.5.5.5"}, map[string]any{"type": "tls", "tag": "dns-remote", "server": "1.1.1.1", "server_port": 853, "detour": "代理选择"}}, "rules": rules, "final": "dns-remote", "strategy": "prefer_ipv4"}
}

func routeConfig(client Client, mode, ruleBase string, directDomains, downloadedProcesses []string) map[string]any {
	rules := []any{map[string]any{"action": "sniff"}, map[string]any{"protocol": "dns", "action": "hijack-dns"}}
	if len(directDomains) > 0 {
		// Keep ProxyLens' own public subscription endpoint reachable while the
		// system/VPN proxy is active. Otherwise an unclassified DDNS name falls
		// through to the proxy selector and commonly loops or returns 502.
		rules = append(rules, map[string]any{"domain": directDomains, "action": "route", "outbound": "DIRECT"})
	}
	if carton(client) {
		processes := []string{
			// Windows and Linux/CachyOS share one Carton configuration. Keep both
			// executable-name variants here because Carton's default UA has no OS.
			"aria2c.exe", "aria2c",
			"qbittorrent.exe", "qbittorrent",
			"Thunder.exe", "DownloadService.exe",
			"Transmission.exe", "transmission-daemon", "transmission-gtk", "transmission-qt",
			"uTorrent.exe", "BitComet.exe",
			"fdm.exe", "fdm", "fdm.bin",
			"WebTorrent.exe", "webtorrent-desktop",
		}
		seen := map[string]bool{}
		for _, name := range processes {
			seen[strings.ToLower(name)] = true
		}
		for _, name := range downloadedProcesses {
			name = strings.TrimSpace(name)
			if name != "" && !seen[strings.ToLower(name)] {
				processes = append(processes, name)
				seen[strings.ToLower(name)] = true
			}
		}
		rules = append(rules, map[string]any{"process_name": processes, "action": "route", "outbound": "DIRECT"})
	}
	rules = append(rules, map[string]any{"rule_set": []string{"geosite-abema", "geosite-dmm", "geosite-niconico", "geosite-pixiv", "geosite-tver", "geosite-radiko", "geosite-nhk"}, "action": "route", "outbound": "日本自动选择"})
	// Domain rules, not the Steam process, distinguish large depot downloads
	// from login/store/community requests made by the same executable.
	rules = append(rules,
		map[string]any{"domain_suffix": []string{"steamcontent.com"}, "action": "route", "outbound": "DIRECT"},
		map[string]any{"rule_set": []string{"geosite-game-download"}, "action": "route", "outbound": "DIRECT"},
		map[string]any{"rule_set": []string{"geosite-steam"}, "action": "route", "outbound": "代理选择"})
	rules = append(rules, map[string]any{"domain": []string{"services.googleapis.cn"}, "action": "route", "outbound": "代理选择"})
	if mode == "economy" {
		rules = append(rules, map[string]any{"rule_set": []string{"geosite-google-play-cn"}, "action": "route", "outbound": "DIRECT"})
	}
	rules = append(rules, map[string]any{"rule_set": []string{"geosite-google-play"}, "action": "route", "outbound": "代理选择"})
	rules = append(rules,
		map[string]any{"rule_set": []string{"geosite-private", "geoip-private"}, "action": "route", "outbound": "DIRECT"},
		// Domain classification must run before GeoIP.  Different rule-sets in
		// one rule have OR semantics, so combining geosite-cn and geoip-cn can
		// send a known foreign domain DIRECT when its (possibly stale or poisoned)
		// address happens to be classified as China.
		map[string]any{"rule_set": []string{"geosite-geolocation-not-cn"}, "action": "route", "outbound": "代理选择"},
		map[string]any{"rule_set": []string{"geosite-cn"}, "action": "route", "outbound": "DIRECT"},
		map[string]any{"rule_set": []string{"geoip-cn"}, "action": "route", "outbound": "DIRECT"})
	result := map[string]any{"rules": rules, "rule_set": ruleSets(client, ruleBase), "final": "代理选择", "auto_detect_interface": true, "default_domain_resolver": "dns-cn"}
	if modern(client) {
		result["default_http_client"] = "rule-set-http"
	}
	return result
}

func ruleSets(client Client, ownBase string) []any {
	// Use jsDelivr's GitHub CDN endpoint instead of raw.githubusercontent.com.
	// The latter is frequently poisoned to 0.0.0.0 on mainland DNS before the
	// selected proxy can carry the request.
	base := "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@sing/geo/"
	specs := [][3]string{{"geosite-private", "geosite/private.srs", "binary"}, {"geoip-private", "geoip/private.srs", "binary"}, {"geosite-cn", "geosite/cn.srs", "binary"}, {"geoip-cn", "geoip/cn.srs", "binary"}, {"geosite-geolocation-not-cn", "geosite/geolocation-!cn.srs", "binary"}, {"geosite-google-play-cn", "geosite/google-play@cn.srs", "binary"}, {"geosite-google-play", "geosite/google-play.srs", "binary"}, {"geosite-steam", "geosite/steam.srs", "binary"}, {"geosite-game-download", "geosite/category-game-platforms-download.srs", "binary"}, {"geosite-abema", "geosite/abema.srs", "binary"}, {"geosite-dmm", "geosite/dmm.srs", "binary"}, {"geosite-niconico", "geosite/niconico.srs", "binary"}, {"geosite-pixiv", "geosite/pixiv.srs", "binary"}, {"geosite-tver", "geosite/tver.srs", "binary"}, {"geosite-radiko", "geosite/radiko.srs", "binary"}, {"geosite-nhk", "geosite/nhk.srs", "binary"}}
	out := make([]any, 0, len(specs))
	for _, s := range specs {
		// Rule sets are either served by ProxyLens itself or by the mainland-
		// reachable jsDelivr fallback. They must bootstrap through DIRECT: making
		// their download depend on a selector that is not initialized yet prevents
		// sing-box from starting.
		url := base + s[1]
		if ownBase != "" {
			url = strings.TrimSuffix(ownBase, "/") + "/" + s[0]
		}
		ruleSet := map[string]any{"type": "remote", "tag": s[0], "format": s[2], "url": url, "update_interval": "12h"}
		if !modern(client) {
			// sing-box 1.13 otherwise downloads through the route final outbound.
			// On first start that creates a bootstrap loop: the proxy waits for the
			// rule-set whose download is itself being sent through that proxy.
			ruleSet["download_detour"] = "DIRECT"
		}
		out = append(out, ruleSet)
	}
	return out
}

func convertNode(n model.Node, tag string) (map[string]any, error) {
	p := n.Config
	typ := strings.ToLower(n.Protocol)
	out := map[string]any{"type": singType(typ), "tag": tag, "server": n.Server, "server_port": n.Port}
	if out["type"] == "" {
		return nil, fmt.Errorf("unsupported proxy type %s", typ)
	}
	copyFields(out, p, map[string]string{"uuid": "uuid", "password": "password", "username": "username", "method": "method", "flow": "flow", "alterId": "alter_id", "alter-id": "alter_id", "packet-encoding": "packet_encoding", "congestion-controller": "congestion_control", "udp-relay-mode": "udp_relay_mode", "private-key": "private_key", "peer-public-key": "peer_public_key", "pre-shared-key": "pre_shared_key", "reserved": "reserved", "local-address": "local_address"})
	if typ == "vmess" {
		if _, ok := out["security"]; !ok {
			out["security"] = "auto"
		}
		if v, ok := p["cipher"]; ok {
			out["security"] = v
		}
	}
	if tlsEnabled(p, typ) {
		out["tls"] = tlsConfig(p)
	}
	if tr := transport(p); tr != nil {
		out["transport"] = tr
	}
	if typ == "hysteria2" || typ == "hy2" {
		if v := first(p, "up", "up-mbps", "up_mbps"); v != nil {
			out["up_mbps"] = v
		}
		if v := first(p, "down", "down-mbps", "down_mbps"); v != nil {
			out["down_mbps"] = v
		}
		for _, key := range []string{"up_mbps", "down_mbps"} {
			if v, ok := out[key]; ok {
				n, err := bandwidthMbps(v)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", key, err)
				}
				out[key] = n
			}
		}
		if obfs := text(p["obfs"]); obfs != "" {
			out["obfs"] = map[string]any{"type": obfs, "password": text(first(p, "obfs-password", "obfs_password"))}
		}
	}
	return out, nil
}

func ConvertNode(n model.Node, tag string) (map[string]any, error) { return convertNode(n, tag) }
func singType(t string) string {
	switch t {
	case "ss", "shadowsocks":
		return "shadowsocks"
	case "vmess", "vless", "trojan", "tuic", "socks", "http", "wireguard", "anytls":
		return t
	case "hysteria2", "hy2":
		return "hysteria2"
	default:
		return ""
	}
}
func copyFields(dst, src map[string]any, m map[string]string) {
	for a, b := range m {
		if v, ok := src[a]; ok && text(v) != "" {
			dst[b] = v
		}
	}
}
func tlsEnabled(p map[string]any, typ string) bool {
	if b, ok := p["tls"].(bool); ok && b {
		return true
	}
	return typ == "trojan" || typ == "hysteria2" || typ == "hy2" || typ == "tuic" || typ == "anytls"
}
func tlsConfig(p map[string]any) map[string]any {
	m := map[string]any{"enabled": true}
	if s := text(first(p, "servername", "server-name", "sni")); s != "" {
		m["server_name"] = s
	}
	if b, ok := first(p, "skip-cert-verify", "insecure").(bool); ok {
		m["insecure"] = b
	}
	if a, ok := p["alpn"]; ok {
		m["alpn"] = a
	}
	if fp := text(first(p, "client-fingerprint", "fingerprint")); fp != "" {
		m["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
	}
	if r, ok := p["reality-opts"].(map[string]any); ok {
		m["reality"] = map[string]any{"enabled": true, "public_key": first(r, "public-key", "public_key"), "short_id": first(r, "short-id", "short_id")}
	}
	return m
}
func transport(p map[string]any) map[string]any {
	network := strings.ToLower(text(p["network"]))
	if network == "" || network == "tcp" {
		return nil
	}
	opts, _ := p[network+"-opts"].(map[string]any)
	switch network {
	case "ws":
		m := map[string]any{"type": "ws"}
		if opts != nil {
			m["path"] = first(opts, "path")
			if h, ok := opts["headers"]; ok {
				m["headers"] = h
			}
			if e := integer(first(opts, "max-early-data", "max_early_data")); e > 0 {
				m["max_early_data"] = e
				m["early_data_header_name"] = text(first(opts, "early-data-header-name", "early_data_header_name"))
			}
		}
		return m
	case "grpc":
		return map[string]any{"type": "grpc", "service_name": text(first(opts, "grpc-service-name", "service-name", "service_name"))}
	case "httpupgrade":
		return map[string]any{"type": "httpupgrade", "path": text(first(opts, "path")), "host": text(first(opts, "host"))}
	case "h2", "http":
		return map[string]any{"type": "http", "host": first(opts, "host"), "path": text(first(opts, "path"))}
	default:
		return nil
	}
}
func first(m map[string]any, keys ...string) any {
	if m == nil {
		return nil
	}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}
func text(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}
func integer(v any) int { i, _ := strconv.Atoi(text(v)); return i }

var bandwidthRE = regexp.MustCompile(`(?i)^\s*([0-9]+(?:\.[0-9]+)?)\s*(k|m|g)?(?:bps|b/s)?\s*$`)

func bandwidthMbps(v any) (int, error) {
	s := text(v)
	m := bandwidthRE.FindStringSubmatch(s)
	if len(m) == 0 {
		return 0, fmt.Errorf("invalid bandwidth %q", s)
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	switch strings.ToLower(m[2]) {
	case "k":
		n /= 1000
	case "g":
		n *= 1000
	}
	if n <= 0 {
		return 0, fmt.Errorf("bandwidth must be positive")
	}
	return int(math.Round(n)), nil
}
