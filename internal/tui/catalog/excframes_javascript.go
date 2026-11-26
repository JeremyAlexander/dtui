package catalog

import (
	"regexp"
	"strings"
)

// JavaScript traces. V8 (Node, Chromium) writes "at fn (/path/file.js:1:2)",
// or "at /path/file.js:1:2" for a frame without a function name; Firefox and
// Safari write "fn@https://host/file.js:1:2".

var (
	// "at fn (location)": the function is one name, with "new" before a
	// constructor and "[as alias]" after a renamed method. Prose after "at"
	// ("at least 3 items (max 5)") has more words. The closing parenthesis
	// may be cut off.
	jsCallRe = regexp.MustCompile(`^at (?:async )?((?:new )?[\w$.<>#]+(?: \[as [^\]]+\])?) \((.*?)\)?$`)
	// "at location": no function name.
	jsBareRe = regexp.MustCompile(`^at (?:async )?(\S+)$`)
	// A location is a file with its line, or one of V8's words for code
	// that has no file. No file starts with a digit; a clock time does.
	jsLocationRe = regexp.MustCompile(`^(?:[^\d\s].*:\d+|<anonymous>|native|index \d+)$`)
	// "fn@location:line:column". Firefox marks an async caller "async*fn".
	jsAtSignRe = regexp.MustCompile(`^(?:[\w ]+\*)?([\w$.<>/\[\]]*)@(\S+:\d+:\d+)$`)

	jsAliasRe = regexp.MustCompile(` \[as [^\]]*\]$`)
	jsIndexRe = regexp.MustCompile(`^index \d+$`)
	jsLinesRe = regexp.MustCompile(`(?::\d+)+`)
	// The application's files are named by an absolute path or a URL.
	jsAbsoluteRe = regexp.MustCompile(`^(?:/|[A-Za-z]:/|[A-Za-z][A-Za-z0-9+.-]+://)`)
	// The content hash a bundler puts into a file name ("main.8f3a2b1c.js").
	jsContentHashRe = regexp.MustCompile(`^(.*[^/.-])[.-][0-9a-f]{8,}((?:\.\w+)+)$`)
)

const (
	// jsRuntimeScheme names Node's own modules ("node:events").
	jsRuntimeScheme = "node:"
	// jsDependencyDir is where installed packages live.
	jsDependencyDir = "node_modules/"
)

// jsNoFile are the locations of code that has no file.
var jsNoFile = []string{FrameAnonymous, "native", "index"}

func jsFrame(line string) (StackFrame, bool) {
	function, loc := "", ""
	if m := jsCallRe.FindStringSubmatch(line); m != nil {
		function, loc = m[1], m[2]
	} else if m := jsBareRe.FindStringSubmatch(line); m != nil {
		loc = m[1]
	} else if m := jsAtSignRe.FindStringSubmatch(line); m != nil {
		function, loc = m[1], m[2]
	}
	if !jsLocationRe.MatchString(loc) {
		return StackFrame{}, false
	}
	return StackFrame{Module: jsModule(loc), Function: jsFunction(function), Syntax: StackJS}, true
}

// jsFunction drops a "[as alias]" suffix; "Object.<anonymous>" is
// module-level code and reads as plain anonymous.
func jsFunction(name string) string {
	name = jsAliasRe.ReplaceAllString(name, "")
	if name == "" || name == "Object."+FrameAnonymous {
		return FrameAnonymous
	}
	return name
}

// jsModule reduces a location to the module it names, without line or
// column:
//
//   - Code without a file keeps V8's word for it, and "index N" (an element
//     of Promise.all) loses its number.
//   - A dependency is addressed from its innermost node_modules directory,
//     wherever that is installed.
//   - Node's own code is "node:<name>"; before Node had that scheme it
//     printed a bare or relative file ("events.js"), which the application's
//     files, always absolute, never are.
//   - Anything else is the application's and goes through sourceModule,
//     then loses the content hash of a bundle's file name.
func jsModule(loc string) string {
	loc = strings.ReplaceAll(jsLinesRe.ReplaceAllString(loc, ""), `\`, "/")
	switch {
	case jsIndexRe.MatchString(loc):
		return "index"
	case loc == FrameAnonymous, loc == "native", strings.HasPrefix(loc, jsRuntimeScheme):
		return loc
	}
	if i := strings.LastIndex(loc, jsDependencyDir); i >= 0 {
		return loc[i:]
	}
	if !jsAbsoluteRe.MatchString(loc) {
		return jsRuntimeScheme + strings.TrimSuffix(loc, ".js")
	}
	return jsContentHashRe.ReplaceAllString(sourceModule(loc), "$1$2")
}

func jsRootCause(lines []string) []StackFrame {
	return framesOf(lines, jsFrame)
}

func jsInApp(f StackFrame) bool {
	for _, word := range jsNoFile {
		if f.Module == word {
			return false
		}
	}
	return !strings.HasPrefix(f.Module, jsRuntimeScheme) && !strings.HasPrefix(f.Module, jsDependencyDir)
}
