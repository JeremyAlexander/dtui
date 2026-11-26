//go:build windows

package source

import "testing"

// A path on Windows starts with a volume: a drive letter or a share. The
// volume is the first part of the path a link names, and a path that only
// looks absolute elsewhere (rooted without a drive, or relative to a
// drive's current directory) is refused.
func TestEditorURLOfAVolume(t *testing.T) {
	for abs, want := range map[string]string{
		`C:\src\shop\Cart Service.java`:     "/C:/src/shop/Cart Service.java",
		`c:\src\shop\..\shop\Cart.java`:     "/c:/src/shop/Cart.java",
		`\\server\share\shop\Cart.java`:     "//server/share/shop/Cart.java",
		`C:/src/shop/sub\Cart Service.java`: "/C:/src/shop/sub/Cart Service.java",
	} {
		for _, id := range EditorIDs() {
			e, _ := EditorByID(id)
			link, err := e.URL(abs, 7)
			if err != nil {
				t.Fatalf("%s: %s: %v", id, abs, err)
			}
			if got, line := placeOf(t, e, link); got != want || line != 7 {
				t.Errorf("%s: %s is worded %q, which names %q at line %d; want %q at 7", id, abs, link, got, line, want)
			}
		}
	}
	e, _ := EditorByID("vscode")
	for _, bad := range []string{`\src\shop\Cart.java`, `/src/shop/Cart.java`, `C:Cart.java`, `shop\Cart.java`} {
		if link, err := e.URL(bad, 1); err == nil {
			t.Errorf("URL(%q) = %q", bad, link)
		}
	}
}
