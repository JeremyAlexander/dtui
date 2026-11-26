package catalog

import (
	"regexp"
	"strconv"
	"strings"
)

// Stack-frame parsing for exception.stack_trace text. A trace is read line by
// line; each line is matched against the frame notations of the runtimes
// that write them (Java, .NET, Go, Node, Python) and reduced to the module
// and function it names. Line numbers, addresses, argument values and
// generated names are dropped, because they change between builds and
// processes while the bug stays the same. Text in no known notation yields
// no frames.

// StackSyntax is the notation a frame was written in.
type StackSyntax int

const (
	// StackDotted is "at pkg.Class.method(…)", shared by Java and .NET.
	StackDotted StackSyntax = iota
	// StackGo is "pkg/path.(*Type).Method(0x…)" from a goroutine dump.
	StackGo
	// StackNode is "at fn (/path/file.js:1:2)".
	StackNode
	// StackPython is `File "/path/file.py", line 1, in fn`.
	StackPython
)

// StackFrame is one normalised frame: the class, package or file that holds
// the code, and the function in it.
type StackFrame struct {
	Module   string
	Function string
	Syntax   StackSyntax
}

// FrameAuto stands in for a generated class name (proxies, reflection
// accessors) whose real name differs in every process.
const FrameAuto = "<auto>"

// FrameAnonymous is the function of a frame that names none.
const FrameAnonymous = "<anonymous>"

var (
	stackMoreRe      = regexp.MustCompile(`^\.\.\. (\d+) (?:more|common frames omitted)`)
	stackGoroutineRe = regexp.MustCompile(`^goroutine \d+ \[`)
	stackPythonRe    = regexp.MustCompile(`^File "([^"]+)", line \d+(?:, in (.+))?$`)
	// A frame without "at": OneAgent call stacks and some log formats. The
	// source location in the parentheses is what tells it from prose.
	stackBareDottedRe = regexp.MustCompile(
		`^([\w$.<>/-]+\.[\w$<>]+) ?\((?:[\w$-]+\.\w+(?::\d+)?|Native Method|Unknown Source|<generated>)\)`)
	stackGoNameRe = regexp.MustCompile(`^[\w./~%·*()\[\],-]+$`)
	// Go prints arguments as hex words, "..." when elided.
	stackGoArgsRe = regexp.MustCompile(`^[0-9a-fx?,{}\s.]*$`)

	frameTypeArgsRe = regexp.MustCompile(`\[[^\]]*\]`)
	// .NET compiler-generated names: async state machines, closure classes
	// and the lambdas in them.
	frameNetAsyncRe   = regexp.MustCompile(`<(\w+)>d__\d+\.MoveNext$`)
	frameNetClosureRe = regexp.MustCompile(`<>c(?:__DisplayClass\d+_\d+)?\.`)
	frameNetLambdaRe  = regexp.MustCompile(`<(\w+)>b__\d+(?:_\d+)?`)
	// Java generated names: CGLIB subclasses ("$$EnhancerBy…$$1a2b",
	// "$$FastClassBy…$$3c4d"), reflection accessors, dynamic proxies, and
	// the numbered package the JDK puts proxies in.
	frameJavaCGLIBRe     = regexp.MustCompile(`\$\$.*$`)
	frameJavaGeneratedRe = regexp.MustCompile(`(?:Generated(?:Method|Constructor)Accessor|\$Proxy)\d+`)
	frameJavaProxyPkgRe  = regexp.MustCompile(`^jdk\.proxy\d+\.`)
	frameJavaLambdaRe    = regexp.MustCompile(`^lambda\$(.+)\$\d+$`)

	frameNodeAliasRe = regexp.MustCompile(` \[as [^\]]*\]$`)
	frameNodeIndexRe = regexp.MustCompile(`^index \d+$`)
	frameLinesRe     = regexp.MustCompile(`(?::\d+)+`)
	framePythonVerRe = regexp.MustCompile(`python\d+(?:\.\d+)*`)
)

