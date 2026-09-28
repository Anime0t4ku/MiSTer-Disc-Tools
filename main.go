package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const (
	version        = "1.4.2"
	baseDir        = "/media/fat/Scripts/.config/disctools"
	binDir         = baseDir + "/bin"
	tempDir        = baseDir + "/temp"
	logDir         = baseDir + "/logs"
	customFontsDir = baseDir + "/fonts"
	device         = "/dev/sr0"
)

var swapABInput atomic.Bool
var swapXYInput atomic.Bool
var screenSaverSeconds atomic.Int64
var screenSaverActive atomic.Bool

type Config struct {
	OLEDMode           bool   `json:"oled_mode"`
	ShowClock          bool   `json:"show_clock"`
	ScreenSaverSeconds int    `json:"screensaver_seconds"`
	SwapAB             bool   `json:"swap_ab"`
	SwapXY             bool   `json:"swap_xy"`
	CustomFont         string `json:"custom_font"`
}

func loadConfig() Config {
	var c Config
	_ = os.MkdirAll(baseDir, 0755)
	b, err := os.ReadFile(filepath.Join(baseDir, "config.json"))
	if err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}
func saveConfig(c Config) {
	_ = os.MkdirAll(baseDir, 0755)
	b, _ := json.MarshalIndent(c, "", "  ")
	_ = os.WriteFile(filepath.Join(baseDir, "config.json"), b, 0644)
}

var (
	bg    = color.RGBA{12, 13, 17, 255}
	panel = color.RGBA{35, 37, 46, 255}
	fg    = color.RGBA{238, 238, 242, 255}
	dim   = color.RGBA{160, 162, 170, 255}
)

type fbVar struct {
	Xres, Yres, XresVirtual, YresVirtual, Xoffset, Yoffset, BitsPerPixel, Grayscale uint32
	RedOffset, RedLength, RedMsb, GreenOffset, GreenLength, GreenMsb                uint32
	BlueOffset, BlueLength, BlueMsb, TranspOffset, TranspLength, TranspMsb          uint32
	Nonstd, Activate, Height, Width, AccelFlags, Pixclock                           uint32
	LeftMargin, RightMargin, UpperMargin, LowerMargin, HsyncLen, VsyncLen           uint32
	Sync, Vmode, Rotate, Colorspace                                                 uint32
	Reserved                                                                        [4]uint32
}

type fbFix struct {
	ID           [16]byte
	SmemStart    uintptr
	SmemLen      uint32
	Type         uint32
	TypeAux      uint32
	Visual       uint32
	Xpanstep     uint16
	Ypanstep     uint16
	Ywrapstep    uint16
	LineLength   uint32
	MmioStart    uintptr
	MmioLen      uint32
	Accel        uint32
	Capabilities uint16
	Reserved     [2]uint16
}

type framebuffer struct {
	f                 *os.File
	mapped            []byte
	data              []byte
	back              []byte
	w, h, stride, bpp int
	mu                sync.Mutex
}

type inputEvent struct {
	Time       syscall.Timeval
	Type, Code uint16
	Value      int32
}

type action int

const (
	actNone action = iota
	actUp
	actDown
	actLeft
	actRight
	actConfirm
	actBack
	actWake
)
const (
	evKey        = 1
	evAbs        = 3
	keyEsc       = 1
	keyBackspace = 14
	keyEnter     = 28
	keyUp        = 103
	keyLeft      = 105
	keyRight     = 106
	keyDown      = 108
	keyBack      = 158
	btnSouth     = 304
	btnEast      = 305
	btnNorth     = 307
	btnWest      = 308
	absHatX      = 16
	absHatY      = 17
)

var font = map[rune][7]byte{
	'A': {14, 17, 17, 31, 17, 17, 17}, 'B': {30, 17, 17, 30, 17, 17, 30}, 'C': {14, 17, 16, 16, 16, 17, 14}, 'D': {30, 17, 17, 17, 17, 17, 30}, 'E': {31, 16, 16, 30, 16, 16, 31}, 'F': {31, 16, 16, 30, 16, 16, 16}, 'G': {14, 17, 16, 23, 17, 17, 15}, 'H': {17, 17, 17, 31, 17, 17, 17}, 'I': {14, 4, 4, 4, 4, 4, 14}, 'J': {7, 2, 2, 2, 18, 18, 12}, 'K': {17, 18, 20, 24, 20, 18, 17}, 'L': {16, 16, 16, 16, 16, 16, 31}, 'M': {17, 27, 21, 21, 17, 17, 17}, 'N': {17, 25, 21, 19, 17, 17, 17}, 'O': {14, 17, 17, 17, 17, 17, 14}, 'P': {30, 17, 17, 30, 16, 16, 16}, 'Q': {14, 17, 17, 17, 21, 18, 13}, 'R': {30, 17, 17, 30, 20, 18, 17}, 'S': {15, 16, 16, 14, 1, 1, 30}, 'T': {31, 4, 4, 4, 4, 4, 4}, 'U': {17, 17, 17, 17, 17, 17, 14}, 'V': {17, 17, 17, 17, 17, 10, 4}, 'W': {17, 17, 17, 21, 21, 21, 10}, 'X': {17, 17, 10, 4, 10, 17, 17}, 'Y': {17, 17, 10, 4, 4, 4, 4}, 'Z': {31, 1, 2, 4, 8, 16, 31},
	'0': {14, 17, 19, 21, 25, 17, 14}, '1': {4, 12, 4, 4, 4, 4, 14}, '2': {14, 17, 1, 2, 4, 8, 31}, '3': {30, 1, 1, 14, 1, 1, 30}, '4': {2, 6, 10, 18, 31, 2, 2}, '5': {31, 16, 16, 30, 1, 1, 30}, '6': {14, 16, 16, 30, 17, 17, 14}, '7': {31, 1, 2, 4, 8, 8, 8}, '8': {14, 17, 17, 14, 17, 17, 14}, '9': {14, 17, 17, 15, 1, 1, 14},
	'-': {0, 0, 0, 31, 0, 0, 0}, '_': {0, 0, 0, 0, 0, 0, 31}, '.': {0, 0, 0, 0, 0, 12, 12}, '/': {1, 2, 2, 4, 8, 8, 16}, ':': {0, 12, 12, 0, 12, 12, 0}, ' ': {0, 0, 0, 0, 0, 0, 0}, '[': {14, 8, 8, 8, 8, 8, 14}, ']': {14, 2, 2, 2, 2, 2, 14}, '(': {2, 4, 8, 8, 8, 4, 2}, ')': {8, 4, 2, 2, 2, 4, 8}, '%': {17, 2, 4, 8, 17, 0, 0}, '+': {0, 4, 4, 31, 4, 4, 0}, '=': {0, 31, 0, 31, 0, 0, 0}, '?': {14, 17, 1, 2, 4, 0, 4}, '!': {4, 4, 4, 4, 4, 0, 4},
}

func openFB() (*framebuffer, error) {
	f, e := os.OpenFile("/dev/fb0", os.O_RDWR, 0)
	if e != nil {
		return nil, e
	}
	var v fbVar
	_, _, er := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), 0x4600, uintptr(unsafe.Pointer(&v)))
	if er != 0 {
		f.Close()
		return nil, er
	}
	bpp := int(v.BitsPerPixel / 8)
	if bpp != 2 && bpp != 4 {
		f.Close()
		return nil, fmt.Errorf("unsupported framebuffer depth %d", v.BitsPerPixel)
	}
	var fixed fbFix
	_, _, er = syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), 0x4602, uintptr(unsafe.Pointer(&fixed)))
	if er != 0 {
		f.Close()
		return nil, er
	}
	stride := int(fixed.LineLength)
	if stride == 0 {
		stride = int(v.XresVirtual) * bpp
	}
	if stride < int(v.Xres)*bpp || v.Yres == 0 || v.YresVirtual == 0 {
		f.Close()
		return nil, fmt.Errorf("invalid framebuffer geometry")
	}
	mapLen64 := uint64(stride) * uint64(v.YresVirtual)
	backLen64 := uint64(stride) * uint64(v.Yres)
	maxInt := uint64(^uint(0) >> 1)
	if mapLen64 == 0 || mapLen64 > maxInt || backLen64 > maxInt || (fixed.SmemLen != 0 && mapLen64 > uint64(fixed.SmemLen)) {
		f.Close()
		return nil, fmt.Errorf("invalid framebuffer memory size")
	}
	mapLen := int(mapLen64)
	mapped, e := syscall.Mmap(int(f.Fd()), 0, mapLen, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	data := mapped
	if e == syscall.ENODEV {
		if fixed.SmemStart == 0 || fixed.SmemLen == 0 {
			f.Close()
			return nil, fmt.Errorf("framebuffer mmap unavailable and no physical framebuffer range was reported")
		}
		pageSize := syscall.Getpagesize()
		physStart := uint64(fixed.SmemStart)
		pageBase := physStart &^ uint64(pageSize-1)
		delta := int(physStart - pageBase)
		if mapLen > int(^uint(0)>>1)-delta {
			f.Close()
			return nil, fmt.Errorf("invalid physical framebuffer mapping size")
		}
		memFD, openErr := syscall.Open("/dev/mem", syscall.O_RDWR|syscall.O_SYNC|syscall.O_CLOEXEC, 0)
		if openErr != nil {
			f.Close()
			return nil, fmt.Errorf("open /dev/mem framebuffer fallback: %w", openErr)
		}
		mapped, e = syscall.Mmap(memFD, int64(pageBase), mapLen+delta, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
		_ = syscall.Close(memFD)
		if e == nil {
			data = mapped[delta : delta+mapLen]
		}
	}
	if e != nil {
		f.Close()
		return nil, e
	}
	back := make([]byte, int(backLen64))
	copy(back, data[:len(back)])
	return &framebuffer{f: f, mapped: mapped, data: data, back: back, w: int(v.Xres), h: int(v.Yres), stride: stride, bpp: bpp}, nil
}
func (f *framebuffer) close() { syscall.Munmap(f.mapped); f.f.Close() }
func (f *framebuffer) put(x, y int, c color.RGBA) {
	if x < 0 || y < 0 || x >= f.w || y >= f.h {
		return
	}
	o := y*f.stride + x*f.bpp
	if f.bpp == 4 {
		f.back[o] = c.B
		f.back[o+1] = c.G
		f.back[o+2] = c.R
		f.back[o+3] = 0
	} else {
		p := uint16(c.R>>3)<<11 | uint16(c.G>>2)<<5 | uint16(c.B>>3)
		f.back[o] = byte(p)
		f.back[o+1] = byte(p >> 8)
	}
}
func (f *framebuffer) rect(x, y, w, h int, c color.RGBA) {
	if f == nil || w <= 0 || h <= 0 {
		return
	}
	if x < 0 {
		w += x
		x = 0
	}
	if y < 0 {
		h += y
		y = 0
	}
	if x+w > f.w {
		w = f.w - x
	}
	if y+h > f.h {
		h = f.h - y
	}
	if w <= 0 || h <= 0 {
		return
	}

	row := make([]byte, w*f.bpp)
	if f.bpp == 4 {
		for i := 0; i < len(row); i += 4 {
			row[i] = c.B
			row[i+1] = c.G
			row[i+2] = c.R
			row[i+3] = 0
		}
	} else {
		p := uint16(c.R>>3)<<11 | uint16(c.G>>2)<<5 | uint16(c.B>>3)
		lo, hi := byte(p), byte(p>>8)
		for i := 0; i < len(row); i += 2 {
			row[i] = lo
			row[i+1] = hi
		}
	}

	start := y*f.stride + x*f.bpp
	for yy := 0; yy < h; yy++ {
		o := start + yy*f.stride
		copy(f.back[o:o+len(row)], row)
	}
}
func (f *framebuffer) border(x, y, w, h, t int, c color.RGBA) {
	f.rect(x, y, w, t, c)
	f.rect(x, y+h-t, w, t, c)
	f.rect(x, y, t, h, c)
	f.rect(x+w-t, y, t, h, c)
}
func (f *framebuffer) fill(c color.RGBA) { f.rect(0, 0, f.w, f.h, c) }
func (f *framebuffer) present() {
	if screenSaverActive.Load() {
		return
	}
	if f == nil || len(f.back) == 0 || len(f.data) == 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.stride * f.h
	if n > len(f.back) {
		n = len(f.back)
	}
	if n > len(f.data) {
		n = len(f.data)
	}
	copy(f.data[:n], f.back[:n])
}
func (f *framebuffer) blankLive() {
	if f == nil || len(f.data) == 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.stride * f.h
	if n > len(f.data) {
		n = len(f.data)
	}
	clear(f.data[:n])
}
func customFallbackPixelHeight(s int) int {
	if s < 1 {
		s = 1
	}
	px := (7*s*175 + 50) / 100
	if px < 1 {
		px = 1
	}
	return px
}
func (f *framebuffer) text(x, y, s int, str string, c color.RGBA) {
	cx := x
	for _, ch := range strings.ToUpper(str) {
		if g, ok := font[ch]; ok {
			for gy, row := range g {
				for gx := 0; gx < 5; gx++ {
					if row&(1<<(4-gx)) != 0 {
						f.rect(cx+gx*s, y+gy*s, s, s, c)
					}
				}
			}
			cx += 6 * s
			continue
		}
		px := customFallbackPixelHeight(s)
		if g, ok := customFontGlyph(ch, px); ok {
			baseline := y + 7*s - (px-7*s)/2 + 2*s
			for gy := 0; gy < g.h; gy++ {
				for gx := 0; gx < g.w; gx++ {
					if g.pixels[gy*g.w+gx] >= 96 {
						f.put(cx+g.xoff+gx, baseline+g.yoff+gy, c)
					}
				}
			}
			cx += g.advance
			continue
		}
		g := font['?']
		for gy, row := range g {
			for gx := 0; gx < 5; gx++ {
				if row&(1<<(4-gx)) != 0 {
					f.rect(cx+gx*s, y+gy*s, s, s, c)
				}
			}
		}
		cx += 6 * s
	}
}
func tw(s int, str string) int {
	w := 0
	for _, ch := range strings.ToUpper(str) {
		if _, ok := font[ch]; ok {
			w += 6 * s
		} else if adv, ok := customFontAdvance(ch, customFallbackPixelHeight(s)); ok {
			w += adv
		} else {
			w += 6 * s
		}
	}
	return w
}

func short(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 4 {
		return string(r[:n])
	}
	return string(r[:n-3]) + "..."
}

func inputLoop(ch chan<- action, done <-chan struct{}) {
	files, _ := filepath.Glob("/dev/input/event*")
	var mu sync.Mutex
	last := map[action]time.Time{}
	emit := func(a action) {
		if a == actNone {
			return
		}
		mu.Lock()
		now := time.Now()
		// Navigation should feel immediate. Keep only a short duplicate-event
		// guard for D-pad/keyboard movement, while confirm/back retain a
		// slightly longer guard to prevent accidental double activation.
		window := 45 * time.Millisecond
		if a == actConfirm || a == actBack {
			window = 110 * time.Millisecond
		}
		if t, ok := last[a]; ok && now.Sub(t) < window {
			mu.Unlock()
			return
		}
		last[a] = now
		mu.Unlock()
		select {
		case ch <- a:
		default:
		}
	}
	for _, p := range files {
		f, e := os.Open(p)
		if e != nil {
			continue
		}
		misterMap := loadMisterControllerMap(p)
		const grab = 0x40044590
		_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), grab, 1)
		go func(f *os.File, misterMap *misterControllerMap) {
			defer func() {
				_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), grab, 0)
				f.Close()
			}()
			var hx, hy int32
			pressed := map[uint16]bool{}
			for {
				select {
				case <-done:
					return
				default:
				}
				var ev inputEvent
				if binary.Read(f, binary.LittleEndian, &ev) != nil {
					return
				}

				if misterMap != nil {
					if a, handled, _ := misterMap.process(f, ev); handled {
						emit(a)
						continue
					}
				}

				a := actNone
				if ev.Type == evKey {
					if ev.Value == 0 {
						pressed[ev.Code] = false
					} else if ev.Value == 1 && !pressed[ev.Code] {
						pressed[ev.Code] = true
						switch ev.Code {
						case keyUp:
							a = actUp
						case keyDown:
							a = actDown
						case keyLeft:
							a = actLeft
						case keyRight:
							a = actRight
						case keyEnter:
							a = actConfirm
						case keyEsc, keyBackspace, keyBack:
							a = actBack
						case btnEast:
							if swapABInput.Load() {
								a = actBack
							} else {
								a = actConfirm
							}
						case btnSouth:
							if swapABInput.Load() {
								a = actConfirm
							} else {
								a = actBack
							}
						case btnWest, btnNorth:
							// Disc Tools has no X/Y-bound action yet; keep these reserved so the setting persists for future actions.
							a = actNone
						}
					}
				}
				if ev.Type == evAbs {
					if ev.Code == absHatX && ev.Value != hx {
						hx = ev.Value
						if ev.Value < 0 {
							a = actLeft
						} else if ev.Value > 0 {
							a = actRight
						}
					}
					if ev.Code == absHatY && ev.Value != hy {
						hy = ev.Value
						if ev.Value < 0 {
							a = actUp
						} else if ev.Value > 0 {
							a = actDown
						}
					}
				}
				emit(a)
			}
		}(f, misterMap)
	}
}

