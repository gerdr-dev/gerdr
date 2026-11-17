package chromeext

import "golang.org/x/sys/windows/registry"

// setRegistry writes the default value of HKCU\<key> (where Chrome looks up a native host's manifest).
func setRegistry(key, value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, key, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue("", value)
}
