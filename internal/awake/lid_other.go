//go:build !darwin

package awake

import "errors"

// 合盖不睡只做了 macOS（pmset）。

func lidReady() bool               { return false }
func lidOn(string) (func(), error) { return nil, errors.New("只支持 macOS") }
func lidCleanup(string)            {}
func battLow() bool                { return false }
func SetupLid() error              { return errors.New("只支持 macOS") }
