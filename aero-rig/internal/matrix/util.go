package matrix

import "math"

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}
