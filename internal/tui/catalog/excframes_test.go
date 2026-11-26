package catalog

import (
	"reflect"
	"strings"
	"testing"
)

// frames builds the expected result of a parse: module, function pairs.
func frames(syntax StackSyntax, pairs ...string) []StackFrame {
	var out []StackFrame
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, StackFrame{Module: pairs[i], Function: pairs[i+1], Syntax: syntax})
	}
	return out
}

func assertFrames(t *testing.T, stack string, want []StackFrame) {
	t.Helper()
	got := ParseStackFrames(stack)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("frames mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

func TestParseStackFramesJava(t *testing.T) {
	cases := []struct {
		name  string
		stack string
		want  []StackFrame
	}{
		{"plain frames lose their line numbers", `java.lang.IllegalStateException: Cart 7 is locked
	at com.acme.shop.CartService.addItem(CartService.java:42)
	at com.acme.shop.CartController.post(CartController.java:17) ~[shop.jar:1.4.2]
	at java.base/java.lang.Thread.run(Thread.java:833)
	at app//com.acme.shop.Main.main(Main.java:9)
	at com.acme.shop.Native.call(Native Method)`,
			frames(StackJava,
				"com.acme.shop.CartService", "addItem",
				"com.acme.shop.CartController", "post",
				"java.lang.Thread", "run",
				"com.acme.shop.Main", "main",
				"com.acme.shop.Native", "call")},
		{"frames without at", "com.acme.shop.CartService.addItem(CartService.java:42)\n" +
			"com.acme.shop.CartService.<init> (CartService.java:9)\n" +
			"com.acme.shop.Native.call(Native Method)\n" +
			"com.acme.shop.Gen.run(Unknown Source)\n" +
			"com.acme.shop.Gen.go(<generated>)\n" +
			"com.acme.shop.Kt.run(Cart.kt)",
			frames(StackJava,
				"com.acme.shop.CartService", "addItem",
				"com.acme.shop.CartService", "<init>",
				"com.acme.shop.Native", "call",
				"com.acme.shop.Gen", "run",
				"com.acme.shop.Gen", "go",
				"com.acme.shop.Kt", "run")},
		{"a OneAgent call stack: no at, a space before the location", "com.example.shop.cart.CartService.addItem (CartService.java:42)\n" +
			"com.example.shop.cart.CartController.post (CartController.java:17)\n" +
			"jdk.internal.reflect.DirectMethodHandleAccessor.invoke (DirectMethodHandleAccessor.java:103)\n" +
			"java.lang.reflect.Method.invoke (Method.java:580)\n" +
			"org.apache.catalina.core.ApplicationFilterChain.doFilter (ApplicationFilterChain.java:129)\n" +
			"java.lang.Thread.run (Thread.java:1583)",
			frames(StackJava,
				"com.example.shop.cart.CartService", "addItem",
				"com.example.shop.cart.CartController", "post",
				"jdk.internal.reflect.DirectMethodHandleAccessor", "invoke",
				"java.lang.reflect.Method", "invoke",
				"org.apache.catalina.core.ApplicationFilterChain", "doFilter",
				"java.lang.Thread", "run")},
		{"prose with parentheses is not a frame", "com.acme.shop.CartService.addItem(sku was empty)\nfailed (see above)", nil},
		{"generated classes collapse", `	at com.acme.shop.CartService$$EnhancerBySpringCGLIB$$1a2b3c4d.addItem(<generated>)
	at com.acme.shop.CartService$$FastClassBySpringCGLIB$$9f8e7d6c.invoke(<generated>)
	at sun.reflect.GeneratedMethodAccessor123.invoke(Unknown Source)
	at jdk.internal.reflect.GeneratedConstructorAccessor7.newInstance(Unknown Source)
	at com.sun.proxy.$Proxy88.addItem(Unknown Source)
	at jdk.proxy3.$Proxy41.addItem(Unknown Source)`,
			frames(StackJava,
				"com.acme.shop.CartService<auto>", "addItem",
				"com.acme.shop.CartService<auto>", "invoke",
				"sun.reflect.<auto>", "invoke",
				"jdk.internal.reflect.<auto>", "newInstance",
				"com.sun.proxy.<auto>", "addItem",
				"jdk.proxy.<auto>", "addItem")},
		{"lambda frames", `	at com.acme.shop.CartService.lambda$addItem$3(CartService.java:51)
	at com.acme.shop.CartService$$Lambda$214/0x0000000800d1a840.accept(Unknown Source)
	at com.acme.shop.CartService$$Lambda/0x0000000800d1a840.accept(Unknown Source)
	at com.acme.shop.CartService.addItem(CartService.java:50)`,
			frames(StackJava, "com.acme.shop.CartService", "addItem")},
		{"the last cause is the root, completed from its parent", `com.acme.shop.CheckoutException: checkout failed
	at com.acme.shop.Checkout.run(Checkout.java:30)
	at com.acme.shop.Api.post(Api.java:12)
	at com.acme.shop.Server.handle(Server.java:80)
Caused by: java.lang.IllegalStateException: Cart 7 is locked
	at com.acme.shop.CartService.addItem(CartService.java:42)
	at com.acme.shop.Checkout.run(Checkout.java:28)
	... 2 more`,
			frames(StackJava,
				"com.acme.shop.CartService", "addItem",
				"com.acme.shop.Checkout", "run",
				"com.acme.shop.Api", "post",
				"com.acme.shop.Server", "handle")},
		{"a chain of two causes", `com.acme.shop.ApiException: failed
	at com.acme.shop.Api.post(Api.java:12)
	at com.acme.shop.Server.handle(Server.java:80)
Caused by: com.acme.shop.CheckoutException: checkout failed
	at com.acme.shop.Checkout.run(Checkout.java:30)
	... 2 common frames omitted
Caused by: java.lang.IllegalStateException: locked
	at com.acme.shop.CartService.addItem(CartService.java:42)
	... 2 more`,
			frames(StackJava,
				"com.acme.shop.CartService", "addItem",
				"com.acme.shop.Api", "post",
				"com.acme.shop.Server", "handle")},
		{"more than the parent holds takes all of it", `	at com.acme.shop.Api.post(Api.java:12)
Caused by: java.lang.IllegalStateException: locked
	at com.acme.shop.CartService.addItem(CartService.java:42)
	... 9 more`,
			frames(StackJava, "com.acme.shop.CartService", "addItem", "com.acme.shop.Api", "post")},
		{"a cause without the marker stands alone", `	at com.acme.shop.Api.post(Api.java:12)
Caused by: java.lang.IllegalStateException: locked
	at com.acme.shop.CartService.addItem(CartService.java:42)`,
			frames(StackJava, "com.acme.shop.CartService", "addItem")},
		{"suppressed exceptions are left out", `	at com.acme.shop.Api.post(Api.java:12)
	Suppressed: java.io.IOException: close failed
		at com.acme.shop.Conn.close(Conn.java:5)
		... 1 more
Caused by: java.lang.IllegalStateException: locked
	at com.acme.shop.CartService.addItem(CartService.java:42)
	... 1 more`,
			frames(StackJava, "com.acme.shop.CartService", "addItem", "com.acme.shop.Api", "post")},
		{"recursion collapses", `	at com.acme.shop.Tree.walk(Tree.java:10)
	at com.acme.shop.Tree.walk(Tree.java:14)
	at com.acme.shop.Tree.walk(Tree.java:14)
	at com.acme.shop.Tree.start(Tree.java:3)
	at com.acme.shop.Tree.walk(Tree.java:14)`,
			frames(StackJava,
				"com.acme.shop.Tree", "walk", "com.acme.shop.Tree", "start", "com.acme.shop.Tree", "walk")},
		{"windows line ends", "\tat com.acme.shop.Api.post(Api.java:12)\r\n\tat com.acme.shop.Server.handle(Server.java:80)\r\n",
			frames(StackJava, "com.acme.shop.Api", "post", "com.acme.shop.Server", "handle")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertFrames(t, tc.stack, tc.want) })
	}
}

func TestParseStackFramesDotNet(t *testing.T) {
	cases := []struct {
		name  string
		stack string
		want  []StackFrame
	}{
		{"arguments and source locations are dropped", `System.InvalidOperationException: Cart 7 is locked
   at Acme.Shop.CartService.AddItem(String sku, Int32 quantity) in C:\src\shop\CartService.cs:line 42
   at Acme.Shop.CartController.Post(CartRequest request)
   at Acme.Shop.Repo` + "`" + `1.Get[T](Int32 id)
   at Acme.Shop.Cart..ctor(String owner)
   at lambda_method(Closure, Object)`,
			frames(StackDotNet,
				"Acme.Shop.CartService", "AddItem",
				"Acme.Shop.CartController", "Post",
				"Acme.Shop.Repo`1", "Get",
				"Acme.Shop.Cart", ".ctor",
				"", "lambda_method")},
		{"async state machines unwrap to the method", `   at Acme.Shop.CartService.<AddItemAsync>d__12.MoveNext()
--- End of stack trace from previous location ---
   at System.Runtime.ExceptionServices.ExceptionDispatchInfo.Throw()
   at Acme.Shop.CartController.<PostAsync>d__4.MoveNext() in /src/shop/CartController.cs:line 20`,
			frames(StackDotNet,
				"Acme.Shop.CartService", "AddItemAsync",
				"System.Runtime.ExceptionServices.ExceptionDispatchInfo", "Throw",
				"Acme.Shop.CartController", "PostAsync")},
		{"closures unwrap to the method", `   at Acme.Shop.CartService.<>c__DisplayClass5_0.<AddItem>b__0(Item item)
   at Acme.Shop.CartService.<>c.<Total>b__7_1(Item item)
   at Acme.Shop.CartService.<>c.<Total>b__9(Item item)`,
			frames(StackDotNet,
				"Acme.Shop.CartService", "AddItem", "Acme.Shop.CartService", "Total")},
		{"a name that starts with a dot", "   at .Run()", frames(StackDotNet, "", "Run")},
		{"mono writes a space before the parameters", `System.InvalidOperationException: Cart 7 is locked
  at Example.Shop.CartService.AddItem (System.String sku, System.Int32 quantity) [0x0001a] in <3f2b8c1e9d4a4b7c8e210a1b2c3d4e5f>:0
  at Example.Shop.CartController.Post (Example.Shop.CartRequest request) [0x00007] in /src/shop/CartController.cs:17
  at Example.Shop.Program.Main () [0x00000] in <3f2b8c1e9d4a4b7c8e210a1b2c3d4e5f>:0`,
			frames(StackDotNet,
				"Example.Shop.CartService", "AddItem",
				"Example.Shop.CartController", "Post",
				"Example.Shop.Program", "Main")},
		{"the innermost exception is the root", `Acme.Shop.CheckoutException: failed
 ---> System.InvalidOperationException: Cart 7 is locked
   at Acme.Shop.CartService.AddItem(String sku)
   --- End of inner exception stack trace ---
   at Acme.Shop.Checkout.Run()`,
			frames(StackDotNet, "Acme.Shop.CartService", "AddItem")},
		{"the inner marker before any frame ends nothing", `   --- End of inner exception stack trace ---
   at Acme.Shop.Checkout.Run()`,
			frames(StackDotNet, "Acme.Shop.Checkout", "Run")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertFrames(t, tc.stack, tc.want) })
	}
}

