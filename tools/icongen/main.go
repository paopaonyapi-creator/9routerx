// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
// Command icongen generates the multi-size Windows .ico from the product logo,
// and dumps an existing .ico back to per-frame PNGs for verification.
//
// Usage:
//
//	go run ./tools/icongen make <source.png> <out.ico>
//	go run ./tools/icongen dump <in.ico> <out-dir>
//
// make resizes the source PNG to 16/24/32/48/64/128/256 px frames (CatmullRom,
// alpha-preserving) and writes every frame as a 32-bit BGRA bitmap inside the
// .ico — the most compatible frame encoding, accepted by every Windows version
// that can render an icon. dump extracts each frame as a PNG so the result can
// be compared against the source artwork (frames land as <dir>/<w>x<h>.png).
//
// The generated .ico feeds three consumers: cmd/9routerx-tray/tray.ico (the
// runtime tray icon, go:embed'd), and — through go-winres — the embedded
// RT_GROUP_ICON resource of both release executables (cmd/9routerx and
// cmd/9routerx-tray). Regenerate with `make icons` after changing the logo.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
)

// frameSizes is the fixed ladder of icon sizes, smallest first. Windows tools
// and Explorer caches expect this spread; 256 is the Vista+ large frame.
var frameSizes = []int{16, 24, 32, 48, 64, 128, 256}

const (
	icoHeaderSize = 6
	icoEntrySize  = 16
	bmpHeaderSize = 40
	bitsPerPixel  = 32
	iconTypeID    = 1 // 1 = icon (2 would be cursor)
	planes        = 1
)

func main() {
	if len(os.Args) < 4 {
		usage()
	}
	switch os.Args[1] {
	case "make":
		if err := runMake(os.Args[2], os.Args[3]); err != nil {
			fatal("make: %v", err)
		}
	case "dump":
		if err := runDump(os.Args[2], os.Args[3]); err != nil {
			fatal("dump: %v", err)
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: icongen make <source.png> <out.ico> | icongen dump <in.ico> <out-dir>")
	os.Exit(2)
}

func fatal(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "icongen: "+f+"\n", a...)
	os.Exit(1)
}

// decodePNG reads a PNG into a non-premultiplied RGBA image.
func decodePNG(path string) (*image.NRGBA, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	nrgba := image.NewNRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
	draw.Draw(nrgba, nrgba.Bounds(), img, img.Bounds().Min, draw.Src)
	return nrgba, nil
}

func sha256sum(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

// runMake reads the source PNG and writes a multi-frame .ico at outPath.
func runMake(srcPath, outPath string) error {
	src, err := decodePNG(srcPath)
	if err != nil {
		return err
	}
	if src.Bounds().Dx() != src.Bounds().Dy() {
		return fmt.Errorf("source is %dx%d; a square logo is required", src.Bounds().Dx(), src.Bounds().Dy())
	}

	frames := make([][]byte, len(frameSizes))
	for i, size := range frameSizes {
		frames[i], err = encodeBMPFrame(resize(src, size))
		if err != nil {
			return fmt.Errorf("frame %d: %w", size, err)
		}
	}

	out := make([]byte, 0, icoHeaderSize+icoEntrySize*len(frames)+len(frames)*(bmpHeaderSize+256*256*4))
	// ICONDIR: reserved, type (1 = icon), frame count.
	out = binary.LittleEndian.AppendUint16(out, 0)
	out = binary.LittleEndian.AppendUint16(out, iconTypeID)
	out = binary.LittleEndian.AppendUint16(out, uint16(len(frames)))

	// All ICONDIRENTRY structures come first as one contiguous table, and only
	// then the frame bodies — each entry's offset is absolute from the file
	// start, so interleaving entries and bodies would misdirect the table.
	entries := make([]byte, 0, icoEntrySize*len(frames))
	bodies := make([]byte, 0, len(frames)*(bmpHeaderSize+256*256*4))
	offset := icoHeaderSize + icoEntrySize*len(frames)
	for i, size := range frameSizes {
		entries = appendICOEntry(entries, size, frames[i], offset)
		bodies = append(bodies, frames[i]...)
		offset += len(frames[i])
	}
	out = append(out, entries...)
	out = append(out, bodies...)

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		return err
	}
	fmt.Printf("%s: %d frames, %d bytes total\n", outPath, len(frames), len(out))
	for i, size := range frameSizes {
		fmt.Printf("  frame %3dx%-3d %8d bytes sha256=%x\n", size, size, len(frames[i]), sha256sum(frames[i]))
	}
	return nil
}

// appendICOEntry appends one ICONDIRENTRY. Width/height fields are byte-sized
// with 0 meaning 256; the data offset is absolute from the file start.
func appendICOEntry(dst []byte, size int, frame []byte, offset int) []byte {
	dst = append(dst, byte(size%256), byte(size%256)) // width, height
	dst = append(dst, 0, 0)                           // color count (0 = truecolor), reserved
	dst = binary.LittleEndian.AppendUint16(dst, planes)
	dst = binary.LittleEndian.AppendUint16(dst, bitsPerPixel)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(frame)))
	dst = binary.LittleEndian.AppendUint32(dst, uint32(offset))
	return dst
}

// resize scales the source to size x size with CatmullRom filtering onto a
// non-premultiplied canvas, so transparent edges do not darken.
func resize(src *image.NRGBA, size int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	return dst
}