// stackSection is one exception of a trace: its frames, and how many frames
// of the enclosing exception a "... N more" line stands for.
type stackSection struct {
	frames   []StackFrame
	more     int
	reversed bool // Python lists the throw site last
}

// stackParser reads a trace line by line.
type stackParser struct {
	sections   []stackSection
	suppressed bool // inside a Java "Suppressed:" block
	skipSource bool // the next line is the source text of a Python frame
	goroutines int
	done       bool // the frames of the root cause are complete
}

// ParseStackFrames extracts the normalised frames of a stack trace, throw
// site first, with adjacent repeats (recursion) collapsed. Where the text
// holds a chain of exceptions the frames are those of the root cause: the
// last "Caused by:" section of a Java trace (completed from the enclosing
// one where it ends in "... N more"), the innermost .NET exception, the
// first Python traceback, the first goroutine. Unparseable text yields nil.
func ParseStackFrames(stack string) []StackFrame {
	p := stackParser{sections: make([]stackSection, 1)}
	for _, line := range strings.Split(stack, "\n") {
		if p.done {
			break
		}
		p.line(strings.TrimSpace(line))
	}
	return collapseFrames(p.rootFrames())
}

func (p *stackParser) line(line string) {
	if p.skipSource {
		p.skipSource = false
		if !stackPythonRe.MatchString(line) {
			return
		}
	}
	cur := &p.sections[len(p.sections)-1]
	switch {
	case line == "":
	case strings.HasPrefix(line, "Caused by:"):
		p.suppressed = false
		p.sections = append(p.sections, stackSection{})
	case strings.HasPrefix(line, "Suppressed:"):
		p.suppressed = true
	case strings.HasPrefix(line, "--- End of inner exception stack trace"),
		strings.HasPrefix(line, "Traceback (most recent call last)"):
		// .NET prints the innermost exception first and Python the first
		// one raised, so the frames read so far are the root cause's.
		p.done = len(cur.frames) > 0
	case stackGoroutineRe.MatchString(line):
		p.goroutines++
		p.done = p.goroutines > 1
	case p.suppressed:
	default:
		if m := stackMoreRe.FindStringSubmatch(line); m != nil {
			cur.more, _ = strconv.Atoi(m[1])
			return
		}
		if f, ok := p.frame(line); ok {
			cur.frames = append(cur.frames, f)
			cur.reversed = cur.reversed || f.Syntax == StackPython
		}
	}
}

// frame matches one line against the frame notations.
func (p *stackParser) frame(line string) (StackFrame, bool) {
	if m := stackPythonRe.FindStringSubmatch(line); m != nil {
		p.skipSource = true
		return StackFrame{Module: pythonModule(m[1]), Function: firstNonEmpty(m[2], "<module>"), Syntax: StackPython}, true
	}
	if rest, ok := strings.CutPrefix(line, "at "); ok {
		return atFrame(strings.TrimSpace(rest))
	}
	if m := stackBareDottedRe.FindStringSubmatch(line); m != nil {
		return dottedFrame(m[1])
	}
	return goFrame(line)
}

// rootFrames assembles the root cause's frames from the sections read.
func (p *stackParser) rootFrames() []StackFrame {
	var full []StackFrame
	for _, sec := range p.sections {
		frames := sec.frames
		if sec.reversed {
			frames = make([]StackFrame, len(sec.frames))
			for i, f := range sec.frames {
				frames[len(frames)-1-i] = f
			}
		}
		// "... N more": the last N frames of the enclosing exception.
		shared := min(sec.more, len(full))
		full = append(frames[:len(frames):len(frames)], full[len(full)-shared:]...)
	}
	return full
}

// collapseFrames drops a frame equal to the one before it, so a recursion
// counts once however deep it went.
func collapseFrames(frames []StackFrame) []StackFrame {
	var out []StackFrame
	for _, f := range frames {
		if len(out) == 0 || out[len(out)-1] != f {
			out = append(out, f)
		}
	}
	return out
}

