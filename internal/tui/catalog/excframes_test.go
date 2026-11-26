package catalog

import (
	"reflect"
	"sort"
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
		{"proxies named at run time collapse", `	at com.example.shop.Cart$HibernateProxy$kL2x9A.getId(Unknown Source)
	at com.example.shop.Cart$HibernateProxy$Zq81mB.getId(Unknown Source)
	at com.example.shop.CartService$ByteBuddy$Ab12Cd.addItem(Unknown Source)
	at com.example.shop.CartService$MockitoMock$1190.addItem(Unknown Source)
	at com.example.shop.CartService$$SpringCGLIB$$0.addItem(<generated>)
	at com.example.shop.CartService$$SpringCGLIB$$FastClass$$1.invoke(<generated>)
	at com.example.shop.CartService$$EnhancerByGuice$$7f3a.addItem(<generated>)
	at com.example.shop.Cart_$$_jvst3c0_1f.getId(Cart_$$_jvst3c0_1f.java)`,
			frames(StackJava,
				"com.example.shop.Cart<auto>", "getId",
				"com.example.shop.CartService<auto>", "addItem",
				"com.example.shop.CartService<auto>", "invoke",
				"com.example.shop.CartService<auto>", "addItem",
				"com.example.shop.Cart<auto>", "getId")},
		{"scala closures unwrap to the method", `	at com.example.shop.Cart$$anonfun$total$1.apply(Cart.scala:30)
	at com.example.shop.Cart$$anonfun$total$1$$anonfun$apply$2.apply$mcV$sp(Cart.scala:31)
	at com.example.shop.Cart.$anonfun$total$3(Cart.scala:30)
	at com.example.shop.Cart.$anonfun$total$3$adapted(Cart.scala:30)
	at com.example.shop.Cart$.$anonfun$apply$1(Cart.scala:9)
	at com.example.shop.Cart$$anon$1.run(Cart.scala:40)
	at com.example.shop.Cart$$inlined$map$1.invoke(Cart.kt:12)`,
			frames(StackJava,
				"com.example.shop.Cart", "total",
				"com.example.shop.Cart$", "apply",
				"com.example.shop.Cart$$anon$1", "run",
				"com.example.shop.Cart$$inlined$map$1", "invoke")},
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
		{"a cause inside a suppressed block is not the root", `com.example.shop.CheckoutException: checkout failed
	at com.example.shop.Checkout.run(Checkout.java:30)
	at com.example.shop.Api.post(Api.java:12)
	Suppressed: java.io.IOException: close failed
		at com.example.shop.Conn.close(Conn.java:5)
		... 2 more
	Caused by: java.net.SocketException: broken pipe
		at com.example.shop.Socket.write(Socket.java:9)
		... 3 more
		Suppressed: java.io.IOException: flush failed
			at com.example.shop.Socket.flush(Socket.java:14)
	Suppressed: java.io.IOException: unlock failed
		at com.example.shop.Lock.release(Lock.java:21)`,
			frames(StackJava, "com.example.shop.Checkout", "run", "com.example.shop.Api", "post")},
		{"a cause after a suppressed block with a cause of its own", `    com.example.shop.CheckoutException: checkout failed
    	at com.example.shop.Checkout.run(Checkout.java:30)
    	at com.example.shop.Api.post(Api.java:12)
    	Suppressed: java.io.IOException: close failed
    		at com.example.shop.Conn.close(Conn.java:5)
    	Caused by: java.net.SocketException: broken pipe
    		at com.example.shop.Socket.write(Socket.java:9)
    Caused by: java.lang.IllegalStateException: locked
    	at com.example.shop.CartService.addItem(CartService.java:42)
    	... 1 more`,
			frames(StackJava, "com.example.shop.CartService", "addItem", "com.example.shop.Api", "post")},
		{"a last cause without frames falls back to the nearest with some", `com.example.shop.ApiException: failed
	at com.example.shop.Api.post(Api.java:12)
	at com.example.shop.Server.handle(Server.java:80)
Caused by: com.example.shop.CheckoutException: checkout failed
	at com.example.shop.Checkout.run(Checkout.java:30)
	... 2 more
Caused by: java.lang.IllegalStateException: Cart 7 is locked
Caused by: java.sql.SQLException: lock timeout`,
			frames(StackJava,
				"com.example.shop.Checkout", "run",
				"com.example.shop.Api", "post",
				"com.example.shop.Server", "handle")},
		{"a cause without frames under a single exception", `com.example.shop.ApiException: failed
	at com.example.shop.Api.post(Api.java:12)
Caused by: java.lang.IllegalStateException: Cart 7 is locked`,
			frames(StackJava, "com.example.shop.Api", "post")},
		{"a cause that shares every frame is still the root", `com.example.shop.ApiException: failed
	at com.example.shop.Api.post(Api.java:12)
	at com.example.shop.Server.handle(Server.java:80)
Caused by: java.lang.IllegalStateException: Cart 7 is locked
	... 1 more`,
			frames(StackJava, "com.example.shop.Server", "handle")},
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
		{"local functions unwrap to their name", `   at Example.Shop.CartService.<Run>g__Validate|5_0(Int32 quantity)
   at Example.Shop.CartService.<Run>g__Validate|7_1(Int32 quantity)
   at Example.Shop.CartService.<<Run>g__Reserve|5_2>d.MoveNext()
   at Example.Shop.CartService.<>c__DisplayClass5_0.<Run>g__Check|3()
   at Program.<<Main>$>g__Handle|0_0(String[] args)
   at Program.<Main>$(String[] args)`,
			frames(StackDotNet,
				"Example.Shop.CartService", "Validate",
				"Example.Shop.CartService", "Reserve",
				"Example.Shop.CartService", "Check",
				"Program", "Handle",
				"Program", "Main")},
		{"compiled delegates lose their number", `   at lambda_method42(Closure, Object)
   at lambda_method7(Closure, Object)
   at System.Linq.Expressions.Compiled.lambda_method9(Closure)
   at Example.Shop.Cart.lambda_method42x(Int32 n)`,
			frames(StackDotNet,
				"", "lambda_method",
				"System.Linq.Expressions.Compiled", "lambda_method",
				"Example.Shop.Cart", "lambda_method42x")},
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
				"github.com/acme/shop/cart", "Service.AddItem",
				"github.com/acme/shop/cart", "Total",
				"net/http", "HandlerFunc.ServeHTTP",
				"main", "main.func1",
				"main", "main")},
		{"the throw site is below the panic and what ran while panicking", `goroutine 51 [running]:
net/http.(*conn).serve.func1()
	/usr/local/go/src/net/http/server.go:1898 +0xbe
panic({0x9a3c80?, 0xe3b1d0?})
	/usr/local/go/src/runtime/panic.go:770 +0x132
runtime.panicmem(...)
	/usr/local/go/src/runtime/panic.go:261
runtime.sigpanic()
	/usr/local/go/src/runtime/signal_unix.go:881 +0x378
shop/internal/cart.(*Service).AddItem(0xc000010000)
	/src/shop/internal/cart/service.go:42 +0x1d
main.main()
	/src/shop/main.go:12 +0x25`,
			frames(StackGo, "shop/internal/cart", "Service.AddItem", "main", "main")},
		{"an explicit panic", "runtime.gopanic({0x1, 0x2})\nmain.run(0x1)\nmain.main()",
			frames(StackGo, "main", "run", "main", "main")},
		{"a later panic is the one that counts", "main.cleanup()\npanic({0x1, 0x2})\nmain.run(0x1)\npanic({0x3, 0x4})\nmain.main()",
			frames(StackGo, "main", "run", "", "panic", "main", "main")},
		{"a trace that ends in the panic keeps its frames", "main.cleanup()\npanic({0x1, 0x2})\nruntime.sigpanic()",
			frames(StackGo, "main", "cleanup", "", "panic", "runtime", "sigpanic")},
		{"a function named like the runtime's is the application's", "shop/panic.panic(0x1)\nmain.panicmem(0x1)",
			frames(StackGo, "shop/panic", "panic", "main", "panicmem")},
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
		{"a vendored package is addressed without its vendor directory", `github.com/example/shop/vendor/github.com/gin-gonic/gin.(*Context).Next(0xc0001a2000)
shop/vendor/golang.org/x/net/http2.(*serverConn).runHandler(0xc000010000)
vendor/golang.org/x/net/http2/hpack.(*Decoder).Write(0xc000010000)`,
			[]StackFrame{
				{Module: "github.com/gin-gonic/gin", Function: "Context.Next", Syntax: StackGo, Dependency: true},
				{Module: "golang.org/x/net/http2", Function: "serverConn.runHandler", Syntax: StackGo, Dependency: true},
				{Module: "golang.org/x/net/http2/hpack", Function: "Decoder.Write", Syntax: StackGo, Dependency: true}}},
		{"the file line tells a dependency from the application", `github.com/gin-gonic/gin.(*Context).Next(0xc0001a2000)
	/go/pkg/mod/github.com/gin-gonic/gin@v1.9.1/context.go:174 +0x2b
github.com/example/shop/cart.(*Service).AddItem(0xc000010000)
	/src/shop/cart/service.go:42 +0x1d
github.com/example/money.Add(0x1, 0x2)
	github.com/example/money@v0.3.0/money.go:9 +0x1d
github.com/example/tax.Rate(0x1)
	/src/shop/vendor/github.com/example/tax/rate.go:12 +0x3c
github.com/example/shop/api.Post(0xc000010000)
created by github.com/example/shop/api.Serve in goroutine 1
	/src/shop/api/serve.go:30 +0x4a`,
			[]StackFrame{
				{Module: "github.com/gin-gonic/gin", Function: "Context.Next", Syntax: StackGo, Dependency: true},
				{Module: "github.com/example/shop/cart", Function: "Service.AddItem", Syntax: StackGo},
				{Module: "github.com/example/money", Function: "Add", Syntax: StackGo, Dependency: true},
				{Module: "github.com/example/tax", Function: "Rate", Syntax: StackGo, Dependency: true},
				{Module: "github.com/example/shop/api", Function: "Post", Syntax: StackGo},
				{Module: "github.com/example/shop/api", Function: "Serve", Syntax: StackGo}}},
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
				"src/cart/service.js", "CartService.addItem",
				"src/cart/controller.js", "CartController.post",
				"src/cart/cart.mjs", "new Cart",
				"src/index.js", "<anonymous>",
				"src/cart/service.js", "CartService.add",
				"node_modules/express/lib/router/layer.js", "Layer.handle",
				"src/cart/routes.js", "<anonymous>",
				"index", "Promise.all",
				"node:internal/process/task_queues", "process.processTicksAndRejections",
				"<anonymous>", "Array.map",
				"<anonymous>", "<anonymous>")},
		{"nested dependencies address the innermost", "    at parse (/app/node_modules/a/node_modules/b/index.js:1:2)",
			frames(StackJS, "node_modules/b/index.js", "parse")},
		{"an unclosed location is kept", "    at parse (/app/src/parse.js:1:2",
			frames(StackJS, "src/parse.js", "parse")},
		{"node's own code before the node: scheme", `    at emitOne (events.js:116:13)
    at process._tickCallback (internal/process/next_tick.js:68:7)
    at Socket.emit (node:events:517:28)`,
			frames(StackJS,
				"node:events", "emitOne",
				"node:internal/process/next_tick", "process._tickCallback",
				"node:events", "Socket.emit")},
		{"prose after at is not a frame", "at least 3 items (max 5)\nat startup\nat parse ()\nat sign (@) expected\n" +
			"at checkout (final step)\nat Checkout (see above)\nat 10:11:12 the cart (id 7) was locked\nat 10:11:12", nil},
		{"function@location, as Firefox and Safari write it", `addItem@https://shop.example.invalid/static/js/cart.js:42:17
async*post@https://shop.example.invalid/static/js/api.js:17:5
@https://shop.example.invalid/static/js/main.js:5:1
CartService.prototype.total@https://shop.example.invalid/static/js/cart.js:60:9`,
			frames(StackJS,
				"/static/js/cart.js", "addItem",
				"/static/js/api.js", "post",
				"/static/js/main.js", "<anonymous>",
				"/static/js/cart.js", "CartService.prototype.total")},
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
				"lib/python/json/decoder.py", "raw_decode",
				"site-packages/sqlalchemy/orm/session.py", "commit",
				"shop/cart.py", "add_item",
				"shop/api.py", "post")},
		{"module-level code and a frame without source", `Traceback (most recent call last):
  File "/app/shop/main.py", line 3
  File "<frozen importlib._bootstrap>", line 241, in _call_with_frames_removed
  File "/usr/lib/python3/dist-packages/yaml/loader.py", line 9, in load`,
			frames(StackPython,
				"dist-packages/yaml/loader.py", "load",
				"<frozen importlib._bootstrap>", "_call_with_frames_removed",
				"shop/main.py", "<module>")},
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
			frames(StackPython, "shop/cart.py", "add_item")},
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
			frames(StackPython, "shop/cart.py", "add_item", "shop/tasks.py", "run")},
		{"a group without sub-exceptions keeps its own frames", `  + Exception Group Traceback (most recent call last):
  |   File "/app/shop/api.py", line 17, in post
  |     raise ExceptionGroup("none", [])
  +------------------------------------`,
			frames(StackPython, "shop/api.py", "post")},
		{"a line in another notation is not a frame", `  File "/app/shop/cart.py", line 42, in add_item
    session.commit()
main.run()
    at com.acme.shop.CartService.addItem(CartService.java:42)`,
			frames(StackPython, "shop/cart.py", "add_item")},
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
	if builtinInApp(StackFrame{Module: "com.acme.shop.Cart", Syntax: StackSyntax(len(stackNotations))}) ||
		builtinInApp(StackFrame{Module: "com.acme.shop.Cart", Syntax: -1}) {
		t.Error("a frame of an unknown notation must not be in-app")
	}
}

