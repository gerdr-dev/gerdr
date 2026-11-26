package chromeext

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// IsHostInvocation: Chrome starts the native host with the calling extension's origin (chrome-extension://<id>/) as
// the first argument (on Windows followed by --parent-window=…). The gerdr program then runs as the host (RunHost)
// instead of its command line.
func IsHostInvocation(args []string) bool {
	return len(args) > 1 && strings.HasPrefix(args[1], "chrome-extension://")
}

// RunHost is the native host: Chrome's native messaging on stdin / stdout (each message a 4-byte little-endian
// length + JSON), relayed message by message to the running Gerdr's WebSocket (GET /api/browser/ext), and back.
// Gerdr restarting: dial again every 2 s (dial re-reads where Gerdr is each time). Gerdr gone for ~10 s: exit, and
// the extension starts a host again within 30 s from whatever manifest is current then — a host that kept retrying
// would keep a dev instance's launcher, or an old App's path, alive for as long as Chrome runs. Chrome closing stdin
// ends it too.
//
// log records each step (started, connected, the connection lost and why, dial failures, exit): Chrome drops a native
// host's stderr, and a profile that stopped reconnecting left no trace anywhere (2026-11-25, its host gone at 17:34 and
// nothing until the user reloaded the extension the next day). nil logs nothing.
func RunHost(origin string, dial func() (url string, header http.Header, err error), log *slog.Logger) error {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(origin, "chrome-extension://"), "/")
	if !slices.Contains(ExtensionIDs, id) {
		log.Warn("browser host: unknown extension", "origin", origin)
		return fmt.Errorf("unknown extension %q", origin)
	}
	log = log.With("pid", os.Getpid())
	log.Info("browser host: started by Chrome", "extension", id)
	in := bufio.NewReader(os.Stdin)
	var outMu sync.Mutex
	writeOut := func(b []byte) error {
		// Chrome takes at most 1 MB per message to the extension
		if len(b) > 1<<20 {
			return errors.New("message to the extension is over 1MB")
		}
		outMu.Lock()
		defer outMu.Unlock()
		var n [4]byte
		binary.LittleEndian.PutUint32(n[:], uint32(len(b)))
		if _, err := os.Stdout.Write(n[:]); err != nil {
			return err
		}
		_, err := os.Stdout.Write(b)
		return err
	}

	var mu sync.Mutex
	var ws *websocket.Conn
	var wm sync.Mutex
	// from Chrome: the extension's answers and events, to Gerdr; dropped while not connected (Gerdr's side of those
	// requests has failed anyway)
	stdinDone := make(chan error, 1)
	go func() {
		for {
			var n [4]byte
			if _, err := io.ReadFull(in, n[:]); err != nil {
				stdinDone <- err
				return
			}
			size := binary.LittleEndian.Uint32(n[:])
			if size > 64<<20 {
				stdinDone <- errors.New("message from the extension is too large")
				return
			}
			b := make([]byte, size)
			if _, err := io.ReadFull(in, b); err != nil {
				stdinDone <- err
				return
			}
			mu.Lock()
			c := ws
			mu.Unlock()
			if c != nil {
				wm.Lock()
				c.SetWriteDeadline(time.Now().Add(30 * time.Second))
				c.WriteMessage(websocket.TextMessage, b)
				wm.Unlock()
			}
		}
	}()
	done := func(err error) error {
		if err == io.EOF {
			// the extension let go of its port (reloaded, its profile closed, Chrome quit)
			log.Info("browser host: Chrome closed the port, exiting")
			return nil
		}
		log.Warn("browser host: reading from Chrome failed, exiting", "err", err)
		return err
	}

	failures := 0
	for {
		select {
		case err := <-stdinDone:
			return done(err)
		default:
		}
		if failures >= 5 {
			log.Warn("browser host: Gerdr not reachable after 5 tries, exiting (the extension starts a new host within 30 s)")
			return errors.New("gerdr is not reachable")
		}
		var c *websocket.Conn
		u, header, err := dial()
		if err == nil {
			d := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
			c, _, err = d.Dial(u+"?ext="+id, header)
		}
		if err != nil {
			failures++
			log.Info("browser host: can't reach Gerdr", "try", failures, "err", err)
			select {
			case err := <-stdinDone:
				return done(err)
			case <-time.After(2 * time.Second):
			}
			continue
		}
		failures = 0
		log.Info("browser host: connected to Gerdr")
		c.SetReadLimit(1 << 20)
		mu.Lock()
		ws = c
		mu.Unlock()
		// from Gerdr: requests, to the extension
		readDone := make(chan error, 1)
		go func() {
			for {
				_, b, err := c.ReadMessage()
				if err != nil {
					readDone <- err
					return
				}
				if err := writeOut(b); err != nil {
					readDone <- fmt.Errorf("writing to Chrome: %w", err)
					return
				}
			}
		}()
		select {
		case err := <-stdinDone:
			c.Close()
			return done(err)
		case err := <-readDone:
			log.Info("browser host: lost Gerdr, dialing again", "err", err)
		}
		mu.Lock()
		ws = nil
		mu.Unlock()
		c.Close()
	}
}