func TestParseStackFramesGo(t *testing.T) {
	cases := []struct {
		name  string
		stack string
		want  []StackFrame
	}{
		{"goroutine header, arguments and offsets are dropped", `panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x10a2b3c]

goroutine 42 [running]:
panic({0x10a0b20?, 0xc000012345?})
	/usr/local/go/src/runtime/panic.go:770 +0x132
github.com/acme/shop/cart.(*Service).AddItem(0xc000010000, {0x10b1c2d, 0x5})
	/src/shop/cart/service.go:42 +0x1d
github.com/acme/shop/cart.Total(...)
	/src/shop/cart/total.go:9
net/http.HandlerFunc.ServeHTTP(0x10c2d3e?, {0x1234560, 0xc0000a0000}, 0xc0000b0000)
	/usr/local/go/src/net/http/server.go:2166 +0x29
main.main.func1()
	/src/shop/main.go:12 +0x25
created by main.main in goroutine 1
	/src/shop/main.go:11 +0x4a`,
			frames(StackGo,
				"", "panic",
				"github.com/acme/shop/cart", "Service.AddItem",
				"github.com/acme/shop/cart", "Total",
				"net/http", "HandlerFunc.ServeHTTP",
				"main", "main.func1",
				"main", "main")},
		{"only the first goroutine counts", `goroutine 7 [running]:
main.worker(0x1)
	/src/shop/main.go:30 +0x1d

goroutine 1 [chan receive]:
main.main()
	/src/shop/main.go:12 +0x25`,
			frames(StackGo, "main", "worker")},
		{"generic instantiations are dropped", `github.com/acme/shop/list.Map[...](0x1, 0x2)
github.com/acme/shop/list.(*Set[go.shape.int,go.shape.string]).Add(0x1)
created by github.com/acme/shop/list.Start`,
			frames(StackGo,
				"github.com/acme/shop/list", "Map",
				"github.com/acme/shop/list", "Set.Add",
				"github.com/acme/shop/list", "Start")},
		{"a name that starts with a dot", ".run()\nshop/.run()",
			frames(StackGo, "", "run", "shop/", "run")},
		{"a one-letter name", "f(0x1)", frames(StackGo, "", "f")},
		{"lines that are not frames", "exit status 2\nmain.run(context deadline exceeded)\n(0x1)\ncreated by \nfoo bar()\nmain.run(0x1) tail", nil},
		{"a call in prose is not a frame", "decode(abc)\nretry(3)\nfailed()\ncart.decode(abc, 7)\ncreated by admin", nil},
		{"a frame needs a package or arguments", "run()\nmain.run()\nrun(0x1, {0xc000010000?, 0x5}, ...)",
			frames(StackGo, "main", "run", "", "run")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertFrames(t, tc.stack, tc.want) })
	}
}

