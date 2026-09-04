package naming

import "testing"

func TestDisplayNameUsesChineseCountry(t *testing.T) {
	if got := DisplayName("US", "United States", 7, 1.5); got != "美国 007 1.5x" {
		t.Fatalf("DisplayName()=%q", got)
	}
}

func TestHasCountryHintIncludesUnflaggedLocations(t *testing.T) {
	for _, value := range []string{"澳门 01", "阿联酋高速", "Turkey Premium", "巴西-02", "上海 BGP"} {
		if !HasCountryHint(value) {
			t.Fatalf("country hint not recognized: %q", value)
		}
	}
	for _, value := range []string{"剩余流量 10GB", "距离到期 3 天", "官网通知"} {
		if HasCountryHint(value) {
			t.Fatalf("notice recognized as country: %q", value)
		}
	}
}
