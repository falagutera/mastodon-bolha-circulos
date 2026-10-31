package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	_ "golang.org/x/image/webp"
)

const (
	canvasSize   = 1000
	selfRadius   = 110
	maxAvatar    = 5 << 20
	maxDimension = 4096
)

var (
	ringSizes  = [3]int{8, 15, 26}
	ringDist   = [3]float64{200, 330, 450}
	ringRadius = [3]int{64, 58, 50}

	paper   = color.RGBA{0x14, 0x16, 0x1B, 0xFF}
	surface = color.RGBA{0x1B, 0x1E, 0x25, 0xFF}
	border  = color.RGBA{0x2C, 0x31, 0x3B, 0xFF}
	accent  = color.RGBA{0xFF, 0xC7, 0x2C, 0xFF}
	text    = color.RGBA{0xEC, 0xEE, 0xF1, 0xFF}
	dim     = color.RGBA{0x99, 0xA0, 0xA9, 0xFF}

	labelFace   = newFace(goregular.TTF, 12)
	markFace    = newFace(gobold.TTF, 15)
	initialFace = newFace(gobold.TTF, 42)
)

func newFace(ttf []byte, size float64) font.Face {
	f, err := opentype.Parse(ttf)
	if err != nil {
		panic(err)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err)
	}
	return face
}

type avatar struct{ img image.Image }

// fetchAvatar returns nil when the image can't be used. The person then gets a
// disc with their initial, so one broken avatar doesn't cost a whole circle.
func fetchAvatar(ctx context.Context, client *http.Client, url string) *avatar {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAvatar+1))
	if err != nil || len(body) > maxAvatar {
		return nil
	}
	return decodeAvatar(body)
}

func decodeAvatar(body []byte) *avatar {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || cfg.Width > maxDimension || cfg.Height > maxDimension {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil
	}
	return &avatar{img: img}
}

// drawCircle lays out the rings: same geometry as the original Fedi Circle
// Creator, on the bolha palette.
func drawCircle(self person, rings [3][]person, avatars map[string]*avatar, host string) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, canvasSize, canvasSize))
	draw.Draw(img, img.Bounds(), image.NewUniform(paper), image.Point{}, draw.Src)
	const c = canvasSize / 2
	for _, d := range ringDist {
		strokeCircle(img, c, c, d, border)
	}
	// Labels go last so no avatar covers a neighbour's name.
	var labels []func()
	place := func(p person, x, y, r int, center bool) {
		drawPerson(img, p, avatars[p.ID], x, y, r, center)
		labels = append(labels, func() { drawLabel(img, p, x, y, r) })
	}
	for layer, people := range rings {
		step := 360 / float64(ringSizes[layer])
		for i, p := range people {
			angle := (float64(i)*step + float64(layer)*30) * math.Pi / 180
			x := c + int(math.Round(math.Cos(angle)*ringDist[layer]))
			y := c + int(math.Round(math.Sin(angle)*ringDist[layer]))
			place(p, x, y, ringRadius[layer], false)
		}
	}
	place(self, c, c, selfRadius, true)
	for _, label := range labels {
		label()
	}
	drawText(img, markFace, host, canvasSize-textWidth(markFace, host)-16, canvasSize-16, accent)
	return img
}

func drawPerson(img *image.RGBA, p person, a *avatar, x, y, r int, center bool) {
	ringColor := border
	if center {
		ringColor = accent
	}
	fillDisc(img, x, y, r+3, ringColor)
	if a != nil {
		drawAvatar(img, a.img, x, y, r)
		return
	}
	fillDisc(img, x, y, r, surface)
	initial := strings.ToUpper(firstRune(p.Username))
	drawText(img, initialFace, initial, x-textWidth(initialFace, initial)/2, y+10, dim)
}

// drawLabel sets the username on a plate at the bottom of the avatar. The full
// address is in the list under the image.
func drawLabel(img *image.RGBA, p person, x, y, r int) {
	label := fitText(labelFace, "@"+p.Username, 2*r-12)
	lx := x - textWidth(labelFace, label)/2
	ly := y + r - 12
	plate := image.Rect(lx-5, ly-12, lx+textWidth(labelFace, label)+5, ly+5)
	draw.Draw(img, plate, image.NewUniform(color.RGBA{0x14, 0x16, 0x1B, 0xD8}), image.Point{}, draw.Over)
	drawText(img, labelFace, label, lx, ly, text)
}

// fitText shortens s to fit maxWidth, ending it with an ellipsis.
func fitText(face font.Face, s string, maxWidth int) string {
	if textWidth(face, s) <= maxWidth {
		return s
	}
	runes := []rune(s)
	for len(runes) > 1 {
		runes = runes[:len(runes)-1]
		if short := string(runes) + "…"; textWidth(face, short) <= maxWidth {
			return short
		}
	}
	return "…"
}

func firstRune(s string) string {
	for _, r := range s {
		return string(r)
	}
	return "?"
}

func drawAvatar(img *image.RGBA, src image.Image, cx, cy, r int) {
	box := image.Rect(cx-r, cy-r, cx+r, cy+r)
	scaled := image.NewRGBA(image.Rect(0, 0, 2*r, 2*r))
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	draw.DrawMask(img, box, scaled, image.Point{}, discMask(r), image.Point{}, draw.Over)
}

// discMask is an antialiased disc of radius r.
func discMask(r int) *image.Alpha {
	m := image.NewAlpha(image.Rect(0, 0, 2*r, 2*r))
	for y := range 2 * r {
		for x := range 2 * r {
			d := math.Hypot(float64(x)+0.5-float64(r), float64(y)+0.5-float64(r))
			m.SetAlpha(x, y, color.Alpha{uint8(255 * clamp(float64(r)-d+0.5))})
		}
	}
	return m
}

func fillDisc(img *image.RGBA, cx, cy, r int, c color.RGBA) {
	draw.DrawMask(img, image.Rect(cx-r, cy-r, cx+r, cy+r), image.NewUniform(c), image.Point{}, discMask(r), image.Point{}, draw.Over)
}

func strokeCircle(img *image.RGBA, cx, cy int, radius float64, c color.RGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			d := math.Abs(math.Hypot(float64(x-cx), float64(y-cy)) - radius)
			if d < 1.5 {
				a := clamp(1.5 - d)
				img.SetRGBA(x, y, blend(img.RGBAAt(x, y), c, a))
			}
		}
	}
}

func blend(bg, fg color.RGBA, a float64) color.RGBA {
	mix := func(b, f uint8) uint8 { return uint8(float64(b)*(1-a) + float64(f)*a) }
	return color.RGBA{mix(bg.R, fg.R), mix(bg.G, fg.G), mix(bg.B, fg.B), 0xFF}
}

func clamp(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func textWidth(face font.Face, s string) int {
	return font.MeasureString(face, s).Round()
}

func drawText(img *image.RGBA, face font.Face, s string, x, y int, c color.Color) {
	d := font.Drawer{Dst: img, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, y)}
	d.DrawString(s)
}
