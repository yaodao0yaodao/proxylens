package quality

import (
	"fmt"
	"github.com/yaodao0yaodao/proxylens/internal/model"
	"math"
	"testing"
	"time"
)

func TestConfidenceAndRobustOutlierHandling(t *testing.T) {
	now := time.Now()
	ok := true
	lat := 100.0
	var samples []model.Measurement
	for i := 0; i < 20; i++ {
		l := lat
		if i == 19 {
			l = 5000
		}
		samples = append(samples, model.Measurement{Kind: "availability", Available: &ok, TestedAt: now.Add(-time.Duration(i) * time.Hour)}, model.Measurement{Kind: "latency", LatencyMS: &l, TestedAt: now.Add(-time.Duration(i) * time.Hour)})
	}
	q := Calculate(model.Node{ID: "n", CountryCode: "JP"}, samples, now)
	if q.Availability >= 1 || q.Availability <= 0.8 {
		t.Fatalf("unexpected Wilson bound %v", q.Availability)
	}
	if q.AverageLatencyMS >= 500 {
		t.Fatalf("outlier dominated latency: %v", q.AverageLatencyMS)
	}
}

func TestOnlyAvailabilityKindAffectsAvailability(t *testing.T) {
	now := time.Now()
	no := false
	yes := true
	q := Calculate(model.Node{ID: "n"}, []model.Measurement{{Kind: "availability", Available: &yes, TestedAt: now}, {Kind: "latency", Available: &no, TestedAt: now}}, now)
	if q.Samples != 1 || q.AvailabilityRaw != 1 {
		t.Fatalf("non-availability probes changed availability: %+v", q)
	}
}
func TestDisplayedLatencyIsNeverGeographicallyReduced(t *testing.T) {
	now := time.Now()
	ok := true
	lat := 10.0
	q := Calculate(model.Node{ID: "n", CountryCode: "US"}, []model.Measurement{{Kind: "availability", Available: &ok, TestedAt: now}, {Kind: "latency", LatencyMS: &lat, TestedAt: now}}, now)
	if q.AverageLatencyMS != lat {
		t.Fatalf("displayed latency was geographically reduced: %v", q.AverageLatencyMS)
	}
}

func TestDisplayedLatencyKeepsObservedExperience(t *testing.T) {
	now := time.Now()
	firstLatency, firstDirect := 200.0, 100.0
	secondLatency, secondDirect := 300.0, 200.0
	q := Calculate(model.Node{ID: "n"}, []model.Measurement{
		{Kind: "latency", LatencyMS: &firstLatency, DirectLatencyMS: &firstDirect, TestedAt: now},
		{Kind: "latency", LatencyMS: &secondLatency, DirectLatencyMS: &secondDirect, TestedAt: now},
	}, now)
	if math.Abs(q.AverageLatencyMS-275.25) > 0.001 {
		t.Fatalf("displayed latency no longer represents observed samples: %v", q.AverageLatencyMS)
	}
}

func TestLatencyUtilityKeepsAbsoluteExperienceDominant(t *testing.T) {
	near := latencyUtility(80, 35)
	far := latencyUtility(300, 185)
	if near <= far {
		t.Fatalf("physical allowance hid a materially slower path: near=%v far=%v", near, far)
	}
	perfect := latencyUtility(1, 185)
	if far >= perfect*0.7 {
		t.Fatalf("distant path received an excessive physical-distance bonus: far=%v perfect=%v", far, perfect)
	}
}

func TestDirectCorrectionCannotBeatAccessBaseline(t *testing.T) {
	now := time.Now()
	fastNode, quietDirect := 120.0, 60.0
	busyNode, busyDirect := 260.0, 200.0
	q := Calculate(model.Node{ID: "n"}, []model.Measurement{
		{Kind: "latency", LatencyMS: &fastNode, DirectLatencyMS: &quietDirect, TestedAt: now},
		{Kind: "latency", LatencyMS: &busyNode, DirectLatencyMS: &busyDirect, TestedAt: now},
	}, now)
	if q.AverageLatencyMS < 120 {
		t.Fatalf("displayed latency was unexpectedly corrected: %v", q.AverageLatencyMS)
	}
}

func TestDiverseCoverageHasNoImplicitEightNodeCap(t *testing.T) {
	var nodes []model.Node
	qualities := map[string]model.Quality{}
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("n%d", i)
		nodes = append(nodes, model.Node{ID: id})
		profile := map[int]float64{}
		for hour := 0; hour < 12; hour++ {
			profile[hour] = 1
		}
		if i == 1 {
			for hour := 0; hour < 12; hour++ {
				profile[hour] = .9
			}
		} else if i >= 2 {
			profile[i-2] = 0
		}
		qualities[id] = model.Quality{UnavailableByHour: profile}
	}
	if selected := DiverseCoverageTop(nodes, qualities, 2, 0); len(selected) <= 8 {
		t.Fatalf("unlimited coverage selection was still capped: %d", len(selected))
	}
}