type App struct {
	fb   *framebuffer
	acts chan action
	done chan struct{}
	cfg  *Config
}

func (a *App) background() color.RGBA {
	if a.cfg != nil && a.cfg.OLEDMode {
		return color.RGBA{0, 0, 0, 255}
	}
	return bg
}
func (a *App) drawClock() {
	if a.cfg == nil || !a.cfg.ShowClock {
		return
	}
	scale := max(2, a.fb.h/300)
	txt := time.Now().Format("15:04")
	a.fb.text(a.fb.w-36-tw(scale, txt), 30, scale, txt, fg)
}
func (a *App) present() { a.drawClock(); a.fb.present() }

func (a *App) title(s string) {
	a.fb.text(36, 30, max(2, a.fb.h/300), s, fg)
}
func drawLine(fb *framebuffer, x0, y0, x1, y1, thick int, c color.RGBA) {
	if thick < 1 {
		thick = 1
	}
	dx := x1 - x0
	if dx < 0 {
		dx = -dx
	}
	sx := -1
	if x0 < x1 {
		sx = 1
	}
	dy := y1 - y0
	if dy > 0 {
		dy = -dy
	}
	sy := -1
	if y0 < y1 {
		sy = 1
	}
	err := dx + dy
	for {
		fb.rect(x0-thick/2, y0-thick/2, thick, thick, c)
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func drawFooterCircle(fb *framebuffer, cx, cy, r, thick int, c color.RGBA) {
	// Integer midpoint circle keeps the footer lightweight and avoids floating point.
	x, y := r, 0
	err := 1 - x
	for x >= y {
		pts := [][2]int{
			{cx + x, cy + y}, {cx + y, cy + x}, {cx - y, cy + x}, {cx - x, cy + y},
			{cx - x, cy - y}, {cx - y, cy - x}, {cx + y, cy - x}, {cx + x, cy - y},
		}
		for _, pt := range pts {
			fb.rect(pt[0]-thick/2, pt[1]-thick/2, thick, thick, c)
		}
		y++
		if err < 0 {
			err += 2*y + 1
		} else {
			x--
			err += 2*(y-x) + 1
		}
	}
}

func drawFooterButton(fb *framebuffer, x, cy, iconSize, textScale int, button, label string, c color.RGBA) int {
	cx := x + iconSize/2
	drawFooterCircle(fb, cx, cy, iconSize/2, max(1, iconSize/10), c)
	buttonScale := max(1, iconSize/12)
	fb.text(cx-tw(buttonScale, button)/2, cy-7*buttonScale/2, buttonScale, button, c)
	tx := x + iconSize + max(5, iconSize/5)
	fb.text(tx, cy-7*textScale/2, textScale, label, c)
	return tx + tw(textScale, label) + max(14, iconSize*2/5)
}

func (a *App) footer(msg string) {
	// Match MiSTer Hi-Fi's compact, resolution-scaled footer treatment.
	ts := max(1, a.fb.h/540)
	icon := max(14, 14*ts)
	cy := a.fb.h - max(18, icon/2+7)
	x := 36
	upper := strings.ToUpper(msg)

	// Disc Tools currently uses A/ENTER and B/ESC control hints. Render those as
	// Hi-Fi-style outlined controller buttons while preserving keyboard labels.
	if strings.Contains(upper, "A/ENTER") {
		label := "SELECT"
		if strings.Contains(upper, "A/ENTER OR B/ESC BACK") {
			label = "BACK"
		}
		x = drawFooterButton(a.fb, x, cy, icon, ts, "A", label, dim)
	}
	if strings.Contains(upper, "B/ESC") {
		label := "BACK"
		if strings.Contains(upper, "CANCEL") {
			label = "CANCEL"
		}
		x = drawFooterButton(a.fb, x, cy, icon, ts, "B", label, dim)
	}
	if !strings.Contains(upper, "A/ENTER") && !strings.Contains(upper, "B/ESC") {
		a.fb.text(x, cy-7*ts/2, ts, short(upper, max(20, (a.fb.w-72)/(6*ts))), dim)
	}
	_ = x
}
func (a *App) menu(title string, items []string, initial int) (int, bool) {
	return a.menuWithBack(title, items, initial, true)
}

func (a *App) menuWithBack(title string, items []string, initial int, allowBack bool) (int, bool) {
	if len(items) == 0 {
		return 0, false
	}
	sel := initial
	if sel < 0 || sel >= len(items) {
		sel = 0
	}
	isSelectable := func(i int) bool { return i >= 0 && i < len(items) && strings.TrimSpace(items[i]) != "" }
	if !isSelectable(sel) {
		for i := range items {
			if isSelectable(i) {
				sel = i
				break
			}
		}
	}
	moveSel := func(from, dir int) int {
		for i := from + dir; i >= 0 && i < len(items); i += dir {
			if isSelectable(i) {
				return i
			}
		}
		return from
	}
	for {
		a.fb.fill(a.background())
		a.title(title)
		row := max(42, a.fb.h/10)
		maxRows := max(1, (a.fb.h-130)/row)
		first := 0
		if sel >= maxRows {
			first = sel - maxRows + 1
		}
		if first+maxRows > len(items) {
			first = max(0, len(items)-maxRows)
		}
		y := 82
		for i := first; i < len(items) && i < first+maxRows; i++ {
			if i == sel {
				a.fb.rect(38, y-7, a.fb.w-76, row-4, panel)
				a.fb.border(38, y-7, a.fb.w-76, row-4, 2, fg)
			}
			a.fb.text(58, y+4, max(1, row/25), short(items[i], 70), fg)
			y += row
		}
		if allowBack {
			a.footer("A/ENTER Select   B/ESC Back")
		} else {
			a.footer("A/ENTER Select")
		}
		a.present()
		switch <-a.acts {
		case actUp:
			sel = moveSel(sel, -1)
		case actDown:
			sel = moveSel(sel, 1)
		case actLeft:
			target := max(0, sel-5)
			if !isSelectable(target) {
				target = moveSel(sel, -1)
			}
			sel = target
		case actRight:
			target := min(len(items)-1, sel+5)
			if !isSelectable(target) {
				target = moveSel(sel, 1)
			}
			sel = target
		case actConfirm:
			return sel, true
		case actBack:
			if allowBack {
				return 0, false
			}
		}
	}
}

// chdFastCodecs are the chdman createcd options of FAST CHD: zlib only.
var chdFastCodecs = []string{"-c", "cdzl"}

func (a *App) chdWarning() bool {
	sel := 1 // Default to Cancel so CHD work is never started accidentally.
	items := []string{"CONTINUE", "CANCEL"}
	for {
		a.fb.fill(a.background())
		a.title("CHD PERFORMANCE WARNING")

		y := 92
		lines := []string{
			"CHD processing is very slow on MiSTer.",
			"Creating or extracting a CHD can take a long time.",
			"You may want to use MiSTer Companion on desktop instead.",
		}
		for _, line := range lines {
			for _, part := range wrap(line, max(20, (a.fb.w-90)/12)) {
				a.fb.text(48, y, 2, part, fg)
				y += 28
			}
		}

		y += 24
		row := max(48, a.fb.h/11)
		for i, item := range items {
			if i == sel {
				a.fb.rect(38, y-7, a.fb.w-76, row-4, panel)
				a.fb.border(38, y-7, a.fb.w-76, row-4, 2, fg)
			}
			a.fb.text(58, y+4, max(1, row/25), item, fg)
			y += row
		}
		a.footer("A/ENTER Select   B/ESC Cancel")
		a.present()

		switch <-a.acts {
		case actUp, actLeft:
			if sel > 0 {
				sel--
			}
		case actDown, actRight:
			if sel < len(items)-1 {
				sel++
			}
		case actConfirm:
			return sel == 0
		case actBack:
			return false
		}
	}
}

func (a *App) message(title string, lines []string) {
	for {
		a.fb.fill(a.background())
		a.title(title)
		y := 90
		for _, line := range lines {
			for _, part := range wrap(line, max(20, (a.fb.w-90)/12)) {
				a.fb.text(48, y, 2, part, fg)
				y += 24
			}
		}
		a.footer("A/ENTER or B/ESC Back")
		a.present()
		x := <-a.acts
		if x == actConfirm || x == actBack {
			return
		}
	}
}
func wrap(s string, n int) []string {
	var out []string
	for len([]rune(s)) > n {
		r := []rune(s)
		cut := n
		for cut > 0 && r[cut] != ' ' {
			cut--
		}
		if cut < 1 {
			cut = n
		}
		out = append(out, string(r[:cut]))
		s = strings.TrimSpace(string(r[cut:]))
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

func helper(name string) string { return filepath.Join(binDir, name) }
func checkHelper(name string) error {
	p := helper(name)
	st, e := os.Stat(p)
	if e != nil {
		return fmt.Errorf("missing helper: %s", p)
	}
	if st.Mode()&0111 == 0 {
		_ = os.Chmod(p, 0755)
	}
	return nil
}

var jobPercentRE = regexp.MustCompile(`(?i)(?:^|[^0-9])([0-9]{1,3}(?:\.[0-9]+)?)\s*%`)
var cdrdaoWroteRE = regexp.MustCompile(`(?i)\bWrote\s+([0-9]+(?:\.[0-9]+)?)\s+of\s+([0-9]+(?:\.[0-9]+)?)\s+([KMGT]?B)\b`)
var cdrdaoLeadoutRE = regexp.MustCompile(`(?i)^\s*Leadout\b.*\(\s*([0-9]+)\s*\)`)
var tocDataFileRE = regexp.MustCompile(`(?im)^\s*(?:DATAFILE|AUDIOFILE|FILE)\s+"([^"]+)"`)

func chdSourceBytes(args []string) int64 {
	input := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-i" || args[i] == "--input" {
			input = args[i+1]
			break
		}
	}
	if input == "" {
		return 0
	}
	ext := strings.ToLower(filepath.Ext(input))
	if ext != ".toc" && ext != ".cue" {
		if st, err := os.Stat(input); err == nil {
			return st.Size()
		}
		return 0
	}
	b, err := os.ReadFile(input)
	if err != nil {
		return 0
	}
	seen := map[string]bool{}
	var total int64
	base := filepath.Dir(input)
	for _, m := range tocDataFileRE.FindAllStringSubmatch(string(b), -1) {
		if len(m) != 2 {
			continue
		}
		name := strings.TrimSpace(m[1])
		path := name
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, filepath.FromSlash(path))
		}
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		if st, e := os.Stat(path); e == nil && !st.IsDir() {
			total += st.Size()
		}
	}
	// cdrdao's generated TOC normally references the raw BIN. If the exact
	// directive was not recognized, use the matching basename as a fallback.
	if total == 0 && ext == ".toc" {
		bin := strings.TrimSuffix(input, filepath.Ext(input)) + ".bin"
		if st, e := os.Stat(bin); e == nil {
			total = st.Size()
		}
	}
	return total
}

func procReadChars(pid int) int64 {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/io", pid))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimSuffix(f[0], ":") == "rchar" {
			if v, e := strconv.ParseInt(f[1], 10, 64); e == nil {
				return v
			}
		}
	}
	return 0
}

type jobUpdate struct {
	text    string
	percent float64
	hasPct  bool
}

func parseJobUpdate(s string) jobUpdate {
	s = strings.TrimSpace(s)
	u := jobUpdate{text: s}
	if m := jobPercentRE.FindStringSubmatch(s); len(m) == 2 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			if v < 0 {
				v = 0
			}
			if v > 100 {
				v = 100
			}
			u.percent = v
			u.hasPct = true
		}
	}
	return u
}

