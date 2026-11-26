package catalog

import (
	"net/netip"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Exception message parameterising: the variable parts of a message (ids,
// addresses, timestamps, numbers, quoted values, URLs, paths) are replaced
// with fixed placeholders, so two throws of one bug that differ only in
// their data produce the same text. Pure string work — no tenant, no clock,
// no locale.

// Placeholders, one per kind of variable text.
const (
	msgQuoted = "<q>"
	msgURL    = "<url>"
	msgUUID   = "<uuid>"
	msgDate   = "<date>"
	msgIP     = "<ip>"
	msgPath   = "<path>"
	msgHex    = "<hex>"
	msgNumber = "<n>"
)

const (
	// exceptionMessageInput caps how much of a raw message is read at all.
	exceptionMessageInput = 4096
	// ExceptionMessageMax caps a parameterised message, in bytes.
	ExceptionMessageMax = 256
	// msgIPv6Max is the longest IPv6 text form (an IPv4-mapped address).
	msgIPv6Max = 45
	// msgPlaceholderMax is the longest placeholder; a cut never leaves a
	// shorter, unclosed one behind.
	msgPlaceholderMax = len(msgUUID)
)

var (
	msgDoubleQuotedRe = regexp.MustCompile(`"[^"]{0,200}"`)
	// \B on both sides keeps an apostrophe inside a word ("can't") from
	// opening or closing a quoted value.
	msgSingleQuotedRe = regexp.MustCompile(`\B'[^']{0,200}'\B`)
	msgBackQuotedRe   = regexp.MustCompile("`[^`]{0,200}`")
	msgURLRe          = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9+.-]*://[^\s<>"']+`)
	msgUUIDRe         = regexp.MustCompile(
		`\b[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\b`)
	// An ISO date with an optional time and zone, or a bare clock time.
	msgDateRe = regexp.MustCompile(
		`\b\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2}(?:[.,]\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?)?\b` +
			`|\b\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\b`)
	msgIPv4Re = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?\b`)
	// IPv6 candidates; netip decides which ones are addresses.
	msgIPv6BracketRe = regexp.MustCompile(`\[([0-9A-Fa-f:.]+)\](?::\d{1,5})?`)
	msgIPv6Re        = regexp.MustCompile(`[0-9A-Fa-f:.]+`)
	// A path needs two segments ("/users/42"), so a lone "/" or "and/or"
	// stays; the leading group is the character the path follows.
	msgUnixPathRe = regexp.MustCompile(`(^|[\s=(\[,;:])(?:~|\.{1,2})?(?:/[\w.@%+~-]+){2,}/?`)
	msgWinPathRe  = regexp.MustCompile(`\b[A-Za-z]:\\[^\s"'<>:]+`)
	msgHexLitRe   = regexp.MustCompile(`\b0[xX][0-9A-Fa-f]+\b`)
	// Object identity suffixes ("Cart@1b2c3d") and long hex runs.
	msgHexAtRe  = regexp.MustCompile(`@[0-9A-Fa-f]+\b`)
	msgHexRunRe = regexp.MustCompile(`\b[0-9A-Fa-f]{8,}\b`)
	// A number with an optional short unit ("250ms", "10MB").
	msgNumberRe = regexp.MustCompile(`\b\d+(?:\.\d+)?[A-Za-z]{0,3}\b`)
)

// messageRule is one replacement step of the parameteriser.
type messageRule struct {
	name  string
	apply func(string) string
}

// messageRules run in this order, and the order is part of the result:
// quoted values go first because they may hold anything, URLs before the
// paths and numbers inside them, UUIDs and dates before the hex runs and
// numbers they are made of, IPs before numbers, and paths before the ids in
// their segments. Changing the order changes fingerprints.
var messageRules = []messageRule{
	{"quoted", func(s string) string {
		s = msgDoubleQuotedRe.ReplaceAllString(s, msgQuoted)
		s = msgSingleQuotedRe.ReplaceAllString(s, msgQuoted)
		return msgBackQuotedRe.ReplaceAllString(s, msgQuoted)
	}},
	{"url", func(s string) string { return msgURLRe.ReplaceAllString(s, msgURL) }},
	{"uuid", func(s string) string { return msgUUIDRe.ReplaceAllString(s, msgUUID) }},
	{"date", func(s string) string { return msgDateRe.ReplaceAllString(s, msgDate) }},
	{"ip", func(s string) string {
		s = msgIPv6BracketRe.ReplaceAllStringFunc(s, func(m string) string {
			if isIPv6(msgIPv6BracketRe.FindStringSubmatch(m)[1]) {
				return msgIP
			}
			return m
		})
		// IPv6 before IPv4, so an IPv4-mapped address is taken whole.
		return msgIPv4Re.ReplaceAllString(replaceBareIPv6(s), msgIP)
	}},
	{"path", func(s string) string {
		s = msgUnixPathRe.ReplaceAllString(s, "${1}"+msgPath)
		return msgWinPathRe.ReplaceAllString(s, msgPath)
	}},
	{"hex", func(s string) string {
		s = msgHexLitRe.ReplaceAllString(s, msgHex)
		s = msgHexAtRe.ReplaceAllStringFunc(s, func(m string) string {
			if hasDigit(m) {
				return "@" + msgHex
			}
			return m
		})
		// A run without a digit is a word ("deadbeef" is rare, "defaced" is
		// English); a run of digits alone still counts, so an id is <hex>
		// whether or not it happens to contain a letter.
		return msgHexRunRe.ReplaceAllStringFunc(s, func(m string) string {
			if hasDigit(m) {
				return msgHex
			}
			return m
		})
	}},
	{"number", func(s string) string { return msgNumberRe.ReplaceAllString(s, msgNumber) }},
}

// ParameterizeMessage replaces the variable parts of an exception message
// with placeholders (<q>, <url>, <uuid>, <date>, <ip>, <path>, <hex>, <n>),
// collapses whitespace and bounds the result to ExceptionMessageMax bytes.
// It is idempotent: parameterising its own output changes nothing.
func ParameterizeMessage(msg string) string {
	s := cutBytes(strings.ToValidUTF8(msg, ""), exceptionMessageInput)
	// A replacement or the cut can expose a new match (a quote pair around a
	// placeholder, a number at the new end), so run to a fixed point. Every
	// changing pass removes literal text, which bounds the passes; the cap
	// only guards that argument.
	for range exceptionMessageInput {
		next := parameterizeOnce(s)
		if next == s {
			break
		}
		s = next
	}
	return s
}

// parameterizeOnce is one pass: whitespace, every rule in order, the cut.
func parameterizeOnce(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	for _, rule := range messageRules {
		s = rule.apply(s)
	}
	return strings.TrimRight(cutMessage(s), " ")
}

// cutMessage bounds s to ExceptionMessageMax bytes without leaving half a
// placeholder at the end.
func cutMessage(s string) string {
	if len(s) <= ExceptionMessageMax {
		return s
	}
	s = cutBytes(s, ExceptionMessageMax)
	tail := s[len(s)-msgPlaceholderMax+1:]
	if open := strings.LastIndexByte(tail, '<'); open >= 0 && !strings.Contains(tail[open:], ">") {
		s = s[:len(s)-len(tail)+open]
	}
	return s
}

// cutBytes truncates s to at most limit bytes on a rune boundary.
func cutBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}

// replaceBareIPv6 replaces unbracketed IPv6 addresses. A candidate must not
// touch a word character on either side ("Foo::bar" is not an address), and
// trailing sentence punctuation is kept.
func replaceBareIPv6(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range msgIPv6Re.FindAllStringIndex(s, -1) {
		start, end := m[0], m[1]
		for end > start && (s[end-1] == '.' || s[end-1] == ':') && !isIPv6(s[start:end]) {
			end--
		}
		if !isIPv6(s[start:end]) || (start > 0 && isWordByte(s[start-1])) ||
			(end < len(s) && isWordByte(s[end])) {
			continue
		}
		b.WriteString(s[last:start])
		b.WriteString(msgIP)
		last = end
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// isIPv6 reports whether s is an IPv6 address literal. A bare "::" is the
// unspecified address but reads as an operator in a message, so it is not
// one here.
func isIPv6(s string) bool {
	if len(s) > msgIPv6Max || strings.Count(s, ":") < 2 || strings.Trim(s, ":.") == "" {
		return false
	}
	addr, err := netip.ParseAddr(s)
	return err == nil && addr.Is6()
}

func hasDigit(s string) bool {
	return strings.ContainsAny(s, "0123456789")
}

// wordBytes are the bytes regexp's \w matches.
const wordBytes = "_0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func isWordByte(c byte) bool {
	return strings.IndexByte(wordBytes, c) >= 0
}
