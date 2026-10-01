#!/usr/bin/env python3
"""Generate the committed ICO from web/src/assets/logo.svg. Requires Pillow.

The logo intentionally uses only a rounded rectangle and polygons, so this
generator renders the source geometry without a browser or SVG runtime.
Run from any directory: python3 scripts/generate-favicon.py
"""
from pathlib import Path
import xml.etree.ElementTree as ET

from PIL import Image, ImageDraw

root = Path(__file__).resolve().parents[1]
svg = ET.parse(root / "web/src/assets/logo.svg").getroot()
_, _, width, height = map(float, svg.attrib["viewBox"].split())
if width != height:
    raise ValueError("The favicon source must be square")
scale = 2048 / width
image = Image.new("RGBA", (2048, 2048))
draw = ImageDraw.Draw(image)
for element in svg:
    tag = element.tag.split("}")[-1]
    if tag == "rect":
        x = float(element.get("x", "0")) * scale
        y = float(element.get("y", "0")) * scale
        right = x + float(element.attrib["width"]) * scale
        bottom = y + float(element.attrib["height"]) * scale
        draw.rounded_rectangle((x, y, right, bottom), radius=float(element.get("rx", "0")) * scale, fill=element.attrib["fill"])
    elif tag == "polygon":
        points = [tuple(float(v) * scale for v in pair.split(",")) for pair in element.attrib["points"].split()]
        draw.polygon(points, fill=element.attrib["fill"])
    else:
        raise ValueError(f"Unsupported logo geometry: {tag}; update the renderer before changing the source")
sizes = [(n, n) for n in (16, 32, 48, 64, 128, 256)]
rendered = [image.resize(size, Image.Resampling.LANCZOS) for size in sizes]
output = root / "web/public/favicon.ico"
output.parent.mkdir(parents=True, exist_ok=True)
rendered[-1].save(output, format="ICO", sizes=sizes, append_images=rendered[:-1])
print(f"Generated {output} with {len(sizes)} sizes from logo.svg")
