package catalog

import (
	"net/netip"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Exception message parameterising: the variable parts of a message (ids,
// addresses, timestamps, numbers, quoted values, URLs, file paths) are
// replaced with fixed placeholders, so two throws of one bug that differ only
// in their data produce the same text. What tells one bug from another is
// kept: a status or error code after the wording that names it, and the
// words of a route. Pure string work — no tenant, no clock, no locale.

// Placeholders, one per kind of variable text.
const (
	msgQuoted = "<q>"
	msgURL    = "<url>"
	msgUUID   = "<uuid>"
	msgDate   = "<date>"
	msgIP     = "<ip>"
	msgPath   = "<path>"
	msgHex    = "<hex>"
	msgID     = "<id>"
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
	msgIPv4Re = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?\b`)
	// IPv6 candidates; netip decides which ones are addresses.
	msgIPv6BracketRe = regexp.MustCompile(`\[([0-9A-Fa-f:.]+)\](?::\d{1,5})?`)
	msgIPv6Re        = regexp.MustCompile(`[0-9A-Fa-f:.]+`)
	// An ISO date with an optional time and zone, or a bare clock time.
	msgDateRe = regexp.MustCompile(
		`\b\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2}(?:[.,]\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?)?\b` +
			`|\b\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\b`)
	// A path needs two segments ("/users/42"), so a lone "/" or "and/or"
	// stays; the leading group is the character the path follows. A segment
	// may be a placeholder an earlier rule left ("/orders/<uuid>/lines").
	msgUnixPathRe = regexp.MustCompile(`(?:^|[\s=(\[,;:])(?:~|\.{1,2})?(?:/(?:[\w.@%+~-]+|<\w+>)){2,}/?`)
	// A file name ends in an extension that starts with a letter, so the
	// "5" of "/carts/1.5" is none.
	msgFileNameRe = regexp.MustCompile(`\.[A-Za-z][A-Za-z0-9]{0,4}$`)
	// A Windows path may hold spaces in its directories ("C:\Program
	// Files\…"): a segment with spaces counts only when a backslash ends it,
	// so the path stops at the first space after its last backslash.
	msgWinPathRe = regexp.MustCompile(
		`\b[A-Za-z]:\\(?:(?:[^\s"'<>:\\]|<\w+>)+(?: (?:[^\s"'<>:\\]|<\w+>)+)*\\)*(?:[^\s"'<>:\\]|<\w+>)*`)
	msgHexLitRe = regexp.MustCompile(`\b0[xX][0-9A-Fa-f]+\b`)
	// Object identity suffixes ("Cart@1b2c3d") and long hex runs.
	msgHexAtRe  = regexp.MustCompile(`@[0-9A-Fa-f]+\b`)
	msgHexRunRe = regexp.MustCompile(`\b[0-9A-Fa-f]{8,}\b`)
	// An id behind a lower-case prefix ("user_12345", "cus_9s6XAbCdEf"); the
	// prefix says what it identifies and is kept. An upper-case name with
	// digits ("ERR_HTTP2_STREAM_ERROR", "E_42") is a constant, not an id.
	msgPrefixedIDRe = regexp.MustCompile(`\b((?:[a-z]+_)+)[A-Za-z0-9]*\d[A-Za-z0-9]*\b`)
	// One pass over the numbers, first alternative first:
	//
	//  1. a code after the wording that names it — an HTTP status, a status,
	//     exit or error code, an errno, a SQLSTATE — of at most five
	//     characters, or an Oracle-style code ("ORA-00942"). It is kept: 404
	//     and 500 are different failures.
	//  2. a number with an optional unit ("250ms", "12.5secs") and, after
	//     the start, a space or one of =:([,; a minus sign ("Index -1").
	msgNumberRe = regexp.MustCompile(
		`(?i:\b(?:HTTP(?:/\d(?:\.\d)?)?(?: status)?|(?:(?:http|exit|response)[ _-]?)?status(?:[ _-]?code)?` +
			`|(?:(?:error|err|exit|response|return|result|sql)[ _-]?)?code|SQLSTATE|errno)[ :=#\[(]{1,3}(?:\d{1,5}|[0-9A-Z]{5})\b)` +
			`|\b(?:ORA|PLS|TNS)-\d{5}\b` +
			`|(?:(?:^|[\s=:(\[,;])-)?\b\d+(?:\.\d+)?[A-Za-z]{0,7}\b`)
)

// messageRule is one replacement step of the parameteriser.
type messageRule struct {
	name  string
	apply func(string) string
}

// messageRules run in this order, and the order is part of the result:
// quoted values go first because they may hold anything, URLs before the
// paths and numbers inside them, UUIDs before the hex runs and numbers they
// are made of, IPs before dates (three groups of an IPv6 address read as a
// clock time) and before numbers, dates before the paths that hold one, and
// paths before the ids in their segments. A digit run is a number however
// long it is, so hex needs a letter. Changing the order changes fingerprints.
var messageRules = []messageRule{
	{"quoted", func(s string) string {
		s = msgDoubleQuotedRe.ReplaceAllString(s, msgQuoted)
		s = msgSingleQuotedRe.ReplaceAllString(s, msgQuoted)
		return msgBackQuotedRe.ReplaceAllString(s, msgQuoted)
	}},
	{"url", func(s string) string { return msgURLRe.ReplaceAllString(s, msgURL) }},
	{"uuid", func(s string) string { return msgUUIDRe.ReplaceAllString(s, msgUUID) }},
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
	{"date", func(s string) string { return msgDateRe.ReplaceAllString(s, msgDate) }},
	{"path", func(s string) string {
		s = msgUnixPathRe.ReplaceAllStringFunc(s, replaceFilePath)
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
		// A run without a digit is a word ("defaced"), one without a letter
		// is a number.
		return msgHexRunRe.ReplaceAllStringFunc(s, func(m string) string {
			if hasDigit(m) && strings.ContainsAny(m, hexLetters) {
				return msgHex
			}
			return m
		})
	}},
	{"id", func(s string) string { return msgPrefixedIDRe.ReplaceAllString(s, "${1}"+msgID) }},
	{"number", func(s string) string { return msgNumberRe.ReplaceAllStringFunc(s, replaceNumber) }},
}

// replaceFilePath decides what a path match becomes. A path that names a
// file — it starts at the home or the working directory, or its last segment
// has an extension — is user data and becomes <path>. Any other path is kept
// as a route ("GET /cart/items" and "GET /checkout/pay" are different
// failures), and the rules that follow replace the ids in its segments, so
// "/cart/42/items" reads "/cart/<n>/items". This reads a route that ends in
// an extension as a file, and keeps a file that has none.
func replaceFilePath(m string) string {
	lead := ""
	if c := m[0]; c != '/' && c != '~' && c != '.' {
		lead, m = m[:1], m[1:]
	}
	name := strings.TrimRight(m, "./")
	if m[0] == '/' && !msgFileNameRe.MatchString(name) {
		return lead + m
	}
	return lead + msgPath
}

// replaceNumber decides what a msgNumberRe match becomes: a code after its
// wording stays as it is, a number is replaced, and the character a minus
// sign followed is kept.
func replaceNumber(m string) string {
	switch c := m[0]; {
	case c >= '0' && c <= '9', c == '-':
		return msgNumber
	case isWordByte(c):
		return m
	}
	return m[:1] + msgNumber
}

// ParameterizeMessage replaces the variable parts of an exception message
// with placeholders (<q>, <url>, <uuid>, <ip>, <date>, <path>, <hex>, <id>,
// <n>),
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

const hexLetters = "abcdefABCDEF"

// wordBytes are the bytes regexp's \w matches.
const wordBytes = "_0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func isWordByte(c byte) bool {
	return strings.IndexByte(wordBytes, c) >= 0
}
