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
)

// jsRuntimePrefixes are the module prefixes of Node's own code.
var jsRuntimePrefixes = []string{"node:", "internal/"}

const jsDependencyDir = "node_modules/"

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
