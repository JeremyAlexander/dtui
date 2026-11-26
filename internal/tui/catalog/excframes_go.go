package catalog

import (
	"regexp"
	"strings"
)

// Go goroutine dumps: "pkg/path.(*Type).Method(0x1, 0x2)" frames, each
// followed by its file line, under a "goroutine N [state]:" header.

var (
	goGoroutineRe = regexp.MustCompile(`^goroutine \d+ \[`)
	goNameRe      = regexp.MustCompile(`^[\w./~%·*()\[\],-]+$`)
	// Arguments are printed as hex words, "..." when elided.
	goArgsRe     = regexp.MustCompile(`^[0-9a-fx?,{}\s.]*$`)
	goTypeArgsRe = regexp.MustCompile(`\[[^\]]*\]`)
	// A method's receiver is written "(*Type)" or "(Type)".
	goReceiver = strings.NewReplacer("(*", "", "(", "", ")", "")
)

// goVendorPrefixes are widely used module hosts that are never the
// application itself.
var goVendorPrefixes = []string{
	"golang.org/", "google.golang.org/", "go.opentelemetry.io/", "go.uber.org/", "gopkg.in/",
}

// goFrame reads a frame or a "created by" line. The file line that follows
// each frame matches nothing.
func goFrame(line string) (StackFrame, bool) {
	name, created := strings.CutPrefix(line, "created by ")
	if created {
		name, _, _ = strings.Cut(name, " in goroutine ")
	} else {
		open := strings.LastIndexByte(line, '(')
		if open == -1 || !strings.HasSuffix(line, ")") || !goArgsRe.MatchString(line[open+1:len(line)-1]) {
			return StackFrame{}, false
		}
		name = line[:open]
	}
	if !goNameRe.MatchString(name) {
		return StackFrame{}, false
	}
	name = goTypeArgsRe.ReplaceAllString(name, "")
	// The package path ends at the first dot after its last slash.
	slash := strings.LastIndexByte(name, '/') + 1
	module, function := "", name
	if dot := strings.IndexByte(name[slash:], '.'); dot >= 0 {
		module, function = name[:slash+dot], name[slash+dot+1:]
	}
	function = goReceiver.Replace(function)
	if function == "" {
		return StackFrame{}, false
	}
	return StackFrame{Module: module, Function: function, Syntax: StackGo}, true
}

// goRootCause returns the frames of the first goroutine, the one that
// panicked.
func goRootCause(lines []string) []StackFrame {
	var out []StackFrame
	goroutines := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if goGoroutineRe.MatchString(line) {
			if goroutines++; goroutines > 1 {
				break
			}
		} else if f, ok := goFrame(line); ok {
			out = append(out, f)
		}
	}
	return out
}

func goInApp(f StackFrame) bool {
	// The standard library has no host in its import path.
	host, _, _ := strings.Cut(f.Module, "/")
	return f.Module == "main" || strings.Contains(host, ".") && !hasAnyPrefix(f.Module, goVendorPrefixes)
}
