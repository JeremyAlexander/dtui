package catalog

import (
	"regexp"
	"strings"
)

// .NET traces: "at Ns.Type.Method(String arg) in File.cs:line 1" frames. An
// exception that wraps another prints the inner one first, closed by an
// "End of inner exception stack trace" line. Mono writes a space before the
// parameters: "at Ns.Type.Method (System.String arg) [0x0001a] in <…>:0".

var (
	// The method name, the space Mono puts after it, the parameters, and
	// what follows them.
	dotnetFrameRe = regexp.MustCompile("^at ([\\p{L}\\p{N}_.<>|$`\\[\\],+]+)( ?)\\(([^()]*)\\)(.*)$")
	// Mono's notation is close to prose ("at checkout (final step)") and to
	// V8's "at fn (location)". So there the name must be qualified, and
	// either the offset Mono prints after the parameters follows
	// (dotnetMonoOffset) or every parameter starts with a type name.
	dotnetMonoParamsRe = regexp.MustCompile("^(?:[A-Z][\\w.`\\[\\]<>&*+]*(?: \\w+)?(?:, |$))*$")
	dotnetTypeArgsRe   = regexp.MustCompile(`\[[^\]]*\]`)
	// An inner exception's type, after the arrow that introduces it.
	dotnetInnerRe = regexp.MustCompile("---> (?:\\(Inner Exception #\\d+\\) )?([\\w.+`]+)")
	// Compiler-generated names: the method of top-level statements
	// ("<Main>$"), local functions ("<Run>g__Validate|5_0"), async state
	// machines, closure classes and the lambdas in them.
	dotnetMainRe    = regexp.MustCompile(`<(\w+)>\$`)
	dotnetLocalRe   = regexp.MustCompile(`<\w+>g__(\w+)\|\d+(?:_\d+)?`)
	dotnetAsyncRe   = regexp.MustCompile(`<(\w+)>d(?:__\d+)?\.MoveNext$`)
	dotnetClosureRe = regexp.MustCompile(`<>c(?:__DisplayClass\d+_\d+)?\.`)
	dotnetLambdaRe  = regexp.MustCompile(`<(\w+)>b__\d+(?:_\d+)?`)
	// A delegate compiled from an expression tree is numbered per process.
	dotnetDelegateRe = regexp.MustCompile(`^lambda_method\d+$`)
)

// dotnetVendorPrefixes are the runtime and framework namespaces. Matching is
// case-sensitive: "System." is .NET, "system." is somebody's namespace.
var dotnetVendorPrefixes = []string{
	"System.", "Microsoft.", "Newtonsoft.", "Grpc.", "OpenTelemetry.", "Dynatrace.",
}

// dotnetPassThrough: middleware, by the convention ASP.NET finds it by, and
// delegate calls.
var dotnetPassThrough = []string{"Invoke", "InvokeAsync"}

const dotnetInnerEnd = "--- End of inner exception stack trace"

// dotnetMonoOffset starts what Mono prints after a frame's parameters: the
// IL offset, "[0x0001a] in <module>:0".
const dotnetMonoOffset = " [0x"

// dotnetFrameParts reads a frame line as its method name and parameters.
func dotnetFrameParts(line string) (name, params string, ok bool) {
	if !strings.HasPrefix(line, "at ") {
		return "", "", false
	}
	m := dotnetFrameRe.FindStringSubmatch(line)
	if m == nil {
		return "", "", false
	}
	if m[2] != "" && (!strings.Contains(m[1], ".") ||
		!strings.HasPrefix(m[4], dotnetMonoOffset) && !dotnetMonoParamsRe.MatchString(m[3])) {
		return "", "", false
	}
	return m[1], m[3], true
}

// dotnetOwns: a frame that is not Java's. Every Java frame with "at" in
// front reads as a .NET frame whose parameters are its source location.
func dotnetOwns(line string) bool {
	_, _, ok := dotnetFrameParts(line)
	return ok && !javaOwns(line)
}

func dotnetFrame(line string) (StackFrame, bool) {
	name, _, ok := dotnetFrameParts(line)
	if !ok {
		return StackFrame{}, false
	}
	name = dotnetTypeArgsRe.ReplaceAllString(name, "")
	name = dotnetMainRe.ReplaceAllString(name, "$1")
	name = dotnetLocalRe.ReplaceAllString(name, "$1")
	name = dotnetAsyncRe.ReplaceAllString(name, "$1")
	name = dotnetClosureRe.ReplaceAllString(name, "")
	name = dotnetLambdaRe.ReplaceAllString(name, "$1")

	module, function := "", name
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		module, function = name[:dot], name[dot+1:]
	}
	// Constructors are "Type..ctor".
	if trimmed, ok := strings.CutSuffix(module, "."); ok {
		module, function = trimmed, "."+function
	}
	if function == "" {
		return StackFrame{}, false
	}
	function = dotnetDelegateRe.ReplaceAllString(function, "lambda_method")
	return StackFrame{Module: module, Function: function, Syntax: StackDotNet}, true
}

// dotnetRootCause returns the frames of the innermost exception, which is
// printed first, and its type: the last one an arrow introduces ("--->")
// above the first frame.
func dotnetRootCause(lines []string) (frames []StackFrame, typ string) {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(frames) > 0 && strings.HasPrefix(line, dotnetInnerEnd) {
			break
		}
		if f, ok := dotnetFrame(line); ok {
			frames = append(frames, f)
		} else if inner := dotnetInnerRe.FindAllStringSubmatch(line, -1); inner != nil && len(frames) == 0 {
			typ = inner[len(inner)-1][1]
		}
	}
	return frames, typ
}

func dotnetInApp(f StackFrame) bool {
	return f.Module != "" && !hasAnyPrefix(f.Module, dotnetVendorPrefixes)
}