// A frame's module is the same wherever the code is deployed: each group
// lists one file as different hosts, releases and operating systems name it.
func TestSourceModulesAreDeployIndependent(t *testing.T) {
	py := func(path string) string { return `File "` + path + `", line 42, in add_item` }
	js := func(loc string) string { return "at addItem (" + loc + ":42:17)" }
	merge := map[string][]string{
		"shop/cart.py": {
			py("/app/shop/cart.py"), py("/srv/releases/20261126101112/shop/cart.py"), py("/var/task/shop/cart.py"),
			py("/home/deploy/shop/cart.py"), py(`C:\app\shop\cart.py`), py("/usr/src/app/shop/cart.py"),
			py("/srv/www/checkout/current/shop/cart.py"), py("/opt/shop/cart.py"), py("./shop/cart.py"),
			py("/data/builds/3f2b8c1e9d/shop/cart.py"), py("/opt/apps/v2.14.0-rc.1/shop/cart.py"),
		},
		"lib/python/json/decoder.py": {
			py("/usr/lib/python3.11/json/decoder.py"), py("/usr/local/lib/python3.12/json/decoder.py"),
			py("/opt/conda/lib64/python3.9/json/decoder.py"), py(`C:\Python311\Lib\json\decoder.py`),
		},
		"site-packages/sqlalchemy/orm/session.py": {
			py("/usr/lib/python3.11/site-packages/sqlalchemy/orm/session.py"),
			py("/app/.venv/lib/python3.12/site-packages/sqlalchemy/orm/session.py"),
			py(`C:\app\venv\Lib\site-packages\sqlalchemy\orm\session.py`),
		},
		"src/cart/service.js": {
			js("/app/src/cart/service.js"), js("/var/task/src/cart/service.js"), js("file:///usr/src/app/src/cart/service.js"),
			js("/srv/www/shop/releases/a1b2c3d4e5/src/cart/service.js"), js(`C:\app\src\cart\service.js`),
			js("/workspace/src/cart/service.js"), js("/home/site/wwwroot/src/cart/service.js"),
		},
		"node_modules/express/lib/router/layer.js": {
			js("/app/node_modules/express/lib/router/layer.js"), js("/var/task/node_modules/express/lib/router/layer.js"),
			js(`C:\app\node_modules\express\lib\router\layer.js`),
			js("/app/node_modules/.pnpm/express@4.19.2/node_modules/express/lib/router/layer.js"),
		},
		"/static/js/main.js": {
			js("https://shop.example.invalid/static/js/main.8f3a2b1c.js"),
			js("https://staging.shop.example.invalid:8443/static/js/main-0a1b2c3d4e5f.js?v=7"),
			js("http://localhost:3000/static/js/main.js"),
		},
		"/data/apps/shop/cart.py": {py("/data/apps/shop/cart.py")}, // an unknown root stays
		// A directory of hex letters without a digit is a word, not a hash.
		"defaced/shop/cart.py": {py("/srv/defaced/shop/cart.py")},
		"index.js":             {js("/app/index.js"), js("/var/task/index.js")},
	}
	for want, lines := range merge {
		for _, line := range lines {
			got := ParseStackFrames(line)
			if len(got) != 1 || got[0].Module != want {
				t.Errorf("%s: module %+v, want %q", line, got, want)
			}
		}
	}
	split := [][2]string{
		{py("/app/shop/cart.py"), py("/app/shop/checkout.py")},
		{py("/app/shop/cart.py"), py("/app/billing/cart.py")},
		{py("/app/shop/json/decoder.py"), py("/usr/lib/python3.11/json/decoder.py")},
		{py("/app/shop/session.py"), py("/app/.venv/lib/python3.12/site-packages/shop/session.py")},
		{js("/app/src/cart/service.js"), js("/app/src/order/service.js")},
		{js("/app/src/cart/service.js"), js("/app/node_modules/cart/service.js")},
		{js("/app/events.js"), js("events.js")},
		{js("https://shop.example.invalid/static/js/main.8f3a2b1c.js"), js("https://shop.example.invalid/static/js/vendor.8f3a2b1c.js")},
	}
	for _, pair := range split {
		a, b := ParseStackFrames(pair[0]), ParseStackFrames(pair[1])
		if len(a) != 1 || len(b) != 1 || a[0].Module == b[0].Module {
			t.Errorf("%s and %s must differ: %+v, %+v", pair[0], pair[1], a, b)
		}
	}
	for i, root := range deployRoots {
		for _, earlier := range deployRoots[:i] {
			if strings.HasPrefix(root, earlier) {
				t.Errorf("deploy root %s is never reached: %s before it matches first", root, earlier)
			}
		}
	}
}