func TestParseStackFramesNode(t *testing.T) {
	cases := []struct {
		name  string
		stack string
		want  []StackFrame
	}{
		{"locations lose line and column", `TypeError: Cannot read properties of undefined (reading 'sku')
    at CartService.addItem (/app/src/cart/service.js:42:17)
    at async CartController.post (/app/src/cart/controller.js:17:5)
    at new Cart (file:///app/src/cart/cart.mjs:3:9)
    at Object.<anonymous> (/app/src/index.js:5:1)
    at CartService.add [as addItem] (/app/src/cart/service.js:40:3)
    at Layer.handle (/app/node_modules/express/lib/router/layer.js:95:5)
    at /app/src/cart/routes.js:9:3
    at async /app/src/cart/routes.js:11:3
    at async Promise.all (index 3)
    at process.processTicksAndRejections (node:internal/process/task_queues:95:5)
    at Array.map (<anonymous>)
    at <anonymous>`,
			frames(StackJS,
				"/app/src/cart/service.js", "CartService.addItem",
				"/app/src/cart/controller.js", "CartController.post",
				"/app/src/cart/cart.mjs", "new Cart",
				"/app/src/index.js", "<anonymous>",
				"/app/src/cart/service.js", "CartService.add",
				"node_modules/express/lib/router/layer.js", "Layer.handle",
				"/app/src/cart/routes.js", "<anonymous>",
				"index", "Promise.all",
				"node:internal/process/task_queues", "process.processTicksAndRejections",
				"<anonymous>", "Array.map",
				"<anonymous>", "<anonymous>")},
		{"nested dependencies address the innermost", "    at parse (/app/node_modules/a/node_modules/b/index.js:1:2)",
			frames(StackJS, "node_modules/b/index.js", "parse")},
		{"an unclosed location is kept", "    at parse (/app/src/parse.js:1:2",
			frames(StackJS, "/app/src/parse.js", "parse")},
		{"prose after at is not a frame", "at least 3 items (max 5)\nat startup\nat parse ()\nat sign (@) expected\n" +
			"at checkout (final step)\nat Checkout (see above)\nat 10:11:12 the cart (id 7) was locked\nat 10:11:12", nil},
		{"function@location, as Firefox and Safari write it", `addItem@https://shop.example.invalid/static/js/cart.js:42:17
async*post@https://shop.example.invalid/static/js/api.js:17:5
@https://shop.example.invalid/static/js/main.js:5:1
CartService.prototype.total@https://shop.example.invalid/static/js/cart.js:60:9`,
			frames(StackJS,
				"https://shop.example.invalid/static/js/cart.js", "addItem",
				"https://shop.example.invalid/static/js/api.js", "post",
				"https://shop.example.invalid/static/js/main.js", "<anonymous>",
				"https://shop.example.invalid/static/js/cart.js", "CartService.prototype.total")},
		{"a mail address is not a frame", "bob@shop.example.invalid\nask bob@shop.example.invalid:25:1", nil},
		{"at without a frame", "    at \n    at async \n    at some words here", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertFrames(t, tc.stack, tc.want) })
	}
}

