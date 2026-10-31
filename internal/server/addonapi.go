package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gerdr-dev/gerdr/internal/addons"
)

// Add-ons (internal/addons): the installed ones, their files, and the stores they come from.
//
//	GET    /api/addons                         installed add-ons, built, with their service status
//	POST   /api/addons/{id}/message            to the add-on's service (g.service.send)
//	PUT    /api/addons/{id}/enabled            {"enabled": bool}
//	PUT    /api/addons/{id}/settings           {"values": {key: value | null}} (null = back to the default)
//	DELETE /api/addons/{id}                    uninstall (delete its folder)
//	GET    /api/addons/{id}/files/{path...}    a file of its folder (g.asset, the icon)
//	GET    /api/addon-stores                   the stores and what they offer, from the cache (no network)
//	POST   /api/addon-stores                   {"source"}: add one and download it
//	DELETE /api/addon-stores?source=           forget one
//	POST   /api/addon-stores/refresh           {"source"}: download again
//	POST   /api/addon-stores/install           {"source","id","force"}: install or update; 409 code=conflict when
//	                                           it would overwrite a folder not from a store or changed locally
//	GET    /api/addon-stores/file?source=&id=&path=   a file of an add-on in a store (its icon)
//
// Changes to the add-on directory reach the UI through the watcher ("addons" broadcast); store changes are broadcast
// here as "addon_stores" so every open window's store tab follows.
func (s *Server) addonRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/addons", s.addonList)
	mux.HandleFunc("POST /api/addons/{id}/message", s.addonMessage)
	mux.HandleFunc("PUT /api/addons/{id}/enabled", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Enabled bool `json:"enabled"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if err := s.Addons.SetEnabled(r.PathValue("id"), b.Enabled); err != nil {
			httpError(w, 500, err.Error())
			return
		}
		s.addonsChanged()
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("PUT /api/addons/{id}/settings", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Values map[string]any `json:"values"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if err := s.Addons.SetSettings(r.PathValue("id"), b.Values); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		s.Hub.Broadcast("addons", nil) // right away, not at the watcher's next tick
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("DELETE /api/addons/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Addons.Uninstall(r.PathValue("id")); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		s.addonsChanged()
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/addons/{id}/files/{path...}", func(w http.ResponseWriter, r *http.Request) {
		p, err := s.Addons.File(r.PathValue("id"), r.PathValue("path"))
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		serveRaw(w, r, p, false)
	})

	mux.HandleFunc("GET /api/addon-stores", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.Addons.Stores()) })
	mux.HandleFunc("POST /api/addon-stores", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Source string `json:"source"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		src, err := s.Addons.AddStore(r.Context(), b.Source)
		if src != "" {
			s.Hub.Broadcast("addon_stores", nil) // added even when the download failed: its error shows on it
		}
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"source": src})
	})
	mux.HandleFunc("DELETE /api/addon-stores", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Addons.RemoveStore(r.URL.Query().Get("source")); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		s.Hub.Broadcast("addon_stores", nil)
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/addon-stores/refresh", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Source string `json:"source"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		err := s.Addons.RefreshStore(r.Context(), b.Source)
		s.Hub.Broadcast("addon_stores", nil)
		if err != nil {
			httpError(w, 502, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/addon-stores/install", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Source string `json:"source"`
			ID     string `json:"id"`
			Force  bool   `json:"force"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if err := s.Addons.InstallFromStore(b.Source, b.ID, b.Force); err != nil {
			if errors.Is(err, addons.ErrConflict) {
				writeJSONStatus(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": "conflict"})
				return
			}
			httpError(w, 400, err.Error())
			return
		}
		s.addonsChanged()
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/addon-stores/file", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		p, err := s.Addons.StoreFile(q.Get("source"), q.Get("id"), q.Get("path"))
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		serveRaw(w, r, p, false)
	})
}

// addonsChanged: tell the windows and the services now rather than at the watcher's next tick, so the button
// pressed shows its result right away.
func (s *Server) addonsChanged() {
	s.Hub.Broadcast("addons", nil)
	if s.AddonSvc != nil {
		go s.AddonSvc.Sync()
	}
}

func (s *Server) addonList(w http.ResponseWriter, r *http.Request) {
	type view struct {
		*addons.Addon
		Service *addons.ServiceStatus `json:"service,omitempty"`
	}
	out := []view{}
	for _, a := range s.Addons.List() {
		v := view{Addon: a}
		if s.AddonSvc != nil {
			v.Service = s.AddonSvc.Status(a.ID)
		}
		out = append(out, v)
	}
	writeJSON(w, out)
}

func (s *Server) addonMessage(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Data json.RawMessage `json:"data"`
	}
	if err := decode(r, &b); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if len(b.Data) == 0 {
		b.Data = json.RawMessage("null")
	}
	line, _ := json.Marshal(map[string]any{"type": "message", "data": b.Data})
	if err := s.AddonSvc.Send(r.PathValue("id"), line); err != nil {
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
