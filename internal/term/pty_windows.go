package term

import (
	"errors"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Windows 上要走 ConPTY，还没做。
func (t *Term) start(env []string, cols, rows uint16) error {
	return errors.New(i18n.S("Windows 上还不支持终端", "Terminals aren't supported on Windows yet"))
}

func (t *Term) Resize(cols, rows uint16) error { return nil }

func (t *Term) kill() {}

func (t *Term) width() int { return 0 }

func (t *Term) LiveCwd() string { return "" }
