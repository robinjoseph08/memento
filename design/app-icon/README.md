# Memento app icon

## Concept

Memento gathers scattered family moments into one carefully ordered gallery archive, represented by three fanned photo tiles becoming one stack.

## Design decision

Three concepts were rendered at 512, 64, 32, and 16 pixels:

1. Fanned gallery
2. Gallery grid
3. Archive slot

The fanned gallery won because it reads as both a gallery and a collected archive, remains recognizable at 16 pixels, and is less generic than the grid. The archive slot looked too much like an inbox or stamp. The selected geometry was widened after the first render so the rear cards remain visible at favicon sizes.

The dark icon is canonical because Memento defaults to dark mode. The light icon uses identical geometry with inverted value hierarchy. The monochrome reduction keeps the front card whole and separates the two rear-card silhouettes so it does not collapse into a blob.

## Palette

The icon uses Tailwind's sky family:

- Sky 200 and 300 for the hero card on dark tiles
- Sky 400 through 700 for rear cards
- Sky 600 through 800 for hierarchy on light tiles
- A subtly blue-tinted dark or light tile

## Source files

- `memento-icon-dark.svg`: canonical master
- `memento-icon-light.svg`: light-polarity variant with identical geometry
- `memento-icon-mono.svg`: one-color reduction
- `memento-icon-maskable.svg`: safe-zone PWA source
- `candidates/`: rejected concepts retained as design evidence

## Generate assets

```sh
./design/app-icon/generate.sh
```

The script uses CairoSVG through `uvx` for verified SVG rendering and ImageMagick only for raster resizing and packaging.

## Packaged web assets

The generated files in `public/` include the adaptive SVG favicon, 16 and 32 pixel ICO, opaque Apple touch icon, PWA icons, maskable icon, monochrome themed icon, and web manifest.