// atFrame reads the text after "at ": a Node frame when the function and its
// location are separated by a space or the location stands alone, else a
// Java or .NET frame.
func atFrame(rest string) (StackFrame, bool) {
	// Node marks a frame resumed after an await; it is the same frame.
	rest = strings.TrimSpace(strings.TrimPrefix(rest+" ", "async "))
	open := strings.IndexByte(rest, '(')
	switch {
	case open < 0:
		if rest == "" || strings.ContainsAny(rest, " \t") {
			return StackFrame{}, false
		}
		return StackFrame{Module: nodeModule(rest), Function: FrameAnonymous, Syntax: StackNode}, true
	case open > 0 && rest[open-1] == ' ':
		loc := rest[open+1:]
		if end := strings.LastIndexByte(loc, ')'); end >= 0 {
			loc = loc[:end]
		}
		return StackFrame{Module: nodeModule(loc), Function: nodeFunction(rest[:open-1]), Syntax: StackNode}, true
	}
	return dottedFrame(rest[:open])
}

// nodeFunction drops a "[as alias]" suffix; "Object.<anonymous>" is
// module-level code and reads as plain anonymous.
func nodeFunction(name string) string {
	name = frameNodeAliasRe.ReplaceAllString(strings.TrimSpace(name), "")
	if name == "" || name == "Object."+FrameAnonymous {
		return FrameAnonymous
	}
	return name
}

// nodeModule reduces a location to its file: no line or column, no file://
// scheme, and a dependency addressed from its node_modules directory
// wherever that is installed.
func nodeModule(loc string) string {
	loc = strings.TrimPrefix(frameLinesRe.ReplaceAllString(loc, ""), "file://")
	if frameNodeIndexRe.MatchString(loc) { // "Promise.all (index 3)"
		return "index"
	}
	return loc[max(strings.LastIndex(loc, "node_modules/"), 0):]
}

// pythonModule addresses an installed package from its site-packages
// directory and drops the interpreter version from a standard-library path.
func pythonModule(path string) string {
	for _, dir := range []string{"site-packages/", "dist-packages/"} {
		if i := strings.LastIndex(path, dir); i >= 0 {
			return path[i:]
		}
	}
	return framePythonVerRe.ReplaceAllString(path, "python")
}

// dottedFrame normalises a Java or .NET qualified method name. The two
// notations share the "Type.method" shape and their generated names do not
// overlap, so one pass applies both sets of rules.
func dottedFrame(name string) (StackFrame, bool) {
	// A Java lambda's synthetic class ("Cart$$Lambda$14/0x0800.accept") is
	// plumbing between two real frames.
	if strings.Contains(name, "$$Lambda") {
		return StackFrame{}, false
	}
	// Java module and class-loader prefixes: "java.base/", "app//".
	name = name[strings.LastIndexByte(name, '/')+1:]
	name = frameTypeArgsRe.ReplaceAllString(name, "")
	name = frameNetAsyncRe.ReplaceAllString(name, "$1")
	name = frameNetClosureRe.ReplaceAllString(name, "")
	name = frameNetLambdaRe.ReplaceAllString(name, "$1")

	module, function := "", name
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		module, function = name[:dot], name[dot+1:]
	}
	// .NET constructors are "Type..ctor".
	if trimmed, ok := strings.CutSuffix(module, "."); ok {
		module, function = trimmed, "."+function
	}
	if function == "" {
		return StackFrame{}, false
	}
	function = frameJavaLambdaRe.ReplaceAllString(function, "$1")
	module = frameJavaCGLIBRe.ReplaceAllString(module, FrameAuto)
	module = frameJavaGeneratedRe.ReplaceAllString(module, FrameAuto)
	module = frameJavaProxyPkgRe.ReplaceAllString(module, "jdk.proxy.")
	return StackFrame{Module: module, Function: function, Syntax: StackDotted}, true
}

