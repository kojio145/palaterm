package sheet

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// formatNumber renders a cell number the way a person would type it back:
// no exponent, no trailing zeros. Values are first rounded to 15 significant
// digits (what Excel itself keeps and shows) so binary noise such as
// 0.30000000000000004 does not leak into host names or port numbers.
func formatNumber(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return ""
	}
	if r, err := strconv.ParseFloat(strconv.FormatFloat(v, 'g', 15, 64), 64); err == nil {
		v = r
	}
	if v == 0 {
		return "0" // never "-0"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// formatPadded renders a whole number the way a zero-padding format shows
// it: "000" makes 1 read 001, "\"TKY-\"0000" makes 12 read TKY-0012 — a
// site code or asset number typed as a number and shown padded, which a
// name or a match key is built from. Only that shape is handled (zeros,
// with quoted or escaped text around them, first section); anything else,
// or a value with a fraction, is not (ok false).
func formatPadded(v float64, code string) (string, bool) {
	if code == "" || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v != math.Trunc(v) || v > 1e15 {
		return "", false
	}
	var pre, post strings.Builder
	zeros, done := 0, false
	lit := func(s string) {
		if zeros > 0 {
			done = true
			post.WriteString(s)
		} else {
			pre.WriteString(s)
		}
	}
	for i := 0; i < len(code); i++ {
		ch := code[i]
		switch {
		case ch == ';':
			i = len(code) // the first section only
		case ch == '"':
			j := strings.IndexByte(code[i+1:], '"')
			if j < 0 {
				return "", false
			}
			lit(code[i+1 : i+1+j])
			i += j + 1
		case ch == '\\' && i+1 < len(code):
			r := []rune(code[i+1:])[0]
			lit(string(r))
			i += len(string(r))
		case ch == '0':
			if done {
				return "", false // zeros on both sides of a text: not this shape
			}
			zeros++
		case ch == '-' || ch == ' ':
			lit(string(ch))
		default:
			return "", false
		}
	}
	if zeros < 2 {
		return "", false // "0" is the plain number
	}
	n := strconv.FormatFloat(v, 'f', 0, 64)
	for len(n) < zeros {
		n = "0" + n
	}
	return pre.String() + n + post.String(), true
}

// isBuiltinDateFmt reports whether a built-in number format id is a date or
// time. 27–36 and 50–58 are the Japanese-locale (CJK) date formats, which
// files saved by Japanese Excel reference without defining them.
func isBuiltinDateFmt(id int) bool {
	switch {
	case id >= 14 && id <= 22, id >= 45 && id <= 47,
		id >= 27 && id <= 36, id >= 50 && id <= 58:
		return true
	}
	return false
}

// isDateFormatCode reports whether a custom format code shows a date/time:
// it has y/m/d/h/s tokens outside quoted literals, escapes and [..] sections
// (colours, locales). Only the first (positive) section is examined.
func isDateFormatCode(code string) bool {
	inQuote := false
	for i := 0; i < len(code); i++ {
		ch := code[i]
		if inQuote {
			if ch == '"' {
				inQuote = false
			}
			continue
		}
		switch ch {
		case '"':
			inQuote = true
		case '\\', '_', '*':
			i++ // the next character is a literal / padding
		case '[':
			j := strings.IndexByte(code[i:], ']')
			if j < 0 {
				return false
			}
			// [h], [mm], [ss] are elapsed-time tokens, not modifiers.
			inner := strings.ToLower(code[i+1 : i+j])
			if inner != "" && strings.Trim(inner, "hms") == "" {
				return true
			}
			i += j
		case ';':
			return false
		case 'y', 'Y', 'm', 'M', 'd', 'D', 'h', 'H', 's', 'S':
			return true
		}
	}
	return false
}

// formatSerialDate renders an Excel serial date. ok is false when the value
// is outside Excel's date range, so the caller can fall back to a number.
func formatSerialDate(v float64, date1904 bool) (string, bool) {
	if math.IsNaN(v) || v < 0 || v > 2958466 {
		return "", false
	}
	secs := int64(math.Round(v * 86400))
	days, rem := secs/86400, secs%86400
	if days == 0 && rem > 0 {
		return formatClock(rem), true // a time without a date
	}
	var base time.Time
	switch {
	case date1904:
		base = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	case days < 60:
		// Excel counts the non-existent 1900-02-29 as serial 60.
		base = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC)
	default:
		base = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	}
	d := base.AddDate(0, 0, int(days)).Format("2006/01/02")
	if rem == 0 {
		return d, true
	}
	return d + " " + formatClock(rem), true
}

func formatClock(secs int64) string {
	t := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(secs) * time.Second)
	if secs%60 != 0 {
		return t.Format("15:04:05")
	}
	return t.Format("15:04")
}

// formatISODateTime renders an ISO 8601 timestamp (xlsx t="d", SpreadsheetML
// DateTime) in the same style as serial dates; unparseable input is kept.
func formatISODateTime(s string) string {
	s = strings.TrimSpace(s)
	for _, layout := range []string{
		"2006-01-02T15:04:05.999999999Z07:00", "2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02",
	} {
		t, err := time.Parse(layout, s)
		if err != nil {
			continue
		}
		secs := int64(t.Hour()*3600 + t.Minute()*60 + t.Second())
		// SpreadsheetML writes pure times on Excel's day zero.
		if t.Year() == 1899 && t.Month() == 12 && t.Day() == 31 && secs > 0 {
			return formatClock(secs)
		}
		if secs == 0 {
			return t.Format("2006/01/02")
		}
		return t.Format("2006/01/02") + " " + formatClock(secs)
	}
	return s
}
