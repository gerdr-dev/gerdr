# A title bar of our own (macOS): shelved

**Status**: looked into and prototyped on 2026-11-13, then shelved. The desktop window's top strip stays Chrome's
(grey, follows light / dark). This page is why, what was measured, and what doing it would take, so it doesn't have
to be found out again.

**The ask**: the desktop window's top strip should be ours to style, like Notion's (the UI drawn up
to the top edge, only the traffic lights left from the system). Today each window is a Chrome `--app=<url>` window and
the strip is Chrome's own frame, grey, following only light / dark mode.

## What was measured

Chrome 157, a throwaway profile, a localhost page with a manifest:

- `<meta name="theme-color">` in an `--app` window: no effect. A theme colour in the profile's Preferences
  (`browser.theme.user_color`): no effect either (or the wrong key; either way it would only be a tint).
- Installed as a web app it works: `PWA.install` {manifestId, installUrlOrBundleUrl} then `PWA.changeAppUserSettings`
  {displayMode: "standalone"} then `PWA.launch` (without the settings call the launch opened a browser tab). The title
  bar takes `theme_color`. With `"display_override": ["window-controls-overlay"]` in the manifest, Chrome shows a
  chevron in the title bar; clicked, the page gets the whole top edge (`navigator.windowControlsOverlay.visible`,
  rect x=86 y=0 h=38 CSS px; `env(titlebar-area-*)`, `-webkit-app-region: drag`), and the choice is kept across
  relaunches.
- The `PWA.*` CDP domain only exists over `--remote-debugging-pipe` (over a port: "'PWA.install' wasn't found").
  Lightron already drives Chrome over a pipe.
- The overlay is on from the first launch, and the chevron gone, with `--enable-features=DesktopPWAsWindowControlsOverlayWithNoToggle`
  (checked on fresh profiles over CDP: `navigator.windowControlsOverlay.visible` false without, true with).
- The origin ("localhost:8977") at the right fades in ~0.75s after launch, stays ~2.5s and fades out
  (web_app_toolbar_button_container.cc, web_app_origin_text.cc; seen to go). It shows again after a cross-document
  navigation, which the UI doesn't do.
- The ⋮ app menu: `AppBrowserController::HasTitlebarMenuButton()` is true on macOS, no flag. Chrome's own way to
  drop it is the "unframed" display mode, which `WebAppBrowserController::AppUsesUnframedMode()` allows for Isolated
  Web Apps only (`isolated-app://`, strict CSP, window-management permission): too big a change.
