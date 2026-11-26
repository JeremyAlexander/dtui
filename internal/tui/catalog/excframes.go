package catalog

import (
	"regexp"
	"strings"
)

// Stack-frame parsing for exception.stack_trace text. A trace is read in the
// notation of the runtime that wrote it (Java, .NET, JavaScript, Python, Go)
// and each frame is reduced to the module and function it names. Line
// numbers, addresses, argument values and generated names are dropped,
// because they change between builds and processes while the bug stays the
// same. Text in no known notation yields no frames.

// StackSyntax is the notation a frame was written in.
type StackSyntax int

const (
	// StackJava is "at pkg.Class.method(File.java:1)", with or without "at".
	StackJava StackSyntax = iota
	// StackDotNet is "at Ns.Type.Method(String arg) in File.cs:line 1".
	StackDotNet
	// StackJS is "at fn (/path/file.js:1:2)" or "fn@https://host/file.js:1:2".
	StackJS
	// StackPython is `File "/path/file.py", line 1, in fn`.
	StackPython
	// StackGo is "pkg/path.(*Type).Method(0x…)" from a goroutine dump.
	StackGo
)

// StackFrame is one normalised frame: the class, package or file that holds
// the code, and the function in it.
type StackFrame struct {
	Module   string
	Function string
	Syntax   StackSyntax
	// Dependency is set where the trace itself shows that the frame is
	// third-party code and the module cannot: a Go package under a vendor
	// directory or with its file in the module cache.
	Dependency bool
}

// FrameAuto stands in for a generated class name (proxies, reflection
// accessors) whose real name differs in every process.
const FrameAuto = "<auto>"

// FrameAnonymous is the function of a frame that names none.
const FrameAnonymous = "<anonymous>"

// stackNotation is how one runtime writes a trace. Everything specific to a
// runtime sits behind this type in the runtime's own file
// (excframes_<runtime>.go), so supporting another one takes that file, a
// StackSyntax constant and an entry in stackNotations.
type stackNotation struct {
	// frame reads one line, already trimmed, as a frame.
	frame func(line string) (StackFrame, bool)
	// rootCause reads a trace written in this notation and returns the
	// frames of its root cause, throw site first, and that exception's type
	// where the trace is a chain and names it; a trace of one exception
	// does not have to say what it is, so its type is "".
	rootCause func(lines []string) (frames []StackFrame, typ string)
	// inApp reports whether a frame is application code rather than the
	// runtime's or a framework's.
	inApp func(f StackFrame) bool
	// passThrough are the functions that only hand a call on: a link of a
	// filter or middleware chain, a reflective call, async plumbing. Such a
	// frame is on the stack of every request, whatever fails below it.
	passThrough []string
}

// stackNotations is indexed by StackSyntax. A trace is read in the first
// notation, in this order, that recognises one of its lines, so the order
// runs from the strictest notation to the loosest.
var stackNotations = [...]stackNotation{
	StackJava:   {frame: javaFrame, rootCause: javaRootCause, inApp: javaInApp, passThrough: javaPassThrough},
	StackDotNet: {frame: dotnetFrame, rootCause: dotnetRootCause, inApp: dotnetInApp, passThrough: dotnetPassThrough},
	StackJS:     {frame: jsFrame, rootCause: jsRootCause, inApp: jsInApp, passThrough: jsPassThrough},
	StackPython: {frame: pythonFrame, rootCause: pythonRootCause, inApp: pythonInApp, passThrough: pythonPassThrough},
	StackGo:     {frame: goFrame, rootCause: goRootCause, inApp: goInApp, passThrough: goPassThrough},
}

// ParseStackFrames extracts the normalised frames of a stack trace, throw
// site first, with adjacent repeats (recursion) collapsed. Where the text
// holds a chain of exceptions the frames are those of the root cause: the
// last "Caused by:" section of a Java trace (completed from the enclosing
// one where it ends in "... N more"), the innermost .NET exception, the
// first Python traceback, the first goroutine. Unparseable text yields nil.
func ParseStackFrames(stack string) []StackFrame {
	frames, _ := parseStack(stack)
	return frames
}

// parseStack is ParseStackFrames plus the type of the root cause, where the
// trace is a chain of exceptions and names it.
func parseStack(stack string) (frames []StackFrame, typ string) {
	lines := stackLines(stack)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		for i := range stackNotations {
			if _, ok := stackNotations[i].frame(line); ok {
				frames, typ = stackNotations[i].rootCause(lines)
				return collapseFrames(frames), typ
			}
		}
	}
	return nil, ""
}

var (
	stackLineBreaks = strings.NewReplacer("\r\n", "\n", "\r", "\n")
	// stackEscapedBreaks are line breaks and tabs that arrived escaped.
	stackEscapedBreaks = strings.NewReplacer(`\r\n`, "\n", `\n`, "\n", `\r`, "\n", `\t`, "\t")
)