func TestParseStackFramesPython(t *testing.T) {
	cases := []struct {
		name  string
		stack string
		want  []StackFrame
	}{
		{"the throw site comes first", `Traceback (most recent call last):
  File "/app/shop/api.py", line 17, in post
    cart.add_item(sku)
  File "/app/shop/cart.py", line 42, in add_item
    self.session.commit()
  File "/usr/lib/python3.11/site-packages/sqlalchemy/orm/session.py", line 1900, in commit
    trans.commit(_to_root=True)
  File "/usr/lib/python3.11/json/decoder.py", line 355, in raw_decode
    raise JSONDecodeError("Expecting value", s, err.value) from None
ValueError: cart 7 is locked`,
			frames(StackPython,
				"/usr/lib/python/json/decoder.py", "raw_decode",
				"site-packages/sqlalchemy/orm/session.py", "commit",
				"/app/shop/cart.py", "add_item",
				"/app/shop/api.py", "post")},
		{"module-level code and a frame without source", `Traceback (most recent call last):
  File "/app/shop/main.py", line 3
  File "<frozen importlib._bootstrap>", line 241, in _call_with_frames_removed
  File "/usr/lib/python3/dist-packages/yaml/loader.py", line 9, in load`,
			frames(StackPython,
				"dist-packages/yaml/loader.py", "load",
				"<frozen importlib._bootstrap>", "_call_with_frames_removed",
				"/app/shop/main.py", "<module>")},
		{"a relative package directory keeps its path", `  File "site-packages/python3/yaml/loader.py", line 9, in load`,
			frames(StackPython, "site-packages/python3/yaml/loader.py", "load")},
		{"the first traceback is the root", `Traceback (most recent call last):
  File "/app/shop/cart.py", line 42, in add_item
    raise ValueError("locked")
ValueError: locked

During handling of the above exception, another exception occurred:

Traceback (most recent call last):
  File "/app/shop/api.py", line 17, in post
    cart.add_item(sku)
RuntimeError: failed`,
			frames(StackPython, "/app/shop/cart.py", "add_item")},
		{"an exception group yields its first sub-exception", `  + Exception Group Traceback (most recent call last):
  |   File "/app/shop/api.py", line 17, in post
  |     async with asyncio.TaskGroup() as tg:
  |   File "/usr/lib/python3.11/asyncio/taskgroups.py", line 145, in __aexit__
  |     raise me from None
  | ExceptionGroup: unhandled errors in a TaskGroup (2 sub-exceptions)
  +-+---------------- 1 ----------------
    | Traceback (most recent call last):
    |   File "/app/shop/tasks.py", line 30, in run
    |     cart.add_item(sku)
    |   File "/app/shop/cart.py", line 42, in add_item
    |     raise ValueError("locked")
    | ValueError: locked
    +---------------- 2 ----------------
    | Traceback (most recent call last):
    |   File "/app/shop/stock.py", line 9, in reserve
    |     raise KeyError(sku)
    | KeyError: 'kettle'
    +------------------------------------`,
			frames(StackPython, "/app/shop/cart.py", "add_item", "/app/shop/tasks.py", "run")},
		{"a group without sub-exceptions keeps its own frames", `  + Exception Group Traceback (most recent call last):
  |   File "/app/shop/api.py", line 17, in post
  |     raise ExceptionGroup("none", [])
  +------------------------------------`,
			frames(StackPython, "/app/shop/api.py", "post")},
		{"a line in another notation is not a frame", `  File "/app/shop/cart.py", line 42, in add_item
    session.commit()
main.run()
    at com.acme.shop.CartService.addItem(CartService.java:42)`,
			frames(StackPython, "/app/shop/cart.py", "add_item")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertFrames(t, tc.stack, tc.want) })
	}
}

