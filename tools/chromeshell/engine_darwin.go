package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// brand: who the browser process is to macOS. The engine clone carries it, so the Dock / ⌘Tab / menu bar /
// notifications say Gerdr, with Gerdr's icon, and the windows are plain --app windows of that app.
type brand struct {
	name string // CFBundleName: Dock, ⌘Tab, menu bar, notifications
	id   string // CFBundleIdentifier: its own, so macOS keeps its notification permission and Dock place apart from Chrome
	icns string // the icon; "" = keep the browser's
}

// prepareEngine makes a copy of the installed Chrome-family browser that is Gerdr to macOS, and returns its executable.
//
// Why a copy: what the Dock, ⌘Tab and the menu bar show for a process comes from its bundle's Info.plist, and macOS lets
// a process change only its *own* type (`lsappinfo setinfo` on another one is ignored). Run as installed, the browser
// is "Google Chrome" (a second Chrome icon if the user's own is open). Before this the window was an installed web app
// (an app shim with Gerdr's name) on a Dock-less copy; but Chrome draws its own buttons in an app's title bar (the app
// menu "⋮", translate) and no setting removes them. A --app window of a browser that is itself Gerdr has a bare title bar.
//
//   - APFS clone (`cp -c`): ~1.5s, shares blocks with the original, ~0 bytes until Chrome updates and drops the version we
//     kept (then ~725MB is ours). Hence one version only, re-cloned when the installed browser's version (or the brand)
//     changes, older clones deleted.
//   - Name, id and icon only. Not LSUIElement / LSBackgroundOnly-style tricks on the browser: Chromium reads LSUIElement to
//     decide it is a helper process and then can't find its framework ("icudtl.dat not found in bundle", from the second
//     launch on, once LaunchServices registered the bundle). Symlinking Chrome's binary / Frameworks fails the same way.
//   - Editing the plist breaks Google's signature (SIGKILL at launch), so the copy is re-signed ad hoc; that fails on
//     extended attributes ("detritus") unless they are stripped first. The nested framework keeps Google's signature.
//   - The copy has a different code identity: the user's "Chrome Safe Storage" keychain item would prompt, so the
//     browser runs with --use-mock-keychain (our profile holds no secrets worth the real keychain).
//   - The first launch of a fresh copy takes ~10s (macOS scanning the newly signed bundle), once per browser version.
func prepareEngine(exe, dir string, b brand) (string, error) {
	i := strings.Index(exe, ".app/Contents/MacOS/")
	if i < 0 {
		return "", fmt.Errorf("not inside an app bundle: %s", exe)
	}
	src := exe[:i+4]
	bin := filepath.Base(exe)
	version, err := plistString(filepath.Join(src, "Contents/Info.plist"), "CFBundleShortVersionString")
	if err != nil {
		return "", err
	}
	browserName := strings.TrimSuffix(filepath.Base(src), ".app")
	// The Dock shows the bundle's folder name, not CFBundleName: the folder is the name, the version goes one level up.
	slot := filepath.Join(dir, fmt.Sprintf("%s %s", browserName, version))
	dst := filepath.Join(slot, b.name+".app")
	out := filepath.Join(dst, "Contents/MacOS", bin)
	// engineLayout: bump when what a clone holds changes, so existing ones are made again.
	const engineLayout = "2" // 2: CFBundleIconName removed
	stamp := engineLayout + "\n" + b.name + "\n" + b.id + "\n" + iconStamp(b.icns)
	if got, err := os.ReadFile(filepath.Join(dst, ".gerdr-ready")); err == nil && string(got) == stamp {
		return out, nil
	}

	start := time.Now()
	_ = os.MkdirAll(slot, 0o755)
	tmp := filepath.Join(slot, ".building.app")
	_ = os.RemoveAll(tmp)
	if o, err := exec.Command("cp", "-cR", src, tmp).CombinedOutput(); err != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("clone (needs APFS, same volume): %v %s", err, o)
	}
	// Keep only the running version of the framework, so an update of the original frees the rest.
	vers, _ := filepath.Glob(filepath.Join(tmp, "Contents/Frameworks/*.framework/Versions/*"))
	for _, v := range vers {
		if n := filepath.Base(v); n != version && n != "Current" {
			_ = os.RemoveAll(v)
		}
	}
	plist := filepath.Join(tmp, "Contents/Info.plist")
	steps := [][]string{
		{"plutil", "-replace", "CFBundleName", "-string", b.name, plist},
		{"plutil", "-replace", "CFBundleDisplayName", "-string", b.name, plist},
		{"plutil", "-replace", "CFBundleIdentifier", "-string", b.id, plist},
	}
	if b.icns != "" {
		if icon, err := plistString(plist, "CFBundleIconFile"); err == nil {
			if !strings.HasSuffix(icon, ".icns") {
				icon += ".icns"
			}
			steps = append(steps, []string{"cp", b.icns, filepath.Join(tmp, "Contents/Resources", icon)},
				// Chrome's icon also sits in Assets.car (CFBundleIconName), which macOS prefers to the .icns: without
				// this the Dock said "Gerdr exp" under Chrome's icon.
				[]string{"plutil", "-remove", "CFBundleIconName", plist})
		}
	}
	steps = append(steps,
		[]string{"xattr", "-cr", tmp}, // some files refuse (com.apple.provenance); the signature only cares about the rest
		[]string{"codesign", "--force", "-s", "-", tmp},
	)
	for _, s := range steps {
		o, err := exec.Command(s[0], s[1:]...).CombinedOutput()
		if err != nil && s[0] != "xattr" && !(s[0] == "plutil" && s[1] == "-remove") {
			_ = os.RemoveAll(tmp)
			return "", fmt.Errorf("%s: %v %s", s[0], err, o)
		}
	}
	_ = os.WriteFile(filepath.Join(tmp, ".gerdr-ready"), []byte(stamp), 0o644)
	_ = os.RemoveAll(dst)
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	// Remade at the same path, LaunchServices may still hand out what it cached for the old one (Chrome's icon).
	_ = exec.Command("/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister", "-f", dst).Run()
	// Older copies (the browser moved on, another brand) now hold blocks of their own: drop them.
	old, _ := filepath.Glob(filepath.Join(dir, "*"))
	for _, o := range old {
		if o != slot {
			_ = os.RemoveAll(o)
		}
	}
	others, _ := filepath.Glob(filepath.Join(slot, "*.app"))
	for _, o := range others {
		if o != dst {
			_ = os.RemoveAll(o)
		}
	}
	log.Printf("engine: cloned %s %s as %q in %v", browserName, version, b.name, time.Since(start).Round(time.Millisecond))
	return out, nil
}

