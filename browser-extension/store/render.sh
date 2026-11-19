#!/bin/sh
# Renders shots.html into the images the Chrome Web Store asks for: out/<lang>-<n>.png (1280×800) and
# out/<lang>-tile.png (440×280, the small promo tile). The alpha channel is dropped: the store only takes 24-bit PNGs.
set -e
cd "$(dirname "$0")"
chrome="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
mkdir -p out
for lang in en zh; do
  for shot in 1 2 3 tile; do
    if [ "$shot" = tile ]; then size=440,280; else size=1280,800; fi
    out="$PWD/out/$lang-$shot.png"
    "$chrome" --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 --allow-file-access-from-files \
      --window-size=$size --screenshot="$out" "file://$PWD/shots.html?shot=$shot&lang=$lang" 2>/dev/null
    python3 -c "import sys;from PIL import Image;Image.open(sys.argv[1]).convert('RGB').save(sys.argv[1])" "$out"
  done
done
ls -1 out