// parseCdrdaoBurnUpdate deliberately ignores percentages in cdrdao's
// "Buffers 100% 99%" status. Those describe buffer fullness, not overall
// burn completion. Real burn progress is reported as "Wrote X of Y MB".
func parseCdrdaoBurnUpdate(s string) jobUpdate {
	s = strings.TrimSpace(s)
	u := jobUpdate{text: s}
	if m := cdrdaoWroteRE.FindStringSubmatch(s); len(m) == 4 {
		written, e1 := strconv.ParseFloat(m[1], 64)
		total, e2 := strconv.ParseFloat(m[2], 64)
		if e1 == nil && e2 == nil && total > 0 {
			v := written * 100.0 / total
			if v < 0 {
				v = 0
			}
			if v > 100 {
				v = 100
			}
			u.percent = v
			u.hasPct = true
		}
	}
	return u
}

// readJobOutput treats both newline and carriage return as record separators.
// Tools such as chdman update progress in-place with '\r', so line-only
// scanners would otherwise make a healthy operation look frozen.
func readJobOutput(r io.Reader, out chan<- string) {
	defer close(out)
	br := bufio.NewReaderSize(r, 64*1024)
	var b strings.Builder
	flush := func() {
		s := strings.TrimSpace(b.String())
		b.Reset()
		if s != "" {
			out <- s
		}
	}
	for {
		c, err := br.ReadByte()
		if err != nil {
			flush()
			return
		}
		switch c {
		case '\r', '\n':
			flush()
		default:
			if b.Len() < 1024*1024 {
				b.WriteByte(c)
			}
		}
	}
}

func centeredX(width, scale int, text string) int {
	x := (width - tw(scale, text)) / 2
	if x < 12 {
		return 12
	}
	return x
}

func jobStatus(title string, finalizing bool) string {
	if finalizing {
		return "Please do not eject the disc"
	}
	switch title {
	case "RIPPING PHYSICAL DISC":
		return "Reading physical disc"
	case "CREATING CUE":
		return "Preparing disc layout"
	case "CONVERTING TO CHD":
		return "Compressing disc image"
	case "VERIFYING CHD":
		return "Checking CHD image"
	case "EXTRACTING CHD":
		return "Preparing disc image"
	case "BURNING DISC":
		return "Writing disc"
	case "BUILDING ISO9660/JOLIET":
		return "Building data disc image"
	case "BURNING DATA DISC":
		return "Writing data disc"
	case "ERASING CD-RW":
		return "Erasing rewritable disc"
	default:
		return "Processing"
	}
}

func jobDisplayFile(args []string) string {
	for i := len(args) - 1; i >= 1; i-- {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(a))
		switch ext {
		case ".chd", ".cue", ".bin", ".iso", ".toc":
			return filepath.Base(a)
		}
	}
	return ""
}

func cdrdaoAmount(s string) string {
	if m := cdrdaoWroteRE.FindStringSubmatch(s); len(m) == 4 {
		return fmt.Sprintf("%s %s / %s %s", m[1], strings.ToUpper(m[3]), m[2], strings.ToUpper(m[3]))
	}
	return ""
}

func helperFailure(err error, lines []string) error {
	if err == nil {
		return nil
	}

	// Keep the failure screen useful without dumping the helper's full console
	// chatter. Prefer the last non-empty, non-progress lines, which is where
	// chdman/cdrdao/xorriso normally print their actual diagnostic.
	meaningful := make([]string, 0, 6)
	for i := len(lines) - 1; i >= 0 && len(meaningful) < 6; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if jobPercentRE.MatchString(line) &&
			!strings.Contains(lower, "error") &&
			!strings.Contains(lower, "failed") &&
			!strings.Contains(lower, "fatal") &&
			!strings.Contains(lower, "unable") &&
			!strings.Contains(lower, "cannot") &&
			!strings.Contains(lower, "invalid") {
			continue
		}
		meaningful = append(meaningful, line)
	}
	for i, j := 0, len(meaningful)-1; i < j; i, j = i+1, j-1 {
		meaningful[i], meaningful[j] = meaningful[j], meaningful[i]
	}

	if len(meaningful) == 0 {
		return err
	}
	return fmt.Errorf("%v\n%s", err, strings.Join(meaningful, "\n"))
}

func (a *App) runJob(title string, cmd *exec.Cmd) error {
	_ = os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, "disctools.log")
	lf, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if lf != nil {
		defer lf.Close()
		fmt.Fprintf(lf, "\n[%s] %s\n", time.Now().Format(time.RFC3339), strings.Join(cmd.Args, " "))
	}
	isCdrdaoBlank := len(cmd.Args) >= 2 && filepath.Base(cmd.Args[0]) == "cdrdao" && cmd.Args[1] == "blank"

	// Present the next operation before starting the helper. This prevents the
	// completed frame from the previous job (notably RIP 100%) from remaining
	// on screen while the next helper is being launched or initialized.
	a.fb.fill(a.background())
	a.title("DISC TOOLS V" + version)
	opScale := max(2, a.fb.h/240)
	workScale := max(3, a.fb.h/180)
	centerY := a.fb.h / 2
	opY := centerY - max(155, a.fb.h/5)
	a.fb.text(centeredX(a.fb.w, opScale, title), opY, opScale, title, fg)
	a.fb.text(centeredX(a.fb.w, workScale, "STARTING..."), opY+90, workScale, "STARTING...", fg)
	a.fb.text(centeredX(a.fb.w, 1, jobStatus(title, false)), opY+175, 1, jobStatus(title, false), fg)
	if isCdrdaoBlank {
		a.footer("Erasing cannot be cancelled - do not eject")
	} else {
		a.footer("B/ESC Cancel")
	}
	a.present()

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		return err
	}

	// Heavy helpers run below the UI priority so framebuffer redraw and input
	// remain responsive even during CHD work or sustained optical-disc I/O.
	if cmd.Process != nil {
		_ = syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, 10)
	}

	updates := make(chan string, 64)
	outputTail := make([]string, 0, 16)
	rememberOutput := func(line string) {
		line = strings.TrimSpace(line)
		if line == "" {
			return
		}
		outputTail = append(outputTail, line)
		if len(outputTail) > 16 {
			outputTail = outputTail[len(outputTail)-16:]
		}
	}
	done := make(chan error, 1)
	go readJobOutput(pr, updates)
	go func() {
		err := cmd.Wait()
		_ = pw.Close()
		done <- err
	}()

	percent := 0.0
	hasPercent := false
	isCdrdaoBurn := len(cmd.Args) >= 2 && filepath.Base(cmd.Args[0]) == "cdrdao" && (cmd.Args[1] == "write" || cmd.Args[1] == "simulate")
	isCdrdaoRead := len(cmd.Args) >= 2 && filepath.Base(cmd.Args[0]) == "cdrdao" && cmd.Args[1] == "read-cd"
	isChdCreate := len(cmd.Args) >= 2 && filepath.Base(cmd.Args[0]) == "chdman" && cmd.Args[1] == "createcd"
	ripDataFile := ""
	if isCdrdaoRead {
		for i := 0; i+1 < len(cmd.Args); i++ {
			if cmd.Args[i] == "--datafile" {
				ripDataFile = cmd.Args[i+1]
				break
			}
		}
	}
	ripTotalSectors := int64(0)
	finalizing := false
	ripFinalizing := false
	amount := ""
	fileName := jobDisplayFile(cmd.Args)
	started := time.Now()
	spin := 0
	blankMode := "Fast erase in progress"
	if isCdrdaoBlank {
		for i := 0; i+1 < len(cmd.Args); i++ {
			if cmd.Args[i] == "--blank-mode" && cmd.Args[i+1] == "full" {
				blankMode = "Full erase in progress"
				break
			}
		}
	}

	handleOutput := func(s string) {
		rememberOutput(s)
		if lf != nil {
			fmt.Fprintln(lf, s)
		}
		if isCdrdaoRead && ripTotalSectors == 0 {
			if m := cdrdaoLeadoutRE.FindStringSubmatch(s); len(m) == 2 {
				if v, e := strconv.ParseInt(m[1], 10, 64); e == nil && v > 0 {
					ripTotalSectors = v
				}
			}
		}
		var u jobUpdate
		if isCdrdaoBurn {
			u = parseCdrdaoBurnUpdate(s)
			if v := cdrdaoAmount(s); v != "" {
				amount = v
			}
		} else {
			u = parseJobUpdate(s)
		}
		if u.hasPct {
			// Burn progress must be monotonic even if helper messages were queued.
			if !hasPercent || u.percent > percent {
				percent = u.percent
			}
			hasPercent = true
			if isCdrdaoBurn && percent >= 100 {
				finalizing = true
			}
		}
		if isCdrdaoBurn {
			lower := strings.ToLower(s)
			if strings.Contains(lower, "flushing cache") ||
				strings.Contains(lower, "writing lead-out") ||
				strings.Contains(lower, "fixating") ||
				strings.Contains(lower, "writing finished successfully") {
				finalizing = true
				percent = 100
				hasPercent = true
			}
		}
	}

	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

	for {
		a.fb.fill(a.background())
		a.title("DISC TOOLS V" + version)

		// Console-style operation view: the operation, percentage and progress
		// bar are the visual focus, while details stay intentionally minimal.
		opScale := max(2, a.fb.h/240)
		pctScale := max(4, a.fb.h/145)
		statusScale := max(1, a.fb.h/360)
		centerY := a.fb.h / 2
		opY := centerY - max(155, a.fb.h/5)
		a.fb.text(centeredX(a.fb.w, opScale, title), opY, opScale, title, fg)

		if hasPercent {
			pctText := fmt.Sprintf("%.1f%%", percent)
			a.fb.text(centeredX(a.fb.w, pctScale, pctText), opY+70, pctScale, pctText, fg)

			barW := max(260, (a.fb.w*3)/4)
			if barW > a.fb.w-80 {
				barW = a.fb.w - 80
			}
			barH := max(24, a.fb.h/28)
			barX := (a.fb.w - barW) / 2
			barY := opY + 145
			a.fb.border(barX, barY, barW, barH, max(2, barH/10), dim)
			innerMax := max(0, barW-8)
			inner := int(float64(innerMax) * percent / 100.0)
			if inner > 0 {
				a.fb.rect(barX+4, barY+4, inner, max(1, barH-8), fg)
			}

			status := jobStatus(title, finalizing)
			if ripFinalizing {
				status = "FINALIZING RIP" + strings.Repeat(".", spin%4)
			}
			statusY := barY + barH + 35
			a.fb.text(centeredX(a.fb.w, statusScale, status), statusY, statusScale, status, fg)

			detailY := statusY + 34
			if amount != "" && !finalizing {
				a.fb.text(centeredX(a.fb.w, 1, amount), detailY, 1, amount, dim)
				detailY += 22
			}
			if fileName != "" {
				name := short(fileName, max(24, (a.fb.w-100)/6))
				a.fb.text(centeredX(a.fb.w, 1, name), detailY, 1, name, dim)
				detailY += 22
			}
			elapsed := "Elapsed " + time.Since(started).Round(time.Second).String()
			a.fb.text(centeredX(a.fb.w, 1, elapsed), detailY, 1, elapsed, dim)
		} else if isCdrdaoBlank {
			// cdrdao's command-line blank operation does not expose real media
			// progress. Keep the standard Disc Tools progress-bar presentation,
			// but animate a moving segment rather than inventing a percentage.
			workScale := max(3, a.fb.h/180)
			erasing := "ERASING" + strings.Repeat(".", spin%4)
			a.fb.text(centeredX(a.fb.w, workScale, erasing), opY+70, workScale, erasing, fg)

			barW := max(260, (a.fb.w*3)/4)
			if barW > a.fb.w-80 {
				barW = a.fb.w - 80
			}
			barH := max(24, a.fb.h/28)
			barX := (a.fb.w - barW) / 2
			barY := opY + 145
			a.fb.border(barX, barY, barW, barH, max(2, barH/10), dim)
			innerMax := max(1, barW-8)
			segmentW := max(24, innerMax/4)
			travel := max(1, innerMax-segmentW)
			phase := (spin * max(2, travel/30)) % (travel * 2)
			if phase > travel {
				phase = travel*2 - phase
			}
			a.fb.rect(barX+4+phase, barY+4, segmentW, max(1, barH-8), fg)

			a.fb.text(centeredX(a.fb.w, statusScale, blankMode), barY+barH+35, statusScale, blankMode, fg)
			elapsed := "Elapsed " + time.Since(started).Round(time.Second).String()
			a.fb.text(centeredX(a.fb.w, 1, elapsed), barY+barH+69, 1, elapsed, dim)
		} else {
			working := "WORKING" + strings.Repeat(".", spin%4)
			workScale := max(3, a.fb.h/180)
			a.fb.text(centeredX(a.fb.w, workScale, working), opY+90, workScale, working, fg)
			status := jobStatus(title, false)
			a.fb.text(centeredX(a.fb.w, statusScale, status), opY+175, statusScale, status, fg)
			detailY := opY + 215
			if fileName != "" {
				name := short(fileName, max(24, (a.fb.w-100)/6))
				a.fb.text(centeredX(a.fb.w, 1, name), detailY, 1, name, dim)
				detailY += 22
			}
			elapsed := "Elapsed " + time.Since(started).Round(time.Second).String()
			a.fb.text(centeredX(a.fb.w, 1, elapsed), detailY, 1, elapsed, dim)
		}

		if isCdrdaoBlank {
			a.footer("Erasing cannot be cancelled - do not eject")
		} else {
			a.footer("B/ESC Cancel")
		}
		a.present()

		select {
		case s, ok := <-updates:
			if ok {
				handleOutput(s)

				// cdrdao emits one update per MB and can outrun framebuffer redraws.
				// Collapse everything already queued into the latest state so the UI
				// never continues replaying old percentages after the drive ejects.
			drainOutput:
				for {
					select {
					case more, moreOK := <-updates:
						if !moreOK {
							break drainOutput
						}
						handleOutput(more)
					default:
						break drainOutput
					}
				}
			}
		case err := <-done:
			if err == nil && isCdrdaoBurn {
				percent = 100
				hasPercent = true
				finalizing = true
			}
			// Drain final output to the logfile and retain a small diagnostic tail.
			for {
				select {
				case s, ok := <-updates:
					if !ok {
						return helperFailure(err, outputTail)
					}
					rememberOutput(s)
					if lf != nil {
						fmt.Fprintln(lf, s)
					}
				default:
					return helperFailure(err, outputTail)
				}
			}
		case <-tick.C:
			spin++
			// CHDMan createcd normally reports progress through carriage-return
			// percentage updates, handled by readJobOutput/parseJobUpdate. Keep the
			// UI animated even before its first percentage arrives; do not probe
			// /proc or network-backed source files from the render loop.
			_ = isChdCreate
			if isCdrdaoRead && ripTotalSectors > 0 && ripDataFile != "" {
				if st, e := os.Stat(ripDataFile); e == nil {
					totalBytes := ripTotalSectors * 2352
					if totalBytes > 0 {
						readBytes := st.Size()
						if readBytes > totalBytes {
							readBytes = totalBytes
						}
						percent = float64(readBytes) * 100.0 / float64(totalBytes)
						if percent >= 100.0 {
							percent = 100.0
							ripFinalizing = true
						}
						hasPercent = true
						amount = fmt.Sprintf("%.1f MB / %.1f MB", float64(readBytes)/(1024*1024), float64(totalBytes)/(1024*1024))
					}
				}
			}
		case x := <-a.acts:
			if x == actBack && !isCdrdaoBlank {
				_ = cmd.Process.Kill()
				<-done
				return errors.New("cancelled")
			}
		}
	}
}

