package catalog

import (
	"regexp"
	"strings"
)

// Python tracebacks: `File "/path/file.py", line 1, in fn` frames, each
// followed by its source line, the throw site last. An exception group
// prints its own traceback and then one per sub-exception, in a gutter of
// "|" under numbered "+---- 1 ----" rules.

var (
	pythonFrameRe = regexp.MustCompile(`^File "([^"]+)", line \d+(?:, in (.+))?$`)
	// The standard library's directory, as Unix and Windows installs name it.
	pythonStdlibRe = regexp.MustCompile(`^.*?(?:^|/)(?:lib(?:64)?/python\d+(?:\.\d+)*|[Pp]ython\d+/[Ll]ib)/`)
	// The rule above a sub-exception of a group, numbered, and the
	// unnumbered one that closes the group.
	pythonGroupRuleRe = regexp.MustCompile(`^\+[-+]{3,}(?: (\d+) -+)?$`)
)

// pythonPackageDirs are where installed packages live, and what their
// modules start with; pythonStdlibDir is what the standard library's start
// with.
var pythonPackageDirs = []string{"site-packages/", "dist-packages/"}

const pythonStdlibDir = "lib/python/"

const (
	pythonTraceback = "Traceback (most recent call last)"
	// pythonGutter is what an exception group puts before each line.
	pythonGutter = "|+ "
)

func pythonFrame(line string) (StackFrame, bool) {
	m := pythonFrameRe.FindStringSubmatch(strings.TrimLeft(line, pythonGutter))
	if m == nil {
		return StackFrame{}, false
	}
	return StackFrame{Module: pythonModule(m[1]), Function: firstNonEmpty(m[2], "<module>"), Syntax: StackPython}, true
}

// pythonModule reduces a file to the module it names: an installed package
// is addressed from its site-packages directory, the standard library from
// "lib/python/" whatever the interpreter's version and prefix, a name that
// is no file ("<frozen importlib._bootstrap>") is kept, and anything else is
// the application's and goes through sourceModule.
func pythonModule(path string) string {
	path = strings.ReplaceAll(path, `\`, "/")
	for _, dir := range pythonPackageDirs {
		if i := strings.LastIndex(path, dir); i >= 0 {
			return path[i:]
		}
	}
	if loc := pythonStdlibRe.FindStringIndex(path); loc != nil {
		return pythonStdlibDir + path[loc[1]:]
	}
	if strings.HasPrefix(path, "<") {
		return path
	}
	return sourceModule(path)
}

// pythonRootCause returns the frames of the first traceback, which is the
// first exception raised, reversed so that the throw site comes first. An
// exception group is a wrapper around the exceptions it collected, so there
// the first sub-exception is the root, at any depth of nesting.
func pythonRootCause(lines []string) []StackFrame {
	var out []StackFrame
	source := false // the line is the source text of the frame before it
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if f, ok := pythonFrame(line); ok {
			out = append(out, f)
			source = true
			continue
		}
		if m := pythonGroupRuleRe.FindStringSubmatch(line); m != nil && m[1] == "1" {
			out = nil
		} else if len(out) > 0 && (m != nil || !source && strings.Contains(line, pythonTraceback)) {
			break
		}
		source = false
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func pythonInApp(f StackFrame) bool {
	return !strings.HasPrefix(f.Module, "<") && !strings.HasPrefix(f.Module, pythonStdlibDir) &&
		!hasAnyPrefix(f.Module, pythonPackageDirs)
}
