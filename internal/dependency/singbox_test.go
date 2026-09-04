package dependency

import (
	"encoding/json"
	"runtime"
	"testing"
)

func TestCompareVersion(t *testing.T) {
	for _, test := range []struct {
		a, b string
		want int
	}{{"1.14.0", "1.13.19", 1}, {"1.13.19", "1.13.19", 0}, {"1.13.12", "1.14.0", -1}} {
		if got := compareVersion(test.a, test.b); got != test.want {
			t.Fatalf("compareVersion(%q,%q)=%d want %d", test.a, test.b, got, test.want)
		}
	}
}

func TestLinuxLibcVariant(t *testing.T) {
	if runtime.GOOS == "linux" {
		if got := linuxLibcVariant(); got != "-glibc" && got != "-musl" {
			t.Fatalf("unexpected libc variant %q", got)
		}
	}
}

func TestReleaseAssetJSONFields(t *testing.T) {
	var value release
	if err := json.Unmarshal([]byte(`{"tag_name":"v1.14.0","assets":[{"name":"asset","digest":"sha256:abc","browser_download_url":"https://example.test/a","size":12}]}`), &value); err != nil {
		t.Fatal(err)
	}
	if len(value.Assets) != 1 || value.Assets[0].BrowserDownloadURL == "" || value.Assets[0].Digest == "" {
		t.Fatalf("release asset fields not decoded: %+v", value)
	}
}
