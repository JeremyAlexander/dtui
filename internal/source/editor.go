package source

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// Editor is an editor that opens a file at a line when the system is handed
// a link in its scheme. dtui never starts an editor: it words the link.
type Editor struct {
	// ID is the name a setting uses, and Name the name a reader knows.
	ID, Name string
	scheme   string
	form     linkForm
}

type linkForm int

const (
	// formFile is scheme://file/<path>:<line>, VS Code's and its relatives'.
	formFile linkForm = iota
	// formOpen is scheme://open?file=<path>&line=<line>, JetBrains'.
	formOpen
	// formURL is scheme://open?url=file://<path>&line=<line>.
	formURL
)

var editors = []Editor{
	{"vscode", "VS Code", "vscode", formFile},
	{"vscode-insiders", "VS Code Insiders", "vscode-insiders", formFile},
	{"vscodium", "VSCodium", "vscodium", formFile},
	{"cursor", "Cursor", "cursor", formFile},
	{"windsurf", "Windsurf", "windsurf", formFile},
	{"zed", "Zed", "zed", formFile},
	{"idea", "IntelliJ IDEA", "idea", formOpen},
	{"pycharm", "PyCharm", "pycharm", formOpen},
	{"goland", "GoLand", "goland", formOpen},
	{"webstorm", "WebStorm", "webstorm", formOpen},
	{"rider", "Rider", "rider", formOpen},
	{"phpstorm", "PhpStorm", "phpstorm", formOpen},
	{"rubymine", "RubyMine", "rubymine", formOpen},
	{"clion", "CLion", "clion", formOpen},
	{"sublime", "Sublime Text", "subl", formURL},
	{"textmate", "TextMate", "txmt", formURL},
}

// EditorIDs are the names EditorByID knows, in the order they are listed.
func EditorIDs() []string {
	ids := make([]string, len(editors))
	for i, e := range editors {
		ids[i] = e.ID
	}
	return ids
}

// EditorByID is the editor a setting names.
func EditorByID(id string) (Editor, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, e := range editors {
		if e.ID == id {
			return e, true
		}
	}
	return Editor{}, false
}

// commands are the programs VISUAL and EDITOR name, by the editor whose
// link scheme opens the same thing. A terminal editor has no scheme.
var commands = map[string]string{
	"code": "vscode", "code-insiders": "vscode-insiders", "codium": "vscodium", "cursor": "cursor",
	"windsurf": "windsurf", "zed": "zed", "zeditor": "zed", "idea": "idea", "pycharm": "pycharm",
	"goland": "goland", "webstorm": "webstorm", "rider": "rider", "phpstorm": "phpstorm",
	"rubymine": "rubymine", "clion": "clion", "subl": "sublime", "mate": "textmate",
}

// DetectEditor picks the editor from the environment: DTUI_EDITOR names one,
// else the terminal dtui runs in says which editor it belongs to, else
// VISUAL or EDITOR names a program one of them is known by. The values are
// read as names only; nothing in them is run.
func DetectEditor(getenv func(string) string) (Editor, bool) {
	// A blank setting is no setting. One that names no editor is not
	// guessed around: UnknownEditor has it, for the reader to be told.
	if id := strings.TrimSpace(getenv("DTUI_EDITOR")); id != "" {
		return EditorByID(id)
	}
	hint := ""
	switch {
	case getenv("CURSOR_TRACE_ID") != "":
		hint = "cursor"
	case getenv("TERM_PROGRAM") == "vscode" && strings.Contains(getenv("TERM_PROGRAM_VERSION"), "insider"):
		hint = "vscode-insiders"
	case getenv("TERM_PROGRAM") == "vscode":
		hint = "vscode"
	case getenv("TERM_PROGRAM") == "zed" || getenv("ZED_TERM") != "":
		hint = "zed"
	case strings.Contains(getenv("TERMINAL_EMULATOR"), "JetBrains"):
		hint = "idea"
	}
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if hint != "" {
			break
		}
		// "code --wait" names code; the rest is for a shell, which is not asked.
		fields := strings.Fields(getenv(name))
		if len(fields) == 0 {
			continue
		}
		// The value may be another platform's path: its last part either way.
		program := strings.ToLower(fields[0])
		program = program[strings.LastIndexAny(program, `/\`)+1:]
		for _, ext := range []string{".exe", ".cmd", ".bat"} {
			program = strings.TrimSuffix(program, ext)
		}
		hint = commands[program]
	}
	return EditorByID(hint)
}

// UnknownEditor is what DTUI_EDITOR is set to when it names no editor of
// EditorIDs, and "" otherwise. Such a setting leaves dtui without an editor,
// which nothing else would say.
func UnknownEditor(getenv func(string) string) string {
	id := strings.TrimSpace(getenv("DTUI_EDITOR"))
	if _, ok := EditorByID(id); ok {
		return ""
	}
	return id
}

// escapePath percent-encodes every byte of a path but the slashes between
// its segments and the characters no URL gives a meaning to. That is more
// than a URL needs: the colon that would end the path in a file link goes,
// and so does everything a careless handler of the link could trip over.
func escapePath(slashed string) string {
	var b strings.Builder
	for i := 0; i < len(slashed); i++ {
		c := slashed[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte("/-._~", c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// URL is the link that opens an absolute path in the editor, at a line when
// one is known (line > 0). The path is the platform's own: a volume name
// (C:, \\server\share) becomes the path's first part. Every byte of the path
// that could end the path or start another part of the link is
// percent-encoded.
func (e Editor) URL(abs string, line int) (string, error) {
	if e.scheme == "" {
		return "", fmt.Errorf("no editor")
	}
	if !filepath.IsAbs(abs) || strings.ContainsRune(abs, 0) {
		return "", fmt.Errorf("not an absolute path: %q", abs)
	}
	slashed := filepath.ToSlash(filepath.Clean(abs))
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed // a drive letter
	}
	return e.link(slashed, line), nil
}

// link words the link for a path that is absolute and slash-separated on
// every platform, which is all a link knows of a path.
func (e Editor) link(slashed string, line int) string {
	escaped := escapePath(slashed)
	switch e.form {
	case formOpen:
		// A space is %20 and not the + of a form: a handler that reads the
		// query as a plain URI keeps a + for what it is.
		link := e.scheme + "://open?file=" + strings.ReplaceAll(url.QueryEscape(slashed), "+", "%20")
		if line > 0 {
			link += fmt.Sprintf("&line=%d", line)
		}
		return link
	case formURL:
		link := e.scheme + "://open?url=" + url.QueryEscape("file://"+escaped)
		if line > 0 {
			link += fmt.Sprintf("&line=%d", line)
		}
		return link
	}
	link := e.scheme + "://file" + escaped
	if line > 0 {
		link += fmt.Sprintf(":%d", line)
	}
	return link
}
