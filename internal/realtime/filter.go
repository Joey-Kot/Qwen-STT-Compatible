// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
// SPDX-License-Identifier: GPL-3.0-or-later

package realtime

import "math"

var decimatorCoefficients = func() [63]float64 {
	var coefficients [63]float64
	const cutoff = 3600.0 / 24000
	var sum float64
	for i := range coefficients {
		x := float64(i - 31)
		value := 2 * cutoff
		if x != 0 {
			value = math.Sin(2*math.Pi*cutoff*x) / (math.Pi * x)
		}
		value *= 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/62)
		coefficients[i] = value
		sum += value
	}
	for i := range coefficients {
		coefficients[i] /= sum
	}
	return coefficients
}()