func findUSBRoots() []string {
	ms, _ := filepath.Glob("/media/usb*")
	var out []string
	for _, p := range ms {
		if st, e := os.Stat(p); e == nil && st.IsDir() {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

type browserEntry struct {
	name, path string
	dir        bool
}

func (a *App) browse(root string, exts map[string]bool, foldersOnly bool) (string, bool) {
	cur := root
	for {
		es, e := os.ReadDir(cur)
		if e != nil {
			a.message("ERROR", []string{e.Error()})
			return "", false
		}
		entries := []browserEntry{}
		for _, x := range es {
			if strings.HasPrefix(x.Name(), ".") {
				continue
			}
			p := filepath.Join(cur, x.Name())
			if x.IsDir() {
				entries = append(entries, browserEntry{x.Name(), p, true})
			} else if !foldersOnly && exts[strings.ToLower(filepath.Ext(x.Name()))] {
				entries = append(entries, browserEntry{x.Name(), p, false})
			}
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].dir != entries[j].dir {
				return entries[i].dir
			}
			return strings.ToLower(entries[i].name) < strings.ToLower(entries[j].name)
		})
		items := []string{"[..]"}
		if foldersOnly {
			items = append(items, "[USE THIS FOLDER]")
		}
		for _, e := range entries {
			n := e.name
			if e.dir {
				n = "[DIR] " + n
			}
			items = append(items, n)
		}
		i, ok := a.menu("BROWSE: "+short(cur, 42), items, 0)
		if !ok {
			return "", false
		}
		if i == 0 {
			if filepath.Clean(cur) == filepath.Clean(root) {
				return "", false
			}
			cur = filepath.Dir(cur)
			continue
		}
		offset := 1
		if foldersOnly {
			if i == 1 {
				return cur, true
			}
			offset = 2
		}
		entry := entries[i-offset]
		if entry.dir {
			cur = entry.path
			continue
		}
		return entry.path, true
	}
}
func (a *App) chooseStorage(title string) (string, bool) {
	items := []string{"SD CARD"}
	roots := []string{"/media/fat"}
	for _, u := range findUSBRoots() {
		items = append(items, "USB: "+filepath.Base(u))
		roots = append(roots, u)
	}
	i, ok := a.menu(title, items, 0)
	if !ok {
		return "", false
	}
	return roots[i], true
}

func (a *App) discInfo() {
	if err := checkHelper("cdrdao"); err != nil {
		a.message("DEPENDENCY", []string{err.Error()})
		return
	}
	cmd := exec.Command(helper("cdrdao"), "disk-info", "--device", device)
	out, e := cmd.CombinedOutput()
	if e != nil {
		a.message("DISC INFORMATION", []string{"Unable to read disc information.", string(out)})
		return
	}
	a.message("DISC INFORMATION", strings.Split(string(out), "\n"))
}
func (a *App) eject() {
	cmd := exec.Command("eject", device)
	if err := cmd.Run(); err != nil {
		if checkHelper("cdrdao") == nil {
			_ = exec.Command(helper("cdrdao"), "unlock", "--device", device).Run()
			_ = exec.Command("eject", device).Run()
		}
	}
}

type discMediaInfo struct {
	cdrwKnown  bool
	cdrw       bool
	emptyKnown bool
	empty      bool
}

func parseDiscMediaInfo(out string) discMediaInfo {
	var info discMediaInfo
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.ToLower(strings.TrimSpace(parts[1]))
		switch key {
		case "CD-RW":
			if value == "yes" || value == "no" {
				info.cdrwKnown = true
				info.cdrw = value == "yes"
			}
		case "CD-R empty":
			if value == "yes" || value == "no" {
				info.emptyKnown = true
				info.empty = value == "yes"
			}
		}
	}
	return info
}

func (a *App) readDiscMediaInfo() (discMediaInfo, error) {
	cmd := exec.Command(helper("cdrdao"), "disk-info", "--device", device)
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return discMediaInfo{}, fmt.Errorf("%v\n%s", err, detail)
		}
		return discMediaInfo{}, err
	}
	info := parseDiscMediaInfo(string(out))
	if !info.cdrwKnown || !info.emptyKnown {
		return info, errors.New("the inserted media type could not be determined")
	}
	return info, nil
}

func (a *App) eraseCDRW(mode string) error {
	cmd := exec.Command(helper("cdrdao"), "blank", "--device", device, "--blank-mode", mode)
	if err := a.runJob("ERASING CD-RW", cmd); err != nil {
		return err
	}
	info, err := a.readDiscMediaInfo()
	if err != nil {
		return fmt.Errorf("erase completed, but the disc could not be verified: %w", err)
	}
	if !info.cdrw || !info.empty {
		return errors.New("the drive did not report the CD-RW as blank after erasing")
	}
	return nil
}

func (a *App) eraseDisc() {
	if err := checkHelper("cdrdao"); err != nil {
		a.message("DEPENDENCY", []string{err.Error()})
		return
	}
	info, err := a.readDiscMediaInfo()
	if err != nil {
		a.message("CD-RW CHECK FAILED", []string{"Unable to identify the inserted disc.", err.Error()})
		return
	}
	if !info.cdrw {
		a.message("NOT A CD-RW", []string{"The inserted disc is not rewritable and cannot be erased."})
		return
	}
	if info.empty {
		a.message("CD-RW ALREADY BLANK", []string{"The inserted CD-RW is already ready for burning."})
		return
	}
	i, ok := a.menu("ERASE CD-RW", []string{"FAST ERASE", "FULL ERASE"}, 0)
	if !ok {
		return
	}
	mode := "minimal"
	label := "FAST"
	if i == 1 {
		mode = "full"
		label = "FULL"
	}
	i, ok = a.menu("CONFIRM "+label+" ERASE", []string{"ERASE CD-RW", "CANCEL"}, 1)
	if !ok || i != 0 {
		return
	}
	if err := a.eraseCDRW(mode); err != nil {
		a.message("ERASE FAILED", []string{err.Error(), "The CD-RW may need to be erased again."})
		return
	}
	a.message("ERASE COMPLETE", []string{"The CD-RW was erased successfully and is ready for burning."})
}

func (a *App) prepareBurnMedia() bool {
	info, err := a.readDiscMediaInfo()
	if err != nil {
		a.message("DISC CHECK FAILED", []string{"Unable to verify that the inserted disc is writable.", err.Error()})
		return false
	}
	if info.empty {
		return true
	}
	if !info.cdrw {
		a.message("DISC IS NOT BLANK", []string{"Insert a blank CD-R or CD-RW before burning."})
		return false
	}
	i, ok := a.menu("USED CD-RW DETECTED", []string{"FAST ERASE AND CONTINUE", "CANCEL"}, 1)
	if !ok || i != 0 {
		return false
	}
	if err := a.eraseCDRW("minimal"); err != nil {
		a.message("ERASE FAILED", []string{err.Error(), "Burning was not started."})
		return false
	}
	return true
}

func safeBaseName(s string) string {
	re := regexp.MustCompile(`[^A-Za-z0-9._ -]+`)
	s = re.ReplaceAllString(s, "_")
	s = strings.TrimSpace(s)
	if s == "" {
		s = "disc"
	}
	return s
}
func newRipBase(dest string) string {
	return filepath.Join(dest, "Disc-"+time.Now().Format("20060102-150405"))
}

// normalizeDescriptorBinReference keeps CDRDAO/CHDMan descriptor files portable.
// cdrdao writes the --datafile path exactly as supplied, which is absolute in
// Disc Tools. CHDMan resolves descriptor references relative to the descriptor
// directory, so an absolute-looking reference can become /dir//dir/file.bin.
// Rewrite only this rip's BIN reference to its basename; the BIN itself stays
// in place next to the TOC/CUE.
func normalizeDescriptorBinReference(descriptor, bin string) error {
	b, err := os.ReadFile(descriptor)
	if err != nil {
		return err
	}

	contents := string(b)
	base := filepath.Base(bin)
	contents = strings.ReplaceAll(contents, `"`+bin+`"`, `"`+base+`"`)
	contents = strings.ReplaceAll(contents, bin, base)

	return os.WriteFile(descriptor, []byte(contents), 0644)
}

func (a *App) ripDisc() {
	if err := checkHelper("cdrdao"); err != nil {
		a.message("DEPENDENCY", []string{err.Error()})
		return
	}
	root, ok := a.chooseStorage("RIP DISC - DESTINATION")
	if !ok {
		return
	}
	dest, ok := a.browse(root, nil, true)
	if !ok {
		return
	}
	i, ok := a.menu("RIP DISC - OUTPUT", []string{"BIN/CUE", "CHD (KEEP BIN/CUE)", "CHD (DELETE BIN/CUE AFTER VERIFY)", "FAST CHD (DELETE BIN/CUE AFTER VERIFY)", "ULTRA FAST CHD - UNCOMPRESSED (DELETE BIN/CUE AFTER VERIFY)"}, 0)
	if !ok {
		return
	}
	// FAST CHD: same image, same verification, but every hunk is stored with
	// zlib (cdzl) only. chdman normally compresses each hunk with LZMA, zlib
	// and FLAC and keeps the smallest result; LZMA and FLAC are the slow ones.
	// About 4x quicker to create, about 15% larger for data tracks (more for
	// CD-DA audio), and quicker to decompress. Subchannel data is identical.
	fastCHD := i == 3
	// ULTRA FAST CHD: no compression at all. One file with data and subchannel
	// (instead of BIN + CUE + .sub), about the size of the BIN, created in
	// about the time of a file copy. chdman verify cannot check uncompressed
	// CHDs, so every frame is compared with the rip instead.
	uncompressedCHD := i == 4
	deleteAfterVerify := i == 2 || i == 3 || i == 4
	if i != 0 && !uncompressedCHD && !a.chdWarning() { // no compression: nothing slow to warn about
		return
	}
	base := newRipBase(dest)
	bin := base + ".bin"
	toc := base + ".toc"
	cue := base + ".cue"
	chd := base + ".chd"
	sub := base + ".sub"
	// Keep cdrdao's native raw image untouched while the TOC is still in use.
	// cdrdao stores raw CD-DA samples in big-endian order; standard CUE/BIN
	// images use the opposite byte order. We create a separate CUE-compatible
	// BIN below with toc2cue -C -s, swapping only the AUDIO tracks.
	//
	// The raw P-W subchannel is read too: it holds the Q position data of every
	// sector, including deliberately broken Q sectors used by copy protections
	// such as PSX LibCrypt. It ends up in the CHD and in a .sub file next to the
	// CUE/BIN. Drives that cannot return raw subchannel are ripped without it.
	subStatus := ""
	cmd := exec.Command(helper("cdrdao"), "read-cd", "--device", device, "--read-raw", "--read-subchan", "rw_raw", "--datafile", bin, toc)
	if err := a.runJob("RIPPING PHYSICAL DISC", cmd); err != nil {
		// Retry without subchannel only when the drive rejected subchannel
		// reading; any other failure (no disc, read error, cancel) is final.
		if !strings.Contains(strings.ToLower(err.Error()), "sub-channel") {
			a.message("RIP FAILED", []string{err.Error(), "Partial files were kept."})
			return
		}
		appendDiscToolsLog("rip with raw subchannel failed, retrying without subchannel: " + err.Error())
		_ = os.Remove(bin)
		_ = os.Remove(toc)
		cmd = exec.Command(helper("cdrdao"), "read-cd", "--device", device, "--read-raw", "--datafile", bin, toc)
		if err := a.runJob("RIPPING PHYSICAL DISC", cmd); err != nil {
			a.message("RIP FAILED", []string{err.Error(), "Partial files were kept."})
			return
		}
		subStatus = "Subchannel: not supported by this drive"
	}
	if tocHasRawSubchannel(toc) {
		status, ok := a.finishRawSubchannel(bin, sub, base)
		if !ok {
			return
		}
		subStatus = status
	}
	if err := normalizeDescriptorBinReference(toc, bin); err != nil {
		a.message("RIP FAILED", []string{"Could not normalize TOC BIN path:", err.Error(), "BIN and TOC were kept."})
		return
	}

	// makeCHD builds the CHD from cdrdao's native TOC/BIN (CD-DA byte order and
	// raw disc layout, subchannel included) and verifies it. It returns the
	// error title and lines, or ok.
	makeCHD := func() (string, []string, bool) {
		if err := checkHelper("chdman"); err != nil {
			return "DEPENDENCY", []string{err.Error()}, false
		}
		chdArgs := []string{"createcd", "-i", filepath.Base(toc), "-o", chd}
		if fastCHD {
			chdArgs = append(chdArgs, chdFastCodecs...)
		}
		if uncompressedCHD {
			chdArgs = append(chdArgs, "-c", "none")
		}
		chdCmd := exec.Command(helper("chdman"), chdArgs...)
		chdCmd.Dir = dest
		if err := a.runJob("CONVERTING TO CHD", chdCmd); err != nil {
			return "CHD CONVERSION FAILED", []string{err.Error()}, false
		}
		if uncompressedCHD {
			binSize := func(total *int64) {
				if info, e := os.Stat(bin); e == nil {
					*total = info.Size()
				}
			}
			if err := a.runWorkWithProgress("VERIFYING CHD", filepath.Base(chd), binSize,
				func(cancel <-chan struct{}, onBytes func(int64)) error {
					return verifyUncompressedCHD(chd, bin, cancel, onBytes)
				}); err != nil {
				return "CHD VERIFY FAILED", []string{err.Error()}, false
			}
			return "", nil, true
		}
		if err := a.runJob("VERIFYING CHD", exec.Command(helper("chdman"), "verify", "-i", chd)); err != nil {
			return "CHD VERIFY FAILED", []string{err.Error()}, false
		}
		return "", nil, true
	}

	// CHD with BIN/CUE removed afterwards: the CUE-compatible BIN would be
	// deleted right away, so it is not created at all (saves a full copy of
	// the image). Only if the CHD fails, the BIN/CUE is created as a fallback.
	var chdFailTitle string
	var chdFailLines []string
	if deleteAfterVerify {
		title, lines, ok := makeCHD()
		if ok {
			_ = os.Remove(toc)
			_ = os.Remove(bin)
			_ = os.Remove(sub)
			a.message("RIP COMPLETE", withStatus([]string{"Created and verified:", chd}, subStatus))
			return
		}
		_ = os.Remove(chd)
		if len(lines) > 0 && lines[0] == "cancelled" {
			a.message(title, []string{"cancelled", "Native BIN and TOC were kept."})
			return
		}
		chdFailTitle, chdFailLines = title, lines
	}

	convertedBin := base + "-cue.bin"
	cmd = exec.Command(helper("toc2cue"), "-C", filepath.Base(convertedBin), "-s", filepath.Base(toc), filepath.Base(cue))
	cmd.Dir = dest
	if err := a.runJob("CREATING CUE", cmd); err != nil {
		a.message("CUE FAILED", append(chdFailLines, err.Error(), "Native BIN and TOC were kept."))
		return
	}
	if _, err := os.Stat(convertedBin); err != nil {
		a.message("CUE FAILED", append(chdFailLines, "toc2cue did not create the expected CUE-compatible BIN.", "Native BIN and TOC were kept."))
		return
	}
	if err := normalizeDescriptorBinReference(cue, convertedBin); err != nil {
		a.message("CUE FAILED", append(chdFailLines, "Could not normalize CUE BIN path:", err.Error(), "Native BIN/TOC and converted BIN were kept."))
		return
	}

	if i == 1 {
		// Build CHD from the native TOC/BIN before replacing the BIN with the
		// CUE-compatible byte-swapped copy.
		if title, lines, ok := makeCHD(); !ok {
			a.message(title, append(lines, "BIN/CUE/TOC were kept."))
			return
		}
	}

	// Publish the standard CUE/BIN pair only after native TOC/CHD work is done.
	_ = os.Remove(bin)
	if err := os.Rename(convertedBin, bin); err != nil {
		a.message("RIP FAILED", []string{"Could not finalize CUE-compatible BIN:", err.Error(), "CUE and converted BIN were kept."})
		return
	}
	if b, err := os.ReadFile(cue); err == nil {
		text := strings.ReplaceAll(string(b), filepath.Base(convertedBin), filepath.Base(bin))
		_ = os.WriteFile(cue, []byte(text), 0644)
	}
	_ = os.Remove(toc)
	kept := []string{cue, bin}
	if _, err := os.Stat(sub); err == nil {
		kept = append(kept, sub)
	}
	switch {
	case chdFailTitle != "":
		a.message(chdFailTitle, withStatus(append(append(chdFailLines, "BIN/CUE created instead:"), kept...), subStatus))
	case i == 0:
		a.message("RIP COMPLETE", withStatus(append([]string{"Created:"}, kept...), subStatus))
	default:
		a.message("RIP COMPLETE", withStatus(append([]string{"Created and verified:", chd, "Kept:"}, kept...), subStatus))
	}
}