func TestParseStackFramesLineBreaks(t *testing.T) {
	want := frames(StackJava, "com.example.shop.Api", "post", "com.example.shop.Server", "handle")
	for name, stack := range map[string]string{
		"line feeds":       "\tat com.example.shop.Api.post(Api.java:12)\n\tat com.example.shop.Server.handle(Server.java:80)\n",
		"carriage returns": "\tat com.example.shop.Api.post(Api.java:12)\r\tat com.example.shop.Server.handle(Server.java:80)\r",
		"both":             "\tat com.example.shop.Api.post(Api.java:12)\r\n\tat com.example.shop.Server.handle(Server.java:80)\r\n",
		"escaped":          `java.lang.IllegalStateException: locked\n\tat com.example.shop.Api.post(Api.java:12)\n\tat com.example.shop.Server.handle(Server.java:80)`,
		"escaped, spaces":  `java.lang.IllegalStateException: locked\n    at com.example.shop.Api.post(Api.java:12)\n    at com.example.shop.Server.handle(Server.java:80)`,
		"escaped pair":     `java.lang.IllegalStateException: locked\r\n\tat com.example.shop.Api.post(Api.java:12)\r\n\tat com.example.shop.Server.handle(Server.java:80)`,
	} {
		t.Run(name, func(t *testing.T) { assertFrames(t, stack, want) })
	}
	assertFrames(t, `goroutine 1 [running]:\nmain.run(0x1)\n\t/src/shop/main.go:12 +0x25\nmain.main()\n\t/src/shop/main.go:5 +0x1d`,
		frames(StackGo, "main", "run", "main", "main"))
	// A backslash and an n are text where nothing shows they are a break,
	// and wherever the text has real line breaks: here part of a path.
	assertFrames(t, `at addItem (C:\app\node_modules\cart\index.js:42:17)`,
		frames(StackJS, "node_modules/cart/index.js", "addItem"))
	assertFrames(t, "   at Example.Shop.Cart.Add(String sku) in C:\\src\\new\\Cart.cs:line 4\n   at Example.Shop.Api.Post()",
		frames(StackDotNet, "Example.Shop.Cart", "Add", "Example.Shop.Api", "Post"))
}