// stackLines splits a trace into lines at "\n", "\r\n" or "\r". A trace
// without any line break whose breaks arrived escaped (a backslash and an
// "n") is split at those instead. Only an escaped break followed by
// indentation shows that this happened: a backslash and an "n" alone are
// also part of "C:\app\node_modules".
func stackLines(stack string) []string {
	if !strings.ContainsAny(stack, "\r\n") && (strings.Contains(stack, `\n `) || strings.Contains(stack, `\n\t`)) {
		stack = stackEscapedBreaks.Replace(stack)
	}
	return strings.Split(stackLineBreaks.Replace(stack), "\n")
}

// framesOf reads every line that is a frame.
func framesOf(lines []string, frame func(string) (StackFrame, bool)) []StackFrame {
	var out []StackFrame
	for _, line := range lines {
		if f, ok := frame(strings.TrimSpace(line)); ok {
			out = append(out, f)
		}
	}
	return out
}

var (
	// A URL's scheme and host, or a Windows drive.
	sourceOriginRe = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9+.-]+://[^/]*|[A-Za-z]:)`)
	// A directory named by a release, not by the code: a timestamp or build
	// number, a version, a commit hash.
	sourceReleaseRe = regexp.MustCompile(`^(?:\d{6,}|v?\d+(?:\.\d+)+(?:[-+][\w.]+)?|[0-9a-f]{7,40})$`)
)

// sourceCurrentDir is the link a release layout keeps to the live release.
const sourceCurrentDir = "current"

// deployRoots are the directories an application is commonly deployed into,
// each before any shorter root it starts with. deployHomes hold one
// directory per user above the application.
var (
	deployRoots = []string{
		"/usr/src/app/", "/usr/app/", "/home/site/wwwroot/", "/inetpub/wwwroot/", "/var/www/html/", "/var/www/",
		"/var/task/", "/var/app/", "/opt/app/", "/opt/", "/app/", "/srv/", "/workspace/", "/function/", "/code/",
	}
	deployHomes = []string{"/home/", "/Users/"}
)

// sourceModule reduces the path of an application's source file (with "/"
// as its separator) to a module that is the same wherever the application is
// deployed, so that a release does not start a new issue for an old bug:
//
//  1. A URL loses its scheme, host and query, a Windows path its drive, and
//     "./" steps go.
//  2. If a directory of the path is named by a release — six or more digits
//     (a timestamp, a build number), a version ("1.4.2", "v2.14.0-rc.1"), a
//     hex hash of seven or more characters, or "current" — everything up to
//     and including the last such directory goes.
//  3. Otherwise the deploy root the path starts with goes: one of
//     deployRoots, or a user's directory under one of deployHomes.
//
// What is left is the path below the deployment: "shop/cart.py" for
// "/app/shop/cart.py" and for "/srv/releases/20261126101112/shop/cart.py".
// A path under a root that is not listed stays whole, which is stable as
// long as the application keeps being deployed there.
func sourceModule(path string) string {
	path = sourceOriginRe.ReplaceAllString(path, "")
	path, _, _ = strings.Cut(path, "?")
	path = strings.TrimPrefix(strings.ReplaceAll(path, "/./", "/"), "./")

	for end := strings.LastIndexByte(path, '/'); end > 0; {
		start := strings.LastIndexByte(path[:end], '/') + 1
		if dir := path[start:end]; dir == sourceCurrentDir || sourceReleaseRe.MatchString(dir) && hasDigit(dir) {
			return path[end+1:]
		}
		end = start - 1
	}
	for _, root := range deployRoots {
		if rest, ok := strings.CutPrefix(path, root); ok {
			return rest
		}
	}
	for _, home := range deployHomes {
		if rest, ok := strings.CutPrefix(path, home); ok {
			if _, below, ok := strings.Cut(rest, "/"); ok {
				return below
			}
		}
	}
	return path
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

// FrameClassifier decides which frames are application code. The fingerprint
// prefers those: the frames of a runtime or framework are shared by
// unrelated bugs.
//
// The built-in rules (nil wherever a FrameClassifier is taken) are part of
// the key algorithm and of ExceptionKeyVersion. Any other classifier moves
// exceptions between keys without the version knowing, so it names itself:
// ID is hashed into every key computed with it, which keeps its keys apart
// from the built-in ones and from those of a classifier with another ID. It
// must be the same in every process that applies the same rules, and change
// when the rules do.
type FrameClassifier interface {
	InApp(f StackFrame) bool
	ID() string
}

// builtinInApp treats the known runtime and framework modules of each
// notation as not in-app, and everything else as the application's.
func builtinInApp(f StackFrame) bool {
	if f.Syntax < 0 || int(f.Syntax) >= len(stackNotations) {
		return false
	}
	return stackNotations[f.Syntax].inApp(f)
}

// isPassThrough reports whether a frame only hands the call on. The function
// is compared without the type it is a method of ("Auth.ServeHTTP"), unless
// the notation lists it with one ("Generator.next").
func isPassThrough(f StackFrame) bool {
	if f.Syntax < 0 || int(f.Syntax) >= len(stackNotations) {
		return false
	}
	method := f.Function[strings.LastIndexByte(f.Function, '.')+1:]
	for _, name := range stackNotations[f.Syntax].passThrough {
		if name == f.Function || name == method {
			return true
		}
	}
	return false
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
