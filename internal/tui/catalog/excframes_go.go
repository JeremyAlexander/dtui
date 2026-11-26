package catalog

import (
	"regexp"
	"sort"
	"strings"
)

// Go goroutine dumps: "pkg/path.(*Type).Method(0x1, 0x2)" frames, each
// followed by its file line, under a "goroutine N [state]:" header.
//
// Which frames are the application's cannot be read from an import path:
// the dump does not name the main module, and a module path needs no host
// ("shop/internal/cart"). The rule is by elimination. A frame is in-app
// unless
//
//   - its package is the standard library's: the first element of the path
//     is one of goStdlib;
//   - the trace shows it is a dependency: the package sits under a vendor
//     directory, or the file line after the frame is in the module cache
//     (a "module@version" directory, kept by -trimpath) or under vendor/;
//   - its module host is one of goVendorPrefixes.
//
// This takes a dependency for the application where a trace has no file
// lines and the host is not listed, and an application package for the
// standard library where its path starts with one of the library's names.

var (
	// "goroutine 1 [running]:", and the header of a system dump, which
	// names the goroutine's pointers first ("goroutine 1 gp=0x… m=0 […").
	goGoroutineRe = regexp.MustCompile(`^goroutine \d+ (?:[\w=]+ )*\[`)
	goNameRe      = regexp.MustCompile(`^[\p{L}\p{N}_./~%·*()\[\],-]+$`)
	// Arguments are printed as hex words ("0x1f", "0xc000012345?"), in
	// braces where they are the fields of one value, "..." when elided and
	// "_" where the runtime cannot read one.
	goArgsRe     = regexp.MustCompile(`^(?:0x[0-9a-f]+\??|\.\.\.|[{},\s_])*$`)
	goTypeArgsRe = regexp.MustCompile(`\[[^\]]*\]`)
	// The file line under a frame, and what in its path shows a dependency.
	// An absolute path may hold spaces; a system dump prints the frame's
	// pointers after it.
	goFileRe = regexp.MustCompile(
		`^((?:/|[A-Za-z]:[\\/]).*\.go|\S+\.go):\d+(?: \+0x[0-9a-f]+)?(?: fp=0x[0-9a-f]+ sp=0x[0-9a-f]+ pc=0x[0-9a-f]+)?$`)
	goDependencyRe = regexp.MustCompile(`@v\d|/pkg/mod/|(?:^|/)vendor/`)
	// The runtime's entry points of a panic. "panic" itself is printed
	// without a package.
	goPanicRe = regexp.MustCompile(`^(?:gopanic|goPanic\w+|panic\w*|sigpanic\w*|fatalpanic|throw)$`)
	// A method's receiver is written "(*Type)" or "(Type)".
	goReceiver = strings.NewReplacer("(*", "", "(", "", ")", "")
)

// goStdlib are the top-level names of the standard library (go list std,
// Go 1.26), sorted.
var goStdlib = []string{
	"archive", "bufio", "bytes", "cmp", "compress", "container", "context", "crypto", "database", "debug", "embed",
	"encoding", "errors", "expvar", "flag", "fmt", "go", "hash", "html", "image", "index", "internal", "io", "iter",
	"log", "maps", "math", "mime", "net", "os", "path", "plugin", "reflect", "regexp", "runtime", "slices", "sort",
	"strconv", "strings", "structs", "sync", "syscall", "testing", "text", "time", "unicode", "unique", "unsafe",
	"vendor", "weak",
}

// goVendorPrefixes are widely used modules and module hosts that are never
// the application itself: the Go project's own, database drivers and the
// cloud SDKs. A trace with file lines shows a dependency without them.
//
// The table is part of the key algorithm: once a release has shipped keys
// it only changes together with ExceptionKeyVersion (see
// javaVendorPrefixes).
var goVendorPrefixes = []string{
	"golang.org/", "google.golang.org/", "go.opentelemetry.io/", "go.uber.org/", "gopkg.in/",
	// Database drivers.
	"github.com/lib/pq", "github.com/jackc/", "github.com/go-sql-driver/", "github.com/mattn/go-sqlite3",
	"go.mongodb.org/", "github.com/redis/", "github.com/go-redis/",
	// Cloud SDKs.
	"github.com/aws/", "github.com/Azure/", "cloud.google.com/", "github.com/googleapis/",
}

// goPassThrough: a handler that wraps the next handler.
var goPassThrough = []string{"ServeHTTP"}

const (
	// goGoroutine is what goGoroutineRe starts with, and goFileSuffix what
	// goFileRe holds: most lines of most traces have neither.
	goGoroutine  = "goroutine "
	goFileSuffix = ".go:"
)

// goVendorDir is the directory a module keeps its dependencies' packages in.
const goVendorDir = "vendor/"

