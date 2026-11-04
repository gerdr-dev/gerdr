package server

import (
	"net/http"
	"net/http/pprof"
)

// debugRoutes: Go's profiler under /api/debug/pprof/, behind the same token as the rest of /api/ (GET only, so no
// X-Gerdr header is needed). The running service can sit at 150%+ CPU in garbage collection, and a released binary is
// stripped and has no other way in: macOS `sample` can't unwind goroutine stacks, so who allocates stays unknown.
// Read it from the machine itself:
//
//	go tool pprof -http=: "http://127.0.0.1:7870/api/debug/pprof/profile?seconds=20" (Authorization: Bearer <~/.gerdr/token>)
//
// e.g. curl -H "Authorization: Bearer $(cat ~/.gerdr/token)" -o cpu.pb.gz …/profile?seconds=20, then go tool pprof cpu.pb.gz;
// /allocs for what allocates, /goroutine?debug=2 for every stack.
func debugRoutes(mux *http.ServeMux) {
	// Index also serves /heap, /allocs, /goroutine… by name, reading it after "/debug/pprof/": hence the prefix strip
	mux.Handle("GET /api/debug/pprof/", http.StripPrefix("/api", http.HandlerFunc(pprof.Index)))
	mux.HandleFunc("GET /api/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /api/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /api/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /api/debug/pprof/trace", pprof.Trace)
}