// stackNotations is indexed by StackSyntax: each notation must read its own
// sample line and stamp the frame with the index it sits at.
func TestStackNotationsMatchTheirSyntax(t *testing.T) {
	samples := map[StackSyntax]string{
		StackJava:   "at com.acme.shop.CartService.addItem(CartService.java:42)",
		StackDotNet: "at Acme.Shop.CartService.AddItem(String sku)",
		StackJS:     "at CartService.addItem (/app/src/cart/service.js:42:17)",
		StackPython: `File "/app/shop/cart.py", line 42, in add_item`,
		StackGo:     "github.com/acme/shop/cart.(*Service).AddItem(0xc000010000)",
	}
	if len(samples) != len(stackNotations) {
		t.Fatalf("%d notations, %d samples", len(stackNotations), len(samples))
	}
	for syntax, line := range samples {
		f, ok := stackNotations[syntax].frame(line)
		if !ok || f.Syntax != syntax {
			t.Errorf("notation %d read %q as %+v, %v", syntax, line, f, ok)
		}
		// The trace as a whole is read in the same notation.
		if got := ParseStackFrames(line); len(got) != 1 || got[0] != f {
			t.Errorf("ParseStackFrames(%q) = %+v, want %+v", line, got, f)
		}
	}
	if DefaultFrameClassifier.InApp(StackFrame{Module: "com.acme.shop.Cart", Syntax: StackSyntax(len(stackNotations))}) ||
		DefaultFrameClassifier.InApp(StackFrame{Module: "com.acme.shop.Cart", Syntax: -1}) {
		t.Error("a frame of an unknown notation must not be in-app")
	}
}

