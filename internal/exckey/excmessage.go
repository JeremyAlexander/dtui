package exckey

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
	// msgWinPathWord is one word of a segment of a Windows path.
	msgWinPathWord = `(?:[^\s"'<>:\\()\[\]{},;]|<\w+>|\(\w+\))+`
	// msgWinPathWords is how many words may follow the first in a segment:
	// "Documents and Settings" has two.
	msgWinPathWords = `2`
)

// msgPlaceholders are all of them, for the cut.
var msgPlaceholders = []string{msgQuoted, msgURL, msgUUID, msgDate, msgIP, msgPath, msgHex, msgID, msgNumber}

const (
	// exceptionMessageInput caps how much of a raw message is read at all.
	exceptionMessageInput = 4096
	// ExceptionMessageMax caps a parameterised message, in bytes.
	ExceptionMessageMax = 256
	// msgIPv6Max is the longest IPv6 text form (an IPv4-mapped address).
	msgIPv6Max = 45
	// msgIPv6Colons separate the eight groups of an address written in
	// full; one less where the last two groups are an IPv4 address.
	msgIPv6Colons = 7
	// msgIPv6WordColons is how many colons make an address of a bare
	// candidate without a digit.
	msgIPv6WordColons = 3
	// msgPlaceholderMax is the longest placeholder; a cut never leaves a
	// shorter, unclosed one behind.
	msgPlaceholderMax = len(msgUUID)
)

