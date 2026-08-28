//go:build ignore

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

var (
	bg   = color.NRGBA{0x11, 0x15, 0x1c, 0xff}
	blue = color.NRGBA{0x3b, 0x82, 0xf6, 0xff}
)

func distSeg(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	l2 := dx*dx + dy*dy
	if l2 == 0 {
		return math.Hypot(px-ax, py-ay)
	}
	t := ((px-ax)*dx + (py-ay)*dy) / l2
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}

func render(s int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, s, s))
	f := float64(s)
	radius := 0.18 * f
	stroke := 0.095 * f
	// chevron ">" points
	ax, ay := 0.32*f, 0.30*f
	bx, by := 0.58*f, 0.50*f
	cx, cy := 0.32*f, 0.70*f
	// underscore rect
	ux0, uy0, ux1, uy1 := 0.60*f, 0.64*f, 0.78*f, 0.70*f
	for y := 0; y < s; y++ {
		for x := 0; x < s; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			// rounded-rect background mask
			inside := true
			cxr, cyr := fx, fy
			if fx < radius && fy < radius {
				inside = math.Hypot(fx-radius, fy-radius) <= radius
			} else if fx > f-radius && fy < radius {
				inside = math.Hypot(fx-(f-radius), fy-radius) <= radius
			} else if fx < radius && fy > f-radius {
				inside = math.Hypot(fx-radius, fy-(f-radius)) <= radius
			} else if fx > f-radius && fy > f-radius {
				inside = math.Hypot(fx-(f-radius), fy-(f-radius)) <= radius
			}
			_ = cxr
			_ = cyr
			if !inside {
				continue
			}
			px := bg
			// glyph: chevron or underscore in blue
			d := math.Min(distSeg(fx, fy, ax, ay, bx, by), distSeg(fx, fy, bx, by, cx, cy))
			onChevron := d <= stroke/2
			onUnder := fx >= ux0 && fx <= ux1 && fy >= uy0 && fy <= uy1
			if onChevron || onUnder {
				px = blue
			}
			img.SetNRGBA(x, y, px)
		}
	}
	return img
}

func main() {
	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	type entry struct {
		w, h int
		data []byte
	}
	var entries []entry
	for _, s := range sizes {
		var buf bytes.Buffer
		if err := png.Encode(&buf, render(s)); err != nil {
			panic(err)
		}
		entries = append(entries, entry{s, s, buf.Bytes()})
	}

	// go-winres（VERSIONINFO埋込）用に個別PNGも出力する
	dir := filepath.Dir(os.Args[1])
	for _, e := range entries {
		name := filepath.Join(dir, fmt.Sprintf("icon_%d.png", e.w))
		if err := os.WriteFile(name, e.data, 0o644); err != nil {
			panic(err)
		}
	}

	var out bytes.Buffer
	// ICONDIR
	binary.Write(&out, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(&out, binary.LittleEndian, uint16(1)) // type: icon
	binary.Write(&out, binary.LittleEndian, uint16(len(entries)))
	offset := 6 + 16*len(entries)
	for _, e := range entries {
		wb := byte(e.w)
		hb := byte(e.h)
		if e.w >= 256 {
			wb = 0
		}
		if e.h >= 256 {
			hb = 0
		}
		out.WriteByte(wb)
		out.WriteByte(hb)
		out.WriteByte(0)                                    // color count
		out.WriteByte(0)                                    // reserved
		binary.Write(&out, binary.LittleEndian, uint16(1))  // planes
		binary.Write(&out, binary.LittleEndian, uint16(32)) // bit count
		binary.Write(&out, binary.LittleEndian, uint32(len(e.data)))
		binary.Write(&out, binary.LittleEndian, uint32(offset))
		offset += len(e.data)
	}
	for _, e := range entries {
		out.Write(e.data)
	}
	if err := os.WriteFile(os.Args[1], out.Bytes(), 0o644); err != nil {
		panic(err)
	}
}
