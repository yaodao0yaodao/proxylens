package quality

import (
	"math"
	"sort"
	"time"

	"github.com/yaodao0yaodao/proxylens/internal/model"
)

type weighted struct{ v, w float64 }
type latencyWeighted struct {
	v, w   float64
	direct *float64
}

func Calculate(node model.Node, samples []model.Measurement, now time.Time) model.Quality {
	q := model.Quality{NodeID: node.ID, UnavailableByHour: map[int]float64{}, CalculatedAt: now}
	var latencyInputs []latencyWeighted
	var directInputs []weighted
	var weightedSuccess, weightedTotal, weightedSquares float64
	var availableN, totalN int
	var hourTotals [24]float64
	for _, m := range samples {
		age := now.Sub(m.TestedAt).Hours() / 24
		w := math.Pow(0.5, age/14)
		if w < 0.05 {
			w = 0.05
		}
		if (m.Kind == "cycle" || m.Kind == "availability") && (m.Available != nil || m.SampleCount > 0) {
			count, success := m.SampleCount, m.AvailableCount
			if count <= 0 {
				count = 1
				if m.Available != nil && *m.Available {
					success = 1
				}
			}
			if success < 0 {
				success = 0
			}
			if success > count {
				success = count
			}
			totalN += count
			availableN += success
			weightedSuccess += float64(success) * w
			weightedTotal += float64(count) * w
			weightedSquares += float64(count) * w * w
			hour := m.TestedAt.Local().Hour()
			hourTotals[hour] += float64(count) * w
			q.UnavailableByHour[hour] += float64(count-success) * w
		}
		if m.LatencyMS != nil && *m.LatencyMS >= 0 {
			count := m.SampleCount
			if count < 1 {
				count = 1
			}
			valueWeight := w * float64(count)
			latencyInputs = append(latencyInputs, latencyWeighted{*m.LatencyMS, valueWeight, m.DirectLatencyMS})
			if m.DirectLatencyMS != nil && *m.DirectLatencyMS >= 0 {
				directInputs = append(directInputs, weighted{*m.DirectLatencyMS, valueWeight})
			}
		}
	}
	var directFloor float64
	if len(directInputs) > 0 {
		sort.Slice(directInputs, func(i, j int) bool { return directInputs[i].v < directInputs[j].v })
		directFloor = weightedQuantile(directInputs, 0.10)
	}
	// Keep the displayed experience latency in real observed milliseconds.
	// Baseline and geographic compensation are useful for fair scoring, but
	// exposing that compensated value produced misleading 1 ms results.
	displayLatencies := make([]weighted, 0, len(latencyInputs))
	scoreLatencies := make([]weighted, 0, len(latencyInputs))
	for _, sample := range latencyInputs {
		displayLatencies = append(displayLatencies, weighted{sample.v, sample.w})
		commonJitter := 0.0
		if sample.direct != nil && directFloor > 0 {
			commonJitter = math.Max(0, *sample.direct-directFloor)
		}
		adjusted := sample.v - commonJitter
		if directFloor > 0 {
			// A proxy cannot legitimately erase the access network itself. This
			// floor prevents an imprecise batch correction from turning a real
			// observation into an artificial 1 ms score.
			adjusted = math.Max(directFloor, adjusted)
		} else {
			adjusted = math.Max(1, adjusted)
		}
		scoreLatencies = append(scoreLatencies, weighted{adjusted, sample.w})
	}
	q.Samples = totalN
	if q.Samples > 0 {
		q.AvailabilityRaw = float64(availableN) / float64(q.Samples)
		p, effectiveN := weightedSuccess/math.Max(weightedTotal, 1e-9), weightedTotal*weightedTotal/math.Max(weightedSquares, 1e-9)
		q.Availability = wilsonLowerWeighted(p, effectiveN, 1.645)
	}
	q.AverageLatencyMS = conservativeLatency(displayLatencies)
	scoreLatencyMS := conservativeLatency(scoreLatencies)
	for hour, total := range hourTotals {
		if total > 0 {
			q.UnavailableByHour[hour] /= total
		}
	}
	// Availability remains dominant. The former speed dimension has been
	// removed, so its weight is redistributed proportionally: 75% availability
	// and 25% latency.
	availabilityScore := clamp(q.Availability, 0.01, 1)
	latencyScore := 0.01
	if scoreLatencyMS > 0 && scoreLatencyMS < 800 {
		latencyScore = latencyUtility(scoreLatencyMS, PhysicalBaselineMS(node.CountryCode))
	}
	q.Priority = 100 * math.Pow(availabilityScore, 0.75) * math.Pow(latencyScore, 0.25)
	if q.Samples < 5 {
		confidence := 0.55 + 0.09*float64(q.Samples)
		q.Priority *= confidence
	}
	return q
}

// latencyUtility keeps actual interactive latency dominant while granting a
// small efficiency allowance to geographically distant exits. The allowance
// is intentionally blended rather than subtracted, so it can never turn a
// high-latency path into a near-perfect 1 ms result.
func latencyUtility(observedMS, physicalBaselineMS float64) float64 {
	absolute := math.Exp(-observedMS / 250)
	if physicalBaselineMS <= 0 {
		return clamp(absolute, 0.01, 1)
	}
	efficiency := math.Exp(-math.Max(0, observedMS-physicalBaselineMS) / 250)
	return clamp(0.80*absolute+0.20*efficiency, 0.01, 1)
}

func PhysicalBaselineMS(code string) float64 {
	switch code {
	case "JP":
		return 35
	case "KR":
		return 45
	case "TW", "HK", "MO":
		return 20
	case "SG", "MY", "TH", "VN", "PH":
		return 65
	case "AU", "NZ":
		return 125
	case "US", "CA":
		return 145
	case "GB", "FR", "DE", "NL":
		return 185
	case "RU":
		return 120
	case "IN":
		return 95
	default:
		return 0
	}
}

