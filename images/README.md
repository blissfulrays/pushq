# pushq logo

The mark is the dashboard's icon: an up-arrow (a task being pushed out) in a
rounded square. The wordmark is drawn as strokes, not set in a font, so it
renders identically everywhere and carries no font license.

| File | Use |
|---|---|
| `logo.svg` | Icon for light backgrounds (dark square, white arrow) |
| `logo-dark.svg` | Icon for dark backgrounds (light square, dark arrow) |
| `wordmark.svg` / `wordmark.png` | Icon + "pushq" for light backgrounds |
| `wordmark-dark.svg` / `wordmark-dark.png` | Icon + "pushq" for dark backgrounds |
| `logo-512.png`, `logo-256.png`, `logo-128.png` | App/avatar icons; 128 px is NuGet's package icon size |
| `logo-dark-512.png` | Dark-background avatar |
| `social-preview.png` | GitHub social preview (1280×640): repo Settings → General → Social preview |

Colors: ink `#18181b`, paper `#fafafa` (the dashboard's primary colors).

Theme-aware wordmark in Markdown:

```html
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="images/wordmark-dark.svg">
  <img src="images/wordmark.svg" alt="pushq" height="48">
</picture>
```

Re-export PNGs after editing an SVG:

```bash
for n in 512 256 128; do rsvg-convert -w $n -h $n logo.svg -o logo-$n.png; done
rsvg-convert -w 512 -h 512 logo-dark.svg -o logo-dark-512.png
rsvg-convert -h 128 wordmark.svg -o wordmark.png
rsvg-convert -h 128 wordmark-dark.svg -o wordmark-dark.png
rsvg-convert -w 1280 -h 640 social-preview.svg -o social-preview.png
```