// goStdlib must be the top-level names of the standard library, sorted: the
// in-app rule looks a module's first element up in it.
func TestGoStdlibNamesAreSorted(t *testing.T) {
	if !sort.StringsAreSorted(goStdlib) {
		t.Error("goStdlib must stay sorted")
	}
	for _, name := range []string{"archive", "net", "runtime", "weak"} {
		if !isGoStdlib(name + "/x") {
			t.Errorf("%s is not taken for the standard library", name)
		}
	}
	for _, module := range []string{"main", "shop/net", "nets", "github.com/example/shop", ""} {
		if isGoStdlib(module) {
			t.Errorf("%q is taken for the standard library", module)
		}
	}
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

func TestBuiltinInApp(t *testing.T) {
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
		{StackFrame{Module: "com.example.shop.Cart$$anon$1", Syntax: StackJava}, true},
		{StackFrame{Module: "", Function: "lambda_method", Syntax: StackDotNet}, false},

		{StackFrame{Module: "main", Syntax: StackGo}, true},
		{StackFrame{Module: "github.com/acme/shop/cart", Syntax: StackGo}, true},
		{StackFrame{Module: "shop.example.invalid/cart", Syntax: StackGo}, true},
		{StackFrame{Module: "shop/internal/cart", Syntax: StackGo}, true},
		{StackFrame{Module: "cart", Syntax: StackGo}, true},
		{StackFrame{Module: "mainframe", Syntax: StackGo}, true},
		{StackFrame{Module: "github.com/gin-gonic/gin", Syntax: StackGo, Dependency: true}, false},
		{StackFrame{Module: "shop/internal/cart", Syntax: StackGo, Dependency: true}, false},
		{StackFrame{Module: "internal/poll", Syntax: StackGo}, false},
		{StackFrame{Module: "sync/atomic", Syntax: StackGo}, false},
		{StackFrame{Module: "runtime", Syntax: StackGo}, false},
		{StackFrame{Module: "net/http", Syntax: StackGo}, false},
		{StackFrame{Module: "encoding/json.v2", Syntax: StackGo}, false},
		{StackFrame{Module: "golang.org/x/sync/errgroup", Syntax: StackGo}, false},
		{StackFrame{Module: "google.golang.org/grpc", Syntax: StackGo}, false},
		{StackFrame{Module: "", Function: "panic", Syntax: StackGo}, false},

		{StackFrame{Module: "src/cart/service.js", Syntax: StackJS}, true},
		{StackFrame{Module: "index.js", Syntax: StackJS}, true},
		{StackFrame{Module: "/static/js/cart.js", Syntax: StackJS}, true},
		{StackFrame{Module: "lib/node_modules.js", Syntax: StackJS}, true},
		{StackFrame{Module: "node_modules/express/lib/router/layer.js", Syntax: StackJS}, false},
		{StackFrame{Module: "node:internal/process/task_queues", Syntax: StackJS}, false},
		{StackFrame{Module: "node:events", Syntax: StackJS}, false},
		{StackFrame{Module: "<anonymous>", Syntax: StackJS}, false},
		{StackFrame{Module: "native", Syntax: StackJS}, false},
		{StackFrame{Module: "index", Syntax: StackJS}, false},

		{StackFrame{Module: "shop/cart.py", Syntax: StackPython}, true},
		{StackFrame{Module: "cart.py", Syntax: StackPython}, true},
		{StackFrame{Module: "shop/lib/python/cart.py", Syntax: StackPython}, true},
		{StackFrame{Module: "site-packages/sqlalchemy/orm/session.py", Syntax: StackPython}, false},
		{StackFrame{Module: "dist-packages/yaml/loader.py", Syntax: StackPython}, false},
		{StackFrame{Module: "lib/python/json/decoder.py", Syntax: StackPython}, false},
		{StackFrame{Module: "<frozen importlib._bootstrap>", Syntax: StackPython}, false},
	}
	for _, tc := range cases {
		if got := builtinInApp(tc.frame); got != tc.inApp {
			t.Errorf("InApp(%+v) = %v, want %v", tc.frame, got, tc.inApp)
		}
	}
}

