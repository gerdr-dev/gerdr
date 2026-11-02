//go:build !darwin

package awake

import (
	"errors"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// 合盖不睡只做了 macOS（pmset）。

func lidReady() bool               { return false }
func lidOn(string) (func(), error) { return nil, errors.New(i18n.S("只支持 macOS", "macOS only")) }
func lidCleanup(string)            {}
func battery() Battery             { return Battery{Percent: -1} }
func SetupLid() error              { return errors.New(i18n.S("只支持 macOS", "macOS only")) }
