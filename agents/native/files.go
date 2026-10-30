package native

import (
	"os"
	"path/filepath"
)

// File access for read / write / edit: Options.Files takes the paths it owns (e.g. a working directory kept in
// object storage), the local disk the rest. Owned files carry a version: a write sends the version the model
// last read, so the store can refuse to overwrite a newer copy.

func (c *call) owned(p string) bool {
	f := c.r.s.a.opt.Files
	return f != nil && f.Owns(p)
}

// readFile reads p and remembers the version the model saw.
func (c *call) readFile(p string) ([]byte, error) {
	if c.owned(p) {
		b, v, err := c.r.s.a.opt.Files.Read(c.ctx, p)
		if err == nil {
			c.r.s.markRead(p, v)
		}
		return b, err
	}
	return os.ReadFile(p)
}

func (c *call) writeFile(p string, data []byte) error {
	if c.owned(p) {
		return c.r.s.a.opt.Files.Write(c.ctx, p, data, c.r.s.readVersion(p))
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// readOld is the current content for the write tool's diff (nil = no such file). Owned files are read through
// Files without touching the remembered version: overwriting a file the model never read sends "".
func (c *call) readOld(p string) *string {
	var b []byte
	var err error
	if c.owned(p) {
		b, _, err = c.r.s.a.opt.Files.Read(c.ctx, p)
	} else {
		b, err = os.ReadFile(p)
	}
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}