// Every entry of a vendor table takes its frames out of the application's,
// the last as much as the first, and a library frame between two of the
// application's does not count toward a key.
func TestVendorTablesApplyWhole(t *testing.T) {
	tables := []struct {
		syntax   StackSyntax
		prefixes []string
	}{
		{StackJava, javaVendorPrefixes}, {StackDotNet, dotnetVendorPrefixes}, {StackGo, goVendorPrefixes},
		{StackPython, pythonPackageDirs},
	}
	for _, table := range tables {
		for _, prefix := range table.prefixes {
			if f := (StackFrame{Module: prefix + "x", Function: "f", Syntax: table.syntax}); builtinInApp(f) {
				t.Errorf("%+v counts as application code", f)
			}
		}
	}
	for _, name := range goStdlib {
		if f := (StackFrame{Module: name + "/x", Function: "f", Syntax: StackGo}); builtinInApp(f) {
			t.Errorf("%+v counts as application code", f)
		}
	}
	fp := mustFingerprint(t, SpanException{Type: "T", StackTrace: `   at Newtonsoft.Json.JsonConvert.DeserializeObject(String value)
   at Example.Shop.Cart.Load(String id)
   at Newtonsoft.Json.Linq.JToken.ToObject(Type objectType)
   at Example.Shop.Api.Get(String id)`})
	want := frames(StackDotNet, "Newtonsoft.Json.JsonConvert", "DeserializeObject", "Example.Shop.Cart", "Load", "Example.Shop.Api", "Get")
	if fp.Rung != RungAppFrames || !reflect.DeepEqual(fp.Frames, want) {
		t.Errorf("rung %v frames %+v", fp.Rung, fp.Frames)
	}
}