func TestParseStackFramesLineBreaks(t *testing.T) {
	want := frames(StackJava, "com.example.shop.Api", "post", "com.example.shop.Server", "handle")
	for name, stack := range map[string]string{
		"line feeds":       "\tat com.example.shop.Api.post(Api.java:12)\n\tat com.example.shop.Server.handle(Server.java:80)\n",
		"carriage returns": "\tat com.example.shop.Api.post(Api.java:12)\r\tat com.example.shop.Server.handle(Server.java:80)\r",
		"both":             "\tat com.example.shop.Api.post(Api.java:12)\r\n\tat com.example.shop.Server.handle(Server.java:80)\r\n",
		"escaped":          `java.lang.IllegalStateException: locked\n\tat com.example.shop.Api.post(Api.java:12)\n\tat com.example.shop.Server.handle(Server.java:80)`,
		"escaped pair":     `java.lang.IllegalStateException: locked\r\n\tat com.example.shop.Api.post(Api.java:12)\r\n\tat com.example.shop.Server.handle(Server.java:80)`,
	} {
		t.Run(name, func(t *testing.T) { assertFrames(t, stack, want) })
	}
	// Where the text has real line breaks, a backslash and an n are text:
	// here part of a Windows path.
	assertFrames(t, "   at Example.Shop.Cart.Add(String sku) in C:\\src\\new\\Cart.cs:line 4\n   at Example.Shop.Api.Post()",
		frames(StackDotNet, "Example.Shop.Cart", "Add", "Example.Shop.Api", "Post"))
}