var (
	// A quoted value runs to the next quote of its kind, whatever its
	// length; a backslash takes the character after it along (\").
	msgDoubleQuotedRe = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
	// \B on both sides keeps an apostrophe inside a word ("can't") from
	// opening or closing a quoted value.
	msgSingleQuotedRe = regexp.MustCompile(`\B'(?:[^'\\]|\\.)*'\B`)
	msgBackQuotedRe   = regexp.MustCompile("`[^`]*`")
	msgURLRe          = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9+.-]*://[^\s<>"']+`)
	// A UUID, also behind the prefix of an id ("order_<uuid>"), which is
	// kept.
	msgUUIDRe = regexp.MustCompile(
		`(\b|_)[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\b`)
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
	// so the path stops at the first space after its last backslash. A
	// segment has at most msgWinPathWords words, so that the prose after a
	// path does not become part of it wherever a backslash follows later
	// ("… is locked by CORP\alice"). Brackets, commas and semicolons end a
	// path; a word in parentheses ("(x86)") is part of one.
	msgWinPathRe = regexp.MustCompile(
		`\b[A-Za-z]:\\(?:` + msgWinPathWord + `(?: ` + msgWinPathWord + `){0,` + msgWinPathWords + `}\\)*(?:` + msgWinPathWord + `)?`)
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
	//  1. a SQLSTATE, five digits or upper-case letters, and an Oracle-style
	//     code ("ORA-00942"). Kept: they name the failure.
	//  2. a number after the wording of a code: an HTTP status ("HTTP 404",
	//     "HTTP Error 404"), a status, an errno, or "code" behind what says
	//     whose it is (exit, error, response, return, result, SQL). Kept
	//     where it is at most msgCodeDigits digits and nothing else but a
	//     minus sign before them ("error code -32601"), since 404 and 500
	//     are different failures; "code" alone names a value ("promo code
	//     12345"), and "status: 12.5ms" is a duration.
	//
	//     The wording is matched without regard to case, which in a regular
	//     expression also takes "ſ" for s and the kelvin sign for k:
	//     replaceNumbers takes wording beyond ASCII for none.
	//  3. a number (msgNumberText) and, after the start, a space or one of
	//     msgNumberLeads, a minus sign before it ("Index -1").
	msgNumberRe = regexp.MustCompile(
		`(?i:\bSQLSTATE)[ :=#\[(]{1,3}(\d{1,5}|[0-9A-Z]{5})\b` +
			`|\b(?:ORA|PLS|TNS)-\d{5}\b` +
			`|(?i:\b(?:HTTP(?:/\d(?:\.\d)?)?(?: status| error)?|(?:(?:http|exit|response)[ _-]?)?status(?:[ _-]?code)?` +
			`|(?:error|err|exit|response|return|result|sql)[ _-]?code|errno)[ :=#\[(]{1,3})(-?` + msgNumberText + `)\b` +
			`|(?:(?:^|[\s=:(\[,;])-)?\b` + msgNumberText + `\b`)
	// A SQLSTATE in quotes ("SQLSTATE '23505'"), which is the code and no
	// value: the quotes go, so that the rule for quoted values leaves it.
	msgQuotedStateRe = regexp.MustCompile(`((?i:\bSQLSTATE)[ :=#\[(]{1,3})(?:'([0-9A-Z]{5})'|"([0-9A-Z]{5})")`)
	// A CSI sequence (a colour, a cursor move), or any other control
	// character but the white space collapseSpaces handles.
	msgControlRe = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|[\x00-\x08\x0b\x0e-\x1f\x7f]`)
)

const (
	// msgNumberText is a number with an optional unit of up to seven
	// letters ("250ms", "12.5secs", "1.5µs"), or a run of them without a
	// space ("1m0s", "2h45m30.5s").
	msgNumberText = `(?:\d+(?:\.\d+)?[A-Za-zµ]{1,7})*\d+(?:\.\d+)?[A-Za-zµ]{0,7}`
	// msgNumberLeads are the characters a minus sign may follow, as
	// msgNumberRe lists them.
	msgNumberLeads = " \t\n\f\r=:([,;"
	// msgCodeDigits is the longest status or error code that is kept.
	msgCodeDigits = 5
	// msgEscape starts a control sequence.
	msgEscape = "\x1b"
)

// messageRule is one replacement step of the parameteriser. applies is a
// cheap test on the bytes the rule cannot do without: most messages hold
// nothing for most rules, and a pass after the one that replaced everything
// holds nothing for any, so the regular expressions are only run where they
// can match.
type messageRule struct {
	name    string
	applies func(string) bool
	apply   func(string) string
}

// messageRules run in this order, and the order is part of the result: a
// quoted SQLSTATE loses its quotes before quoted values go, which go next
// because they may hold anything, URLs before the
// paths and numbers inside them, UUIDs before the hex runs and numbers they
// are made of, IPs before dates (three groups of an IPv6 address read as a
// clock time) and before numbers, dates before the paths that hold one, and
// paths before the ids in their segments. A digit run is a number however
// long it is, so hex needs a letter. Changing the order changes fingerprints.
var messageRules = []messageRule{
	{"sqlstate", func(s string) bool { return strings.ContainsAny(s, "\"'") && len(s) > msgCodeDigits }, func(s string) string {
		return msgQuotedStateRe.ReplaceAllStringFunc(s, func(m string) string {
			parts := msgQuotedStateRe.FindStringSubmatch(m)
			if !isASCII(parts[1]) {
				return m
			}
			return parts[1] + parts[2] + parts[3]
		})
	}},
	{"quoted", func(s string) bool { return strings.ContainsAny(s, "\"'`") }, func(s string) string {
		s = msgDoubleQuotedRe.ReplaceAllString(s, msgQuoted)
		s = msgSingleQuotedRe.ReplaceAllString(s, msgQuoted)
		return msgBackQuotedRe.ReplaceAllString(s, msgQuoted)
	}},
	{"url", func(s string) bool { return strings.Contains(s, "://") },
		func(s string) string { return msgURLRe.ReplaceAllString(s, msgURL) }},
	{"uuid", func(s string) bool { return strings.Count(s, "-") >= 4 },
		func(s string) string { return msgUUIDRe.ReplaceAllString(s, "${1}"+msgUUID) }},
	// Each expression of the IP rule runs only on text that holds what it
	// needs: most messages with colons or full stops hold no address.
	{"ip", func(s string) bool { return strings.Count(s, ".") >= 3 || strings.Count(s, ":") >= 2 }, func(s string) string {
		colons := strings.Count(s, ":") >= 2
		if colons && strings.Contains(s, "[") {
			s = msgIPv6BracketRe.ReplaceAllStringFunc(s, func(m string) string {
				if isIPv6(msgIPv6BracketRe.FindStringSubmatch(m)[1]) {
					return msgIP
				}
				return m
			})
		}
		// IPv6 before IPv4, so an IPv4-mapped address is taken whole.
		if colons {
			s = replaceBareIPv6(s)
		}
		if hasDigit(s) && strings.Count(s, ".") >= 3 {
			s = msgIPv4Re.ReplaceAllString(s, msgIP)
		}
		return s
	}},
	{"date", func(s string) bool { return hasDigit(s) && strings.ContainsAny(s, "-:") },
		func(s string) string { return msgDateRe.ReplaceAllString(s, msgDate) }},
	{"path", func(s string) bool { return strings.ContainsAny(s, `/\`) }, func(s string) string {
		s = msgUnixPathRe.ReplaceAllStringFunc(s, replaceFilePath)
		return msgWinPathRe.ReplaceAllString(s, msgPath)
	}},
	{"hex", hasDigit, func(s string) string {
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
	{"id", func(s string) bool { return hasDigit(s) && strings.Contains(s, "_") },
		func(s string) string { return msgPrefixedIDRe.ReplaceAllString(s, "${1}"+msgID) }},
	{"number", hasDigit, replaceNumbers},
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

// replaceNumbers decides what each msgNumberRe match becomes: a number is
// replaced, and the character a minus sign followed is kept; a number after
// the wording of a code is replaced behind the wording unless it is a code
// and the wording is ASCII; any other match is a code and stays as it is.
func replaceNumbers(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range msgNumberRe.FindAllStringSubmatchIndex(s, -1) {
		start, end, state, code := m[0], m[1], m[2], m[4]
		keep := start
		switch c := s[start]; {
		case state >= 0:
			if isASCII(s[start:state]) || !hasDigit(s[state:end]) {
				continue
			}
			keep = state
		case code >= 0:
			value := strings.TrimPrefix(s[code:end], "-")
			if isASCII(s[start:code]) && len(value) <= msgCodeDigits && strings.Trim(value, digits) == "" {
				continue
			}
			keep = code
		case c == '-' || strings.IndexByte(digits, c) >= 0:
		case strings.IndexByte(msgNumberLeads, c) >= 0:
			keep = start + 1
		default:
			continue
		}
		b.WriteString(s[last:keep])
		b.WriteString(msgNumber)
		last = end
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// ParameterizeMessage replaces the variable parts of an exception message
// with placeholders (<q>, <url>, <uuid>, <ip>, <date>, <path>, <hex>, <id>,
// <n>), collapses whitespace and bounds the result to ExceptionMessageMax
// bytes. It is idempotent: parameterising its own output changes nothing.
func ParameterizeMessage(msg string) string {
	// The cap is on the bytes that arrived, so nothing past it is looked
	// at; a rune it goes through is invalid and dropped with the rest.
	s := strings.ToValidUTF8(msg[:min(len(msg), exceptionMessageInput)], "")
	if strings.ContainsAny(s, msgControls) {
		s = msgControlRe.ReplaceAllStringFunc(s, func(m string) string {
			if strings.HasPrefix(m, msgEscape) && len(m) > len(msgEscape) {
				return "" // a control sequence is not text
			}
			return " "
		})
	}
	// A replacement or the cut can expose a new match (a quote pair around a
	// placeholder, a number at the new end), so run to a fixed point. Every
	// changing pass removes literal text, which bounds the passes; the cap
	// only guards that argument. The pass that confirms the fixed point
	// finds little left for the rules to apply to.
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
	s = collapseSpaces(s)
	for _, rule := range messageRules {
		if rule.applies(s) {
			s = rule.apply(s)
		}
	}
	return strings.TrimRight(cutMessage(s), " ")
}

// collapseSpaces trims s and turns every run of whitespace into one space.
// Text already in that form, which the second pass always is, is returned as
// it is.
func collapseSpaces(s string) string {
	last := byte(' ')
	for i := 0; i < len(s); i++ {
		c := s[i]
		// Anything that may be whitespace other than a single space inside
		// the text; a byte beyond ASCII may start one (U+00A0).
		if c >= utf8.RuneSelf || c < ' ' || c == ' ' && last == ' ' {
			return strings.Join(strings.Fields(s), " ")
		}
		last = c
	}
	return strings.TrimRight(s, " ")
}

// cutMessage bounds s to ExceptionMessageMax bytes without leaving half a
// placeholder at the end: a "<" near the cut goes with what follows it
// where a placeholder starts that way, and is text otherwise ("x < y").
func cutMessage(s string) string {
	if len(s) <= ExceptionMessageMax {
		return s
	}
	s = cutBytes(s, ExceptionMessageMax)
	tail := s[max(0, len(s)-msgPlaceholderMax+1):]
	if open := strings.LastIndexByte(tail, '<'); open >= 0 && hasPrefixIn(msgPlaceholders, tail[open:]) {
		s = s[:len(s)-len(tail)+open]
	}
	return s
}

// hasPrefixIn reports whether prefix starts one of the words without being
// the whole of it.
func hasPrefixIn(words []string, prefix string) bool {
	for _, word := range words {
		if len(prefix) < len(word) && strings.HasPrefix(word, prefix) {
			return true
		}
	}
	return false
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
		// Hex letters around a scope operator are a name ("db::add").
		if cand := s[start:end]; !hasDigit(cand) && strings.Count(cand, ":") < msgIPv6WordColons {
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
	if len(s) > msgIPv6Max || strings.Trim(s, ":.") == "" {
		return false
	}
	// An address has a "::" or all of its groups; the test spares parsing
	// the many candidates that are neither ("10:11:12").
	if colons := strings.Count(s, ":"); !strings.Contains(s, "::") &&
		colons != msgIPv6Colons && (colons != msgIPv6Colons-1 || !strings.Contains(s, ".")) {
		return false
	}
	addr, err := netip.ParseAddr(s)
	return err == nil && addr.Is6()
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func hasDigit(s string) bool {
	return strings.ContainsAny(s, digits)
}

const (
	hexLetters = "abcdefABCDEF"
	digits     = "0123456789"
	// msgControls are the control characters msgControlRe matches.
	msgControls = "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x0b\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f\x7f"
)

// wordBytes are the bytes regexp's \w matches.
const wordBytes = "_0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func isWordByte(c byte) bool {
	return strings.IndexByte(wordBytes, c) >= 0
}
