package ident

import "strconv"

func strconvFormat(x float64) string {
	return strconv.FormatFloat(x, 'f', 1, 64)
}
