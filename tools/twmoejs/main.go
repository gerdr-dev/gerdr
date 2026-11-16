// Command twmoejs compiles the UI's stylesheet with Tailwind's own compiler running in moejs, the experiment behind
// docs/dev/TAILWIND-SERVER.md: does Tailwind's core run in our embedded JS engine, does it give the same CSS as
// Node, and how long does it take.
//
//	mkdir -p /tmp/tw && cd /tmp/tw && npm pack tailwindcss@4.1.11 && tar xzf tailwindcss-4.1.11.tgz
//	go run ./tools/twmoejs -pkg /tmp/tw/package -page http://127.0.0.1:7870/ -out /tmp/tw/out.css
//
// -page takes the stylesheet the running Gerdr serves (with the backend's @source inline lines in it); -css takes a
// file instead. Compare with Node by running the same dist/lib.js there on the same input: on 2026-11-16 the two
// outputs were byte-identical.
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/MoeForge/moejs"
)

// The browser build prepends this when the sheet has no @import (Gerdr's backend strips it, runtime.go browserCSS);
// without it only the theme variables come out, no utilities.
const tailwindImport = `@import "tailwindcss";`

var styleRE = regexp.MustCompile(`(?s)<style type="text/tailwindcss" id="__gerdr_css__">(.*?)</style>`)

func main() {
	pkg := flag.String("pkg", "", "unpacked npm tailwindcss package (the dir with dist/lib.js and index.css)")
	page := flag.String("page", "", "URL of a Gerdr page to take the stylesheet from")
	cssFile := flag.String("css", "", "stylesheet file (instead of -page)")
	out := flag.String("out", "", "write the generated CSS here")
	runs := flag.Int("n", 5, "how many times to compile + build")
	flag.Parse()
	if *pkg == "" || (*page == "") == (*cssFile == "") {
		flag.Usage()
		os.Exit(2)
	}
	input, err := readInput(*page, *cssFile)
	must(err)
	if !strings.Contains(input, "@import") {
		input = tailwindImport + input
	}
	lib, err := os.ReadFile(filepath.Join(*pkg, "dist", "lib.js"))
	must(err)

	// lib.js is a CommonJS bundle with no require() at all: a module/exports shell is enough.
	src := "const module = {exports: {}};\n(function(module, exports){\n" + string(lib) + "\n})(module, module.exports);\n" + `
const tw = module.exports;
let compiler;
export async function setup(css) {
  compiler = await tw.compile(css, {
    base: "/",
    loadStylesheet: async (id, base) => {
      const f = id === "tailwindcss" ? "index.css" : id.replace(/^tailwindcss\//, "").replace(/^\.\//, "");
      return { path: f, base: "/", content: host.read(f.endsWith(".css") ? f : f + ".css") };
    },
  });
}
export function build(candidates) { return compiler.build(candidates); }
`
	t := time.Now()
	mod, err := moejs.Compile("tailwind.mjs", src)
	must(err)
	fmt.Println("compile tailwind module:", time.Since(t))

	rt := moejs.NewRuntime(moejs.Options{})
	must(rt.SetGlobal("host", map[string]any{"read": moejs.NativeFunc(func(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name, err := r.ToString(moejs.Arg(args, 0))
		if err != nil {
			return moejs.Undefined(), err
		}
		b, err := os.ReadFile(filepath.Join(*pkg, filepath.Clean("/"+name.GoString())))
		if err != nil {
			return moejs.Undefined(), err
		}
		return moejs.String(string(b)), nil
	})}))
	t = time.Now()
	must(rt.Load(mod))
	fmt.Println("load:", time.Since(t))
	setup, err := mod.Hook("setup")
	must(err)
	build, err := mod.Hook("build")
	must(err)

	var css string
	for i := 0; i < *runs; i++ {
		t = time.Now()
		p, err := rt.Call(setup, moejs.String(input))
		must(err)
		if st, v, _ := moejs.PromiseResult(p); st != moejs.PromiseFulfilled {
			s, _ := rt.ToGo(v)
			must(fmt.Errorf("compile: %v", s))
		}
		tc := time.Since(t)
		t = time.Now()
		empty, err := rt.FromGo([]any{})
		must(err)
		res, err := rt.Call(build, empty)
		must(err)
		must(rt.Unmarshal(res, &css))
		fmt.Printf("compile %v  build %v  %d bytes\n", tc, time.Since(t), len(css))
	}
	if *out != "" {
		must(os.WriteFile(*out, []byte(css), 0o644))
	}
	// What a class nobody has used yet costs on a warm compiler (incremental build).
	t = time.Now()
	more, err := rt.FromGo([]any{"pb-[7.25px]", "w-[123px]"})
	must(err)
	res, err := rt.Call(build, more)
	must(err)
	must(rt.Unmarshal(res, &css))
	fmt.Printf("build +2 new classes %v  %d bytes\n", time.Since(t), len(css))
}

func readInput(page, file string) (string, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		return string(b), err
	}
	resp, err := http.Get(page)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	m := styleRE.FindSubmatch(b)
	if m == nil {
		return "", fmt.Errorf("%s: no __gerdr_css__ style in the page", page)
	}
	return strings.ReplaceAll(string(m[1]), `<\/`, "</"), nil // undo html.go escapeStyle
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
