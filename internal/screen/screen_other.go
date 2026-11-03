//go:build !darwin

package screen

import "image"

func Grab(int) (*image.RGBA, error)    { return nil, ErrUnsupported }
func Cursor() (float64, float64, bool) { return 0, 0, false }

func GetStatus() Status                       { return Status{} }
func RequestAccess() Status                   { return Status{} }
func click(float64, float64, bool, int) error { return ErrUnsupported }
func scroll(float64, float64, int) error      { return ErrUnsupported }
func typeText(string) error                   { return ErrUnsupported }
func pressKey(string, []string) error         { return ErrUnsupported }
