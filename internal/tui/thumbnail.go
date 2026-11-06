package tui

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Thumbnail size in terminal cells. Half-block characters give each cell two
// pixels, and cells are about twice as tall as wide, so pixels are near square.
const (
	thumbCols = 22
	thumbRows = 14
)

// thumbnail is a PDF's first page as brightness values from 0 to 1.
type thumbnail struct {
	pixels [][]float64
}

// renderThumbnail rasterizes a PDF's first page with sips, which ships with
// macOS, so zotty needs no PDF library of its own.
func renderThumbnail(pdf string) (thumbnail, error) {
	dir, err := os.MkdirTemp("", "zotty-thumb-")
	if err != nil {
		return thumbnail{}, err
	}
	defer os.RemoveAll(dir)

	out := filepath.Join(dir, "page.png")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "sips", "-s", "format", "png", "-Z", "280", pdf, "--out", out).Run(); err != nil {
		return thumbnail{}, fmt.Errorf("render first page: %w", err)
	}
	file, err := os.Open(out)
	if err != nil {
		return thumbnail{}, err
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil {
		return thumbnail{}, err
	}
	return downsample(img, thumbCols, thumbRows*2), nil
}

// downsample averages source pixels into a width by height grid. Thin text
// averages out to near white, so the result is darkened to keep lines visible.
func downsample(img image.Image, width, height int) thumbnail {
	bounds := img.Bounds()
	grid := make([][]float64, height)
	for y := range grid {
		grid[y] = make([]float64, width)
		y0 := bounds.Min.Y + y*bounds.Dy()/height
		y1 := bounds.Min.Y + (y+1)*bounds.Dy()/height
		for x := range grid[y] {
			x0 := bounds.Min.X + x*bounds.Dx()/width
			x1 := bounds.Min.X + (x+1)*bounds.Dx()/width
			var sum float64
			count := 0
			for sy := y0; sy < max(y1, y0+1); sy++ {
				for sx := x0; sx < max(x1, x0+1); sx++ {
					r, g, b, a := img.At(sx, sy).RGBA()
					alpha := float64(a) / 0xffff
					luma := (0.3*float64(r) + 0.59*float64(g) + 0.11*float64(b)) / 0xffff
					sum += luma + (1 - alpha)
					count++
				}
			}
			grid[y][x] = math.Pow(min(sum/float64(count), 1), 5)
		}
	}
	return thumbnail{pixels: grid}
}

// lines renders the thumbnail in the theme's paper colors. Each cell is a
// half block whose foreground is the upper pixel and background the lower.
func (t thumbnail) lines(th theme) []string {
	shade := func(v float64) string {
		c := [3]int{}
		for i := range c {
			c[i] = int(th.paperDark[i] + v*(th.paperLight[i]-th.paperDark[i]))
		}
		return fmt.Sprintf("%d;%d;%d", c[0], c[1], c[2])
	}
	out := make([]string, 0, len(t.pixels)/2)
	for y := 0; y+1 < len(t.pixels); y += 2 {
		var b strings.Builder
		for x := range t.pixels[y] {
			fmt.Fprintf(&b, "\x1b[38;2;%sm\x1b[48;2;%sm▀", shade(t.pixels[y][x]), shade(t.pixels[y+1][x]))
		}
		b.WriteString("\x1b[0m")
		out = append(out, b.String())
	}
	return out
}
