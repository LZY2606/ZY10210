package web

import "strconv"

func formatN(v float64, prec int) string {
	return strconv.FormatFloat(v, 'f', prec, 64)
}
