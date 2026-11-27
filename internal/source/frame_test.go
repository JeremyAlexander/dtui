package source

import "testing"

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		label string
		want  Frame
	}{
		{"com.example.shop.CartService.addItem", Frame{Notation: Dotted, Package: "com.example.shop", Type: "CartService", Function: "addItem"}},
		{"com.example.shop.CartService$Line$1.total", Frame{Notation: Dotted, Package: "com.example.shop", Type: "CartService$Line$1", Function: "total"}},
		{"com.example.shop.CartService.lambda$addItem$3", Frame{Notation: Dotted, Package: "com.example.shop", Type: "CartService", Function: "lambda$addItem$3"}},
		{"com.example.shop.CartService.<init>", Frame{Notation: Dotted, Package: "com.example.shop", Type: "CartService", Function: "<init>"}},
		{"com.example.Cart.total(int, java.lang.String)", Frame{Notation: Dotted, Package: "com.example", Type: "Cart", Function: "total"}},
		{"java.lang.Thread.run", Frame{Notation: Dotted, Package: "java.lang", Type: "Thread", Function: "run", Runtime: "the JDK"}},
		{"jdk.internal.misc.Unsafe.park", Frame{Notation: Dotted, Package: "jdk.internal.misc", Type: "Unsafe", Function: "park", Runtime: "the JDK"}},
		{"javanese.Cart.total", Frame{Notation: Dotted, Package: "javanese", Type: "Cart", Function: "total"}},
		{"System.Threading.Tasks.Task.Run", Frame{Notation: Dotted, Package: "System.Threading.Tasks", Type: "Task", Function: "Run", Runtime: ".NET"}},
		{"Shop.Services.CartService+Line.Total", Frame{Notation: Dotted, Package: "Shop.Services", Type: "CartService+Line", Function: "Total"}},
		{"Cart.total", Frame{Notation: Dotted, Type: "Cart", Function: "total"}},
		{"main", Frame{Notation: Dotted, Function: "main"}},

		{"github.com/acme/shop/internal/cart.(*Service).AddItem", Frame{Notation: Go, Package: "github.com/acme/shop/internal/cart", Type: "Service", Function: "AddItem"}},
		{"github.com/acme/shop/internal/cart.Service.AddItem", Frame{Notation: Go, Package: "github.com/acme/shop/internal/cart", Type: "Service", Function: "AddItem"}},
		{"github.com/acme/shop/internal/cart.Total", Frame{Notation: Go, Package: "github.com/acme/shop/internal/cart", Function: "Total"}},
		{"github.com/acme/shop/internal/cart.Total.func1", Frame{Notation: Go, Package: "github.com/acme/shop/internal/cart", Function: "Total"}},
		{"github.com/acme/shop/internal/cart.(*Service).AddItem.func2", Frame{Notation: Go, Package: "github.com/acme/shop/internal/cart", Type: "Service", Function: "AddItem"}},
		{"github.com/acme/shop/internal/cart.Map[go.shape.int].Get", Frame{Notation: Go, Package: "github.com/acme/shop/internal/cart", Type: "Map", Function: "Get"}},
		{"net/http.(*conn).serve", Frame{Notation: Go, Package: "net/http", Type: "conn", Function: "serve", Runtime: "the Go standard library"}},
		// The standard library is a fixed list of names, not "a path without
		// a domain": a module may be named without one.
		{"sync.(*Mutex).Lock", Frame{Notation: Go, Package: "sync", Type: "Mutex", Function: "Lock", Runtime: "the Go standard library"}},
		{"vendor/golang.org/x/net/http2/hpack.(*Decoder).Write", Frame{Notation: Go, Package: "vendor/golang.org/x/net/http2/hpack", Type: "Decoder", Function: "Write", Runtime: "the Go standard library"}},
		{"shop/internal/cart.(*Service).AddItem", Frame{Notation: Go, Package: "shop/internal/cart", Type: "Service", Function: "AddItem"}},
		{"main.(*server).serve", Frame{Notation: Go, Package: "main", Type: "server", Function: "serve"}},

		{"src/server/handler.js:resolve", Frame{Notation: Path, File: "src/server/handler.js", Function: "resolve"}},
		{"/app/src/server/handler.ts:resolve", Frame{Notation: Path, File: "/app/src/server/handler.ts", Function: "resolve"}},
		{"handler.py:resolve", Frame{Notation: Path, File: "handler.py", Function: "resolve"}},
		{"node:internal/timers:processTimers", Frame{Notation: Path, File: "node:internal/timers", Function: "processTimers", Runtime: "Node.js"}},
		{"node:fs:readFileSync", Frame{Notation: Path, File: "node:fs", Function: "readFileSync", Runtime: "Node.js"}},
		{"src/a.js:", Frame{Notation: Path, File: "src/a.js"}},
		// A file under a directory of dependencies is not the application's.
		{"/app/node_modules/express/lib/router/index.js:handle", Frame{Notation: Path, File: "/app/node_modules/express/lib/router/index.js", Function: "handle", Runtime: "a dependency (node_modules)"}},
		{`C:\app\venv\Lib\site-packages\flask\app.py:wsgi_app`, Frame{Notation: Path, File: `C:\app\venv\Lib\site-packages\flask\app.py`, Function: "wsgi_app", Runtime: "a dependency (site-packages)"}},
		{"src/node_modules_shim/a.js:f", Frame{Notation: Path, File: "src/node_modules_shim/a.js", Function: "f"}},

		// .NET spells a constructor with a dot of its own.
		{"Shop.Services.CartService..ctor", Frame{Notation: Dotted, Package: "Shop.Services", Type: "CartService", Function: ".ctor"}},
		{"Shop.Services.CartService..cctor", Frame{Notation: Dotted, Package: "Shop.Services", Type: "CartService", Function: ".cctor"}},

		// Go's standard library is spelled without a slash too, and is the
		// runtime then as well: time.Now is not a script called time.
		{"time.Now", Frame{Notation: Dotted, Type: "time", Function: "Now", Runtime: "the Go standard library"}},
		{"runtime.main", Frame{Notation: Dotted, Type: "runtime", Function: "main", Runtime: "the Go standard library"}},
		{"time.Time.Add", Frame{Notation: Dotted, Package: "time", Type: "Time", Function: "Add", Runtime: "the Go standard library"}},
		{"io.example.tracing.Span.context", Frame{Notation: Dotted, Package: "io.example.tracing", Type: "Span", Function: "context"}},
		// The JVM names a hidden class with an address after a slash: it is
		// no import path.
		{"com.example.shop.CartService$$Lambda$14/0x000000080012.run", Frame{Notation: Dotted, Package: "com.example.shop", Type: "CartService$$Lambda$14", Function: "run"}},
		{"com.example.shop.CartService$$Lambda/0x000000080012.run(int)", Frame{Notation: Dotted, Package: "com.example.shop", Type: "CartService$$Lambda", Function: "run"}},
		{"java.util.stream.ReferencePipeline$$Lambda$7/0x0000000800ab.accept", Frame{Notation: Dotted, Package: "java.util.stream", Type: "ReferencePipeline$$Lambda$7", Function: "accept", Runtime: "the JDK"}},
		{"/app/src/0xbeef.js:run", Frame{Notation: Path, File: "/app/src/0xbeef.js", Function: "run"}},
		{"example.com/shop/0xbeef.Run", Frame{Notation: Go, Package: "example.com/shop/0xbeef", Function: "Run"}},
		// Type parameters hold slashes and dots of their own.
		{"pkg.Process[*example.com/other/pkg.T]", Frame{Notation: Dotted, Type: "pkg", Function: "Process"}},
		{"example.com/shop/pkg.Process[*example.com/other/pkg.T]", Frame{Notation: Go, Package: "example.com/shop/pkg", Function: "Process"}},
		{"example.com/shop/pkg.Map[string,[]example.com/other/pkg.T].Get", Frame{Notation: Go, Package: "example.com/shop/pkg", Type: "Map", Function: "Get"}},
		// A method value.
		{"github.com/acme/shop/internal/cart.(*Service).AddItem-fm", Frame{Notation: Go, Package: "github.com/acme/shop/internal/cart", Type: "Service", Function: "AddItem"}},
		// A script's frame with its parameters, and a file with brackets.
		{"/app/src/handler.ts:resolve(req)", Frame{Notation: Path, File: "/app/src/handler.ts", Function: "resolve"}},
		{"pages/[id].tsx:handler", Frame{Notation: Path, File: "pages/[id].tsx", Function: "handler"}},
		{"src/a.js:(anonymous)", Frame{Notation: Path, File: "src/a.js", Function: "(anonymous)"}},

		{"", Frame{}},
		{"...", Frame{}},
		{"a..b", Frame{Notation: Dotted, Type: "a", Function: "b"}},
	} {
		if got := Parse(tc.label); got != tc.want {
			t.Errorf("Parse(%q)\n got  %+v\n want %+v", tc.label, got, tc.want)
		}
	}
}