func iconStamp(p string) string {
	if p == "" {
		return ""
	}
	st, err := os.Stat(p)
	if err != nil {
		return p
	}
	return fmt.Sprintf("%s %d %d", p, st.Size(), st.ModTime().Unix())
}

// makeIcns: an .icns from a PNG (sips + iconutil), for running from the repo; the App carries its own (package.sh).
func makeIcns(png, out string) error {
	set := out + ".iconset"
	_ = os.RemoveAll(set)
	if err := os.MkdirAll(set, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(set)
	for _, s := range []int{16, 32, 128, 256, 512} {
		for _, k := range []int{1, 2} {
			name := fmt.Sprintf("icon_%dx%d.png", s, s)
			if k == 2 {
				name = fmt.Sprintf("icon_%dx%d@2x.png", s, s)
			}
			if o, err := exec.Command("sips", "-z", fmt.Sprint(s*k), fmt.Sprint(s*k), png, "--out", filepath.Join(set, name)).CombinedOutput(); err != nil {
				return fmt.Errorf("sips: %v %s", err, o)
			}
		}
	}
	if o, err := exec.Command("iconutil", "-c", "icns", set, "-o", out).CombinedOutput(); err != nil {
		return fmt.Errorf("iconutil: %v %s", err, o)
	}
	return nil
}

func plistString(path, key string) (string, error) {
	b, err := exec.Command("plutil", "-extract", key, "raw", "-o", "-", path).Output()
	if err != nil {
		return "", fmt.Errorf("read %s from %s: %v", key, path, err)
	}
	return strings.TrimSpace(string(b)), nil
}