// finishRawSubchannel realigns, verifies against the disc and saves the raw
// subchannel of a fresh rip. It shows its own error message and returns
// false when the rip must be abandoned.
func (a *App) finishRawSubchannel(bin, sub, base string) (string, bool) {
	var subs []byte
	var rep subchannelReport
	binSize := func(total *int64) {
		if info, e := os.Stat(bin); e == nil {
			*total = info.Size()
		}
	}
	err := a.runWorkWithProgress("PROCESSING SUBCHANNEL", filepath.Base(bin), binSize,
		func(cancel <-chan struct{}, onBytes func(int64)) error {
			var e error
			subs, rep, e = loadRawSubchannel(bin, cancel, onBytes)
			return e
		})
	if err != nil {
		a.message("RIP FAILED", []string{"Subchannel processing failed:", err.Error(), "BIN and TOC were kept."})
		return "", false
	}

	// Re-read every suspicious sector from the disc: a single raw pass is
	// not an exact copy of the subchannel (see subverify.go).
	var vres verifyResult
	verified := false
	suspects := len(subSuspects(subs, rep.Frames))
	if suspects > 0 {
		dev, e := openRawSubReader(device, func(lba int) bool { return subIsAudio(subs, rep.Frames, lba) })
		if e != nil {
			appendDiscToolsLog("subchannel verification skipped: " + e.Error())
		} else {
			speedNote := fmt.Sprintf("Verification read speed: %dx\n", verifySpeed)
			if e := dev.setSpeed(verifySpeed); e != nil {
				speedNote = "Verification read speed: drive default (" + e.Error() + ")\n"
			}
			err = a.runWorkWithProgressUnit("VERIFYING SUBCHANNEL", filepath.Base(bin), "sectors",
				func(total *int64) { *total = int64(suspects) },
				func(cancel <-chan struct{}, onBytes func(int64)) error {
					var e error
					vres, e = verifySubchannel(subs, rep.Frames, dev.read, cancel, func() { onBytes(1) })
					return e
				})
			_ = dev.setSpeed(0) // back to the drive maximum
			dev.Close()
			rep.VerifyNote = speedNote
			if err != nil {
				a.message("RIP FAILED", []string{"Subchannel verification failed:", err.Error(), "BIN and TOC were kept unchanged."})
				return "", false
			}
			verified = true
		}
	}

	err = a.runWorkWithProgress("SAVING SUBCHANNEL", filepath.Base(bin), binSize,
		func(cancel <-chan struct{}, onBytes func(int64)) error {
			return saveRawSubchannel(bin, sub, subs, &rep, onBytes)
		})
	if err != nil {
		_ = os.Remove(sub)
		a.message("RIP FAILED", []string{"Saving the subchannel failed:", err.Error(), "BIN and TOC were kept."})
		return "", false
	}

	report := rep.text()
	if verified {
		report += rep.VerifyNote + vres.text()
	} else if suspects > 0 {
		report += "Verification: not done, the first-pass subchannel was kept\n"
	}
	_ = os.WriteFile(base+".subq.log", []byte(report), 0644)
	appendDiscToolsLog("subchannel " + filepath.Base(bin) + ":\n" + report)

	status := fmt.Sprintf("Subchannel saved: %d sector(s) with a broken Q", len(rep.BadCRCLBAs))
	if verified {
		status += fmt.Sprintf(" (verified, %d unresolved)", len(vres.Unresolved))
	} else if suspects > 0 {
		status += " (not verified)"
	}
	return status, true
}

func withStatus(lines []string, status string) []string {
	if status == "" {
		return lines
	}
	return append(lines, "", status)
}

func appendDiscToolsLog(msg string) {
	_ = os.MkdirAll(logDir, 0755)
	f, err := os.OpenFile(filepath.Join(logDir, "disctools.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), msg)
}

// runWorkWithProgress runs an in-process task with the same progress screen
// used for file copies. sizeFn sets the total amount of work in bytes.
func (a *App) runWorkWithProgress(title, name string, sizeFn func(total *int64), work func(cancel <-chan struct{}, onBytes func(int64)) error) error {
	return a.runWorkWithProgressUnit(title, name, "", sizeFn, work)
}

// runWorkWithProgressUnit is runWorkWithProgress with a unit name for the
// amount line ("" = megabytes).
func (a *App) runWorkWithProgressUnit(title, name, unit string, sizeFn func(total *int64), work func(cancel <-chan struct{}, onBytes func(int64)) error) error {
	var total int64
	sizeFn(&total)
	updates := make(chan int64, 16)
	done := make(chan error, 1)
	cancel := make(chan struct{})
	var cancelOnce sync.Once
	stop := func() { cancelOnce.Do(func() { close(cancel) }) }

	go func() {
		var processed int64
		lastSent := int64(-1)
		done <- work(cancel, func(n int64) {
			processed += n
			mb := processed / (1024 * 1024)
			if mb != lastSent || processed >= total {
				lastSent = mb
				select {
				case updates <- processed:
				default:
				}
			}
		})
	}()

	current := int64(0)
	started := time.Now()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		a.fb.fill(a.background())
		a.title("DISC TOOLS V" + version)
		opScale := max(2, a.fb.h/240)
		pctScale := max(4, a.fb.h/145)
		statusScale := max(1, a.fb.h/360)
		centerY := a.fb.h / 2
		opY := centerY - max(155, a.fb.h/5)
		a.fb.text(centeredX(a.fb.w, opScale, title), opY, opScale, title, fg)

		pct := 0.0
		if total > 0 {
			pct = float64(current) * 100 / float64(total)
			if pct > 100 {
				pct = 100
			}
		}
		pctText := fmt.Sprintf("%.1f%%", pct)
		a.fb.text(centeredX(a.fb.w, pctScale, pctText), opY+70, pctScale, pctText, fg)
		barW := max(260, (a.fb.w*3)/4)
		if barW > a.fb.w-80 {
			barW = a.fb.w - 80
		}
		barH := max(24, a.fb.h/28)
		barX := (a.fb.w - barW) / 2
		barY := opY + 145
		a.fb.border(barX, barY, barW, barH, max(2, barH/10), dim)
		innerMax := max(0, barW-8)
		inner := int(float64(innerMax) * pct / 100)
		if inner > 0 {
			a.fb.rect(barX+4, barY+4, inner, max(1, barH-8), fg)
		}
		amount := fmt.Sprintf("%.1f MB / %.1f MB", float64(current)/(1024*1024), float64(total)/(1024*1024))
		if unit != "" {
			amount = fmt.Sprintf("%d / %d %s", current, total, unit)
		}
		a.fb.text(centeredX(a.fb.w, statusScale, amount), barY+barH+35, statusScale, amount, fg)
		shown := short(name, max(24, (a.fb.w-100)/6))
		a.fb.text(centeredX(a.fb.w, 1, shown), barY+barH+69, 1, shown, dim)
		elapsed := "Elapsed " + time.Since(started).Round(time.Second).String()
		a.fb.text(centeredX(a.fb.w, 1, elapsed), barY+barH+91, 1, elapsed, dim)
		a.footer("B/ESC Cancel")
		a.present()

		select {
		case current = <-updates:
		case err := <-done:
			return err
		case <-tick.C:
		case x := <-a.acts:
			if x == actBack {
				stop()
				err := <-done
				if err == nil {
					err = errors.New("cancelled")
				}
				return err
			}
		}
	}
}

func (a *App) burnDisc() {
	if err := checkHelper("cdrdao"); err != nil {
		a.message("DEPENDENCY", []string{err.Error()})
		return
	}
	i, ok := a.menu("BURN DISC", []string{"CUE/BIN IMAGE", "CHD IMAGE", "MSU1 / MD+ DATA DISC"}, 0)
	if !ok {
		return
	}
	switch i {
	case 0:
		a.burnCue()
	case 1:
		a.burnCHD()
	case 2:
		a.burnDataFolder()
	}
}
func (a *App) chooseBurnSpeed() (string, bool) {
	i, ok := a.menu("BURN SPEED", []string{"AUTO", "4X", "8X", "16X", "MAXIMUM"}, 0)
	if !ok {
		return "", false
	}
	switch i {
	case 1:
		return "4", true
	case 2:
		return "8", true
	case 3:
		return "16", true
	case 4:
		return "0", true
	default:
		return "", true
	}
}
func cueNeedsAudioSwap(cue string) bool {
	b, err := os.ReadFile(cue)
	if err != nil {
		return false
	}
	fileRE := regexp.MustCompile(`(?i)^\s*FILE\s+(?:"[^"]+"|\S+)\s+(\S+)`)
	audioRE := regexp.MustCompile(`(?i)^\s*TRACK\s+\d+\s+AUDIO(?:\s|$)`)
	fileKind := ""
	for _, line := range strings.Split(string(b), "\n") {
		if m := fileRE.FindStringSubmatch(line); len(m) == 2 {
			fileKind = strings.ToUpper(m[1])
			continue
		}
		if audioRE.MatchString(line) && fileKind == "BINARY" {
			return true
		}
	}
	return false
}

func cueSingleBinaryFile(cue string) string {
	b, err := os.ReadFile(cue)
	if err != nil {
		return ""
	}
	re := regexp.MustCompile(`(?im)^\s*FILE\s+(?:"([^"]+)"|(\S+))\s+(\S+)\s*$`)
	matches := re.FindAllStringSubmatch(string(b), -1)
	if len(matches) != 1 || strings.ToUpper(matches[0][3]) != "BINARY" {
		return ""
	}
	name := matches[0][1]
	if name == "" {
		name = matches[0][2]
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(filepath.Dir(cue), name)
	}
	if st, err := os.Stat(name); err != nil || st.IsDir() {
		return ""
	}
	return name
}

type cueTrackLayout struct {
	number     int
	mode       string
	index00    int64
	index01    int64
	hasIndex00 bool
	hasIndex01 bool
	pregap     int64
	hasPregap  bool
}

func cueMSFBlocks(v string) (int64, error) {
	parts := strings.Split(strings.TrimSpace(v), ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid CUE time %q", v)
	}
	mm, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid CUE time %q", v)
	}
	ss, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || ss < 0 || ss >= 60 {
		return 0, fmt.Errorf("invalid CUE time %q", v)
	}
	ff, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || ff < 0 || ff >= 75 {
		return 0, fmt.Errorf("invalid CUE time %q", v)
	}
	return (mm*60+ss)*75 + ff, nil
}

func cueBlocksMSF(blocks int64) string {
	if blocks < 0 {
		blocks = 0
	}
	mm := blocks / (60 * 75)
	rest := blocks % (60 * 75)
	ss := rest / 75
	ff := rest % 75
	return fmt.Sprintf("%02d:%02d:%02d", mm, ss, ff)
}

