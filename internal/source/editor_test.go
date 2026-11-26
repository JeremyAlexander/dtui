package source

import (
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// placeOf reads a link that carries a line back: the path it names,
// slash-separated, and the line. A link is tested by what an editor reads
// out of it, which is the same on every platform, where the path it is made
// from is not.
func placeOf(t *testing.T, e Editor, link string) (slashed string, line int) {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil || u.Scheme != e.scheme {
		t.Fatalf("%s: %q is not a link of the scheme %q (%v)", e.ID, link, e.scheme, err)
	}
	number := u.Query().Get("line")
	switch e.form {
	case formOpen:
		slashed = u.Query().Get("file")
	case formURL:
		file, err := url.Parse(u.Query().Get("url"))
		if err != nil || file.Scheme != "file" {
			t.Fatalf("%s: %q carries no file link (%v)", e.ID, link, err)
		}
		slashed = file.Path
	default:
		slashed = u.Path
		if i := strings.LastIndexByte(slashed, ':'); i >= 0 {
			slashed, number = slashed[:i], slashed[i+1:]
		}
	}
	if number != "" {
		if line, err = strconv.Atoi(number); err != nil {
			t.Fatalf("%s: the line of %q is %q", e.ID, link, number)
		}
	}
	return slashed, line
}

// How a link is worded is the same on every platform: it is made from a
// path that is already slash-separated.
func TestEditorLink(t *testing.T) {
	const p = "/home/dev/shop/src/Cart Service#1?.java"
	for id, want := range map[string]string{
		"vscode":          "vscode://file/home/dev/shop/src/Cart%20Service%231%3F.java:42",
		"vscode-insiders": "vscode-insiders://file/home/dev/shop/src/Cart%20Service%231%3F.java:42",
		"vscodium":        "vscodium://file/home/dev/shop/src/Cart%20Service%231%3F.java:42",
		"cursor":          "cursor://file/home/dev/shop/src/Cart%20Service%231%3F.java:42",
		"windsurf":        "windsurf://file/home/dev/shop/src/Cart%20Service%231%3F.java:42",
		"zed":             "zed://file/home/dev/shop/src/Cart%20Service%231%3F.java:42",
		"idea":            "idea://open?file=%2Fhome%2Fdev%2Fshop%2Fsrc%2FCart+Service%231%3F.java&line=42",
		"goland":          "goland://open?file=%2Fhome%2Fdev%2Fshop%2Fsrc%2FCart+Service%231%3F.java&line=42",
		"sublime":         "subl://open?url=file%3A%2F%2F%2Fhome%2Fdev%2Fshop%2Fsrc%2FCart%2520Service%25231%253F.java&line=42",
		"textmate":        "txmt://open?url=file%3A%2F%2F%2Fhome%2Fdev%2Fshop%2Fsrc%2FCart%2520Service%25231%253F.java&line=42",
	} {
		e, ok := EditorByID(id)
		if !ok {
			t.Fatalf("no editor %q", id)
		}
		if got := e.link(p, 42); got != want {
			t.Errorf("%s: %q\n want %q", id, got, want)
		}
	}
	// Every editor has a link, with and without a line, that parses as a
	// URL of its own scheme whatever the path holds, and that gives the path
	// back whole.
	hostile := "/tmp/a:1:2/b&line=9/c d/%2e%2e/é\n\x1b[0m/'\"`$(x);|<>\\{}.java"
	for _, id := range EditorIDs() {
		e, _ := EditorByID(id)
		for _, line := range []int{0, -3, 7} {
			link := e.link(hostile, line)
			u, err := url.Parse(link)
			if err != nil || u.Scheme != e.scheme || strings.ContainsAny(link, " \n\x1b'\"`$();|<>\\{}") {
				t.Errorf("%s: %q does not survive as one link (%v)", id, link, err)
			}
			if (line > 0) != (strings.HasSuffix(link, ":7") || strings.HasSuffix(link, "&line=7")) {
				t.Errorf("%s with line %d: %q", id, line, link)
			}
			if e.form == formFile {
				// The one colon left in the link's path is the line's.
				if rest := strings.TrimPrefix(link, e.scheme+"://file"); strings.Count(rest, ":") != min(max(line, 0), 1) {
					t.Errorf("%s: a colon of the path is left in %q", id, link)
				}
			} else if got := u.Query().Get("line"); (got == "7") != (line > 0) || len(u.Query()["line"]) > 1 {
				t.Errorf("%s: the line of %q is %q", id, link, got)
			}
			if line == 7 {
				if got, at := placeOf(t, e, link); got != hostile || at != 7 {
					t.Errorf("%s: %q names %q at line %d", id, link, got, at)
				}
			}
		}
	}
}

// URL takes the platform's own absolute path, and nothing else. The path is
// made here from a directory of the platform, so the test reads the same
// where a path starts with a volume.
func TestEditorURL(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "src", "Cart Service#1?.java")
	want := filepath.ToSlash(abs)
	if !strings.HasPrefix(want, "/") {
		want = "/" + want
	}
	for _, id := range EditorIDs() {
		e, _ := EditorByID(id)
		link, err := e.URL(abs, 42)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if got, line := placeOf(t, e, link); got != want || line != 42 {
			t.Errorf("%s: %q names %q at line %d, want %q at 42", id, link, got, line, want)
		}
		// The path is cleaned before it is worded.
		sep := string(filepath.Separator)
		if dirty, err := e.URL(filepath.Dir(abs)+sep+"x"+sep+".."+sep+filepath.Base(abs), 42); err != nil || dirty != link {
			t.Errorf("%s: a path with a way round is worded %q (%v), want %q", id, dirty, err, link)
		}
	}
	e, _ := EditorByID("vscode")
	for _, bad := range []string{"", "relative/path.go", "../x.go", filepath.Base(abs), abs + "\x00"} {
		if link, err := e.URL(bad, 1); err == nil {
			t.Errorf("URL(%q) = %q", bad, link)
		}
	}
	if link, err := (Editor{}).URL(abs, 1); err == nil {
		t.Errorf("no editor has the link %q", link)
	}
}

