package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dtui/internal/source"
)

// sourceApp is a flame graph over stacks whose frames have, or have not, a
// file in a directory made for the test, with the opener replaced: opened
// collects the links handed to the system.
func sourceApp(t *testing.T, editor string) (a *app, fv *flameView, root string, opened *[]string) {
	t.Helper()
	root = t.TempDir()
	for rel, content := range map[string]string{
		"src/main/java/com/example/shop/CartService.java": "package com.example.shop;\n\nclass CartService {\n    void addItem() {}\n    void addItem(int n) {}\n    int total() { return 0; }\n}\n",
		"src/main/java/com/example/shop/Unnamed.java":     "package com.example.shop;\n\nclass Unnamed {}\n",
		"a/handler.ts": "export function resolve() {}\n",
		"b/handler.ts": "export function resolve() {}\n",
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, _ = filepath.EvalSymlinks(root)
	a, _, fv, _ = openFlame(t)
	a.ds.source = newSourceLookup(root, editor)
	opened = new([]string)
	a.opts.OpenURL = func(link string) error {
		*opened = append(*opened, link)
		return nil
	}
	answerFlame(a, fv, stackRecords("2026-07-15T12:00:00Z",
		"com.example.shop.CartService.total 9",
		"com.example.shop.CartService.addItem 8",
		"com.example.shop.Unnamed.generated 7",
		"handler.ts:resolve 6",
		"java.lang.Thread.run 5",
		"org.library.Client.call 4",
		"$(touch${IFS}"+filepath.Join(root, "pwned")+").x/../../../etc/passwd.py:root 3",
	), nil)
	fv.graph.ClearSearch()
	a.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	return a, fv, root, opened
}

// onFrame moves the cursor to the frame under the root with this label.
func onFrame(t *testing.T, a *app, fv *flameView, label string) {
	t.Helper()
	press(a, key("g"))
	for fv.graph.Selected() != 0 {
		press(a, tea.KeyMsg{Type: tea.KeyUp})
	}
	press(a, tea.KeyMsg{Type: tea.KeyDown})
	for range 20 {
		if got, _ := fv.selectedFunction(); got == label {
			return
		}
		press(a, tea.KeyMsg{Type: tea.KeyRight})
	}
	for range 20 {
		if got, _ := fv.selectedFunction(); got == label {
			return
		}
		press(a, tea.KeyMsg{Type: tea.KeyLeft})
	}
	t.Fatalf("no frame %q to move to", label)
}

func TestOpenSourceFromAFrame(t *testing.T) {
	a, fv, root, opened := sourceApp(t, "vscode")
	java := root + "/src/main/java/com/example/shop/CartService.java"
	for _, tc := range []struct {
		frame  string
		link   string
		status string
		isErr  bool
	}{
		{"com.example.shop.CartService.total", "vscode://file" + java + ":6",
			"opened src/main/java/com/example/shop/CartService.java:6 in VS Code (the declaration of total)", false},
		{"com.example.shop.CartService.addItem", "vscode://file" + java + ":4",
			"opened src/main/java/com/example/shop/CartService.java:4 in VS Code (approximate: the first of several declarations of addItem)", false},
		{"com.example.shop.Unnamed.generated", "vscode://file" + root + "/src/main/java/com/example/shop/Unnamed.java:3",
			"opened src/main/java/com/example/shop/Unnamed.java:3 in VS Code (approximate: the type's declaration, the function was not found by name)", false},
		{"java.lang.Thread.run", "", "java.lang.Thread.run is part of the JDK: its source is not in this directory", false},
		{"org.library.Client.call", "", "no source file for org.library.Client.call under " + filepath.Base(root) +
			": a library, or another repository (y copies the name)", true},
	} {
		*opened = nil
		onFrame(t, a, fv, tc.frame)
		hinted := strings.Contains(screen(a), "O open source")
		press(a, key("O"))
		if got := strings.Join(*opened, " "); got != tc.link {
			t.Errorf("%s: opened %q, want %q", tc.frame, got, tc.link)
		}
		if a.status != tc.status || a.statusErr != tc.isErr {
			t.Errorf("%s: status %q (error %v)\n want %q", tc.frame, a.status, a.statusErr, tc.status)
		}
		// The footer offers the key where a file of the right name exists,
		// which is known once the directory has been read.
		if want := tc.link != ""; hinted != want && tc.frame != "com.example.shop.CartService.total" {
			t.Errorf("%s: the footer offers O: %v, want %v", tc.frame, hinted, want)
		}
		if a.overlay != nil {
			t.Fatalf("%s: left %T open", tc.frame, a.overlay)
		}
	}

	// The root is no frame.
	press(a, tea.KeyMsg{Type: tea.KeyUp})
	*opened = nil
	if press(a, key("O")); len(*opened) != 0 || !strings.HasPrefix(a.status, "the root is every stack") {
		t.Errorf("O on the root: opened %v, status %q", *opened, a.status)
	}

	// A frame whose name is a command and a way out of the directory is
	// text: nothing is run, nothing outside is opened.
	onFrame(t, a, fv, "$(touch${IFS}"+filepath.Join(root, "pwned")+").x/../../../etc/passwd.py:root")
	press(a, key("O"))
	if _, err := os.Stat(filepath.Join(root, "pwned")); err == nil || len(*opened) != 0 || !a.statusErr {
		t.Errorf("a hostile name: ran %v, opened %v, status %q", err == nil, *opened, a.status)
	}
}

// Several files: a short list to pick from, by enter, by digit, or to copy.
func TestOpenSourcePicksAmongSeveral(t *testing.T) {
	a, fv, root, opened := sourceApp(t, "idea")
	onFrame(t, a, fv, "handler.ts:resolve")
	press(a, key("O"))
	p := overlayAs[*sourcePicker](t, a)
	if out := screen(a); !strings.Contains(out, "1 · a/handler.ts:1") || !strings.Contains(out, "2 · b/handler.ts:1") ||
		!strings.Contains(out, "the declaration of resolve") {
		t.Errorf("the picker:\n%s", out)
	}
	press(a, key("2"))
	if got := strings.Join(*opened, " "); got != "idea://open?file="+strings.ReplaceAll(root, "/", "%2F")+"%2Fb%2Fhandler.ts&line=1" || a.overlay != nil {
		t.Errorf("2 opened %q", got)
	}
	*opened = nil
	press(a, key("O"))
	press(a, key("enter"))
	if len(*opened) != 1 || !strings.Contains((*opened)[0], "%2Fa%2Fhandler.ts") {
		t.Errorf("enter opened %v", *opened)
	}
	*opened = nil
	press(a, key("O"))
	press(a, key("esc"))
	if a.overlay != nil || len(*opened) != 0 {
		t.Errorf("esc: overlay %T, opened %v", a.overlay, *opened)
	}
	press(a, key("O"))
	press(a, key("j"))
	press(a, key("y"))
	if a.overlay != nil || len(*opened) != 0 || a.status != "copied b/handler.ts:1" {
		t.Errorf("y: overlay %T, opened %v, status %q", a.overlay, *opened, a.status)
	}
	_ = p
}

// A lookup that answers after the reader has left the graph opens nothing
// and offers nothing: the editor does not come up over another view, and no
// list of files covers it.
func TestOpenSourceIsDroppedOnceTheGraphIsLeft(t *testing.T) {
	for _, frame := range []string{"com.example.shop.CartService.total", "handler.ts:resolve"} {
		a, fv, _, opened := sourceApp(t, "vscode")
		onFrame(t, a, fv, frame)
		_, ask := a.Update(key("O"))
		_, lookup := a.Update(ask())
		press(a, key("tab")) // back on the functions before the lookup answers
		a.status = ""
		deliver(a, lookup)
		if len(*opened) != 0 || a.overlay != nil || strings.Contains(a.status, "opened") {
			t.Errorf("%s: after leaving, opened %v, overlay %T, status %q", frame, *opened, a.overlay, a.status)
		}
		// Back on the graph the key works as before.
		press(a, key("tab"))
		press(a, key("O"))
		if len(*opened) == 0 && a.overlay == nil {
			t.Errorf("%s: O on the graph again did nothing (status %q)", frame, a.status)
		}
	}
}

// Where more files could hold a frame than are read for one, a single match
// is the first found and not known to be the only one: the status line says
// so, and a miss says it too.
func TestOpenSourceSaysWhatWasNotRead(t *testing.T) {
	a, fv, _, opened := sourceApp(t, "vscode")
	onFrame(t, a, fv, "com.example.shop.CartService.total")
	ix, err := a.ds.source.get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	one := []source.Match{{Path: "src/main/java/com/example/shop/CartService.java", Line: 6, How: source.Declaration}}
	a.Update(sourceFoundMsg{view: fv, label: "com.example.shop.CartService.total", index: ix, matches: one, unread: 1250})
	want := "opened src/main/java/com/example/shop/CartService.java:6 in VS Code (the declaration of total; the first found, 1,250 files that could hold it were not read)"
	if len(*opened) != 1 {
		t.Errorf("opened %v", *opened)
	}
	_, cmd := a.Update(sourceFoundMsg{view: fv, label: "com.example.shop.CartService.total", index: ix, matches: one, unread: 1250})
	deliver(a, cmd)
	if a.status != want {
		t.Errorf("status %q\n want  %q", a.status, want)
	}
	_, cmd = a.Update(sourceFoundMsg{view: fv, label: "main.main", index: ix, unread: 50})
	deliver(a, cmd)
	if !a.statusErr || !strings.HasSuffix(a.status, "· 50 files that could hold it were not read") {
		t.Errorf("a miss: status %q", a.status)
	}
	// The time ran out before the files did.
	_, cmd = a.Update(sourceFoundMsg{view: fv, label: "main.main", index: ix, unread: 50, late: true})
	deliver(a, cmd)
	if !a.statusErr || !strings.HasSuffix(a.status, "· the search was cut after 2 seconds: 50 files that could hold it were not read") {
		t.Errorf("a miss out of time: status %q", a.status)
	}
	_, cmd = a.Update(sourceFoundMsg{view: fv, label: "com.example.shop.CartService.total", index: ix, matches: one, unread: 7, late: true})
	deliver(a, cmd)
	if !strings.HasSuffix(a.status, "(the declaration of total; the first found, the search was cut after 2 seconds: 7 files that could hold it were not read)") {
		t.Errorf("one match out of time: status %q", a.status)
	}
}

// Without an editor the place is copied and said; without a directory to
// look in the key says so and the footer does not offer it; an opener that
// fails is reported.
func TestOpenSourceWithoutAnEditorOrADirectory(t *testing.T) {
	a, fv, _, opened := sourceApp(t, "")
	onFrame(t, a, fv, "com.example.shop.CartService.total")
	press(a, key("O"))
	if len(*opened) != 0 || a.statusErr ||
		!strings.HasPrefix(a.status, "copied src/main/java/com/example/shop/CartService.java:6 (the declaration of total) · no editor is known: DTUI_EDITOR names one (vscode, ") {
		t.Errorf("without an editor: opened %v, status %q", *opened, a.status)
	}

	a.ds.source = nil
	if out := screen(a); strings.Contains(out, "open source") {
		t.Errorf("the footer offers O with no directory to look in:\n%s", out)
	}
	press(a, key("O"))
	if len(*opened) != 0 || !a.statusErr || a.status != "O looks for a frame's file in the repository dtui was started in: start it inside the application's checkout" {
		t.Errorf("without a directory: opened %v, status %q", *opened, a.status)
	}

	a, fv, _, _ = sourceApp(t, "zed")
	a.opts.OpenURL = func(string) error { return errors.New("no handler for zed://\x1b[31m") }
	onFrame(t, a, fv, "com.example.shop.CartService.total")
	press(a, key("O"))
	if !a.statusErr || a.status != "Zed: no handler for zed://␛[31m" {
		t.Errorf("a failing opener: status %q (error %v)", a.status, a.statusErr)
	}
}

// The directory is read once for the session, and a limit that stops the
// walk early is said with the miss it may explain.
func TestSourceLookupIsKeptAndSaysWhenItIsPartial(t *testing.T) {
	a, fv, root, _ := sourceApp(t, "vscode")
	a.ds.source.limits = source.Limits{Entries: 2, Files: 100, Time: source.DefaultLimits.Time}
	onFrame(t, a, fv, "com.example.shop.CartService.total")
	press(a, key("O"))
	if !a.statusErr || !strings.Contains(a.status, "the directory was not read to its end: more than 2 files and directories") {
		t.Errorf("a partial walk: status %q", a.status)
	}
	first := a.ds.source.ready()
	if err := os.WriteFile(filepath.Join(root, "Late.java"), []byte("class Late {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	press(a, key("O"))
	if a.ds.source.ready() != first || first == nil {
		t.Error("the directory was read again")
	}
}
