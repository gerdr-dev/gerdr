//go:build !darwin

package awake

import "errors"

// 合盖不睡只做了 macOS（pmset）。

func lidReady() bool               { return false }
func lidOn(string) (func(), error) { return nil, errors.New("只支持 macOS") }
func lidCleanup(string)            {}
func battery() Battery             { return Battery{Percent: -1} }
func SetupLid() error              { return errors.New("只支持 macOS") }