- What works instead (prototype, 2026-11-13, on a clone of Gerdr's own engine with a test dylib through
  `DYLD_INSERT_LIBRARIES`, like Lightron's menu library; confirmed by eye: no ⋮, one Dock icon):
  - `--disable-features=AppShimRemoteCocoa`. Otherwise a web app's windows are drawn in a separate "app shim" process
    (`~/Applications/Chrome Apps.localized/<name>.app`, `app_mode_loader`) that a dylib in the browser can't reach.
  - The dylib adds an NSView over the right end of the title bar in each app window (subview of
    `contentView.superview`, the `BrowserWindowFrame`; `autoresizingMask` MinX|MinY; ~142×38pt, the part
    `getTitlebarAreaRect()` leaves out), filled with the title bar's colour. Its `mouseDown:` does
    `performWindowDragWithEvent:` (double click: `zoom:`), so the ⋮ can't be clicked and the strip still drags.
  - The shim would still be a second Dock / ⌘Tab app (its own bundle id `<engine id>.app.<app id>`). The dylib makes
    `-[NSWorkspace openApplicationAtURL:configuration:completionHandler:]` fail for paths under
    `Chrome Apps.localized`. Chrome tries once more after recreating the shim, then gives up and keeps the windows:
    app_shim_manager_mac.cc `OnShimProcessDisconnected` closes them only if the shim had connected
    (`close_windows = !UsesRemoteViews() && HasBootstrapConnected()`). Killing a shim that has connected does close
    them.
  - The shim's bundle is still written to `~/Applications/Chrome Apps.localized/` (Launchpad, Spotlight): delete it
    or stop the write.
  The dylib's core:
  ```objc
  @interface CoverView : NSView @end
  @implementation CoverView
  - (BOOL)mouseDownCanMoveWindow { return YES; }
  - (void)mouseDown:(NSEvent *)e { if (e.clickCount == 2) [self.window zoom:nil]; else [self.window performWindowDragWithEvent:e]; }
  @end
  // per app window: NSView *f = w.contentView.superview;
  //   CoverView *c = [[CoverView alloc] initWithFrame:NSMakeRect(W - 142, H - 38, 142, 38)];
  //   c.wantsLayer = YES; c.layer.backgroundColor = <title bar colour>; c.autoresizingMask = NSViewMinXMargin | NSViewMinYMargin;
  //   [f addSubview:c positioned:NSWindowAbove relativeTo:nil];
  static IMP origOpen;
  static void ourOpen(id self, SEL _cmd, NSURL *url, NSWorkspaceOpenConfiguration *cfg, void (^done)(NSRunningApplication *, NSError *)) {
    if ([url.path containsString:@"Chrome Apps.localized"]) {  // a web app's shim: never launched
      if (done) dispatch_async(dispatch_get_global_queue(0, 0), ^{ done(nil, [NSError errorWithDomain:NSCocoaErrorDomain code:NSFeatureUnsupportedError userInfo:nil]); });
      return;
    }
    ((void (*)(id, SEL, NSURL *, NSWorkspaceOpenConfiguration *, id))origOpen)(self, _cmd, url, cfg, done);
  }
  // constructor: origOpen = method_setImplementation(class_getInstanceMethod(NSWorkspace.class,
  //   @selector(openApplicationAtURL:configuration:completionHandler:)), (IMP)ourOpen);
  ```
- All of this leans on Chrome internals (two feature names, how it launches shims, the frame's view tree): recheck
  on each Chrome major, like the menu library.

## Why it's shelved

Three ways, each gets two of the three things wanted:

| | Small download | Stable | Our own title bar |
|---|---|---|---|
| Today: chromeshell, `--app` windows | yes (~18MB) | yes | no |
| chromeshell + installed web app + the dylib above | yes | no | yes |
| Electron (`titleBarStyle: 'hiddenInset'`, Windows `titleBarOverlay`) | no (~120MB, WHY-ELECTRON.md) | yes | yes |

The middle one isn't stable because chromeshell runs on the user's own Chrome, which updates itself every ~4 weeks,
outside our releases. Everything it leans on is internal (two feature names, how Chrome launches shims, the frame's
view tree) and can go in any of those updates, with users meeting it before we do. The worst case is not cosmetic:
if `AppShimRemoteCocoa` goes, app windows move into the shim, and since the dylib refuses to launch shims, Chrome
closes the windows as "never seen" (app_shim_host_mac.cc `OnShimProcessTerminated`): Gerdr wouldn't open. Electron
does all of it with public API on a Chromium we pin, but costs the download size chromeshell was made to cut.

Size and stability won; the title bar lost. Look again if one of those changes: Chrome ships a supported way to drop
the ⋮ for installed web apps (outside Isolated Web Apps), or the download size stops mattering.

## What doing it would take

The middle way, if it is ever done:
- In Lightron, opt-in (an option, off by default: other apps use Lightron too), on macOS only.
- A self-check at start (app window in the browser process, cover added, no shim launched) that falls back to
  today's `--app` windows on any failure, so the worst case is a grey title bar, not no window.

- Opening windows changes: new windows, a notification's click going to its session's window and focusing
  (Lightron's `--app` path, app_darwin.go newWindow / focus) all need redoing on `PWA.launch` and retesting. The
  manifest id is the origin, so the backend's port must stay fixed (it is: 7870).
- The UI then lays out its top row in `env(titlebar-area-*)` with `-webkit-app-region: drag` (buttons: `no-drag`),
  leaving room for the traffic lights (x=86) and the covered strip at the right. The cover's colour must follow the
  UI (theme, light / dark): the UI tells Lightron, Lightron tells the dylib.
- Notifications may be attributed to the shim (`AppShimNotificationAttribution`): check they still come from Gerdr.
- A Lightron change: everyone downloads the whole App again (docs/dev/UPDATE.md).
- Windows (WebView2) is a separate job to match.
