package catalog

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParameterizeMessageReplacesEachKind(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"plain text is kept", "Cart is locked", "Cart is locked"},
		{"whitespace collapses", "  Cart \t is\n locked  ", "Cart is locked"},

		{"double quoted", `No product named "blue kettle" found`, "No product named <q> found"},
		{"single quoted", "column 'sku' is unknown", "column <q> is unknown"},
		{"back quoted", "table `orders` is missing", "table <q> is missing"},
		{"empty quotes", `value "" rejected`, "value <q> rejected"},
		{"apostrophe is not a quote", "can't reach the user's cart", "can't reach the user's cart"},
		{"two single quoted values", "'a' and 'b'", "<q> and <q>"},
		{"unbalanced quote stays", `expected " here`, `expected " here`},

		{"url", "GET https://shop.example.invalid/cart/42?x=1 failed", "GET <url> failed"},
		{"url with another scheme", "dial jdbc:postgresql://db.example.invalid:5432/shop", "dial jdbc:<url>"},

		{"uuid", "order 3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f missing", "order <uuid> missing"},
		{"uuid upper case", "order 3F2B8C1E-9D4A-4B7C-8E21-0A1B2C3D4E5F missing", "order <uuid> missing"},

		{"date", "expired on 2026-03-01", "expired on <date>"},
		{"timestamp utc", "at 2026-03-01T10:11:12Z", "at <date>"},
		{"timestamp fraction and offset", "at 2026-03-01T10:11:12.345+02:00 it broke", "at <date> it broke"},
		{"timestamp with a space", "at 2026-03-01 10:11:12,345 it broke", "at <date> it broke"},
		{"clock time", "started 10:11:12.345", "started <date>"},

		{"ipv4", "connect to 10.0.0.7 refused", "connect to <ip> refused"},
		{"ipv4 with port", "connect to 10.0.0.7:8080 refused", "connect to <ip> refused"},
		{"ipv6", "connect to fd00::1f refused", "connect to <ip> refused"},
		{"ipv6 loopback ends a sentence", "connect to ::1.", "connect to <ip>."},
		{"ipv6 with port", "connect to [fd00::1f]:8443 refused", "connect to <ip> refused"},
		{"ipv4-mapped ipv6", "peer ::ffff:10.0.0.7 gone", "peer <ip> gone"},
		{"longest ipv6 form", "peer 0000:0000:0000:0000:0000:ffff:192.168.100.200 gone", "peer <ip> gone"},
		{"two colons are not enough", "a::g", "a::g"},
		{"one colon is not an address", "f:1", "f:<n>"},
		{"ipv6 ends the message", "peer fd00::1f", "peer <ip>"},
		{"ipv6 starts the message", "::1 refused", "<ip> refused"},
		{"ipv6 before a colon", "peer fd00::1f: gone", "peer <ip>: gone"},
		{"a bare scope operator stays", "a :: b", "a :: b"},
		{"a leading dot stays", ". then", ". then"},
		{"underscore before an address", "_fd00::1f", "_fd00::<n>"},
		{"underscore after an address", "fd00::1f_x", "fd00::1f_x"},
		{"brackets without an address", "index [12] out of range", "index [<n>] out of range"},
		{"scope operator is not an address", "Cart::add failed", "Cart::add failed"},
		{"address glued to a word is not one", "xfd00::1f", "xfd00::<n>"},

		{"unix path", "open /var/data/cart/7.json: no such file", "open <path>: no such file"},
		{"home path", "open ~/data/cart.json", "open <path>"},
		{"relative path", "open ./data/cart.json", "open <path>"},
		{"path after an equals sign", "file=/var/data/cart.json", "file=<path>"},
		{"one segment is not a path", "POST /cart failed", "POST /cart failed"},
		{"a slash between words is not a path", "read/write error", "read/write error"},
		{"windows path", `open C:\data\cart\7.json failed`, "open <path> failed"},

		{"hex literal", "bad pointer 0x1F", "bad pointer <hex>"},
		{"object identity", "Cart@1b2c3d is closed", "Cart@<hex> is closed"},
		{"object identity of digits", "Cart@123456 is closed", "Cart@<hex> is closed"},
		{"mail address is not an identity", "bob@added.example.invalid", "bob@added.example.invalid"},
		{"hex run", "trace 4bf92f3577b34da6a3ce929d0e0e4736 lost", "trace <hex> lost"},
		{"long digit run", "id 1234567890 lost", "id <hex> lost"},
		{"hex word without a digit stays", "deadbeef", "deadbeef"},
		{"short hex run is a number with a unit", "id 12ab lost", "id <n> lost"},

		{"number", "retry 3 of 5", "retry <n> of <n>"},
		{"decimal", "took 1.5 seconds", "took <n> seconds"},
		{"number with unit", "timeout after 250ms", "timeout after <n>"},
		{"digits inside a word stay", "utf8 and http2 and x86_64", "utf8 and http2 and x86_64"},
		{"version", "v 1.2.3", "v <n>.<n>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParameterizeMessage(tc.in); got != tc.want {
				t.Errorf("ParameterizeMessage(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The rule order is part of the fingerprint: reordering changes keys.
func TestParameterizeMessageRuleOrder(t *testing.T) {
	want := []string{"quoted", "url", "uuid", "date", "ip", "path", "hex", "number"}
	if len(messageRules) != len(want) {
		t.Fatalf("rules = %d, want %d", len(messageRules), len(want))
	}
	for i, rule := range messageRules {
		if rule.name != want[i] {
			t.Errorf("rule %d = %s, want %s", i, rule.name, want[i])
		}
	}
}

// Each case is text two rules could claim; the earlier rule must win.
func TestParameterizeMessageEarlierRuleWins(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"quoted over url", `fetch "https://shop.example.invalid/a/b" failed`, "fetch <q> failed"},
		{"quoted over path", "open '/var/data/cart.json'", "open <q>"},
		{"quoted over number", `got "42"`, "got <q>"},
		{"url over path", "GET http://shop.example.invalid/var/data", "GET <url>"},
		{"url over ip", "GET http://10.0.0.7:8080/cart", "GET <url>"},
		{"uuid over hex and number", "3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f", "<uuid>"},
		{"date over number", "2026-03-01", "<date>"},
		{"date over ipv6", "at 10:11:12", "at <date>"},
		{"ip over number", "10.0.0.7", "<ip>"},
		{"path over hex", "/orders/4bf92f3577b34da6", "<path>"},
		{"path over number", "/orders/42", "<path>"},
		{"hex over number", "12345678", "<hex>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParameterizeMessage(tc.in); got != tc.want {
				t.Errorf("ParameterizeMessage(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParameterizeMessageMergesVariants(t *testing.T) {
	pairs := [][2]string{
		{"order 3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f not found for user 17",
			"order 0a1b2c3d-4e5f-4b7c-8e21-3f2b8c1e9d4a not found for user 2301"},
		{"timeout after 250ms calling 10.0.0.7:8080", "timeout after 3s calling 10.0.0.19:9090"},
		{`unknown sku "kettle-1" in /var/data/cart/7.json`, `unknown sku "pan-22" in /var/data/cart/1190.json`},
		{"Cart@1b2c3d closed at 2026-03-01T10:11:12Z", "Cart@99887766 closed at 2026-04-02T01:02:03.5Z"},
	}
	for _, p := range pairs {
		if a, b := ParameterizeMessage(p[0]), ParameterizeMessage(p[1]); a != b {
			t.Errorf("variants differ:\n  %q\n  %q", a, b)
		}
	}
}

func TestParameterizeMessageIsIdempotent(t *testing.T) {
	inputs := []string{
		`'<q>'`,
		`''a''`,
		`"a"b"c"`,
		"<n> <n>",
		"it's '5' o'clock",
		"/a/5 '/b/c' 0x10 10:11:12 ::1 2026-03-01",
		strings.Repeat("7 ", 400),
		strings.Repeat(`"x" `, 400),
	}
	for _, in := range inputs {
		once := ParameterizeMessage(in)
		if twice := ParameterizeMessage(once); twice != once {
			t.Errorf("not idempotent for %q:\n  once  %q\n  twice %q", in, once, twice)
		}
	}
	// A quote pair that only forms once the inner pair is replaced.
	if got := ParameterizeMessage(`''a''`); got != msgQuoted {
		t.Errorf("nested quotes = %q, want %q", got, msgQuoted)
	}
}

func TestParameterizeMessageIsBounded(t *testing.T) {
	long := strings.Repeat("cart ", 200)
	got := ParameterizeMessage(long)
	if len(got) > ExceptionMessageMax {
		t.Errorf("len = %d, want <= %d", len(got), ExceptionMessageMax)
	}
	if want := strings.TrimRight(long[:ExceptionMessageMax], " "); got != want {
		t.Errorf("a long message must keep its head: got %q", got)
	}

	// Exactly at the bound nothing is cut.
	exact := strings.Repeat("a", ExceptionMessageMax)
	if got := ParameterizeMessage(exact); got != exact {
		t.Errorf("a message of exactly %d bytes was changed (len %d)", ExceptionMessageMax, len(got))
	}
	if got := ParameterizeMessage(exact + "a"); got != exact {
		t.Errorf("one byte over the bound must be cut to it (len %d)", len(got))
	}

	// At the bound the tail is left alone, even one that looks half cut.
	half := strings.Repeat("a", ExceptionMessageMax-4) + "<uui"
	if got := ParameterizeMessage(half); got != half {
		t.Errorf("a message of exactly %d bytes lost its tail: %q", ExceptionMessageMax, got[len(got)-8:])
	}

	// The cut never leaves half a placeholder, whether one byte of it is
	// missing or all but one.
	head := strings.Repeat("a", ExceptionMessageMax-6) + " "
	got = ParameterizeMessage(head + "3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f")
	if want := strings.TrimRight(head, " "); got != want {
		t.Errorf("half a placeholder survived the cut: %q", got[len(got)-12:])
	}
	head = strings.Repeat("a", ExceptionMessageMax-2) + " "
	got = ParameterizeMessage(head + "3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f")
	if want := strings.TrimRight(head, " "); got != want {
		t.Errorf("half a placeholder survived the cut: %q", got[len(got)-12:])
	}
	// A placeholder that ends exactly at the bound is kept whole.
	head = strings.Repeat("a", ExceptionMessageMax-len(msgUUID)-1) + " "
	got = ParameterizeMessage(head + "3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f tail")
	if want := head + msgUUID; got != want {
		t.Errorf("a whole placeholder at the bound was cut: %q", got[len(got)-12:])
	}
	// A literal "<" far from the end is not mistaken for one.
	head = "a < b " + strings.Repeat("a", ExceptionMessageMax)
	if got := ParameterizeMessage(head); got != head[:ExceptionMessageMax] {
		t.Errorf("a literal < changed the cut: len %d", len(got))
	}

	// Nor is one a whole placeholder's length before the cut.
	head = strings.Repeat("a", ExceptionMessageMax-len(msgUUID)) + "<aaaaa"
	if got := ParameterizeMessage(head + "aaaa"); got != head {
		t.Errorf("a literal < was cut as a placeholder: %q", got[len(got)-8:])
	}

	// A multi-byte rune on the bound is dropped whole, not split.
	runes := strings.Repeat("a", ExceptionMessageMax-1) + "é"
	got = ParameterizeMessage(runes)
	if !utf8.ValidString(got) || len(got) != ExceptionMessageMax-1 {
		t.Errorf("rune cut: valid=%v len=%d", utf8.ValidString(got), len(got))
	}

	// Input of exactly the read cap is read whole.
	if got := ParameterizeMessage(strings.Repeat(" ", exceptionMessageInput-1) + "x"); got != "x" {
		t.Errorf("the last byte inside the input cap was lost: %q", got)
	}

	// Input beyond the read cap is never looked at.
	if got := ParameterizeMessage(strings.Repeat(" ", exceptionMessageInput) + "late"); got != "" {
		t.Errorf("text past the input cap leaked: %q", got)
	}
	if got := ParameterizeMessage(strings.Repeat(" ", exceptionMessageInput-4) + "late"); got != "late" {
		t.Errorf("text inside the input cap was lost: %q", got)
	}
}

func TestParameterizeMessageDropsInvalidUTF8(t *testing.T) {
	if got := ParameterizeMessage("cart \xff\xfe locked"); got != "cart locked" {
		t.Errorf("got %q", got)
	}
}

// FuzzParameterizeMessage checks the properties that must hold for any
// input: no panic, a bounded valid result, and idempotence.
func FuzzParameterizeMessage(f *testing.F) {
	for _, seed := range []string{
		"", "'<q>'", `''a''`, "order 3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f", "::1.", "[::1]:80",
		"/a/b '/c/d' 0x1F 10.0.0.7:80 2026-03-01T10:11:12Z 250ms", "<", "<uui", "a@1b", `C:\a\b`,
		strings.Repeat("9 ", 300), strings.Repeat("<", 300), "\xff'\xfe'",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := ParameterizeMessage(in)
		if len(got) > ExceptionMessageMax {
			t.Fatalf("len = %d for %q", len(got), in)
		}
		if !utf8.ValidString(got) {
			t.Fatalf("invalid UTF-8 for %q: %q", in, got)
		}
		if again := ParameterizeMessage(got); again != got {
			t.Fatalf("not idempotent for %q:\n  once  %q\n  twice %q", in, got, again)
		}
	})
}
