// Copyright 2026 The typesafe-sdk-go Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package jsonx

import (
	"math"
	"strconv"
)

// Decimal exponents, in scientific notation (d.ddd × 10^exp), between which
// each writer uses positional notation. Outside the range it writes an
// exponent.
const (
	// pydanticMinFixedExp is the lower end of zmij 1.0.6's positional range
	// for a double, (-5..=15).contains(&dec_exp) in its write function;
	// pydantic-core 2.46.4 writes floats through serde_json 1.0.149, which
	// calls zmij.
	pydanticMinFixedExp = -5
	// reprMinFixedExp is CPython 3.14.3's: format_float_short in
	// Python/pystrtod.c uses an exponent when decpt <= -4 || decpt > 16, and
	// decpt is the scientific exponent plus 1.
	reprMinFixedExp = -4
	// maxFixedExp is the upper end of both ranges.
	maxFixedExp = 15
)

// PydanticFloat returns f as pydantic_core.to_json (pydantic-core 2.46.4)
// writes a Python float: the shortest digits that read back as f, in
// positional notation with at least one fraction digit when
// 1e-5 <= |f| < 1e16 or f is a zero (0.00001, 100000.0, -0.0), and else as
// d[.ddd]e±N with the exponent's sign and no padding (1e-6, 1.5e-7, 1e+16).
//
// A value that is not finite is written NaN, Infinity or -Infinity, which is
// not JSON. pydantic-core writes these spellings, and the port keeps them in
// the one place they reach a prompt: a state whose number literal overflows a
// double (1e400) is written Infinity by PydanticJSON and EncodeState, as
// upstream's to_json(json.loads(text)) writes it. The ordered writer refuses
// a value that is not finite (ErrNonFinite).
//
// PydanticFloat is the spelling of every text that goes into a prompt: the
// state, the schema, and the JSON instructions and criteria.
func PydanticFloat(f float64) string {
	return string(appendPydanticFloat(nil, f))
}

// appendPydanticFloat appends PydanticFloat(f) to dst.
func appendPydanticFloat(dst []byte, f float64) []byte {
	switch {
	case math.IsNaN(f):
		return append(dst, "NaN"...)
	case math.IsInf(f, 1):
		return append(dst, "Infinity"...)
	case math.IsInf(f, -1):
		return append(dst, "-Infinity"...)
	}
	var scratch [32]byte
	sci := strconv.AppendFloat(scratch[:0], f, 'e', -1, 64)
	var buf [24]byte
	neg, digits, exp, expText := splitScientific(sci, &buf)
	if neg {
		dst = append(dst, '-')
	}
	if exp >= pydanticMinFixedExp && exp <= maxFixedExp {
		return appendPositional(dst, digits, exp)
	}
	dst = appendMantissa(dst, digits)
	dst = append(dst, 'e', expText[0])
	// strconv pads the exponent to two digits; zmij writes no padding.
	if expText[1] == '0' && len(expText) == 3 {
		return append(dst, expText[2])
	}
	return append(dst, expText[1:]...)
}

// ReprFloat returns f as Python 3.14.3's repr writes a float, which is also
// json.dumps's spelling of every finite float: the shortest digits that read
// back as f, in positional notation with at least one fraction digit when
// 1e-4 <= |f| < 1e16 or f is a zero (0.0001, 100000.0, -0.0), and else as
// d[.ddd]e±NN with the exponent's sign and at least two exponent digits
// (1e-05, 1.5e-07, 1e+16).
//
// A value that is not finite is written nan, inf or -inf, as repr writes it;
// that is not JSON, and the ordered writer refuses such a value
// (ErrNonFinite).
//
// ReprFloat is the spelling of the response body, which then equals what
// json.dumps writes for upstream's model_dump(mode="json").
func ReprFloat(f float64) string {
	return string(appendReprFloat(nil, f))
}

// appendReprFloat appends ReprFloat(f) to dst.
func appendReprFloat(dst []byte, f float64) []byte {
	switch {
	case math.IsNaN(f):
		return append(dst, "nan"...)
	case math.IsInf(f, 1):
		return append(dst, "inf"...)
	case math.IsInf(f, -1):
		return append(dst, "-inf"...)
	}
	var scratch [32]byte
	sci := strconv.AppendFloat(scratch[:0], f, 'e', -1, 64)
	var buf [24]byte
	neg, digits, exp, _ := splitScientific(sci, &buf)
	if exp >= reprMinFixedExp && exp <= maxFixedExp {
		if neg {
			dst = append(dst, '-')
		}
		return appendPositional(dst, digits, exp)
	}
	// strconv's 'e' format with the shortest digits is repr's exponent form.
	return append(dst, sci...)
}

// splitScientific takes strconv's shortest 'e' format of a finite double,
// [-]d[.ddd]e±dd[d], apart without changing it. digits, written into buf, has
// the decimal point removed, exp is the exponent's value, and expText is the
// exponent's sign and digits.
func splitScientific(sci []byte, buf *[24]byte) (neg bool, digits []byte, exp int, expText []byte) {
	if sci[0] == '-' {
		neg = true
		sci = sci[1:]
	}
	e := len(sci) - 1
	for sci[e] != 'e' {
		e--
	}
	expText = sci[e+1:]
	for _, c := range expText[1:] {
		exp = exp*10 + int(c-'0')
	}
	if expText[0] == '-' {
		exp = -exp
	}
	digits = buf[:0]
	digits = append(digits, sci[0])
	if e > 1 {
		digits = append(digits, sci[2:e]...) // skip the decimal point
	}
	return neg, digits, exp, expText
}

// appendMantissa appends digits as d or d.ddd.
func appendMantissa(dst, digits []byte) []byte {
	dst = append(dst, digits[0])
	if len(digits) > 1 {
		dst = append(dst, '.')
		dst = append(dst, digits[1:]...)
	}
	return dst
}

// appendPositional appends digits × 10^(exp-len(digits)+1) in positional
// notation, with ".0" after an integral value.
func appendPositional(dst, digits []byte, exp int) []byte {
	switch {
	case exp >= len(digits)-1:
		dst = append(dst, digits...)
		for range exp - (len(digits) - 1) {
			dst = append(dst, '0')
		}
		return append(dst, '.', '0')
	case exp >= 0:
		dst = append(dst, digits[:exp+1]...)
		dst = append(dst, '.')
		return append(dst, digits[exp+1:]...)
	default:
		dst = append(dst, '0', '.')
		for range -exp - 1 {
			dst = append(dst, '0')
		}
		return append(dst, digits...)
	}
}