func TestDetectEditor(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"nothing", nil, ""},
		{"the setting", map[string]string{"DTUI_EDITOR": "goland", "TERM_PROGRAM": "vscode"}, "goland"},
		{"the setting, loosely spelled", map[string]string{"DTUI_EDITOR": " Cursor "}, "cursor"},
		{"a setting that names no editor is not guessed around", map[string]string{"DTUI_EDITOR": "vim", "TERM_PROGRAM": "vscode"}, ""},
		{"a setting is a name, not a command", map[string]string{"DTUI_EDITOR": "code; rm -rf ~"}, ""},
		{"the terminal of VS Code", map[string]string{"TERM_PROGRAM": "vscode", "EDITOR": "vim"}, "vscode"},
		{"the terminal of VS Code Insiders", map[string]string{"TERM_PROGRAM": "vscode", "TERM_PROGRAM_VERSION": "1.99.0-insider"}, "vscode-insiders"},
		{"the terminal of Cursor, which says it is VS Code", map[string]string{"TERM_PROGRAM": "vscode", "CURSOR_TRACE_ID": "x"}, "cursor"},
		{"the terminal of Zed", map[string]string{"ZED_TERM": "true"}, "zed"},
		{"the terminal of a JetBrains IDE", map[string]string{"TERMINAL_EMULATOR": "JetBrains-JediTerm"}, "idea"},
		{"VISUAL before EDITOR", map[string]string{"VISUAL": "/usr/local/bin/code --wait", "EDITOR": "subl"}, "vscode"},
		{"EDITOR", map[string]string{"EDITOR": "subl -w"}, "sublime"},
		{"a terminal editor has no link", map[string]string{"EDITOR": "nvim", "VISUAL": "vim"}, ""},
		{"a program on Windows", map[string]string{"EDITOR": "Code.exe"}, "vscode"},
	} {
		e, ok := DetectEditor(func(key string) string { return tc.env[key] })
		if e.ID != tc.want || ok != (tc.want != "") {
			t.Errorf("%s: %q (%v), want %q", tc.name, e.ID, ok, tc.want)
		}
	}
}
