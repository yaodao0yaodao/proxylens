package probe

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestLocalProbeRefusalIsEngineFailureCandidate(t *testing.T) {
	message := "Get https://example.com: proxyconnect tcp: dial tcp 127.0.0.1:19002: connect: connection refused"
	if !isLocalProxyFailure(message) {
		t.Fatal("local probe refusal was treated as a remote node failure")
	}
	if isLocalProxyFailure("dial tcp 203.0.113.1:443: connect: connection refused") {
		t.Fatal("remote refusal was treated as a local probe failure")
	}
}

func TestGeoLookupFallsBackAndCoolsDownRateLimitedProvider(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	geo := NewGeo()
	geo.Client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		calls[request.URL.Host]++
		mu.Unlock()
		switch request.URL.Host {
		case "ipwho.is":
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": []string{"3600"}}, Body: io.NopCloser(strings.NewReader(`{"success":false}`))}, nil
		case "ipapi.co":
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"ip":"45.128.210.207","country_code":"JP","country_name":"Japan","asn":"AS3258","org":"xTom Japan Corporation"}`))}, nil
		default:
			t.Fatalf("unexpected provider %s", request.URL.Host)
			return nil, nil
		}
	})}
	result, err := geo.Lookup(t.Context(), "45.128.210.207")
	if err != nil || result.CountryCode != "JP" || result.Country != "Japan" || !strings.Contains(result.ASN, "AS3258") {
		t.Fatalf("fallback result=%+v err=%v", result, err)
	}
	if _, err = geo.Lookup(t.Context(), "203.0.113.9"); err != nil {
		t.Fatal(err)
	}
	if _, err = geo.LookupFresh(t.Context(), "45.128.210.207"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls["ipwho.is"] != 1 || calls["ipapi.co"] != 3 {
		t.Fatalf("provider calls=%v", calls)
	}
}