func wilsonLower(success, total int, z float64) float64 {
	if total == 0 {
		return 0
	}
	n := float64(total)
	p := float64(success) / n
	z2 := z * z
	return clamp((p+z2/(2*n)-z*math.Sqrt((p*(1-p)+z2/(4*n))/n))/(1+z2/n), 0, 1)
}

func wilsonLowerWeighted(p, effectiveN, z float64) float64 {
	if effectiveN <= 0 {
		return 0
	}
	p = clamp(p, 0, 1)
	z2 := z * z
	return clamp((p+z2/(2*effectiveN)-z*math.Sqrt((p*(1-p)+z2/(4*effectiveN))/effectiveN))/(1+z2/effectiveN), 0, 1)
}

func robustExperience(values []weighted, lowerIsBetter bool) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]weighted(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].v < sorted[j].v })
	lo, hi := weightedQuantile(sorted, 0.10), weightedQuantile(sorted, 0.90)
	var sum, weights float64
	for _, x := range values {
		v := clamp(x.v, lo, hi)
		sum += v * x.w
		weights += x.w
	}
	mean := sum / math.Max(weights, 1e-9)
	med := weightedQuantile(sorted, 0.50)
	if lowerIsBetter {
		return 0.65*med + 0.35*mean
	}
	return 0.55*med + 0.45*mean
}

// conservativeLatency makes recurring slow samples visible without exposing a
// separate stability number. P75 dominates while a winsorized mean prevents a
// single outlier from controlling a month of history.
func conservativeLatency(values []weighted) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]weighted(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].v < sorted[j].v })
	return 0.70*weightedQuantile(sorted, 0.75) + 0.30*robustExperience(values, true)
}
func weightedQuantile(sorted []weighted, q float64) float64 {
	var total float64
	for _, x := range sorted {
		total += x.w
	}
	target := q * total
	var seen float64
	for _, x := range sorted {
		seen += x.w
		if seen >= target {
			return x.v
		}
	}
	return sorted[len(sorted)-1].v
}
func normalizeHours(m map[int]float64) {
	var max float64
	for _, v := range m {
		if v > max {
			max = v
		}
	}
	if max == 0 {
		return
	}
	for h, v := range m {
		m[h] = v / max
	}
}
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func OutageSimilarity(a, b map[int]float64) float64 {
	var dot, aa, bb float64
	for h := 0; h < 24; h++ {
		x, y := a[h], b[h]
		dot += x * y
		aa += x * x
		bb += y * y
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return dot / math.Sqrt(aa*bb)
}

func DiverseTop(nodes []model.Node, qualities map[string]model.Quality, limit int, accept func(model.Node) bool) []model.Node {
	filtered := make([]model.Node, 0, len(nodes))
	for _, n := range nodes {
		if accept(n) {
			filtered = append(filtered, n)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool { return qualities[filtered[i].ID].Priority > qualities[filtered[j].ID].Priority })
	out := make([]model.Node, 0, limit)
	for _, candidate := range filtered {
		similar := false
		for _, chosen := range out {
			if OutageSimilarity(qualities[candidate.ID].UnavailableByHour, qualities[chosen.ID].UnavailableByHour) >= 0.88 {
				similar = true
				break
			}
		}
		if !similar {
			out = append(out, candidate)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

// DiverseCoverageTop starts with the best candidates, avoids strongly similar
// outage profiles, then adds a node only when it improves an observed weak
// hour. A non-positive maximum means no fixed cap; selection stops naturally
// once another node no longer improves time-of-day outage coverage.
func DiverseCoverageTop(nodes []model.Node, qualities map[string]model.Quality, minimum, maximum int) []model.Node {
	if maximum <= 0 || maximum > len(nodes) {
		maximum = len(nodes)
	}
	if minimum > maximum {
		minimum = maximum
	}
	chosen := make([]model.Node, 0, maximum)
	seen := map[string]bool{}
	for _, candidate := range nodes {
		if len(chosen) >= minimum {
			break
		}
		similar := false
		for _, existing := range chosen {
			if OutageSimilarity(qualities[candidate.ID].UnavailableByHour, qualities[existing.ID].UnavailableByHour) >= 0.88 {
				similar = true
				break
			}
		}
		if !similar || len(nodes)-len(seen) <= minimum-len(chosen) {
			chosen = append(chosen, candidate)
			seen[candidate.ID] = true
		}
	}
	for _, candidate := range nodes {
		if len(chosen) >= minimum {
			break
		}
		if !seen[candidate.ID] {
			chosen = append(chosen, candidate)
			seen[candidate.ID] = true
		}
	}
	for len(chosen) < maximum {
		bestIndex, bestGain := -1, 0.0
		for i, candidate := range nodes {
			if seen[candidate.ID] {
				continue
			}
			gain := 0.0
			for hour := 0; hour < 24; hour++ {
				current := 1.0
				observed := false
				for _, node := range chosen {
					if v, ok := qualities[node.ID].UnavailableByHour[hour]; ok {
						observed = true
						if v < current {
							current = v
						}
					}
				}
				v, ok := qualities[candidate.ID].UnavailableByHour[hour]
				if ok && observed && current >= 0.25 && v < current {
					gain += current - v
				}
			}
			if gain > bestGain {
				bestGain, bestIndex = gain, i
			}
		}
		if bestIndex < 0 || bestGain <= 0 {
			break
		}
		chosen = append(chosen, nodes[bestIndex])
		seen[nodes[bestIndex].ID] = true
	}
	return chosen
}
