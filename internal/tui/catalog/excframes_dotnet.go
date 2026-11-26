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
	// The method name, the space Mono puts after it, and the parameters.
	dotnetFrameRe = regexp.MustCompile("^at ([\\w.<>|$`\\[\\],+]+)( ?)\\(([^()]*)\\)")
	// Mono's notation is close to prose ("at checkout (final step)"), so
	// there the name must be qualified and every parameter must start with
	// a type name.
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

const dotnetInnerEnd = "--- End of inner exception stack trace"

func dotnetFrame(line string) (StackFrame, bool) {
	m := dotnetFrameRe.FindStringSubmatch(line)
	if m == nil {
		return StackFrame{}, false
	}
	name := m[1]
	if m[2] != "" && (!strings.Contains(name, ".") || !dotnetMonoParamsRe.MatchString(m[3])) {
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
