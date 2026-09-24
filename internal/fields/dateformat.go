package fields

import "strings"

// ── A date box's layout: an ORDER and a SEPARATOR ───────────────────────
//
// The owner, 2026-09-23: "tarih biçimi ve ayraçlarını ayrı ayrı seçebilir
// olalım tarih kısmında yani mmddyyyy mmddyy ddmmyy ddmmyyyy seçsin gibi
// ayrıca da - / . ne isterse onu seçsin".
//
// Two questions, two answers — and ONE stored value: the PATTERN the two
// make ("DD.MM.YYYY", "MM/DD/YY", "YYYY-MM-DD"). That is deliberate. The
// three layouts this app has always written are members of the new set,
// so a request made before this change keeps the layout it was made with
// and no document anybody has already signed is re-formatted under them.
//
// ⚠ The pattern decides how a date is STAMPED and what a typed entry may
// look like. It never touches the audit trail, which stays ISO 8601: a
// two-digit year is a fine thing to print on a form and a poor thing to
// keep a record in.
const (
	OrderDMY4 = "DDMMYYYY"
	OrderDMY2 = "DDMMYY"
	OrderMDY4 = "MMDDYYYY"
	OrderMDY2 = "MMDDYY"
	OrderYMD  = "YYYYMMDD"
)

// The separators offered. A space is one of them: a form printed in boxes
// often wants "31 12 2026" and nothing between the numbers.
const (
	SepDot   = "."
	SepSlash = "/"
	SepDash  = "-"
	SepSpace = " "
)

// DateOrders lists the arrangements, in the order the editor offers them.
func DateOrders() []string {
	return []string{OrderDMY4, OrderDMY2, OrderMDY4, OrderMDY2, OrderYMD}
}

// DateSeparators lists the separators, in the order the editor offers them.
func DateSeparators() []string {
	return []string{SepDot, SepSlash, SepDash, SepSpace}
}

// orderParts are an arrangement's three words, in the order they are
// written.
func orderParts(order string) []string {
	switch order {
	case OrderDMY2:
		return []string{"DD", "MM", "YY"}
	case OrderMDY4:
		return []string{"MM", "DD", "YYYY"}
	case OrderMDY2:
		return []string{"MM", "DD", "YY"}
	case OrderYMD:
		return []string{"YYYY", "MM", "DD"}
	}
	return []string{"DD", "MM", "YYYY"}
}

// NormalizeDateOrder falls back to day-first with a full year, which is
// what a Turkish form expects.
func NormalizeDateOrder(o string) string {
	for _, k := range DateOrders() {
		if k == o {
			return k
		}
	}
	return OrderDMY4
}

// NormalizeDateSeparator falls back to the dot, for the same reason.
func NormalizeDateSeparator(s string) string {
	for _, k := range DateSeparators() {
		if k == s {
			return k
		}
	}
	return SepDot
}

// DateFormat is the pattern an arrangement and a separator make.
func DateFormat(order, sep string) string {
	return strings.Join(orderParts(NormalizeDateOrder(order)), NormalizeDateSeparator(sep))
}

// SplitFormat takes a pattern back apart into the two answers that made
// it. Anything it does not recognise comes back as the defaults, which is
// the same pattern NormalizeFormat would have given.
func SplitFormat(format string) (order, sep string) {
	sep = SepDot
	for _, r := range format {
		if r != 'D' && r != 'M' && r != 'Y' {
			sep = string(r)
			break
		}
	}
	sep = NormalizeDateSeparator(sep)
	letters := strings.Map(func(r rune) rune {
		if r == 'D' || r == 'M' || r == 'Y' {
			return r
		}
		return -1
	}, format)
	return NormalizeDateOrder(letters), sep
}

// goLayout is the pattern as Go writes a time.
func goLayout(format string) string {
	order, sep := SplitFormat(format)
	out := make([]string, 0, 3)
	for _, p := range orderParts(order) {
		switch p {
		case "YYYY":
			out = append(out, "2006")
		case "YY":
			out = append(out, "06")
		case "MM":
			out = append(out, "01")
		default:
			out = append(out, "02")
		}
	}
	return strings.Join(out, sep)
}

// dateLayouts are every shape a typed date may arrive in for a box with
// this pattern: ISO (what the browser's date control posts), the pattern
// itself, its full-year sibling — somebody typing "2026" into a two-digit
// box means the year, not a refusal — and the three layouts this app used
// to offer, so an entry copied from an older form is still understood.
func dateLayouts(format string) []string {
	order, sep := SplitFormat(format)
	full := map[string]string{OrderDMY2: OrderDMY4, OrderMDY2: OrderMDY4}
	out := []string{ISO, goLayout(DateFormat(order, sep))}
	if f, ok := full[order]; ok {
		out = append(out, goLayout(DateFormat(f, sep)))
	}
	return append(out, "02.01.2006", "01/02/2006", "2006-01-02", "02/01/2006")
}
