package term

import "errors"

// Windows 上要走 ConPTY，还没做。
func (t *Term) start(env []string, cols, rows uint16) error {
	return errors.New("Windows 上还不支持终端")
}

func (t *Term) Resize(cols, rows uint16) error { return nil }

func (t *Term) kill() {}

func (t *Term) LiveCwd() string { return "" }
