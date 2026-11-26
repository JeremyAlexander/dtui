package catalog

import (
	"fmt"
	"strings"
	"testing"
)

// benchJavaEvent is a 70-frame exception of a servlet application: a few
// application frames on top of the framework's dispatch and filter chain.
func benchJavaEvent() SpanException {
	var b strings.Builder
	b.WriteString("java.lang.IllegalStateException: Cart 7 is locked by order 3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f\n")
	frame := func(name, file string, line int) { fmt.Fprintf(&b, "\tat %s(%s.java:%d)\n", name, file, line) }
	frame("com.example.shop.cart.CartService.addItem", "CartService", 42)
	b.WriteString("\tat com.example.shop.cart.CartService$$FastClassBySpringCGLIB$$1a2b3c4d.invoke(<generated>)\n")
	frame("org.springframework.cglib.proxy.MethodProxy.invoke", "MethodProxy", 218)
	b.WriteString("\tat com.example.shop.cart.CartService$$EnhancerBySpringCGLIB$$5e6f7a8b.addItem(<generated>)\n")
	frame("com.example.shop.cart.CartController.post", "CartController", 17)
	b.WriteString("\tat jdk.internal.reflect.GeneratedMethodAccessor112.invoke(Unknown Source)\n")
	frame("java.base/java.lang.reflect.Method.invoke", "Method", 568)
	for i := range 20 {
		frame(fmt.Sprintf("org.springframework.web.servlet.mvc.method.Handler%d.invoke", i), "Handler", 100+i)
	}
	for i := range 20 {
		frame("org.apache.catalina.core.ApplicationFilterChain.internalDoFilter", "ApplicationFilterChain", 166)
		frame(fmt.Sprintf("org.springframework.web.filter.Filter%d.doFilter", i), "Filter", 200+i)
	}
	frame("org.apache.tomcat.util.net.SocketProcessorBase.run", "SocketProcessorBase", 52)
	frame("java.base/java.util.concurrent.ThreadPoolExecutor.runWorker", "ThreadPoolExecutor", 1136)
	frame("java.base/java.lang.Thread.run", "Thread", 833)
	return SpanException{
		Type:       "java.lang.IllegalStateException",
		Message:    "Cart 7 is locked by order 3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f",
		StackTrace: b.String(),
	}
}

func BenchmarkFingerprintExceptions(b *testing.B) {
	excs := []SpanException{benchJavaEvent()}
	if n := strings.Count(excs[0].StackTrace, "\n\tat "); n != 70 {
		b.Fatalf("the event has %d frames, want 70", n)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := FingerprintExceptions(excs, nil); !ok {
			b.Fatal("no fingerprint")
		}
	}
}

// A chain of 8,900 causes in which each shares one frame more with the
// exception around it: a megabyte of text, the most a trace is read of.
func BenchmarkParseStackFramesJavaChain(b *testing.B) {
	stack := javaChain(8900)
	if len(stack) < stackTextMax*9/10 || len(stack) > stackTextMax {
		b.Fatalf("the chain is %d bytes, want just under %d", len(stack), stackTextMax)
	}
	b.ReportAllocs()
	for b.Loop() {
		if len(ParseStackFrames(stack)) != 2 {
			b.Fatal("want the root cause's frame and the one all share")
		}
	}
}
