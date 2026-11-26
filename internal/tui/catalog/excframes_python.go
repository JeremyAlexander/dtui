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
	pythonVerRe   = regexp.MustCompile(`python\d+(?:\.\d+)*`)
	// The rule above a sub-exception of a group, numbered, and the
	// unnumbered one that closes the group.
	pythonGroupRuleRe = regexp.MustCompile(`^\+[-+]{3,}(?: (\d+) -+)?$`)
)

// pythonPackageDirs are where installed packages live; pythonRuntimeDirs
// hold the standard library once the interpreter version is dropped.
var (
	pythonPackageDirs = []string{"site-packages/", "dist-packages/"}
	pythonRuntimeDirs = []string{"lib/python/", "lib64/python/"}
)

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

// pythonModule addresses an installed package from its site-packages
// directory and drops the interpreter version from a standard-library path.
func pythonModule(path string) string {
	for _, dir := range pythonPackageDirs {
		if i := strings.LastIndex(path, dir); i >= 0 {
			return path[i:]
		}
	}
	return pythonVerRe.ReplaceAllString(path, "python")
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
	m := f.Module
	if strings.HasPrefix(m, "<") || hasAnyPrefix(m, pythonPackageDirs) {
		return false
	}
	for _, dir := range pythonRuntimeDirs {
		if strings.Contains(m, dir) {
			return false
		}
	}
	return true
}
