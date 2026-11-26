package catalog

import (
	"regexp"
	"strings"
)

// Python tracebacks: `File "/path/file.py", line 1, in fn` frames, each
// followed by its source line, the throw site last.

var (
	pythonFrameRe = regexp.MustCompile(`^File "([^"]+)", line \d+(?:, in (.+))?$`)
	pythonVerRe   = regexp.MustCompile(`python\d+(?:\.\d+)*`)
)

// pythonPackageDirs are where installed packages live; pythonRuntimeDirs
// hold the standard library once the interpreter version is dropped.
var (
	pythonPackageDirs = []string{"site-packages/", "dist-packages/"}
	pythonRuntimeDirs = []string{"lib/python/", "lib64/python/"}
)

const pythonTraceback = "Traceback (most recent call last)"

func pythonFrame(line string) (StackFrame, bool) {
	m := pythonFrameRe.FindStringSubmatch(line)
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
// first exception raised, reversed so that the throw site comes first.
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
		if !source && len(out) > 0 && strings.HasPrefix(line, pythonTraceback) {
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