// encodeBMPFrame packs an NRGBA image as an ICO frame: BITMAPINFOHEADER plus a
// bottom-up 32-bit BGRA XOR plane plus the 1-bit AND (transparency) mask. The
// header's biHeight carries both planes (2 * height) per the ICO format.
func encodeBMPFrame(img *image.NRGBA) ([]byte, error) {
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()
	andRow := ((w + 31) / 32) * 4 // AND mask rows pad to 32 bits
	xorSize := w * h * 4
	andSize := andRow * h
	frame := make([]byte, bmpHeaderSize+xorSize+andSize)

	// BITMAPINFOHEADER — the doubled biHeight is what marks this as an
	// XOR+AND pair rather than a plain bottom-up DIB.
	b := frame
	binary.LittleEndian.PutUint32(b[0:], bmpHeaderSize)
	binary.LittleEndian.PutUint32(b[4:], uint32(w))
	binary.LittleEndian.PutUint32(b[8:], uint32(2*h))
	binary.LittleEndian.PutUint16(b[12:], planes)
	binary.LittleEndian.PutUint16(b[14:], bitsPerPixel)
	// biCompression = BI_RGB (0); biSizeImage covers both planes.
	binary.LittleEndian.PutUint32(b[20:], uint32(xorSize+andSize))

	// XOR plane, bottom-up. BGRA byte order; fully transparent pixels keep
	// their RGB zeroed so no renderer leaks garbage through.
	xor := frame[bmpHeaderSize:]
	for y := h - 1; y >= 0; y-- {
		row := xor[(h-1-y)*w*4:]
		for x := 0; x < w; x++ {
			i := img.PixOffset(x, y)
			a := img.Pix[i+3]
			row[x*4+0] = 0
			row[x*4+1] = 0
			row[x*4+2] = 0
			row[x*4+3] = a
			if a == 0 {
				continue
			}
			row[x*4+0] = img.Pix[i+2] // blue
			row[x*4+1] = img.Pix[i+1] // green
			row[x*4+2] = img.Pix[i+0] // red
		}
	}

	// AND mask, bottom-up: 1 = transparent. Windows honors the alpha channel
	// on 32bpp frames, but the mask must still be present and correct for
	// renderers that ignore alpha.
	and := frame[bmpHeaderSize+xorSize:]
	for y := h - 1; y >= 0; y-- {
		row := and[(h-1-y)*andRow:]
		for x := 0; x < w; x++ {
			if img.Pix[img.PixOffset(x, y)+3] == 0 {
				row[x/8] |= 1 << (7 - x%8)
			}
		}
	}
	return frame, nil
}

// runDump parses an .ico and writes every frame as a PNG into outDir, so the
// artwork inside a built executable's icon resource can be checked against the
// source logo pixel by pixel.
func runDump(icoPath, outDir string) error {
	data, err := os.ReadFile(icoPath)
	if err != nil {
		return err
	}
	if len(data) < icoHeaderSize {
		return fmt.Errorf("file shorter than an ICONDIR")
	}
	count := binary.LittleEndian.Uint16(data[4:6])
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	fmt.Printf("%s: %d frames\n", icoPath, count)
	for i := range int(count) {
		entry := data[icoHeaderSize+i*icoEntrySize:]
		w := int(entry[0])
		h := int(entry[1])
		size := binary.LittleEndian.Uint32(entry[8:12])
		offset := binary.LittleEndian.Uint32(entry[12:16])
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		if int(offset)+int(size) > len(data) {
			return fmt.Errorf("frame %d points past end of file", i)
		}
		frame := data[offset : offset+size]

		var pngBytes []byte
		if len(frame) > 4 && frame[0] == 0x89 && frame[1] == 'P' {
			// PNG-compressed frame: keep verbatim.
			pngBytes = frame
		} else {
			pngBytes, err = decodeBMPFramePNG(frame, w, h)
			if err != nil {
				return fmt.Errorf("frame %d (%dx%d): %w", i, w, h, err)
			}
		}
		name := filepath.Join(outDir, fmt.Sprintf("%dx%d.png", w, h))
		if err := os.WriteFile(name, pngBytes, 0o644); err != nil {
			return err
		}
		fmt.Printf("  frame %3dx%-3d %8d bytes sha256=%x\n", w, h, size, sha256sum(frame))
	}
	return nil
}

// decodeBMPFramePNG converts a 32bpp BMP frame (XOR+AND pair) back to a
// top-down PNG. The AND mask is ignored on read: the alpha channel is
// authoritative for 32bpp frames, which is the only encoding this tool writes.
func decodeBMPFramePNG(frame []byte, w, h int) ([]byte, error) {
	if len(frame) < bmpHeaderSize {
		return nil, fmt.Errorf("frame shorter than BITMAPINFOHEADER")
	}
	if got := binary.LittleEndian.Uint32(frame[0:]); got != bmpHeaderSize {
		return nil, fmt.Errorf("unsupported header size %d", got)
	}
	if got := binary.LittleEndian.Uint16(frame[14:]); got != bitsPerPixel {
		return nil, fmt.Errorf("unsupported bit depth %d", got)
	}
	andRow := ((w + 31) / 32) * 4
	xorSize := w * h * 4
	if len(frame) < bmpHeaderSize+xorSize+andRow*h {
		return nil, fmt.Errorf("frame data truncated")
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		src := frame[bmpHeaderSize+(h-1-y)*w*4:]
		for x := 0; x < w; x++ {
			i := img.PixOffset(x, y)
			if src[x*4+3] == 0 {
				continue // fully transparent: RGB stays zeroed
			}
			img.Pix[i+0] = src[x*4+2] // red
			img.Pix[i+1] = src[x*4+1] // green
			img.Pix[i+2] = src[x*4+0] // blue
			img.Pix[i+3] = src[x*4+3]
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
