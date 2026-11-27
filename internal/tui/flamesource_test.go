package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dtui/internal/source"
	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
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
	a.Update(tea.WindowSizeMsg{Width: 400, Height: 40})
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

// editorLink is the link the editor is handed for a file of the test's
// directory, and the file's path as the platform spells it. How a link is
// worded is internal/source's to test: here it is which file and which line.
func editorLink(t *testing.T, editor, root, rel string, line int) (link, abs string) {
	t.Helper()
	abs = filepath.Join(root, filepath.FromSlash(rel))
	e, ok := source.EditorByID(editor)
	if !ok {
		t.Fatalf("no editor %q", editor)
	}
	link, err := e.URL(abs, line)
	if err != nil {
		t.Fatal(err)
	}
	return link, abs
}

func TestOpenSourceFromAFrame(t *testing.T) {
	a, fv, root, opened := sourceApp(t, "vscode")
	total, java := editorLink(t, "vscode", root, "src/main/java/com/example/shop/CartService.java", 6)
	addItem, _ := editorLink(t, "vscode", root, "src/main/java/com/example/shop/CartService.java", 4)
	generated, unnamed := editorLink(t, "vscode", root, "src/main/java/com/example/shop/Unnamed.java", 3)
	if !strings.HasPrefix(total, "vscode://file/") || !strings.HasSuffix(total, "/src/main/java/com/example/shop/CartService.java:6") {
		t.Fatalf("the link of a file: %q", total)
	}
	for _, tc := range []struct {
		frame  string
		link   string
		status string
		isErr  bool
	}{
		{"com.example.shop.CartService.total", total,
			"asked VS Code to open the declaration of total: " + java + ":6", false},
		{"com.example.shop.CartService.addItem", addItem,
			"approximate: the first of several declarations of addItem · asked VS Code to open it: " + java + ":4", false},
		{"com.example.shop.Unnamed.generated", generated,
			"approximate: the type's declaration, the function was not found by name · asked VS Code to open it: " + unnamed + ":3", false},
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
	second, _ := editorLink(t, "idea", root, "b/handler.ts", 1)
	if got := strings.Join(*opened, " "); got != second || !strings.HasSuffix(got, "%2Fb%2Fhandler.ts&line=1") || a.overlay != nil {
		t.Errorf("2 opened %q, want %q", got, second)
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
	if a.overlay != nil || len(*opened) != 0 || a.status != "copied the path "+filepath.Join(root, "b", "handler.ts")+":1" {
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

// answered hands the app the answer of a lookup for the frame the cursor is
// on, as the lookup last asked would give it.
func answered(t *testing.T, a *app, fv *flameView, found source.Found) {
	t.Helper()
	ix, err := a.ds.source.get()
	if err != nil {
		t.Fatal(err)
	}
	label, _ := fv.selectedFunction()
	_, cmd := a.Update(sourceFoundMsg{seq: a.ds.source.seq, view: fv, label: label, index: ix, found: found})
	deliver(a, cmd)
}

// Where more files could hold a frame than are read for one, a single match
// is not known to be the only one: the status line says so first, and a
// miss says it too.
func TestOpenSourceSaysWhatWasNotRead(t *testing.T) {
	a, fv, root, opened := sourceApp(t, "vscode")
	onFrame(t, a, fv, "com.example.shop.CartService.total")
	one := []source.Match{{Path: "src/main/java/com/example/shop/CartService.java", Line: 6, How: source.Declaration}}
	java := filepath.Join(root, "src", "main", "java", "com", "example", "shop", "CartService.java") + ":6"
	answered(t, a, fv, source.Found{Matches: one, Unread: 1250})
	want := "1,250 files that could hold it were not read · asked VS Code to open the declaration of total: " + java
	if len(*opened) != 1 || a.status != want {
		t.Errorf("opened %v, status %q\n want  %q", *opened, a.status, want)
	}
	// An approximate line is the first thing said, before what was not read.
	several := []source.Match{{Path: one[0].Path, Line: 4, How: source.FirstOfSeveral}}
	answered(t, a, fv, source.Found{Matches: several, Unread: 2})
	if !strings.HasPrefix(a.status, "approximate: the first of several declarations of total · 2 files that could hold it were not read · asked VS Code to open it: ") {
		t.Errorf("an approximate match with files unread: status %q", a.status)
	}
	answered(t, a, fv, source.Found{Unread: 50})
	if !a.statusErr || !strings.HasPrefix(a.status, "50 files that could hold it were not read · no source file for ") {
		t.Errorf("a miss: status %q", a.status)
	}
	// One file is one file: a candidate that turned into something that is
	// not read (a named pipe, a directory) is a count of one.
	answered(t, a, fv, source.Found{Unread: 1})
	if !a.statusErr || !strings.HasPrefix(a.status, "1 file that could hold it was not read · no source file") {
		t.Errorf("a miss with one file unread: status %q", a.status)
	}
	// The time ran out before the files did.
	answered(t, a, fv, source.Found{Unread: 50, Late: true})
	if !a.statusErr || !strings.HasPrefix(a.status, "the search was cut after 2 seconds: 50 files that could hold it were not read · no source") {
		t.Errorf("a miss out of time: status %q", a.status)
	}
	answered(t, a, fv, source.Found{Matches: one, Unread: 7, Late: true, More: 1200})
	if !strings.HasPrefix(a.status, "1,200 more matches are not offered · the search was cut after 2 seconds: 7 files that could hold it were not read · asked VS Code to open") {
		t.Errorf("one match out of time: status %q", a.status)
	}
}

// One match from a directory that was not walked to its end is not known to
// be the only one either, and a list of several says the same under it and
// when one of them is opened.
func TestOpenSourceSaysWhenTheWalkWasPartial(t *testing.T) {
	a, fv, _, opened := sourceApp(t, "vscode")
	onFrame(t, a, fv, "com.example.shop.CartService.total")
	ix, err := a.ds.source.get()
	if err != nil {
		t.Fatal(err)
	}
	ix.Partial = "more than 60000 source files"
	press(a, key("O"))
	if len(*opened) != 1 || !strings.HasPrefix(a.status, "the directory was not read to its end (more than 60000 source files) · asked VS Code to open the declaration of total: ") {
		t.Errorf("one match of a partial walk: opened %v, status %q", *opened, a.status)
	}
	onFrame(t, a, fv, "handler.ts:resolve")
	answered(t, a, fv, source.Found{Unread: 3, More: 2, Matches: []source.Match{
		{Path: "a/handler.ts", Line: 1, How: source.Declaration}, {Path: "b/handler.ts", Line: 1, How: source.Declaration}}})
	overlayAs[*sourcePicker](t, a)
	out := screen(a)
	for _, caveat := range []string{"2 more matches are not offered", "3 files that could hold it were not read", "the directory was not read to its end (more than 60000 source files)"} {
		if !strings.Contains(out, caveat) {
			t.Errorf("the picker does not say %q:\n%s", caveat, out)
		}
	}
	for _, k := range []string{"2", "enter"} {
		if a.overlay == nil {
			answered(t, a, fv, source.Found{Unread: 3, Matches: []source.Match{{Path: "a/handler.ts", Line: 1}, {Path: "b/handler.ts", Line: 1}}})
		}
		press(a, key(k))
		if !strings.Contains(a.status, "3 files that could hold it were not read · the directory was not read to its end") {
			t.Errorf("%s in the picker: status %q", k, a.status)
		}
	}
}

// Only the answer to the last lookup asked is acted on, and only for the
// frame the cursor is on: an editor does not come up for a frame the reader
// has moved on from, and a lookup that is no longer wanted is ended.
func TestOpenSourceActsOnTheLastLookupOnly(t *testing.T) {
	a, fv, root, opened := sourceApp(t, "vscode")
	ask := func(frame string) tea.Cmd {
		onFrame(t, a, fv, frame)
		_, asked := a.Update(key("O"))
		_, lookup := a.Update(asked())
		return lookup
	}
	total := ask("com.example.shop.CartService.total")
	unnamed := ask("com.example.shop.Unnamed.generated")
	// The newer answers first, then the older: one file opens, the newer's.
	deliver(a, unnamed)
	deliver(a, total)
	want, _ := editorLink(t, "vscode", root, "src/main/java/com/example/shop/Unnamed.java", 3)
	if got := strings.Join(*opened, " "); got != want {
		t.Errorf("opened %q, want only %q", got, want)
	}
	// The older lookup was ended when the newer was asked: it read nothing.
	*opened = nil
	first := ask("com.example.shop.CartService.total")
	second := ask("com.example.shop.Unnamed.generated")
	msgs := batchMsgs(first)
	if found, ok := msgs[len(msgs)-1].(sourceFoundMsg); !ok || !found.found.Late || len(found.found.Matches) != 0 {
		t.Errorf("the lookup that was asked first was not ended: %+v", msgs[len(msgs)-1])
	}
	deliver(a, second)
	if len(*opened) != 1 {
		t.Errorf("the last lookup opened %v", *opened)
	}

	// Asked twice on one frame: the first lookup was ended, and what it
	// then has to say (it found nothing in no time) is not said over the
	// answer of the second.
	*opened = nil
	first = ask("com.example.shop.CartService.total")
	second = ask("com.example.shop.CartService.total")
	deliver(a, second)
	for _, msg := range batchMsgs(first) {
		if found, ok := msg.(sourceFoundMsg); ok {
			_, cmd := a.Update(found)
			deliver(a, cmd)
		}
	}
	if len(*opened) != 1 || a.statusErr || !strings.HasPrefix(a.status, "asked VS Code to open") {
		t.Errorf("asked twice on one frame: opened %v, status %q (error %v)", *opened, a.status, a.statusErr)
	}

	// The cursor moved on before the answer: nothing opens, and the status
	// line says why.
	*opened = nil
	lookup := ask("com.example.shop.CartService.total")
	onFrame(t, a, fv, "handler.ts:resolve")
	deliver(a, lookup)
	if len(*opened) != 0 || a.overlay != nil || !strings.Contains(a.status, "after the cursor moved on") {
		t.Errorf("an answer for a frame that is not selected: opened %v, overlay %T, status %q", *opened, a.overlay, a.status)
	}
	// Something is open over the graph: the same.
	lookup = ask("com.example.shop.CartService.total")
	press(a, key("?"))
	deliver(a, lookup)
	if _, help := a.overlay.(*helpOverlay); len(*opened) != 0 || !help || !strings.Contains(a.status, "O looks again") {
		t.Errorf("an answer under the help: opened %v, overlay %T, status %q", *opened, a.overlay, a.status)
	}
	// A key that needs no lookup ends the one in flight as well.
	press(a, key("esc"))
	lookup = ask("com.example.shop.CartService.total")
	onFrame(t, a, fv, "java.lang.Thread.run")
	press(a, key("O"))
	onFrame(t, a, fv, "com.example.shop.CartService.total")
	deliver(a, lookup)
	if len(*opened) != 0 {
		t.Errorf("a lookup from before another O opened %v", *opened)
	}
}

// o opens the process, and a frame's name is no destination: a label that
// reads as a link is not in the record the key reads.
func TestFlameGraphOffersNoLinkFromAFrame(t *testing.T) {
	a, _, fv, _ := openFlame(t)
	answerFlame(a, fv, stackRecords("2026-07-15T12:00:00Z", "https://example.invalid/x.js:run 9"), nil)
	fv.graph.ClearSearch()
	press(a, flameKey("down"))
	if fn, ok := fv.selectedFunction(); !ok || !strings.HasPrefix(fn, "https://") {
		t.Fatalf("the cursor is to be on the frame that reads as a link: %q", fn)
	}
	rec, entity := fv.Selection()
	if entity == nil || len(rec) != 1 || rec[catalog.ProfEntityID] != entity.ID {
		t.Errorf("the record o reads is %v", rec)
	}
	if fields := urlFields(rec); len(fields) != 0 {
		t.Errorf("o is offered %v as a destination", fields)
	}
}

// The picker shows as many matches as fit and scrolls: a number opens only
// a match that is on the screen.
func TestSourcePickerOpensOnlyWhatItShows(t *testing.T) {
	a, fv, _, opened := sourceApp(t, "vscode")
	onFrame(t, a, fv, "handler.ts:resolve")
	var nine []source.Match
	for i := range 9 {
		nine = append(nine, source.Match{Path: "a/handler.ts", Line: i + 1, How: source.FirstOfSeveral})
	}
	a.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	answered(t, a, fv, source.Found{Matches: nine})
	p := overlayAs[*sourcePicker](t, a)
	drawn := func() (rows []string) {
		out := a.View()
		if lines := strings.Split(out, "\n"); len(lines) > 10 {
			t.Fatalf("the picker is %d lines on a screen of 10", len(lines))
		}
		for i := range 9 {
			if strings.Contains(out, fmt.Sprintf("%d · ", i+1)) {
				rows = append(rows, fmt.Sprint(i+1))
			}
		}
		return rows
	}
	shown := drawn()
	if len(shown) == 0 || len(shown) >= 9 || shown[0] != "1" {
		t.Fatalf("at 40x10 the picker draws %v of nine", shown)
	}
	press(a, key("9"))
	if len(*opened) != 0 || a.overlay == nil {
		t.Fatalf("9 opened %v, a match that is not on the screen", *opened)
	}
	for range 8 {
		press(a, key("j"))
	}
	if shown = drawn(); p.sel != 8 || shown[len(shown)-1] != "9" || shown[0] == "1" {
		t.Fatalf("after moving to the last match the picker draws %v", shown)
	}
	if out := a.View(); !strings.Contains(out, " of 9") {
		t.Errorf("the picker does not say where in the list it is:\n%s", out)
	}
	press(a, key("1"))
	if len(*opened) != 0 {
		t.Fatalf("1 opened %v, a match that is scrolled away", *opened)
	}
	press(a, key("9"))
	if len(*opened) != 1 || !strings.HasSuffix((*opened)[0], ":9") {
		t.Errorf("9 on the screen opened %v", *opened)
	}
}

// What the reader must not miss is at the start of the status line, where a
// narrow terminal does not cut it, and the path keeps its end.
func TestOpenSourceStatusLeadsWithItsCaveat(t *testing.T) {
	a, fv, _, _ := sourceApp(t, "vscode")
	onFrame(t, a, fv, "com.example.shop.CartService.addItem")
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	press(a, key("O"))
	line := ""
	for _, l := range strings.Split(screen(a), "\n") {
		if strings.Contains(l, "approximate") {
			line = l
		}
	}
	if !strings.Contains(line, "approximate: the first of several declarations of addItem") || !strings.HasSuffix(strings.TrimRight(line, " "), "CartService.java:4") {
		t.Errorf("at 120 columns the status line is %q\n(status %q)", line, a.status)
	}
}

// The name of the directory is text of the machine like any other: no
// control sequence of it reaches the status line.
func TestOpenSourceNamesTheDirectorySafely(t *testing.T) {
	a, fv, _, _ := sourceApp(t, "vscode")
	root := filepath.Join(t.TempDir(), "re\x1b[31mpo")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Skip("no such directory name here:", err)
	}
	a.ds.source = newSourceLookup(root, "vscode")
	onFrame(t, a, fv, "org.library.Client.call")
	_, asked := a.Update(key("O"))
	_, lookup := a.Update(asked())
	msgs := batchMsgs(lookup)
	for _, m := range msgs {
		_, cmd := a.Update(m)
		deliver(a, cmd)
		if strings.Contains(a.status, "\x1b") || !strings.Contains(a.status, "re␛[31mpo") {
			t.Errorf("the status line names the directory as %q", a.status)
		}
	}
	if len(msgs) != 2 {
		t.Fatalf("a lookup is to say that it looks, and then what it found: %d messages", len(msgs))
	}
}

// A walk that failed has failed for the session: O says so, the footer
// offers the key on no frame, and no further walk is started.
func TestSourceWalkThatFailedIsNotRepeated(t *testing.T) {
	a, fv, root, opened := sourceApp(t, "vscode")
	a.ds.source = newSourceLookup(filepath.Join(root, "gone"), "vscode")
	onFrame(t, a, fv, "com.example.shop.CartService.total")
	if !strings.Contains(screen(a), "O open source") {
		t.Fatal("before the walk the footer is to offer O on a frame that may have a file")
	}
	if a.ds.source.warm() == nil {
		t.Fatal("the first warm is to walk")
	}
	if _, err := a.ds.source.get(); err == nil {
		t.Fatal("the fixture is a directory that is not there")
	}
	if a.ds.source.warm() != nil {
		t.Error("a walk that failed is started again")
	}
	if strings.Contains(screen(a), "O open source") {
		t.Errorf("the footer offers O after the walk failed:\n%s", screen(a))
	}
	press(a, key("O"))
	if len(*opened) != 0 || !a.statusErr || !strings.HasPrefix(a.status, "the source directory could not be read: ") {
		t.Errorf("O after a failed walk: status %q", a.status)
	}
}

// An editor DTUI_EDITOR names and dtui does not know is said where it
// would have been used.
func TestOpenSourceNamesAnUnknownEditor(t *testing.T) {
	a, fv, _, _ := sourceApp(t, "")
	a.ds.source.unknownEditor = "emacs\x1b[31m"
	onFrame(t, a, fv, "com.example.shop.CartService.total")
	press(a, key("O"))
	if !strings.HasPrefix(a.status, "DTUI_EDITOR=emacs␛[31m is no editor dtui knows (it names one of vscode, ") || !strings.Contains(a.status, "copied the path of the declaration of total: ") {
		t.Errorf("status %q", a.status)
	}
}

// A file that stopped being one between the lookup and the opening (picked
// from the list a while later, say) is not handed to an editor: the status
// line says what it is now.
func TestOpenSourceRefusesWhatIsNoLongerAFile(t *testing.T) {
	a, fv, root, opened := sourceApp(t, "vscode")
	onFrame(t, a, fv, "com.example.shop.CartService.total")
	const rel = "src/main/java/com/example/shop/CartService.java"
	p := filepath.Join(root, filepath.FromSlash(rel))
	if _, err := a.ds.source.get(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	answered(t, a, fv, source.Found{Matches: []source.Match{{Path: rel, Line: 6, How: source.Declaration}}})
	if want := `"` + rel + `" is not a regular file any more`; !a.statusErr || a.status != want || len(*opened) != 0 {
		t.Errorf("status %q (error %v), opened %v; want %q and nothing opened", a.status, a.statusErr, *opened, want)
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
		!strings.HasPrefix(a.status, "no editor is known (DTUI_EDITOR names one: vscode, ") ||
		!strings.Contains(a.status, " · copied the path of the declaration of total: ") || !strings.HasSuffix(a.status, "CartService.java:6") {
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

	// A repository that is not looked in says which it is.
	a.opts.SourceRefused = source.ErrHomeDirectory.Error()
	press(a, key("O"))
	if len(*opened) != 0 || !a.statusErr || a.status != "O looks for a frame's file in the repository dtui was started in: the repository here is your home directory, which is not searched: start dtui inside the application's checkout" {
		t.Errorf("in a home directory: opened %v, status %q", *opened, a.status)
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
	if !a.statusErr || !strings.Contains(a.status, "the directory was not read to its end (more than 2 files and directories)") {
		t.Errorf("a partial walk: status %q", a.status)
	}
	first, _, _ := a.ds.source.walked()
	if err := os.WriteFile(filepath.Join(root, "Late.java"), []byte("class Late {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	press(a, key("O"))
	if again, _, done := a.ds.source.walked(); again != first || first == nil || !done {
		t.Error("the directory was read again")
	}
}

// batchMsgs runs a command and gives the messages it and the commands it
// batches return, in order, without handing them to the app.
func batchMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var msgs []tea.Msg
		for _, c := range batch {
			msgs = append(msgs, batchMsgs(c)...)
		}
		return msgs
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// A DTUI_EDITOR that names no editor is said when dtui starts: nothing else
// would tell the reader why no editor opens.
func TestUnknownEditorIsSaidAtTheStart(t *testing.T) {
	a, err := newApp(Options{ContextName: "test", EditorUnknown: "emacs", SourceRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	said, _ := a.startupNotice()().(statusMsg)
	if !said.isErr || !strings.HasPrefix(said.text, "DTUI_EDITOR=emacs is no editor dtui knows") || !strings.Contains(said.text, "vscode, ") || a.ds.source.unknownEditor != "emacs" {
		t.Errorf("at the start: %+v", said)
	}
	// The host's own notice is the one line there is room for.
	a.opts.StartupNotice = "workspace loaded"
	if said, _ := a.startupNotice()().(statusMsg); said.text != "workspace loaded" || said.isErr {
		t.Errorf("with a notice of the host: %+v", said)
	}
	if a, err = newApp(Options{ContextName: "test"}); err != nil || a.startupNotice() != nil {
		t.Errorf("without the setting a notice is made (%v)", err)
	}
}
