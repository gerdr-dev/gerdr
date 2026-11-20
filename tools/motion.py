# Per-frame vertical motion of a screen recording: the shift that best matches each frame to the previous one, over a
# central strip of the chat area. A smooth fling moves one way; "jumps" show as the motion reversing.
import subprocess, sys, numpy as np
path = sys.argv[1]
W, H = 108, 240  # downscaled frames
raw = subprocess.run(["ffmpeg", "-v", "error", "-i", path, "-vf", f"fps=30,scale={W}:{H},format=gray", "-f", "rawvideo", "-"], capture_output=True).stdout
frames = np.frombuffer(raw, np.uint8).reshape(-1, H, W).astype(np.int16)
top, bot = int(H * 0.12), int(H * 0.82)  # below the chip strip, above the composer
shifts = []
for a, b in zip(frames, frames[1:]):
    A = a[top:bot, 10:-10]
    best, bs = None, 0
    for s in range(-110, 111):
        if s >= 0: x, y = A[s:], b[top:bot - s if s else bot, 10:-10][: A.shape[0] - s]
        else: x, y = A[: s], b[top - s:bot, 10:-10][: A.shape[0] + s]
        x = A[max(0, s): A.shape[0] + min(0, s)]
        y = b[top + max(0, -s): bot - max(0, s), 10:-10]
        n = min(len(x), len(y)); d = np.abs(x[:n] - y[:n]).mean()
        if best is None or d < best: best, bs = d, s
    blank = b[top:bot].std() < 6
    shifts.append((bs, round(float(best), 1), blank))
mv = [s for s, _, _ in shifts]
good = [s for s, d, _ in shifts if d < 12]; mv = good; flips = sum(1 for x, y in zip(mv, mv[1:]) if x * y < 0 and abs(x) > 3 and abs(y) > 3)
print(f"{path}: frames {len(frames)}, moving frames {sum(1 for s in mv if s)}, reversals {flips}, blank frames {sum(1 for *_, bl in shifts if bl)}, poor matches {sum(1 for _, d, _ in shifts if d > 25)}")
print(" ".join(str(s) for s in mv))