// Every listed pass-through function is one, bare and as a method.
func TestPassThroughListsApplyWhole(t *testing.T) {
	for syntax, notation := range stackNotations {
		for _, name := range notation.passThrough {
			for _, function := range []string{name, "Type." + name} {
				if f := (StackFrame{Module: "m", Function: function, Syntax: StackSyntax(syntax)}); !isPassThrough(f) {
					t.Errorf("%+v is not pass-through", f)
				}
			}
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
			builtinInApp(fr)
		}
	})
}

func TestJavaFrameName(t *testing.T) {
	cases := []struct {
		line, name string
		ok         bool
	}{
		{"at a.b(C.java:1)", "a.b", true},
		{"a.b(C.java:1)", "a.b", true},
		{"a.b (C.java)", "a.b", true},
		{"at a.b(Native Method)", "a.b", true},
		{"at a.b(Unknown Source) ~[x.jar:1]", "a.b", true},
		{"at a.b(<generated>)x", "a.b", true},
		{"at a..b(C.java:1)", "a..b", true},
		{"at java.base/a.B.<init>(B.java:1)", "java.base/a.B.<init>", true},
		{"at a@1.b(B.java:1)", "a@1.b", true},
		{"ata.b(C.java:1)", "ata.b", true},
		{"at a.b(C.kt:12)(D.java:1)", "a.b", true},
		{"at a.b(C-1.java:1)", "a.b", true},
		// No frame: nothing in the parentheses, no class, no method, more
		// than one space before the location, a second "at".
		{"", "", false},
		{"(", "", false},
		{"a.b(", "", false},
		{"a.b  (C.java:1)", "", false},
		{"at at a.b(C.java:1)", "", false},
		{"at  a.b(C.java:1)", "", false},
		{"at .b(C.java:1)", "", false},
		{"at a.(C.java:1)", "", false},
		{"at b(C.java:1)", "", false},
		// A method name holds no separator of the class path.
		{"at app//a.B$1.c-d(B.java:1)", "", false},
		{"at a.b/c(B.java:1)", "", false},
		{"at a.b@1(B.java:1)", "", false},
		{"at a.é(B.java:1)", "", false},
		// The parentheses hold a source location, not parameters.
		{"at a.b(String s)", "", false},
		{"at a.b(B.java:x)", "", false},
		{"at a.b((B.java:1)", "", false},
		{"at a.b(B.java:1", "", false},
	}
	for _, tc := range cases {
		if name, ok := javaFrameName(tc.line); name != tc.name || ok != tc.ok {
			t.Errorf("javaFrameName(%q) = %q, %v, want %q, %v", tc.line, name, ok, tc.name, tc.ok)
		}
	}
}

