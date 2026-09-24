#!/usr/bin/env python3
"""Regenerate the embedded font subsets in internal/fontkit/fonts.

Each face is fetched from google/fonts, instanced at weight 400 when it is a
variable font, and subset to the Latin + Turkish repertoire the stamper can
draw. Run it with fontTools installed:

    python3 -m venv /tmp/fv && /tmp/fv/bin/pip install fonttools brotli
    /tmp/fv/bin/python scripts/fonts.py

The generated .ttf files are committed: the plugin embeds them, so a build
never needs the network.
"""
import io
import os
import sys
import urllib.request

from fontTools.ttLib import TTFont
from fontTools.subset import Subsetter, Options
from fontTools.varLib.instancer import instantiateVariableFont

RAW = "https://raw.githubusercontent.com/google/fonts/main/"

FACES = [
    ("caveat.ttf", "ofl/caveat/Caveat[wght].ttf", {"wght": 400}, "ofl/caveat/OFL.txt", "caveat-OFL.txt"),
    ("dancing-script.ttf", "ofl/dancingscript/DancingScript[wght].ttf", {"wght": 400}, "ofl/dancingscript/OFL.txt", "dancing-script-OFL.txt"),
    ("homemade-apple.ttf", "apache/homemadeapple/HomemadeApple-Regular.ttf", None, "apache/homemadeapple/LICENSE.txt", "homemade-apple-LICENSE.txt"),
    ("inter.ttf", "ofl/inter/Inter[opsz,wght].ttf", {"wght": 400, "opsz": 18}, "ofl/inter/OFL.txt", "inter-OFL.txt"),
    ("source-serif-4.ttf", "ofl/sourceserif4/SourceSerif4[opsz,wght].ttf", {"wght": 400, "opsz": 14}, "ofl/sourceserif4/OFL.txt", "source-serif-4-OFL.txt"),
]

# Latin, Latin-1, Latin Extended-A (Turkish lives here: Ğ ğ İ ı Ş ş),
# general punctuation and the currency signs a form is likely to carry.
UNICODES = "U+0020-007E,U+00A0-00FF,U+0100-017F,U+2010-2027,U+20A0-20BF,U+2122,U+2713"

here = os.path.dirname(os.path.abspath(__file__))
out_dir = os.path.join(here, "..", "internal", "fontkit", "fonts")
lic_dir = os.path.join(out_dir, "licenses")
os.makedirs(lic_dir, exist_ok=True)


def fetch(path):
    with urllib.request.urlopen(RAW + path, timeout=60) as r:
        return r.read()


for name, path, axes, lic_path, lic_name in FACES:
    raw = fetch(path)
    font = TTFont(io.BytesIO(raw))
    if axes:
        font = instantiateVariableFont(font, axes, updateFontNames=True, inplace=True)
    opts = Options()
    # The stamper maps runes through cmap and draws glyph ids itself, so no
    # shaping table is ever consulted: dropping them halves each face.
    opts.layout_features = []
    opts.name_IDs = [1, 2, 3, 4, 6]
    opts.name_legacy = False
    opts.hinting = False
    opts.desubroutinize = False
    opts.glyph_names = False
    opts.notdef_outline = True
    opts.recalc_bounds = True
    opts.drop_tables += ["DSIG"]
    sub = Subsetter(options=opts)
    sub.populate(unicodes=[int(u[2:], 16) for r in UNICODES.split(",") for u in
                           ([r] if "-" not in r else
                            ["U+%04X" % c for c in range(int(r.split("-")[0][2:], 16), int(r.split("-")[1], 16) + 1)])])
    sub.subset(font)
    dest = os.path.join(out_dir, name)
    font.flavor = None
    font.save(dest)
    print("%-22s %7d bytes  (from %s)" % (name, os.path.getsize(dest), path))
    open(os.path.join(lic_dir, lic_name), "wb").write(fetch(lic_path))