// goFrame reads a frame or a "created by" line. The file line that follows
// each frame matches nothing. A call written in prose ("decode(abc)",
// "failed()") is no frame: a frame names a package, or prints its arguments
// the way the runtime does.
func goFrame(line string) (StackFrame, bool) {
	name, created := strings.CutPrefix(line, "created by ")
	args := ""
	if created {
		name, _, _ = strings.Cut(name, " in goroutine ")
	} else {
		open := strings.LastIndexByte(line, '(')
		if open == -1 || !strings.HasSuffix(line, ")") {
			return StackFrame{}, false
		}
		name, args = line[:open], line[open+1:len(line)-1]
	}
	if !goNameRe.MatchString(name) || !goArgsRe.MatchString(args) ||
		!strings.Contains(name, ".") && args == "" {
		return StackFrame{}, false
	}
	name = goTypeArgsRe.ReplaceAllString(name, "")
	// The package path ends at the first dot after its last slash.
	slash := strings.LastIndexByte(name, '/') + 1
	module, function := "", name
	if dot := strings.IndexByte(name[slash:], '.'); dot >= 0 {
		module, function = name[:slash+dot], name[slash+dot+1:]
	}
	function = goReceiver.Replace(function)
	if function == "" {
		return StackFrame{}, false
	}
	// "shop/vendor/golang.org/x/net/http2", and the standard library's own
	// "vendor/golang.org/x/net/http2/hpack": the package is the vendored one.
	vendored := false
	if i := strings.LastIndex("/"+module, "/"+goVendorDir); i >= 0 {
		module, vendored = module[i+len(goVendorDir):], true
	}
	return StackFrame{Module: module, Function: function, Syntax: StackGo, Dependency: vendored}, true
}

// goOwns: a goroutine header, the file line under a frame, a "created by"
// line, or a frame that prints arguments. A frame without any
// ("main.main()") reads the same as a call in a line of source text or of
// a message.
func goOwns(line string) bool {
	if strings.HasSuffix(line, "()") {
		return false
	}
	if _, ok := goFrame(line); ok {
		return true
	}
	return strings.HasPrefix(line, goGoroutine) && goGoroutineRe.MatchString(line) ||
		strings.Contains(line, goFileSuffix) && goFileRe.MatchString(line)
}

// goRootCause returns the frames of the first goroutine, the one that
// panicked, from the throw site down.
func goRootCause(lines []string) ([]StackFrame, string) {
	return goThrowSite(goFirstGoroutine(lines)), ""
}

// goPanicHeaders start the first line of a crash: a panic, or an error the
// runtime does not let a program recover from.
var goPanicHeaders = []string{"panic: ", "fatal error: "}

// goAtFault reports whether a trace is a panic's. A Go error carries no
// stack: a trace recorded next to one (debug.Stack in a handler that logs
// it) says where the error was seen, and every error that passes there has
// the same. A panic's trace starts where the fault is. It shows by a panic
// entry: a frame of the runtime's (isGoPanic), as a recovered trace has
// them, or the line a crash starts with (goPanicHeaders).
func goAtFault(lines []string) bool {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if hasAnyPrefix(line, goPanicHeaders) {
			return true
		}
		if f, ok := goFrame(line); ok && isGoPanic(f) {
			return true
		}
	}
	return false
}

// goThrowSite drops the frames above the throw site: the deferred calls that
// ran while panicking, which are printed first, and the runtime's entry into
// the panic. A trace that ends there is kept whole.
func goThrowSite(frames []StackFrame) []StackFrame {
	for i := range frames {
		if !isGoPanic(frames[i]) {
			continue
		}
		for i < len(frames) && isGoPanic(frames[i]) {
			i++
		}
		if i < len(frames) {
			return frames[i:]
		}
		break
	}
	return frames
}

func isGoPanic(f StackFrame) bool {
	return f.Module == "" && f.Function == "panic" || f.Module == "runtime" && goPanicRe.MatchString(f.Function)
}

// goFirstGoroutine reads the frames up to the first header that follows
// one: a dump may start without the header of the goroutine it is about.
func goFirstGoroutine(lines []string) []StackFrame {
	var out []StackFrame
	framed := false // the line before was a frame; this one may be its file
	for _, line := range lines {
		line = strings.TrimSpace(line)
		f, ok := goFrame(line)
		switch {
		case ok:
			out = append(out, f)
		case goGoroutineRe.MatchString(line):
			if len(out) > 0 {
				return out
			}
		case framed:
			if m := goFileRe.FindStringSubmatch(line); m != nil && goDependencyRe.MatchString(m[1]) {
				out[len(out)-1].Dependency = true
			}
		}
		framed = ok
	}
	return out
}

func goInApp(f StackFrame) bool {
	return f.Module != "" && !f.Dependency && !isGoStdlib(f.Module) && !hasAnyPrefix(f.Module, goVendorPrefixes)
}

// isGoStdlib reports whether a package path starts with a top-level name of
// the standard library.
func isGoStdlib(module string) bool {
	first, _, _ := strings.Cut(module, "/")
	i := sort.SearchStrings(goStdlib, first)
	return i < len(goStdlib) && goStdlib[i] == first
}