func parseSingleBinCueLayout(cue string) (string, []cueTrackLayout, error) {
	b, err := os.ReadFile(cue)
	if err != nil {
		return "", nil, err
	}
	fileRE := regexp.MustCompile(`(?i)^\s*FILE\s+(?:"([^"]+)"|(\S+))\s+(\S+)\s*$`)
	trackRE := regexp.MustCompile(`(?i)^\s*TRACK\s+(\d+)\s+(\S+)\s*$`)
	indexRE := regexp.MustCompile(`(?i)^\s*INDEX\s+(\d+)\s+(\d{1,3}:\d{1,2}:\d{1,2})\s*$`)
	pregapRE := regexp.MustCompile(`(?i)^\s*PREGAP\s+(\d{1,3}:\d{1,2}:\d{1,2})\s*$`)
	unsupportedRE := regexp.MustCompile(`(?i)^\s*(POSTGAP|FLAGS|ISRC|CATALOG|CDTEXTFILE|TITLE|PERFORMER|SONGWRITER)\b`)

	var source string
	fileCount := 0
	var tracks []cueTrackLayout
	current := -1
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimRight(raw, "\r")
		if m := unsupportedRE.FindStringSubmatch(line); len(m) == 2 {
			return "", nil, fmt.Errorf("CUE directive %s requires the compatibility fallback", strings.ToUpper(m[1]))
		}
		if m := fileRE.FindStringSubmatch(line); len(m) == 4 {
			fileCount++
			if !strings.EqualFold(m[3], "BINARY") {
				return "", nil, fmt.Errorf("robust CUE fallback supports BINARY images only")
			}
			name := m[1]
			if name == "" {
				name = m[2]
			}
			source = name
			continue
		}
		if m := trackRE.FindStringSubmatch(line); len(m) == 3 {
			n, _ := strconv.Atoi(m[1])
			tracks = append(tracks, cueTrackLayout{number: n, mode: strings.ToUpper(m[2])})
			current = len(tracks) - 1
			continue
		}
		if current < 0 {
			continue
		}
		if m := indexRE.FindStringSubmatch(line); len(m) == 3 {
			idx, _ := strconv.Atoi(m[1])
			if idx > 1 {
				return "", nil, fmt.Errorf("INDEX %02d requires the compatibility fallback", idx)
			}
			blocks, err := cueMSFBlocks(m[2])
			if err != nil {
				return "", nil, err
			}
			switch idx {
			case 0:
				tracks[current].index00 = blocks
				tracks[current].hasIndex00 = true
			case 1:
				tracks[current].index01 = blocks
				tracks[current].hasIndex01 = true
			}
			continue
		}
		if m := pregapRE.FindStringSubmatch(line); len(m) == 2 {
			blocks, err := cueMSFBlocks(m[1])
			if err != nil {
				return "", nil, err
			}
			tracks[current].pregap = blocks
			tracks[current].hasPregap = true
		}
	}
	if fileCount != 1 || source == "" {
		return "", nil, fmt.Errorf("robust CUE fallback requires exactly one BINARY FILE")
	}
	if len(tracks) == 0 {
		return "", nil, fmt.Errorf("CUE contains no tracks")
	}
	if !filepath.IsAbs(source) {
		source = filepath.Join(filepath.Dir(cue), filepath.FromSlash(source))
	}
	if st, err := os.Stat(source); err != nil || st.IsDir() {
		if err != nil {
			return "", nil, fmt.Errorf("CUE BIN not found: %w", err)
		}
		return "", nil, fmt.Errorf("CUE BIN path is not a file")
	}
	for i := range tracks {
		if !tracks[i].hasIndex01 {
			return "", nil, fmt.Errorf("track %02d has no INDEX 01", tracks[i].number)
		}
		switch tracks[i].mode {
		case "AUDIO", "MODE1/2352", "MODE2/2352":
		default:
			return "", nil, fmt.Errorf("track %02d mode %s is not supported by robust fallback", tracks[i].number, tracks[i].mode)
		}
		if tracks[i].hasIndex00 && tracks[i].index00 > tracks[i].index01 {
			return "", nil, fmt.Errorf("track %02d INDEX 00 is after INDEX 01", tracks[i].number)
		}
		if tracks[i].hasPregap && tracks[i].hasIndex00 {
			return "", nil, fmt.Errorf("track %02d uses both PREGAP and INDEX 00", tracks[i].number)
		}
	}
	return source, tracks, nil
}

type cueStageRange struct {
	dst    string
	offset int64
	length int64
	track  int
}

type cueStageProgress struct {
	copied int64
	track  int
	file   string
}

func copyFileRangeCancelable(src, dst string, offset, length int64, cancel <-chan struct{}, onBytes func(int64)) error {
	if offset < 0 || length < 0 {
		return errors.New("invalid CUE track range")
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if _, err := in.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	buf := make([]byte, 1024*1024)
	remaining := length
	for remaining > 0 {
		select {
		case <-cancel:
			_ = out.Close()
			return errors.New("cancelled")
		default:
		}
		want := int64(len(buf))
		if remaining < want {
			want = remaining
		}
		n, readErr := in.Read(buf[:int(want)])
		if n > 0 {
			if _, err = out.Write(buf[:n]); err != nil {
				_ = out.Close()
				return err
			}
			remaining -= int64(n)
			if onBytes != nil {
				onBytes(int64(n))
			}
		}
		if readErr == io.EOF && remaining > 0 {
			_ = out.Close()
			return io.ErrUnexpectedEOF
		}
		if readErr != nil && readErr != io.EOF {
			_ = out.Close()
			return readErr
		}
	}
	return out.Close()
}

func (a *App) copyCueRangesWithProgress(src string, ranges []cueStageRange, title string) error {
	var total int64
	for _, r := range ranges {
		total += r.length
	}
	if total <= 0 {
		return errors.New("CUE staging contains no data")
	}

	updates := make(chan cueStageProgress, 32)
	done := make(chan error, 1)
	cancel := make(chan struct{})
	var cancelOnce sync.Once
	stop := func() { cancelOnce.Do(func() { close(cancel) }) }

	go func() {
		var copied int64
		lastSent := int64(-1)
		for _, r := range ranges {
			select {
			case <-cancel:
				done <- errors.New("cancelled")
				return
			default:
			}
			select {
			case updates <- cueStageProgress{copied: copied, track: r.track, file: filepath.Base(r.dst)}:
			default:
			}
			err := copyFileRangeCancelable(src, r.dst, r.offset, r.length, cancel, func(n int64) {
				copied += n
				mb := copied / (1024 * 1024)
				if mb != lastSent || copied >= total {
					lastSent = mb
					select {
					case updates <- cueStageProgress{copied: copied, track: r.track, file: filepath.Base(r.dst)}:
					default:
					}
				}
			})
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	current := int64(0)
	currentTrack := 1
	currentFile := filepath.Base(src)
	started := time.Now()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		a.fb.fill(a.background())
		a.title("DISC TOOLS V" + version)
		opScale := max(2, a.fb.h/240)
		pctScale := max(4, a.fb.h/145)
		statusScale := max(1, a.fb.h/360)
		centerY := a.fb.h / 2
		opY := centerY - max(155, a.fb.h/5)
		a.fb.text(centeredX(a.fb.w, opScale, title), opY, opScale, title, fg)

		pct := float64(current) * 100 / float64(total)
		if pct > 100 {
			pct = 100
		}
		pctText := fmt.Sprintf("%.1f%%", pct)
		a.fb.text(centeredX(a.fb.w, pctScale, pctText), opY+70, pctScale, pctText, fg)
		barW := max(260, (a.fb.w*3)/4)
		if barW > a.fb.w-80 {
			barW = a.fb.w - 80
		}
		barH := max(24, a.fb.h/28)
		barX := (a.fb.w - barW) / 2
		barY := opY + 145
		a.fb.border(barX, barY, barW, barH, max(2, barH/10), dim)
		innerMax := max(0, barW-8)
		inner := int(float64(innerMax) * pct / 100)
		if inner > 0 {
			a.fb.rect(barX+4, barY+4, inner, max(1, barH-8), fg)
		}

		amount := fmt.Sprintf("%.1f MB / %.1f MB", float64(current)/(1024*1024), float64(total)/(1024*1024))
		a.fb.text(centeredX(a.fb.w, statusScale, amount), barY+barH+35, statusScale, amount, fg)
		trackText := fmt.Sprintf("Track %d / %d", currentTrack, len(ranges))
		a.fb.text(centeredX(a.fb.w, 1, trackText), barY+barH+69, 1, trackText, dim)
		name := short(currentFile, max(24, (a.fb.w-100)/6))
		a.fb.text(centeredX(a.fb.w, 1, name), barY+barH+91, 1, name, dim)
		elapsed := "Elapsed " + time.Since(started).Round(time.Second).String()
		a.fb.text(centeredX(a.fb.w, 1, elapsed), barY+barH+113, 1, elapsed, dim)
		a.footer("B/ESC Cancel")
		a.present()

		select {
		case u := <-updates:
			current = u.copied
			currentTrack = u.track
			currentFile = u.file
		case err := <-done:
			if err == nil {
				current = total
			}
			return err
		case <-tick.C:
		case x := <-a.acts:
			if x == actBack {
				stop()
				err := <-done
				if err == nil {
					err = errors.New("cancelled")
				}
				return err
			}
		}
	}
}

// stageRobustCueTOC handles single-BIN raw mixed-mode CUEs that neither
// cdrdao's CUE parser nor cue2toc can represent. It splits the raw image at
// CUE track boundaries and creates a native cdrdao TOC while preserving both
// generated PREGAPs and file-backed INDEX 00 -> INDEX 01 pregap data.
func (a *App) stageRobustCueTOC(cue string) (string, func(), error) {
	source, tracks, err := parseSingleBinCueLayout(cue)
	if err != nil {
		return "", func() {}, err
	}
	info, err := os.Stat(source)
	if err != nil {
		return "", func() {}, err
	}
	if info.Size()%2352 != 0 {
		return "", func() {}, fmt.Errorf("single-BIN raw image size is not aligned to 2352-byte sectors")
	}
	totalBlocks := info.Size() / 2352

	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", func() {}, err
	}
	dir, err := os.MkdirTemp(tempDir, "cue-layout-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	var toc strings.Builder
	hasMode2 := false
	for _, tr := range tracks {
		if tr.mode == "MODE2/2352" {
			hasMode2 = true
			break
		}
	}
	if hasMode2 {
		toc.WriteString("CD_ROM_XA\n\n")
	} else {
		toc.WriteString("CD_ROM\n\n")
	}

	ranges := make([]cueStageRange, 0, len(tracks))
	for i, tr := range tracks {
		startBlock := tr.index01
		if tr.hasIndex00 {
			startBlock = tr.index00
		}
		endBlock := totalBlocks
		if i+1 < len(tracks) {
			next := tracks[i+1]
			endBlock = next.index01
			if next.hasIndex00 {
				endBlock = next.index00
			}
		}
		if startBlock < 0 || endBlock <= startBlock || endBlock > totalBlocks {
			cleanup()
			return "", func() {}, fmt.Errorf("invalid sector range for track %02d", tr.number)
		}

		trackFile := fmt.Sprintf("track%02d.bin", tr.number)
		trackPath := filepath.Join(dir, trackFile)
		ranges = append(ranges, cueStageRange{
			dst:    trackPath,
			offset: startBlock * 2352,
			length: (endBlock - startBlock) * 2352,
			track:  tr.number,
		})

		tocMode := ""
		switch tr.mode {
		case "AUDIO":
			tocMode = "AUDIO"
		case "MODE1/2352":
			tocMode = "MODE1_RAW"
		case "MODE2/2352":
			tocMode = "MODE2_RAW"
		}
		fmt.Fprintf(&toc, "TRACK %s\n", tocMode)
		if tr.hasPregap && tr.pregap > 0 {
			fmt.Fprintf(&toc, "PREGAP %s\n", cueBlocksMSF(tr.pregap))
		}
		if tr.mode == "AUDIO" {
			fmt.Fprintf(&toc, "FILE \"%s\" 0\n", trackFile)
		} else {
			fmt.Fprintf(&toc, "DATAFILE \"%s\"\n", trackFile)
		}
		if tr.hasIndex00 {
			pregapBlocks := tr.index01 - tr.index00
			if pregapBlocks > 0 {
				fmt.Fprintf(&toc, "START %s\n", cueBlocksMSF(pregapBlocks))
			}
		}
		toc.WriteString("\n")
	}

	if err := a.copyCueRangesWithProgress(source, ranges, "PREPARING CUE"); err != nil {
		cleanup()
		return "", func() {}, err
	}

	tocPath := filepath.Join(dir, "image.toc")
	if err := os.WriteFile(tocPath, []byte(toc.String()), 0644); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return tocPath, cleanup, nil
}

func (a *App) copyFileWithProgress(src, dst, title string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	total := info.Size()
	updates := make(chan int64, 16)
	done := make(chan error, 1)
	cancel := make(chan struct{})
	var cancelOnce sync.Once
	stop := func() { cancelOnce.Do(func() { close(cancel) }) }

	go func() {
		var copied int64
		lastSent := int64(-1)
		err := copyFileCancelable(src, dst, cancel, func(n int64) {
			copied += n
			mb := copied / (1024 * 1024)
			if mb != lastSent || copied >= total {
				lastSent = mb
				select {
				case updates <- copied:
				default:
				}
			}
		})
		done <- err
	}()

	current := int64(0)
	started := time.Now()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		a.fb.fill(a.background())
		a.title("DISC TOOLS V" + version)
		opScale := max(2, a.fb.h/240)
		pctScale := max(4, a.fb.h/145)
		statusScale := max(1, a.fb.h/360)
		centerY := a.fb.h / 2
		opY := centerY - max(155, a.fb.h/5)
		a.fb.text(centeredX(a.fb.w, opScale, title), opY, opScale, title, fg)

		pct := 0.0
		if total > 0 {
			pct = float64(current) * 100 / float64(total)
			if pct > 100 {
				pct = 100
			}
		}
		pctText := fmt.Sprintf("%.1f%%", pct)
		a.fb.text(centeredX(a.fb.w, pctScale, pctText), opY+70, pctScale, pctText, fg)
		barW := max(260, (a.fb.w*3)/4)
		if barW > a.fb.w-80 {
			barW = a.fb.w - 80
		}
		barH := max(24, a.fb.h/28)
		barX := (a.fb.w - barW) / 2
		barY := opY + 145
		a.fb.border(barX, barY, barW, barH, max(2, barH/10), dim)
		innerMax := max(0, barW-8)
		inner := int(float64(innerMax) * pct / 100)
		if inner > 0 {
			a.fb.rect(barX+4, barY+4, inner, max(1, barH-8), fg)
		}
		amount := fmt.Sprintf("%.1f MB / %.1f MB", float64(current)/(1024*1024), float64(total)/(1024*1024))
		a.fb.text(centeredX(a.fb.w, statusScale, amount), barY+barH+35, statusScale, amount, fg)
		name := short(filepath.Base(src), max(24, (a.fb.w-100)/6))
		a.fb.text(centeredX(a.fb.w, 1, name), barY+barH+69, 1, name, dim)
		elapsed := "Elapsed " + time.Since(started).Round(time.Second).String()
		a.fb.text(centeredX(a.fb.w, 1, elapsed), barY+barH+91, 1, elapsed, dim)
		a.footer("B/ESC Cancel")
		a.present()

		select {
		case current = <-updates:
		case err := <-done:
			if err == nil {
				current = total
			}
			return err
		case <-tick.C:
		case x := <-a.acts:
			if x == actBack {
				stop()
				err := <-done
				if err == nil {
					err = errors.New("cancelled")
				}
				return err
			}
		}
	}
}

func (a *App) stageNativeCue(cue string) (string, func(), error) {
	sourceBin := cueSingleBinaryFile(cue)
	if sourceBin == "" {
		return cue, func() {}, nil
	}
	dir, err := os.MkdirTemp(filepath.Dir(cue), ".disctools-cue-")
	if err != nil {
		if mkErr := os.MkdirAll(tempDir, 0755); mkErr != nil {
			return "", func() {}, mkErr
		}
		dir, err = os.MkdirTemp(tempDir, "cue-burn-")
		if err != nil {
			return "", func() {}, err
		}
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	stagedCue := filepath.Join(dir, "image.cue")
	stagedBin := filepath.Join(dir, "image.bin")

	// cdrdao's native CUE parser resolves the BIN from the CUE basename
	// (image.cue -> image.bin). Rewrite the one BINARY FILE statement to
	// match the staged filename; otherwise cdrdao looks for the original
	// BIN name in the temporary folder and unnecessarily falls back to cue2toc.
	cueData, err := os.ReadFile(cue)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	fileLineRE := regexp.MustCompile(`(?im)^\s*FILE\s+(?:"[^"]+"|\S+)\s+BINARY\s*$`)
	if len(fileLineRE.FindAllIndex(cueData, -1)) != 1 {
		cleanup()
		return cue, func() {}, nil
	}
	cueData = fileLineRE.ReplaceAll(cueData, []byte(`FILE "image.bin" BINARY`))
	if err := os.WriteFile(stagedCue, cueData, 0644); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := os.Link(sourceBin, stagedBin); err != nil {
		// FAT/exFAT media used by MiSTer do not support hard links. Copying a
		// full CD image can take a while, so keep the framebuffer responsive and
		// show the same percentage/amount progress used by the other long jobs.
		if err := a.copyFileWithProgress(sourceBin, stagedBin, "PREPARING CUE"); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}
	return stagedCue, cleanup, nil
}

func (a *App) cdrdaoAcceptsCue(cue string) bool {
	cmd := exec.Command(helper("cdrdao"), "show-toc", cue)
	cmd.Dir = filepath.Dir(cue)
	// show-toc is normally quick, but on slow USB/SD media it can take long
	// enough to look frozen. Run it through the standard job UI so the user
	// always sees activity and can cancel.
	return a.runJob("CHECKING CUE", cmd) == nil
}

func (a *App) burnCuePrepared(cue, speed string) {
	needsAudioSwap := cueNeedsAudioSwap(cue)
	nativeCue, cleanupNative, err := a.stageNativeCue(cue)
	if err != nil {
		a.message("BURN FAILED", []string{"Could not prepare CUE/BIN image:", err.Error()})
		return
	}
	defer cleanupNative()

	burnDescription := nativeCue
	burnDir := filepath.Dir(nativeCue)
	if !a.cdrdaoAcceptsCue(nativeCue) {
		// Once the native CUE path has failed, discard any full-image staging
		// copy before creating the per-track fallback. This avoids requiring
		// space for two complete temporary copies on FAT/exFAT media.
		cleanupNative()
		cleanupNative = func() {}

		robustTOC, cleanupRobust, robustErr := a.stageRobustCueTOC(cue)
		if robustErr == nil {
			defer cleanupRobust()
			burnDescription = robustTOC
			burnDir = filepath.Dir(robustTOC)
		} else {
			// Keep cue2toc as the final compatibility fallback for layouts that
			// are outside the conservative single-BIN raw parser above.
			if err := checkHelper("cue2toc"); err != nil {
				a.message("BURN FAILED", []string{"CUE layout is not supported by the robust fallback:", robustErr.Error(), err.Error()})
				return
			}
			if err := os.MkdirAll(tempDir, 0755); err != nil {
				a.message("BURN FAILED", []string{err.Error()})
				return
			}
			tocDir, err := os.MkdirTemp(tempDir, "cue2toc-")
			if err != nil {
				a.message("BURN FAILED", []string{err.Error()})
				return
			}
			defer os.RemoveAll(tocDir)
			toc := filepath.Join(tocDir, "image.toc")
			cmd := exec.Command(helper("cue2toc"), "-o", toc, cue)
			cmd.Dir = filepath.Dir(cue)
			if err := a.runJob("PREPARING CUE", cmd); err != nil {
				a.message("BURN FAILED", []string{robustErr.Error(), err.Error()})
				return
			}
			burnDescription = toc
			burnDir = filepath.Dir(cue)
		}
	}

	args := []string{"write", "--device", device, "--eject", "-n"}
	if speed != "" {
		args = append(args, "--speed", speed)
	}
	if needsAudioSwap {
		// Standard CUE/BIN CD-DA is little-endian; cdrdao's raw audio input
		// defaults to big-endian. --swap applies to AUDIO tracks only, leaving
		// mixed-mode data sectors unchanged.
		args = append(args, "--swap")
	}
	args = append(args, burnDescription)
	cmd := exec.Command(helper("cdrdao"), args...)
	cmd.Dir = burnDir
	if err := a.runJob("BURNING DISC", cmd); err != nil {
		a.message("BURN FAILED", []string{err.Error()})
		return
	}
	a.message("BURN COMPLETE", []string{"The disc was written successfully."})
}

func (a *App) burnCue() {
	root, ok := a.chooseStorage("SELECT CUE SOURCE")
	if !ok {
		return
	}
	cue, ok := a.browse(root, map[string]bool{".cue": true}, false)
	if !ok {
		return
	}
	speed, ok := a.chooseBurnSpeed()
	if !ok {
		return
	}
	if !a.prepareBurnMedia() {
		return
	}
	a.burnCuePrepared(cue, speed)
}
func (a *App) burnCHD() {
	if err := checkHelper("chdman"); err != nil {
		a.message("DEPENDENCY", []string{err.Error()})
		return
	}
	if !a.chdWarning() {
		return
	}
	root, ok := a.chooseStorage("SELECT CHD SOURCE")
	if !ok {
		return
	}
	chd, ok := a.browse(root, map[string]bool{".chd": true}, false)
	if !ok {
		return
	}
	speed, ok := a.chooseBurnSpeed()
	if !ok {
		return
	}
	if !a.prepareBurnMedia() {
		return
	}
	job := filepath.Join(tempDir, "chd-burn-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	_ = os.MkdirAll(job, 0755)
	defer os.RemoveAll(job)
	cue := filepath.Join(job, "disc.cue")
	bin := filepath.Join(job, "disc.bin")
	if err := a.runJob("EXTRACTING CHD", exec.Command(helper("chdman"), "extractcd", "-i", chd, "-o", cue, "-ob", bin)); err != nil {
		a.message("CHD EXTRACTION FAILED", []string{err.Error()})
		return
	}
	a.burnCuePath(cue, speed)
}
func (a *App) burnCuePath(cue, speed string) {
	a.burnCuePrepared(cue, speed)
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, e := filepath.Rel(src, p)
		if e != nil {
			return e
		}
		out := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(out, 0755)
		}
		in, e := os.Open(p)
		if e != nil {
			return e
		}
		defer in.Close()
		of, e := os.Create(out)
		if e != nil {
			return e
		}
		_, e = io.Copy(of, in)
		ce := of.Close()
		if e != nil {
			return e
		}
		return ce
	})
}
func jolietSafeName(name string) bool { return len([]rune(name)) <= 64 }
func compactBase(base string, keep int) string {
	r := []rune(base)
	if len(r) <= keep {
		return base
	}
	sum := uint32(2166136261)
	for _, b := range []byte(base) {
		sum ^= uint32(b)
		sum *= 16777619
	}
	suffix := fmt.Sprintf("-%08x", sum)
	n := keep - len([]rune(suffix))
	if n < 1 {
		n = 1
	}
	return string(r[:n]) + suffix
}

