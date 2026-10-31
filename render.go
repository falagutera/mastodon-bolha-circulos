package main

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	_ "golang.org/x/image/webp"
)

// The card: the constellation on a square to the left, a reading panel to the right.
const (
	canvasWidth  = 1600
	canvasHeight = 1000
	panelX       = 1000
	panelPad     = 64
	selfRadius   = 104
	maxAvatar    = 5 << 20
	maxDimension = 1024
)

//go:embed fonts/IBMPlexSans-Regular.ttf
var plexRegular []byte

//go:embed fonts/IBMPlexSans-SemiBold.ttf
var plexSemiBold []byte

//go:embed fonts/IBMPlexSansCondensed-Bold.ttf
var plexCondensedBold []byte

var (
	// Avatars shrink faster than the rings grow, so the outer rings can breathe.
	ringSizes  = [3]int{8, 15, 26}
	ringDist   = [3]float64{205, 318, 428}
	ringRadius = [3]int{62, 44, 32}
	ringOffset = [3]float64{22.5, 12, 7}

	paper   = color.RGBA{0x14, 0x16, 0x1B, 0xFF}
	surface = color.RGBA{0x1B, 0x1E, 0x25, 0xFF}
	border  = color.RGBA{0x2C, 0x31, 0x3B, 0xFF}
	accent  = color.RGBA{0xFF, 0xC7, 0x2C, 0xFF}
	text    = color.RGBA{0xEC, 0xEE, 0xF1, 0xFF}
	dim     = color.RGBA{0x99, 0xA0, 0xA9, 0xFF}
	glow    = color.RGBA{0x4A, 0x3B, 0x14, 0xFF}

	titleFaces  = []font.Face{newFace(plexCondensedBold, 58), newFace(plexCondensedBold, 46), newFace(plexCondensedBold, 36)}
	handleFace  = newFace(plexRegular, 24)
	eyebrowFace = newFace(plexSemiBold, 17)
	rowFace     = newFace(plexSemiBold, 25)
	rowDimFace  = newFace(plexRegular, 18)
	rankFace    = newFace(plexCondensedBold, 26)
	markFace    = newFace(plexSemiBold, 20)
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
	if !strings.HasPrefix(url, "https://") {
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
	// Keep only what the largest disc needs, a circle holds 50 of these.
	small := image.NewRGBA(image.Rect(0, 0, 2*selfRadius, 2*selfRadius))
	xdraw.CatmullRom.Scale(small, small.Bounds(), img, img.Bounds(), xdraw.Src, nil)
	return &avatar{img: small}
}

// ringPoint is the center of the i-th avatar of a ring.
func ringPoint(layer, i int) (x, y int) {
	const c = canvasHeight / 2
	step := 360 / float64(ringSizes[layer])
	angle := (float64(i)*step + ringOffset[layer]) * math.Pi / 180
	return c + int(math.Round(math.Cos(angle)*ringDist[layer])), c + int(math.Round(math.Sin(angle)*ringDist[layer]))
}

// spot is an area over an avatar on the page: the link to the profile and the
// label shown on hover, in image coordinates.
type spot struct {
	X, Y, R    int
	Acct, URL  string
	TipX, TipY int
	TipW, TipH int
	TextX      int
	TextY      int
}

func newSpot(p person, x, y, r int) spot {
	const pad, height, gap = 16, 44, 10
	s := spot{X: x, Y: y, R: r, Acct: "@" + p.Acct, URL: p.URL, TipH: height}
	s.TipW = textWidth(rowFace, s.Acct) + 2*pad
	s.TipX = max(8, min(x-s.TipW/2, canvasWidth-s.TipW-8))
	s.TipY = y + r + gap
	if s.TipY+height > canvasHeight-8 {
		s.TipY = y - r - gap - height // no room below
	}
	s.TextX = s.TipX + s.TipW/2
	s.TextY = s.TipY + 30
	return s
}

// spots lists the areas of a circle, the person in the center last.
func spots(self person, rings [3][]person) []spot {
	var out []spot
	for layer, people := range rings {
		for i, p := range people {
			x, y := ringPoint(layer, i)
			out = append(out, newSpot(p, x, y, ringRadius[layer]))
		}
	}
	return append(out, newSpot(self, canvasHeight/2, canvasHeight/2, selfRadius))
}

// cardText holds the words on the image, in the instance's language.
type cardText struct {
	Eyebrow string
	Closest string
	More    string // with a %d for how many people are in the outer rings
}

// drawCircle composes the card.
func drawCircle(self person, rings [3][]person, avatars map[string]*avatar, host string, t cardText) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, canvasWidth, canvasHeight))
	draw.Draw(img, img.Bounds(), image.NewUniform(paper), image.Point{}, draw.Src)
	const c = canvasHeight / 2
	paintGlow(img, c, c, 480)
	for _, d := range ringDist {
		strokeCircle(img, c, c, d, border)
	}
	for layer, people := range rings {
		for i, p := range people {
			x, y := ringPoint(layer, i)
			fillDisc(img, x, y, ringRadius[layer]+4, paper) // cuts the ring line around the avatar
			drawFace(img, p, avatars[p.ID], x, y, ringRadius[layer])
		}
	}
	fillDisc(img, c, c, selfRadius+6, accent)
	fillDisc(img, c, c, selfRadius+2, paper)
	drawFace(img, self, avatars[self.ID], c, c, selfRadius)
	drawPanel(img, self, rings, avatars, host, t)
	return img
}

