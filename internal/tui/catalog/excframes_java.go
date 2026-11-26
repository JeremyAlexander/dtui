package catalog

import (
	"regexp"
	"strconv"
	"strings"
)

// Java traces: "at pkg.Class.method(File.java:1)" frames under a
// "Type: message" line, the causes of an exception in "Caused by:" sections
// after it.

var (
	// A frame with or without "at" (OneAgent call stacks and some log
	// formats leave it out). The source location in the parentheses is what
	// tells a frame from prose and from a .NET frame, whose parentheses
	// hold parameters.
	javaFrameRe = regexp.MustCompile(
		`^(?:at )?([\w$.<>/@-]+\.[\w$<>]+) ?\((?:[\w$-]+\.\w+(?::\d+)?|Native Method|Unknown Source|<generated>)\)`)
	javaMoreRe = regexp.MustCompile(`^\.\.\. (\d+) (?:more|common frames omitted)`)

	// Generated names: CGLIB subclasses ("$$EnhancerBy…$$1a2b",
	// "$$FastClassBy…$$3c4d"), reflection accessors, dynamic proxies, and
	// the numbered package the JDK puts proxies in.
	javaCGLIBRe     = regexp.MustCompile(`\$\$.*$`)
	javaGeneratedRe = regexp.MustCompile(`(?:Generated(?:Method|Constructor)Accessor|\$Proxy)\d+`)
	javaProxyPkgRe  = regexp.MustCompile(`^jdk\.proxy\d+\.`)
	javaLambdaRe    = regexp.MustCompile(`^lambda\$(.+)\$\d+$`)
)

// javaVendorPrefixes are the runtime and framework packages that are never
// the application's.
var javaVendorPrefixes = []string{
	"java.", "javax.", "jakarta.", "jdk.", "sun.", "com.sun.", "kotlin.", "kotlinx.", "scala.", "groovy.",
	"org.springframework.", "org.apache.", "org.hibernate.", "org.jboss.", "org.eclipse.jetty.", "org.slf4j.",
	"io.netty.", "io.grpc.", "io.undertow.", "io.vertx.", "io.quarkus.", "io.micrometer.", "io.opentelemetry.",
	"reactor.", "akka.", "okhttp3.", "ch.qos.logback.", "com.fasterxml.", "com.google.common.", "com.zaxxer.",
	"com.dynatrace.",
}

func javaFrame(line string) (StackFrame, bool) {
	m := javaFrameRe.FindStringSubmatch(line)
	if m == nil {
		return StackFrame{}, false
	}
	name := m[1]
	// A lambda's synthetic class ("Cart$$Lambda$14/0x0800.accept") is
	// plumbing between two real frames.
	if strings.Contains(name, "$$Lambda") {
		return StackFrame{}, false
	}
	// Module and class-loader prefixes: "java.base/", "app//".
	name = name[strings.LastIndexByte(name, '/')+1:]
	dot := strings.LastIndexByte(name, '.')
	module, function := name[:dot], name[dot+1:]
	function = javaLambdaRe.ReplaceAllString(function, "$1")
	module = javaCGLIBRe.ReplaceAllString(module, FrameAuto)
	module = javaGeneratedRe.ReplaceAllString(module, FrameAuto)
	module = javaProxyPkgRe.ReplaceAllString(module, "jdk.proxy.")
	return StackFrame{Module: module, Function: function, Syntax: StackJava}, true
}

// javaSection is one exception of a trace: its frames, and how many frames
// of the enclosing exception a "... N more" line stands for.
type javaSection struct {
	frames []StackFrame
	more   int
}

// javaRootCause returns the frames of the last "Caused by:" section,
// completed from the enclosing one where it ends in "... N more".
// "Suppressed:" blocks are left out.
func javaRootCause(lines []string) []StackFrame {
	sections := make([]javaSection, 1)
	suppressed := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		cur := &sections[len(sections)-1]
		switch {
		case strings.HasPrefix(line, "Caused by:"):
			suppressed = false
			sections = append(sections, javaSection{})
		case strings.HasPrefix(line, "Suppressed:"):
			suppressed = true
		case suppressed:
		default:
			if m := javaMoreRe.FindStringSubmatch(line); m != nil {
				cur.more, _ = strconv.Atoi(m[1])
			} else if f, ok := javaFrame(line); ok {
				cur.frames = append(cur.frames, f)
			}
		}
	}
	var full []StackFrame
	for _, sec := range sections {
		// "... N more": the last N frames of the enclosing exception.
		shared := min(sec.more, len(full))
		full = append(sec.frames[:len(sec.frames):len(sec.frames)], full[len(full)-shared:]...)
	}
	return full
}

func javaInApp(f StackFrame) bool {
	return !strings.Contains(f.Module, FrameAuto) && !hasAnyPrefix(f.Module, javaVendorPrefixes)
}
