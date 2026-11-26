package catalog

import (
	"regexp"
	"strings"
)

// JavaScript traces as V8 (Node, Chromium) writes them:
// "at fn (/path/file.js:1:2)", or "at /path/file.js:1:2" for a frame
// without a function name.

var (
	jsAliasRe = regexp.MustCompile(` \[as [^\]]*\]$`)
	jsIndexRe = regexp.MustCompile(`^index \d+$`)
	jsLinesRe = regexp.MustCompile(`(?::\d+)+`)
)

// jsRuntimePrefixes are the module prefixes of Node's own code.
var jsRuntimePrefixes = []string{"node:", "internal/"}

const jsDependencyDir = "node_modules/"

func jsFrame(line string) (StackFrame, bool) {
	rest, ok := strings.CutPrefix(line, "at ")
	if !ok {
		return StackFrame{}, false
	}
	// A frame resumed after an await is marked "async"; it is the same frame.
	rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest)+" ", "async "))
	open := strings.IndexByte(rest, '(')
	switch {
	case open < 0:
		if rest == "" || strings.ContainsAny(rest, " \t") {
			return StackFrame{}, false
		}
		return StackFrame{Module: jsModule(rest), Function: FrameAnonymous, Syntax: StackJS}, true
	case open > 0 && rest[open-1] == ' ':
		loc := rest[open+1:]
		if end := strings.LastIndexByte(loc, ')'); end >= 0 {
			loc = loc[:end]
		}
		return StackFrame{Module: jsModule(loc), Function: jsFunction(rest[:open-1]), Syntax: StackJS}, true
	}
	return StackFrame{}, false
}

// jsFunction drops a "[as alias]" suffix; "Object.<anonymous>" is
// module-level code and reads as plain anonymous.
func jsFunction(name string) string {
	name = jsAliasRe.ReplaceAllString(strings.TrimSpace(name), "")
	if name == "" || name == "Object."+FrameAnonymous {
		return FrameAnonymous
	}
	return name
}

// jsModule reduces a location to its file: no line or column, no file://
// scheme, and a dependency addressed from its node_modules directory
// wherever that is installed.
func jsModule(loc string) string {
	loc = strings.TrimPrefix(jsLinesRe.ReplaceAllString(loc, ""), "file://")
	if jsIndexRe.MatchString(loc) { // "Promise.all (index 3)"
		return "index"
	}
	return loc[max(strings.LastIndex(loc, jsDependencyDir), 0):]
}

func jsRootCause(lines []string) []StackFrame {
	return framesOf(lines, jsFrame)
}

func jsInApp(f StackFrame) bool {
	return strings.ContainsAny(f.Module, `/\`) && !strings.Contains(f.Module, jsDependencyDir) &&
		!hasAnyPrefix(f.Module, jsRuntimePrefixes)
}
