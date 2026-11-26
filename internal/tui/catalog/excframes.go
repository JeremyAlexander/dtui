package catalog

import "strings"

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
	// StackJS is V8's "at fn (/path/file.js:1:2)".
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
	// frames of its root cause, throw site first.
	rootCause func(lines []string) []StackFrame
	// inApp reports whether a frame is application code rather than the
	// runtime's or a framework's.
	inApp func(f StackFrame) bool
}

// stackNotations is indexed by StackSyntax. A trace is read in the first
// notation, in this order, that recognises one of its lines, so the order
// runs from the strictest notation to the loosest.
var stackNotations = [...]stackNotation{
	StackJava:   {frame: javaFrame, rootCause: javaRootCause, inApp: javaInApp},
	StackDotNet: {frame: dotnetFrame, rootCause: dotnetRootCause, inApp: dotnetInApp},
	StackJS:     {frame: jsFrame, rootCause: jsRootCause, inApp: jsInApp},
	StackPython: {frame: pythonFrame, rootCause: pythonRootCause, inApp: pythonInApp},
	StackGo:     {frame: goFrame, rootCause: goRootCause, inApp: goInApp},
}

// ParseStackFrames extracts the normalised frames of a stack trace, throw
// site first, with adjacent repeats (recursion) collapsed. Where the text
// holds a chain of exceptions the frames are those of the root cause: the
// last "Caused by:" section of a Java trace (completed from the enclosing
// one where it ends in "... N more"), the innermost .NET exception, the
// first Python traceback, the first goroutine. Unparseable text yields nil.
func ParseStackFrames(stack string) []StackFrame {
	lines := stackLines(stack)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		for i := range stackNotations {
			if _, ok := stackNotations[i].frame(line); ok {
				return collapseFrames(stackNotations[i].rootCause(lines))
			}
		}
	}
	return nil
}

var (
	stackLineBreaks = strings.NewReplacer("\r\n", "\n", "\r", "\n")
	// stackEscapedBreaks are line breaks and tabs that arrived escaped.
	stackEscapedBreaks = strings.NewReplacer(`\r\n`, "\n", `\n`, "\n", `\r`, "\n", `\t`, "\t")
)

// stackLines splits a trace into lines at "\n", "\r\n" or "\r". A trace
// without any line break whose breaks arrived escaped (a backslash and an
// "n") is split at those instead; where real breaks exist, a backslash is
// text ("C:\src\new").
func stackLines(stack string) []string {
	if !strings.ContainsAny(stack, "\r\n") {
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
type FrameClassifier interface {
	InApp(f StackFrame) bool
}

// DefaultFrameClassifier treats the known runtime and framework modules of
// each notation as not in-app, and everything else as the application's.
var DefaultFrameClassifier FrameClassifier = builtinFrameClassifier{}

type builtinFrameClassifier struct{}

func (builtinFrameClassifier) InApp(f StackFrame) bool {
	if f.Syntax < 0 || int(f.Syntax) >= len(stackNotations) {
		return false
	}
	return stackNotations[f.Syntax].inApp(f)
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