func drawPanel(img *image.RGBA, self person, rings [3][]person, avatars map[string]*avatar, host string, t cardText) {
	x := panelX + panelPad
	width := canvasWidth - x - panelPad
	drawText(img, eyebrowFace, strings.ToUpper(t.Eyebrow), x, 96, accent)
	title := drawable(self.DisplayName())
	drawText(img, titleFaceFor(title, width), fitText(titleFaceFor(title, width), title, width), x, 160, text)
	drawText(img, handleFace, fitText(handleFace, "@"+self.Acct, width), x, 200, dim)

	drawText(img, eyebrowFace, strings.ToUpper(t.Closest), x, 268, dim)
	for i, p := range rings[0] {
		y := 324 + i*68
		drawText(img, rankFace, strconv.Itoa(i+1), x, y+9, accent)
		drawFace(img, p, avatars[p.ID], x+62, y, 25)
		textX := x + 102
		drawText(img, rowFace, fitText(rowFace, drawable(p.DisplayName()), width-102), textX, y+1, text)
		drawText(img, rowDimFace, fitText(rowDimFace, "@"+p.Acct, width-102), textX, y+24, dim)
	}
	if n := len(rings[1]) + len(rings[2]); n > 0 {
		drawText(img, rowDimFace, fmt.Sprintf(t.More, n), x, canvasHeight-52, dim)
	}
	drawText(img, markFace, host, canvasWidth-panelPad-textWidth(markFace, host), canvasHeight-52, accent)
}

// drawFace draws an avatar, or a disc with the initial when there is none.
func drawFace(img *image.RGBA, p person, a *avatar, x, y, r int) {
	if a != nil {
		drawAvatar(img, a.img, x, y, r)
		return
	}
	fillDisc(img, x, y, r, border)
	face := rowDimFace
	if r >= 40 {
		face = titleFaces[0]
	} else if r >= 28 {
		face = rowFace
	}
	initial := strings.ToUpper(firstRune(p.Username))
	drawText(img, face, initial, x-textWidth(face, initial)/2, y+face.Metrics().CapHeight.Round()/2, text)
}

// paintGlow lifts the paper around the center, fading out by radius.
func paintGlow(img *image.RGBA, cx, cy int, radius float64) {
	for y := range canvasHeight {
		for x := range panelX {
			d := math.Hypot(float64(x-cx), float64(y-cy)) / radius
			if d < 1 {
				img.SetRGBA(x, y, blend(paper, glow, 0.55*(1-d)*(1-d)))
			}
		}
	}
}

// titleFaceFor picks the largest title size that fits, the smallest otherwise.
func titleFaceFor(s string, width int) font.Face {
	for _, f := range titleFaces {
		if textWidth(f, s) <= width {
			return f
		}
	}
	return titleFaces[len(titleFaces)-1]
}

// drawable drops what the font can't draw (emoji, flags, joiners), which would
// show up as empty boxes.
func drawable(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P, unicode.Zs, unicode.Sm, unicode.Sc) && !unicode.Is(unicode.Variation_Selector, r) {
			return r
		}
		return -1
	}, s)
	return strings.Join(strings.Fields(s), " ")
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
	b := image.Rect(cx-int(radius)-2, cy-int(radius)-2, cx+int(radius)+3, cy+int(radius)+3).Intersect(img.Bounds())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			d := math.Abs(math.Hypot(float64(x-cx), float64(y-cy)) - radius)
			if d < 1 {
				a := clamp(1 - d)
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