var cueFileRE = regexp.MustCompile(`(?i)^\s*FILE\s+(?:\"([^\"]+)\"|([^\s]+))\s+(.+)$`)

func normalizeSpecialNames(root string) ([]string, error) {
	var changes []string
	// MSU1: shorten a long common basename consistently for ROM/.msu/.pcm files.
	entries, _ := os.ReadDir(root)
	for _, x := range entries {
		if x.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(x.Name()), ".msu") {
			base := strings.TrimSuffix(x.Name(), filepath.Ext(x.Name()))
			if jolietSafeName(x.Name()) {
				continue
			}
			newBase := compactBase(base, 48)
			for _, y := range entries {
				n := y.Name()
				if strings.EqualFold(strings.TrimSuffix(n, filepath.Ext(n)), base) || strings.HasPrefix(strings.ToLower(n), strings.ToLower(base)+"-") {
					suffix := n[len(base):]
					nn := newBase + suffix
					if n != nn {
						if e := os.Rename(filepath.Join(root, n), filepath.Join(root, nn)); e != nil {
							return changes, e
						}
						changes = append(changes, n+" -> "+nn)
					}
				}
			}
		}
	}
	// MD+: update FILE references in CUE files when referenced filenames exceed Joliet limits.
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.EqualFold(filepath.Ext(p), ".cue") {
			return nil
		}
		data, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		lines := strings.Split(string(data), "\n")
		changed := false
		for i, line := range lines {
			m := cueFileRE.FindStringSubmatch(strings.TrimRight(line, "\r"))
			if m == nil {
				continue
			}
			name := m[1]
			if name == "" {
				name = m[2]
			}
			if jolietSafeName(name) {
				continue
			}
			ext := filepath.Ext(name)
			nb := compactBase(strings.TrimSuffix(filepath.Base(name), ext), max(16, 55-len([]rune(ext)))) + ext
			oldPath := filepath.Join(filepath.Dir(p), filepath.FromSlash(name))
			newPath := filepath.Join(filepath.Dir(p), nb)
			if _, e := os.Stat(oldPath); e == nil {
				if e = os.Rename(oldPath, newPath); e != nil {
					return e
				}
			}
			prefix := line[:strings.Index(strings.ToUpper(line), "FILE")]
			lines[i] = fmt.Sprintf("%sFILE \"%s\" %s", prefix, nb, m[3])
			changes = append(changes, name+" -> "+nb)
			changed = true
		}
		if changed {
			return os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0644)
		}
		return nil
	})
	// Generic long names: conservatively shorten only files not already handled.
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		name := info.Name()
		if jolietSafeName(name) {
			return nil
		}
		ext := ""
		base := name
		if !info.IsDir() {
			ext = filepath.Ext(name)
			base = strings.TrimSuffix(name, ext)
		}
		nn := compactBase(base, max(12, 55-len([]rune(ext)))) + ext
		np := filepath.Join(filepath.Dir(p), nn)
		if e := os.Rename(p, np); e != nil {
			return e
		}
		changes = append(changes, name+" -> "+nn)
		if info.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	return changes, nil
}

type dataStageUpdate struct {
	phase   string
	current int64
	total   int64
	file    string
}

type dataStageResult struct {
	changes []string
	err     error
}

func copyFileCancelable(src, dst string, cancel <-chan struct{}, onBytes func(int64)) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	buf := make([]byte, 1024*1024)
	for {
		select {
		case <-cancel:
			_ = out.Close()
			return errors.New("cancelled")
		default:
		}
		n, readErr := in.Read(buf)
		if n > 0 {
			if _, err = out.Write(buf[:n]); err != nil {
				_ = out.Close()
				return err
			}
			onBytes(int64(n))
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = out.Close()
			return readErr
		}
	}
	return out.Close()
}

