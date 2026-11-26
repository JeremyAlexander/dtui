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
		{"sync.(*Mutex).Lock", Frame{Notation: Go, Package: "sync", Type: "Mutex", Function: "Lock"}},

		{"src/server/handler.js:resolve", Frame{Notation: Path, File: "src/server/handler.js", Function: "resolve"}},
		{"/app/src/server/handler.ts:resolve", Frame{Notation: Path, File: "/app/src/server/handler.ts", Function: "resolve"}},
		{"handler.py:resolve", Frame{Notation: Path, File: "handler.py", Function: "resolve"}},
		{"node:internal/timers:processTimers", Frame{Notation: Path, File: "node:internal/timers", Function: "processTimers", Runtime: "Node.js"}},
		{"node:fs:readFileSync", Frame{Notation: Path, File: "node:fs", Function: "readFileSync", Runtime: "Node.js"}},
		{"src/a.js:", Frame{Notation: Path, File: "src/a.js"}},

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