// goFrame reads a goroutine-dump frame ("pkg/path.(*Type).Method(0x1, 0x2)")
// or a "created by" line. The file line that follows each frame matches
// nothing and is skipped by the caller.
func goFrame(line string) (StackFrame, bool) {
	name, created := strings.CutPrefix(line, "created by ")
	if created {
		name, _, _ = strings.Cut(name, " in goroutine ")
	} else {
		open := strings.LastIndexByte(line, '(')
		if open == -1 || !strings.HasSuffix(line, ")") || !stackGoArgsRe.MatchString(line[open+1:len(line)-1]) {
			return StackFrame{}, false
		}
		name = line[:open]
	}
	if !stackGoNameRe.MatchString(name) {
		return StackFrame{}, false
	}
	name = frameTypeArgsRe.ReplaceAllString(name, "")
	// The package path ends at the first dot after its last slash.
	slash := strings.LastIndexByte(name, '/') + 1
	module, function := "", name
	if dot := strings.IndexByte(name[slash:], '.'); dot >= 0 {
		module, function = name[:slash+dot], name[slash+dot+1:]
	}
	function = strings.NewReplacer("(*", "", "(", "", ")", "").Replace(function)
	if function == "" {
		return StackFrame{}, false
	}
	return StackFrame{Module: module, Function: function, Syntax: StackGo}, true
}

// FrameClassifier decides which frames are application code. The fingerprint
// prefers those: the frames of a runtime or framework are shared by
// unrelated bugs.
type FrameClassifier interface {
	InApp(f StackFrame) bool
}

// DefaultFrameClassifier treats the known runtime and framework modules of
// each notation as not in-app, and everything else as the application's.
var DefaultFrameClassifier FrameClassifier = builtinFrameClassifier{}

type builtinFrameClassifier struct{}

// dottedVendorPrefixes are the Java and .NET runtime and framework
// namespaces. Matching is case-sensitive: "System." is .NET, "system." is
// somebody's package.
var dottedVendorPrefixes = []string{
	"java.", "javax.", "jakarta.", "jdk.", "sun.", "com.sun.", "kotlin.", "kotlinx.", "scala.", "groovy.",
	"org.springframework.", "org.apache.", "org.hibernate.", "org.jboss.", "org.eclipse.jetty.", "org.slf4j.",
	"io.netty.", "io.grpc.", "io.undertow.", "io.vertx.", "io.quarkus.", "io.micrometer.", "io.opentelemetry.",
	"reactor.", "akka.", "okhttp3.", "ch.qos.logback.", "com.fasterxml.", "com.google.common.", "com.zaxxer.",
	"com.dynatrace.",
	"System.", "Microsoft.", "Newtonsoft.", "Grpc.", "OpenTelemetry.", "Dynatrace.",
}

// goVendorPrefixes are widely used Go module hosts that are never the
// application itself.
var goVendorPrefixes = []string{
	"golang.org/", "google.golang.org/", "go.opentelemetry.io/", "go.uber.org/", "gopkg.in/",
}

func (builtinFrameClassifier) InApp(f StackFrame) bool {
	m := f.Module
	switch f.Syntax {
	case StackGo:
		// The standard library has no host in its import path.
		host, _, _ := strings.Cut(m, "/")
		return m == "main" || strings.Contains(host, ".") && !hasAnyPrefix(m, goVendorPrefixes)
	case StackNode:
		return strings.ContainsAny(m, `/\`) && !strings.Contains(m, "node_modules/") &&
			!hasAnyPrefix(m, []string{"node:", "internal/"})
	case StackPython:
		return !strings.HasPrefix(m, "<") && !strings.Contains(m, "lib/python/") &&
			!strings.Contains(m, "lib64/python/") && !hasAnyPrefix(m, []string{"site-packages/", "dist-packages/"})
	default:
		return m != "" && !strings.Contains(m, FrameAuto) && !hasAnyPrefix(m, dottedVendorPrefixes)
	}
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