// prepareDataStage keeps the framebuffer/input loop responsive while the
// selected MSU1/MD+ folder is scanned, copied into the temporary staging tree,
// and checked for Joliet-safe filenames. The source folder is never modified.
func (a *App) prepareDataStage(src, stage string) ([]string, error) {
	updates := make(chan dataStageUpdate, 16)
	done := make(chan dataStageResult, 1)
	cancel := make(chan struct{})
	var cancelOnce sync.Once
	stop := func() { cancelOnce.Do(func() { close(cancel) }) }

	go func() {
		send := func(u dataStageUpdate) {
			select {
			case updates <- u:
			default:
			}
		}
		send(dataStageUpdate{phase: "SCANNING DATA FOLDER"})
		var total int64
		err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			select {
			case <-cancel:
				return errors.New("cancelled")
			default:
			}
			if !info.IsDir() {
				total += info.Size()
			}
			return nil
		})
		if err != nil {
			done <- dataStageResult{err: err}
			return
		}

		if err = os.MkdirAll(stage, 0755); err != nil {
			done <- dataStageResult{err: err}
			return
		}
		var copied int64
		lastSent := int64(-1)
		err = filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			select {
			case <-cancel:
				return errors.New("cancelled")
			default:
			}
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			outPath := filepath.Join(stage, rel)
			if info.IsDir() {
				return os.MkdirAll(outPath, 0755)
			}
			send(dataStageUpdate{phase: "PREPARING DATA DISC", current: copied, total: total, file: info.Name()})
			return copyFileCancelable(path, outPath, cancel, func(n int64) {
				copied += n
				// Avoid flooding the UI channel. Render updates roughly every MiB.
				mb := copied / (1024 * 1024)
				if mb != lastSent {
					lastSent = mb
					send(dataStageUpdate{phase: "PREPARING DATA DISC", current: copied, total: total, file: info.Name()})
				}
			})
		})
		if err != nil {
			done <- dataStageResult{err: err}
			return
		}

		send(dataStageUpdate{phase: "CHECKING FILENAMES", current: total, total: total})
		changes, err := normalizeSpecialNames(stage)
		done <- dataStageResult{changes: changes, err: err}
	}()

	phase := "SCANNING DATA FOLDER"
	fileName := ""
	current, total := int64(0), int64(0)
	started := time.Now()
	spin := 0
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

	for {
		a.fb.fill(a.background())
		a.title("DISC TOOLS V" + version)
		opScale := max(2, a.fb.h/240)
		pctScale := max(4, a.fb.h/145)
		statusScale := max(1, a.fb.h/360)
		centerY := a.fb.h / 2
		opY := centerY - max(155, a.fb.h/5)
		a.fb.text(centeredX(a.fb.w, opScale, phase), opY, opScale, phase, fg)

		if phase == "PREPARING DATA DISC" && total > 0 {
			pct := float64(current) * 100 / float64(total)
			if pct > 100 {
				pct = 100
			}
			pctText := fmt.Sprintf("%.1f%%", pct)
			a.fb.text(centeredX(a.fb.w, pctScale, pctText), opY+70, pctScale, pctText, fg)
			barW := max(260, (a.fb.w*3)/4)
			if barW > a.fb.w-80 {
				barW = a.fb.w - 80
			}
			barH := max(24, a.fb.h/28)
			barX := (a.fb.w - barW) / 2
			barY := opY + 145
			a.fb.border(barX, barY, barW, barH, max(2, barH/10), dim)
			innerMax := max(0, barW-8)
			inner := int(float64(innerMax) * pct / 100)
			if inner > 0 {
				a.fb.rect(barX+4, barY+4, inner, max(1, barH-8), fg)
			}
			amount := fmt.Sprintf("%.1f MB / %.1f MB", float64(current)/(1024*1024), float64(total)/(1024*1024))
			a.fb.text(centeredX(a.fb.w, statusScale, amount), barY+barH+35, statusScale, amount, fg)
			detailY := barY + barH + 69
			if fileName != "" {
				name := short(fileName, max(24, (a.fb.w-100)/6))
				a.fb.text(centeredX(a.fb.w, 1, name), detailY, 1, name, dim)
				detailY += 22
			}
			elapsed := "Elapsed " + time.Since(started).Round(time.Second).String()
			a.fb.text(centeredX(a.fb.w, 1, elapsed), detailY, 1, elapsed, dim)
		} else {
			working := "WORKING" + strings.Repeat(".", spin%4)
			workScale := max(3, a.fb.h/180)
			a.fb.text(centeredX(a.fb.w, workScale, working), opY+90, workScale, working, fg)
			status := "Preparing temporary disc copy"
			if phase == "SCANNING DATA FOLDER" {
				status = "Calculating folder size"
			} else if phase == "CHECKING FILENAMES" {
				status = "Checking Joliet/MSU1/MD+ names"
			}
			a.fb.text(centeredX(a.fb.w, statusScale, status), opY+175, statusScale, status, fg)
			elapsed := "Elapsed " + time.Since(started).Round(time.Second).String()
			a.fb.text(centeredX(a.fb.w, 1, elapsed), opY+215, 1, elapsed, dim)
		}
		a.footer("B/ESC Cancel")
		a.present()

		select {
		case u := <-updates:
			phase, current, total, fileName = u.phase, u.current, u.total, u.file
		case r := <-done:
			return r.changes, r.err
		case <-tick.C:
			spin++
		case x := <-a.acts:
			if x == actBack {
				stop()
				r := <-done
				if r.err == nil {
					return r.changes, errors.New("cancelled")
				}
				return r.changes, r.err
			}
		}
	}
}

func (a *App) burnDataFolder() {
	if err := checkHelper("xorriso"); err != nil {
		a.message("DEPENDENCY", []string{err.Error()})
		return
	}
	if err := checkHelper("cdrdao"); err != nil {
		a.message("DEPENDENCY", []string{err.Error()})
		return
	}
	root, ok := a.chooseStorage("SELECT MSU1 / MD+ FOLDER")
	if !ok {
		return
	}
	src, ok := a.browse(root, nil, true)
	if !ok {
		return
	}
	speed, ok := a.chooseBurnSpeed()
	if !ok {
		return
	}
	if !a.prepareBurnMedia() {
		return
	}
	job := filepath.Join(tempDir, "data-burn-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	stage := filepath.Join(job, "disc")
	iso := filepath.Join(job, "disc.iso")
	defer os.RemoveAll(job)
	changes, e := a.prepareDataStage(src, stage)
	if e != nil {
		if e.Error() != "cancelled" {
			a.message("DATA PREP FAILED", []string{e.Error()})
		}
		return
	}
	if len(changes) > 0 {
		preview := []string{"Long Joliet names were safely adjusted in the temporary copy:"}
		for i, c := range changes {
			if i >= 6 {
				preview = append(preview, fmt.Sprintf("...and %d more", len(changes)-i))
				break
			}
			preview = append(preview, c)
		}
		a.message("FILENAME CHANGES", preview)
	}
	cmd := exec.Command(helper("xorriso"), "-as", "mkisofs", "-iso-level", "3", "-J", "-joliet-long", "-R", "-V", "DISC_TOOLS", "-o", iso, stage)
	if e := a.runJob("BUILDING ISO9660/JOLIET", cmd); e != nil {
		a.message("ISO BUILD FAILED", []string{e.Error()})
		return
	}
	// xorriso is used only to build the ISO9660/Joliet filesystem.  Burning
	// through its libburn backend is unreliable with some MiSTer USB optical
	// drives, so hand the finished MODE1/2048 image to cdrdao instead.
	toc := filepath.Join(job, "disc.toc")
	tocData := "CD_ROM\n\nTRACK MODE1\nDATAFILE \"disc.iso\"\n"
	if e := os.WriteFile(toc, []byte(tocData), 0644); e != nil {
		a.message("BURN PREP FAILED", []string{e.Error()})
		return
	}

	args := []string{"write", "--device", device, "--eject", "-n"}
	if speed != "" {
		args = append(args, "--speed", speed)
	}
	args = append(args, toc)
	cmd = exec.Command(helper("cdrdao"), args...)
	cmd.Dir = job
	if e := a.runJob("BURNING DATA DISC", cmd); e != nil {
		a.message("BURN FAILED", []string{e.Error()})
		return
	}
	a.message("BURN COMPLETE", []string{"The ISO9660/Joliet data disc was written successfully."})
}

func (a *App) dependencies() {
	names := []string{"cdrdao", "toc2cue", "cue2toc", "chdman", "xorriso"}
	var lines []string
	for _, n := range names {
		if checkHelper(n) == nil {
			lines = append(lines, n+": OK")
		} else {
			lines = append(lines, n+": MISSING")
		}
	}
	a.message("DEPENDENCIES", lines)
}

var screenSaverOptions = []int{0, 30, 60, 120, 300, 600}

func screenSaverLabel(v int) string {
	switch v {
	case 30:
		return "30 SECONDS"
	case 60:
		return "1 MINUTE"
	case 120:
		return "2 MINUTES"
	case 300:
		return "5 MINUTES"
	case 600:
		return "10 MINUTES"
	}
	return "OFF"
}
func cycleScreenSaver(v, dir int) int {
	idx := 0
	for i, x := range screenSaverOptions {
		if x == v {
			idx = i
			break
		}
	}
	idx = (idx + dir + len(screenSaverOptions)) % len(screenSaverOptions)
	return screenSaverOptions[idx]
}
func onoff(v bool) string {
	if v {
		return "ON"
	}
	return "OFF"
}

type customFontOption struct{ Name, File string }

func scanCustomFonts() []customFontOption {
	_ = os.MkdirAll(customFontsDir, 0755)
	es, _ := os.ReadDir(customFontsDir)
	var out []customFontOption
	for _, e := range es {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".ttf" && ext != ".otf" {
			continue
		}
		p := filepath.Join(customFontsDir, e.Name())
		if !customFontValid(p) {
			continue
		}
		n := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		out = append(out, customFontOption{n, e.Name()})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}
func applyCustomFont(c *Config, fs []customFontOption) {
	if c == nil || c.CustomFont == "" {
		setCustomFont("")
		return
	}
	for _, f := range fs {
		if f.File == c.CustomFont && setCustomFont(filepath.Join(customFontsDir, f.File)) {
			return
		}
	}
	c.CustomFont = ""
	setCustomFont("")
}
func customFontLabel(c *Config, fs []customFontOption) string {
	if c == nil || c.CustomFont == "" {
		return "OFF"
	}
	for _, f := range fs {
		if f.File == c.CustomFont {
			return strings.ToUpper(f.Name)
		}
	}
	return "OFF"
}
func cycleCustomFont(c *Config, fs []customFontOption, dir int) {
	if c == nil || len(fs) == 0 {
		return
	}
	idx := 0
	if c.CustomFont != "" {
		for i, f := range fs {
			if f.File == c.CustomFont {
				idx = i + 1
				break
			}
		}
	}
	total := len(fs) + 1
	idx = (idx + dir + total) % total
	if idx == 0 {
		c.CustomFont = ""
		setCustomFont("")
		return
	}
	c.CustomFont = fs[idx-1].File
	if !setCustomFont(filepath.Join(customFontsDir, c.CustomFont)) {
		c.CustomFont = ""
		setCustomFont("")
	}
}
func (a *App) settingsUI() {
	labels := []string{"OLED MODE", "SHOW CLOCK", "SCREENSAVER", "SWAP A/B", "SWAP X/Y", "CUSTOM FALLBACK FONT"}
	fs := scanCustomFonts()
	applyCustomFont(a.cfg, fs)
	sel := 0
	for {
		a.fb.fill(a.background())
		a.title("SETTINGS")
		row := max(48, a.fb.h/10)
		y := 82
		vals := []string{onoff(a.cfg.OLEDMode), onoff(a.cfg.ShowClock), screenSaverLabel(a.cfg.ScreenSaverSeconds), onoff(a.cfg.SwapAB), onoff(a.cfg.SwapXY), customFontLabel(a.cfg, fs)}
		for i, l := range labels {
			enabled := i != 5 || len(fs) > 0
			if i == sel && enabled {
				a.fb.rect(38, y-7, a.fb.w-76, row-4, panel)
				a.fb.border(38, y-7, a.fb.w-76, row-4, 2, fg)
			}
			lc, vc := fg, dim
			if !enabled {
				lc = color.RGBA{90, 90, 96, 255}
				vc = lc
			}
			ts := max(1, row/25)
			a.fb.text(58, y+4, ts, l, lc)
			a.fb.text(a.fb.w-58-tw(ts, vals[i]), y+4, ts, vals[i], vc)
			if i == 2 {
				a.fb.text(58, y+4+ts*9, max(1, ts-1), "BLACKS SCREEN ONLY - DISC ACTIONS CONTINUE RUNNING", dim)
			}
			if i == 5 && len(fs) == 0 {
				a.fb.text(58, y+4+ts*9, max(1, ts-1), "ADD .TTF OR .OTF TO .CONFIG/DISCTOOLS/FONTS", dim)
			}
			y += row
		}
		a.footer("A/ENTER Select   B/ESC Back")
		a.present()
		x := <-a.acts
		if x == actWake {
			continue
		}
		switch x {
		case actBack:
			saveConfig(*a.cfg)
			return
		case actUp:
			if sel > 0 {
				sel--
			}
		case actDown:
			if sel < len(labels)-1 {
				sel++
			}
		case actConfirm, actLeft, actRight:
			dir := 1
			if x == actLeft {
				dir = -1
			}
			switch sel {
			case 0:
				a.cfg.OLEDMode = !a.cfg.OLEDMode
			case 1:
				a.cfg.ShowClock = !a.cfg.ShowClock
			case 2:
				a.cfg.ScreenSaverSeconds = cycleScreenSaver(a.cfg.ScreenSaverSeconds, dir)
				screenSaverSeconds.Store(int64(a.cfg.ScreenSaverSeconds))
			case 3:
				a.cfg.SwapAB = !a.cfg.SwapAB
				swapABInput.Store(a.cfg.SwapAB)
			case 4:
				a.cfg.SwapXY = !a.cfg.SwapXY
				swapXYInput.Store(a.cfg.SwapXY)
			case 5:
				cycleCustomFont(a.cfg, fs, dir)
			}
			saveConfig(*a.cfg)
		}
	}
}
func screenSaverInputLoop(fb *framebuffer, raw <-chan action, out chan<- action, done <-chan struct{}) {
	last := time.Now()
	sleeping := false
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case x := <-raw:
			last = time.Now()
			if sleeping {
				sleeping = false
				screenSaverActive.Store(false)
				select {
				case out <- actWake:
				case <-done:
					return
				}
				continue
			}
			select {
			case out <- x:
			case <-done:
				return
			}
		case now := <-tick.C:
			secs := screenSaverSeconds.Load()
			if secs > 0 && !sleeping && now.Sub(last) >= time.Duration(secs)*time.Second {
				screenSaverActive.Store(true)
				fb.blankLive()
				sleeping = true
			}
		}
	}
}

func (a *App) mainMenu() {
	for {
		i, ok := a.menuWithBack("DISC TOOLS V"+version, []string{"RIP PHYSICAL DISC", "BURN DISC", "ERASE CD-RW", "EJECT DISC", "", "SETTINGS", "", "EXIT"}, 0, false)
		if !ok {
			return
		}
		switch i {
		case 0:
			a.ripDisc()
		case 1:
			a.burnDisc()
		case 2:
			a.eraseDisc()
		case 3:
			a.eject()
		case 5:
			a.settingsUI()
		case 7:
			return
		}
	}
}

func main() {
	_ = os.MkdirAll(binDir, 0755)
	_ = os.MkdirAll(tempDir, 0755)
	_ = os.MkdirAll(logDir, 0755)
	fb, e := openFB()
	if e != nil {
		fmt.Fprintln(os.Stderr, "Disc Tools:", e)
		os.Exit(1)
	}
	defer fb.close()
	cfg := loadConfig()
	fonts := scanCustomFonts()
	applyCustomFont(&cfg, fonts)
	swapABInput.Store(cfg.SwapAB)
	swapXYInput.Store(cfg.SwapXY)
	screenSaverSeconds.Store(int64(cfg.ScreenSaverSeconds))
	acts := make(chan action, 32)
	rawActs := make(chan action, 32)
	app := &App{fb: fb, acts: acts, done: make(chan struct{}), cfg: &cfg}
	go inputLoop(rawActs, app.done)
	go screenSaverInputLoop(fb, rawActs, acts, app.done)
	defer close(app.done)
	app.mainMenu()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