func TestRootCauseBoundaries(t *testing.T) {
	cases := []struct {
		name, stack string
		want        []StackFrame
		typ         string
	}{
		// A trace whose indentation was stripped ends at its first
		// suppressed block: nothing shows where the block does.
		{"java: a suppressed block without indentation",
			"a.B: x\nat a.B.b(B.java:1)\nSuppressed: c.D: y\nat c.D.d(D.java:2)\nCaused by: e.F: z\nat e.F.f(F.java:3)",
			frames(StackJava, "a.B", "b"), ""},
		// A blank line does not end a suppressed block: what follows it at
		// the block's depth is still the suppressed exception's.
		{"java: a blank line inside a suppressed block",
			"a.B: x\n\tat a.B.b(B.java:1)\n\tSuppressed: c.D: y\n\t\tat c.D.d(D.java:2)\n\n\t\tat c.D.e(D.java:3)\n" +
				"\tCaused by: e.F: z\n\t\tat e.F.f(F.java:4)",
			frames(StackJava, "a.B", "b"), ""},
		{"dotnet: two inner exceptions introduced on one line",
			"A.Outer: x ---> B.Middle: y ---> C.Inner: z\n   at N.S.Type.Method(String s)",
			frames(StackDotNet, "N.S.Type", "Method"), "C.Inner"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, typ := parseStack(tc.stack)
			if !reflect.DeepEqual(got, tc.want) || typ != tc.typ {
				t.Errorf("frames %+v type %q\nwant %+v %q", got, typ, tc.want, tc.typ)
			}
		})
	}
}
