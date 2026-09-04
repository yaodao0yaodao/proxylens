package dependency

import "testing"

func TestParseVersion(t *testing.T) {
	for _, test := range []struct {
		output string
		want   string
	}{
		{"sing-box version 1.14.0\nEnvironment: go1.26 linux/arm64", "1.14.0"},
		{"sing-box version v1.13.12", "1.13.12"},
	} {
		got, err := parseVersion(test.output)
		if err != nil || got != test.want {
			t.Fatalf("parseVersion(%q)=%q,%v want %q", test.output, got, err, test.want)
		}
	}
	if _, err := parseVersion("unexpected output"); err == nil {
		t.Fatal("parseVersion accepted output without a version")
	}
}