func TestParseStackFramesUnparseable(t *testing.T) {
	for _, in := range []string{
		"", "\n\n", "not a stack trace", "Error: something failed (code 5)",
		"at", "at ", "0x1)", "File \"x\"", "... 3 more", "Caused by: ", "goroutine 1 [running]:",
		"app/models/cart.rb:42:in `add_item'", "#0 /app/Cart.php(42): Cart->addItem()",
		"at least 3 items (max 5)", "at startup", "decode(abc)", "the cart (id 7) was locked at 10:11:12",
	} {
		if got := ParseStackFrames(in); got != nil {
			t.Errorf("ParseStackFrames(%q) = %+v, want nil", in, got)
		}
	}
}

func TestDefaultFrameClassifier(t *testing.T) {
	cases := []struct {
		frame StackFrame
		inApp bool
	}{
		{StackFrame{Module: "com.acme.shop.CartService", Syntax: StackJava}, true},
		{StackFrame{Module: "Acme.Shop.CartService", Syntax: StackDotNet}, true},
		{StackFrame{Module: "system.cart.Service", Syntax: StackDotNet}, true},
		{StackFrame{Module: "javafx", Syntax: StackJava}, true},
		{StackFrame{Module: "java.lang.Thread", Syntax: StackJava}, false},
		{StackFrame{Module: "org.springframework.web.Dispatcher", Syntax: StackJava}, false},
		{StackFrame{Module: "com.dynatrace.agent.Hook", Syntax: StackJava}, false},
		{StackFrame{Module: "System.Threading.Tasks.Task", Syntax: StackDotNet}, false},
		{StackFrame{Module: "Microsoft.AspNetCore.Routing.Endpoint", Syntax: StackDotNet}, false},
		{StackFrame{Module: "com.acme.shop.CartService<auto>", Syntax: StackJava}, false},
		{StackFrame{Module: "", Function: "lambda_method", Syntax: StackDotNet}, false},

		{StackFrame{Module: "main", Syntax: StackGo}, true},
		{StackFrame{Module: "github.com/acme/shop/cart", Syntax: StackGo}, true},
		{StackFrame{Module: "shop.example.invalid/cart", Syntax: StackGo}, true},
		{StackFrame{Module: "runtime", Syntax: StackGo}, false},
		{StackFrame{Module: "net/http", Syntax: StackGo}, false},
		{StackFrame{Module: "encoding/json.v2", Syntax: StackGo}, false},
		{StackFrame{Module: "golang.org/x/sync/errgroup", Syntax: StackGo}, false},
		{StackFrame{Module: "google.golang.org/grpc", Syntax: StackGo}, false},
		{StackFrame{Module: "", Function: "panic", Syntax: StackGo}, false},

		{StackFrame{Module: "/app/src/cart/service.js", Syntax: StackJS}, true},
		{StackFrame{Module: `C:\app\src\cart.js`, Syntax: StackJS}, true},
		{StackFrame{Module: "node_modules/express/lib/router/layer.js", Syntax: StackJS}, false},
		{StackFrame{Module: "node:internal/process/task_queues", Syntax: StackJS}, false},
		{StackFrame{Module: "internal/process/next_tick.js", Syntax: StackJS}, false},
		{StackFrame{Module: "events.js", Syntax: StackJS}, false},
		{StackFrame{Module: "<anonymous>", Syntax: StackJS}, false},

		{StackFrame{Module: "/app/shop/cart.py", Syntax: StackPython}, true},
		{StackFrame{Module: "cart.py", Syntax: StackPython}, true},
		{StackFrame{Module: "/app/lib/python_tools/cart.py", Syntax: StackPython}, true},
		{StackFrame{Module: "site-packages/sqlalchemy/orm/session.py", Syntax: StackPython}, false},
		{StackFrame{Module: "dist-packages/yaml/loader.py", Syntax: StackPython}, false},
		{StackFrame{Module: "/usr/lib/python/json/decoder.py", Syntax: StackPython}, false},
		{StackFrame{Module: "/usr/lib64/python/json/decoder.py", Syntax: StackPython}, false},
		{StackFrame{Module: "<frozen importlib._bootstrap>", Syntax: StackPython}, false},
	}
	for _, tc := range cases {
		if got := DefaultFrameClassifier.InApp(tc.frame); got != tc.inApp {
			t.Errorf("InApp(%+v) = %v, want %v", tc.frame, got, tc.inApp)
		}
	}
}

