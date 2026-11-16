# The UI's per-device settings in the backend, not only in localStorage

The UI keeps its per-device settings (language, sizes, labs switches…; ui/src/lib/store/prefs.ts) in the page's
localStorage, which is per origin and per browser profile. So they don't follow a change of either: a reinstalled browser profile, the same Gerdr opened in a normal browser or another App
(moving from the Electron App to the Chrome shell lost them once). Worth doing: keep them in the backend as well, keyed by a
device id the page keeps in localStorage (and the App could pass in), with localStorage as the fast, offline copy the
page reads first and the backend's as what a fresh origin / profile starts from. To decide first: which settings are
really per device (window sizes, zoom) and which are per person (language, labs switches: one copy for all devices
would do), and what a remote device (phone, another computer through the relay) gets.
