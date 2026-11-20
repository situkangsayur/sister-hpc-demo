package main

import (
	"math"
	"sort"
)

// Stats adalah ringkasan RTT dalam milidetik.
type Stats struct {
	Count  int     `json:"count"`
	Min    float64 `json:"min"`
	Median float64 `json:"median"`
	P95    float64 `json:"p95"`
	Max    float64 `json:"max"`
	Mean   float64 `json:"mean"`
}

// Summarize menghitung min, median, p95, max, dan rata-rata.
func Summarize(xs []float64) Stats {
	if len(xs) == 0 {
		return Stats{}
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	sum := 0.0
	for _, x := range s {
		sum += x
	}
	return Stats{
		Count:  len(s),
		Min:    round(s[0]),
		Median: round(quantile(s, 0.5)),
		P95:    round(quantile(s, 0.95)),
		Max:    round(s[len(s)-1]),
		Mean:   round(sum / float64(len(s))),
	}
}

// quantile dengan interpolasi linear pada data terurut.
func quantile(s []float64, q float64) float64 {
	if len(s) == 1 {
		return s[0]
	}
	pos := q * float64(len(s)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return s[lo] + (s[hi]-s[lo])*(pos-float64(lo))
}

func round(x float64) float64 { return math.Round(x*1000) / 1000 }

// stallInfo menghitung berapa message "tersendat" dan berapa persen di
// antaranya terjadi bersamaan dengan message tersendat di stream lain
// (tanda head-of-line blocking yang menular antar stream).
func stallInfo(samples []HOLSample, window float64) (stalls int, sharedPct float64, threshold float64) {
	var lats []float64
	for _, s := range samples {
		if !s.Lost {
			lats = append(lats, s.Lat)
		}
	}
	st := Summarize(lats)
	threshold = round(st.Median*1.5 + 15)
	var bad []HOLSample
	for _, s := range samples {
		if s.Lost || s.Lat > threshold {
			bad = append(bad, s)
		}
	}
	shared := 0
	for _, a := range bad {
		for _, b := range bad {
			if a.Stream != b.Stream && math.Abs(a.T-b.T) <= window {
				shared++
				break
			}
		}
	}
	if len(bad) > 0 {
		sharedPct = round(100 * float64(shared) / float64(len(bad)))
	}
	return len(bad), sharedPct, threshold
}