// FuzzParseStackFrames checks what must hold for any input: no panic, the
// same frames on every parse, no empty function, and no adjacent repeats.
func FuzzParseStackFrames(f *testing.F) {
	for _, seed := range []string{
		"", "at ", "at (", "at x (", "at a.b(", "at a..b()", "at .()", "a.b(C.java:1)", "created by ",
		"created by a in goroutine 1", "x.(*T).m(0x1)", "().()", "... 99999999999999999999 more",
		"Caused by: x\n... 3 more", "File \"a\", line 1, in f\nx()\nFile \"b\", line 2",
		"goroutine 1 [running]:\nmain.main()\ngoroutine 2 [x]:\nmain.other()",
		"at a.<b>d__1.MoveNext()", "at a.<>c.<b>b__1()", "at a$$Lambda$1/0x1.run()", "at a$$b.c(<generated>)",
		"at /", "at async ", "at f [as g] (x:1:2)", "--- End of inner exception stack trace ---",
		"Traceback (most recent call last):", "Suppressed: x\nat a.b(C.java:1)",
		"at A.B (System.String s) [0x1] in <a>:0", "f@https://a/b.js:1:2", "@a:1:2", "a.b (C.java:1)",
		"| File \"a\", line 1, in f\n+-+---- 1 ----\n| File \"b\", line 2, in g\n+---- 2 ----", `at a.b(C.java:1)\nat c.d(E.java:2)`,
		"at a.b(C.java:1)\rat c.d(E.java:2)", "run(0x1, {0x2?}, ...)", "created by a.b",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := ParseStackFrames(in)
		if again := ParseStackFrames(in); !reflect.DeepEqual(got, again) {
			t.Fatalf("not deterministic for %q", in)
		}
		for i, fr := range got {
			if fr.Function == "" {
				t.Fatalf("empty function in frame %d for %q", i, in)
			}
			if i > 0 && got[i-1] == fr {
				t.Fatalf("adjacent repeat at frame %d for %q", i, in)
			}
			if strings.ContainsAny(fr.Module+fr.Function, "\n") {
				t.Fatalf("line break in frame %d for %q", i, in)
			}
			DefaultFrameClassifier.InApp(fr)
		}
	})
}