func TestOuterType(t *testing.T) {
	for in, want := range map[string]string{
		"CartService": "CartService", "CartService$Line$1": "CartService", "CartService+Line": "CartService",
		"CartService$$Lambda$14/0x000000080012": "CartService", "List`1": "List", "$Proxy12": "$Proxy12", "": "",
	} {
		if got := (Frame{Type: in}).OuterType(); got != want {
			t.Errorf("OuterType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSymbol(t *testing.T) {
	for _, tc := range []struct {
		frame Frame
		want  string
	}{
		{Frame{Type: "Cart", Function: "total"}, "total"},
		{Frame{Type: "Cart", Function: "lambda$total$3"}, "total"},
		{Frame{Type: "Cart$Line", Function: "<init>"}, "Cart"},
		{Frame{Type: "Cart", Function: "<clinit>"}, ""},
		{Frame{Type: "Cart", Function: ".ctor"}, "Cart"},
		{Frame{Type: "Cart", Function: "<Total>b__0"}, "Total"},
		{Frame{Function: "(anonymous)"}, ""},
		{Frame{Function: "<anonymous>"}, ""},
		{Frame{Function: ""}, ""},
	} {
		if got := tc.frame.Symbol(); got != tc.want {
			t.Errorf("%+v.Symbol() = %q, want %q", tc.frame, got, tc.want)
		}
	}
}
