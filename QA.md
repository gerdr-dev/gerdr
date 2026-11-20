# nsh for Gerdr: questions and answers

The questions asked while trying nsh (2026-11-19/20), each answered briefly. The measurements behind them are in
README.md.

## Performance and how nsh works

**Does nsh have the problem the wristband app had (React on a compiled template engine)?**
No. The wristband's quick-app runtime only accepts templates fixed at build time, so a React tree had to be pushed
through a generic template of fixed depth. nsh compiles JSX into direct calls to its renderer: static parts are set
once, changing expressions become bindings, components run once, and signals (tracked in Rust) re-run only the
bindings that read them.

**How does it update native views fast when the UI changes all the time?**
Native views (UIView, Android View, NSView) can be created, moved and changed at any time; the question is only
cost. nsh turns each change into an operation in a queue, computes layout itself in Rust (Taffy, flexbox / grid)
once per JS turn, diffs the frames and styles, and hands the main thread one batch. Lists recycle rows natively;
animations and gestures run on the main thread without JS. Measured: 295 KB of conversation events parsed in 1 ms;
a streamed reply updates 1 of 139 rows.

**Does Taffy mean every native view is absolutely positioned?**
To the native view, yes: it gets a frame computed by Taffy, and the platform's own layout system is not used. To the
developer, no: you write flexbox / grid. Text is still measured by the platform. React Native does the same with Yoga.
Cost: layout properties (width, position) can't be driven per frame by animated values, only transforms, opacity
and colours.

**Is a shared layout engine (Rust here, C++ in React Native) the right way? Is there better?**
For "native controls from one codebase" it is the best known compromise (React Native Fabric, Lynx, Litho do the
same). The alternatives change the goal: draw everything yourself (Flutter, the browser) for identical pixels and
free animation, at the cost of rebuilding text input, IME, selection and accessibility; or write each platform
natively, at the cost of two or three UIs. Within the approach, better would be synchronous layout on the main thread
for interactive moments and animated values driving layout.

**How do components pass data?**
In one app: props (changing ones are getters), callbacks, shared signals (a store), context, refs. With a host
app's native view: props, events and commands. Between two nsh apps (the UI and a plugin): JSON over the host
channel (`host.call` / `host.on`), relayed by the host app.

**What does Rust give nsh over Go?**
No garbage collector pausing a frame; embeddable as a plain library in an iOS / Android app (Go brings its own
scheduler and GC into the host process); cheap calls across languages (cgo is not); small, feature-trimmed builds and
WebAssembly; the right libraries (Taffy, vte, a ProseMirror port). Cost: slow builds (5 min for the first runtime
build) and harder code. Gerdr's backend stays Go; its UI would be TSX.

## React and signals

**Can React be used with nsh?**
No. The syntax is JSX, the model is Solid's: `signal` instead of `useState`, `<Each>` / `<If>` instead of `map` / `&&`,
components that run once.

**Why signals rather than React?**
Because JS runs in an interpreter (QuickJS): React re-runs components and diffs trees in JS on every change, which is
exactly the wristband's cost; signals leave only the changed bindings to JS. They also map one to one onto native
property changes, and Rust and TSX share the model. Cost: fewer people and agents know it, and React habits break.

**React elements are values you can pass around; isn't that a hard limit?**
No. In nsh JSX can be passed as props, children and render functions too. What's lost is treating an element as inert
data to inspect or clone (`cloneElement`, `Children.map`); a function returning JSX replaces most uses. Gerdr's UI
passes elements in 39 files but inspects one only once (`isValidElement`).

**In the age of AI, the ecosystem isn't a problem?**
Agreed for Gerdr: nsh's own components cover what the UI uses (RichEditor ~ Tiptap, Terminal ~ xterm, TextView ~
streamdown, Select / Dialog / Toast ~ Radix, signals ~ zustand). The risk moves to nsh's maturity: the years of
on-device edge cases a library holds (IME, selection, scroll) now depend on nsh, which is a month old.

## nsh against the browser

**Isn't nsh doing what a browser does?**
Same aim (one codebase, every platform), different drawing: a browser draws every pixel itself and is identical
everywhere; nsh shares layout but lets each platform's controls draw, so text input, IME, selection and scrolling
are native, and looks differ per platform. Gerdr went back to Electron for "identical everywhere" and a real page in
the window; nsh gives that up for size, memory and native feel.

**Could Gerdr drop Chrome on every platform with nsh?**
Only the desktop app's bundled Chromium. Phones already use the system WebView; the Browser pane needs a real
Chromium page (nsh's WebView is WebKit on macOS), so Chrome stays for that.

## Android

**Why would we need the Android NDK?**
Only to compile native code of our own (a Rust module). TSX alone uses nsh's prebuilt runtime. The microphone was
done in Kotlin inside Gerdr's own Android app instead, which needs no NDK.

**Why is the APK so big (Gerdr's is ~6 MB)?**
Gerdr's uses the system's WebView. nsh ships its runtime: `libnsh.so` is 50 MB for arm64 stripped (~20 MB
compressed). The debug APK held three ABIs unstripped (217–227 MB). A trimmed runtime needs nsh's custom build (with
the NDK on Android).

## Plugins

**Can plugins update without a new APK, right away?**
Yes. A release nsh app runs no downloaded code (no eval, `import()` only of its own modules), so each plugin is its
own nsh app in its own view, placed by the host. Its TSX is rebuilt on save and the running app loads the new build
within seconds; only native capabilities (Kotlin) need a new APK. In the background Android pauses the app's timers,
so it updates when it comes back to the front.

## The chat

**Scrolling jumps and flickers while an agent writes: fixable?**
Fixed. Every streamed chunk rebuilt every row (each Markdown re-laid out, the height wobbling ~25 pt), and a
scroll-to-end queued from an update pulled the reader back as they dragged. Now only the changed row updates (1 of
139) and nothing scrolls while a finger is down or a fling runs.
