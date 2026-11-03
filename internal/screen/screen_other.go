//go:build !darwin

package screen

func GetStatus() Status                       { return Status{} }
func RequestAccess() Status                   { return Status{} }
func Capture(int, int) ([]byte, error)        { return nil, ErrUnsupported }
func click(float64, float64, bool, int) error { return ErrUnsupported }
func scroll(float64, float64, int) error      { return ErrUnsupported }
func typeText(string) error                   { return ErrUnsupported }
func pressKey(string, []string) error         { return ErrUnsupported }
