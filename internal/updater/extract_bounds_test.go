// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
)

// targetExecutablePayload returns bytes that parse as an executable for the
// platform the tests run on. Extraction now validates the header, so a text
// blob can no longer stand in for a binary.
func targetExecutablePayload(t *testing.T, fill int) []byte {
	t.Helper()
	header, err := headerFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	return append(header, bytes.Repeat([]byte{0x90}, fill)...)
}

// foreignExecutablePayload returns an executable header for a platform this
// process does not run on, so a target mismatch can be exercised.
func foreignExecutablePayload(t *testing.T, fill int) []byte {
	t.Helper()
	osFor := "windows"
	if runtime.GOOS == "windows" {
		osFor = "linux"
	}
	header, err := headerFor(osFor, "amd64")
	if err != nil {
		t.Skip(err)
	}
	return append(header, bytes.Repeat([]byte{0x90}, fill)...)
}

func headerFor(goos, goarch string) ([]byte, error) {
	switch goos {
	case "linux":
		class, machine, ok := elfTarget(goarch)
		if !ok {
			return nil, fmt.Errorf("no ELF mapping for %s", goarch)
		}
		return testELFHeader(class, machine), nil
	case "darwin":
		cpu, ok := machoCPU(goarch)
		if !ok {
			return nil, fmt.Errorf("no Mach-O mapping for %s", goarch)
		}
		return testMachOHeader(cpu), nil
	case "windows":
		machine, ok := peMachine(goarch)
		if !ok {
			return nil, fmt.Errorf("no PE mapping for %s", goarch)
		}
		return testPEHeader(machine), nil
	}
	return nil, fmt.Errorf("no executable format known for %s", goos)
}

func testELFHeader(class elf.Class, machine elf.Machine) []byte {
	h := make([]byte, 64)
	copy(h, "\x7fELF")
	h[4] = byte(class)
	h[5] = byte(elf.ELFDATA2LSB)
	h[6] = byte(elf.EV_CURRENT)
	binary.LittleEndian.PutUint16(h[16:], uint16(elf.ET_EXEC))
	binary.LittleEndian.PutUint16(h[18:], uint16(machine))
	binary.LittleEndian.PutUint32(h[20:], uint32(elf.EV_CURRENT))
	return h
}

func testMachOHeader(cpu macho.Cpu) []byte {
	h := make([]byte, 32)
	binary.LittleEndian.PutUint32(h[0:], uint32(macho.Magic64))
	binary.LittleEndian.PutUint32(h[4:], uint32(cpu))
	binary.LittleEndian.PutUint32(h[8:], 3) // CPU_SUBTYPE_ANY
	binary.LittleEndian.PutUint32(h[12:], 2)
	return h
}

func testPEHeader(machine uint16) []byte {
	h := make([]byte, 128) // NewFile reads a 96-byte DOS header
	copy(h, "MZ")
	binary.LittleEndian.PutUint32(h[0x3c:], 0x40)
	copy(h[0x40:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(h[0x44:], machine)
	binary.LittleEndian.PutUint16(h[0x5a:], 0x0102) // executable image
	return h
}

// tarHeaderBlock builds one 512-byte tar header, so a test can declare a size
// far larger than the bytes that follow without ever writing that much.
func tarHeaderBlock(name string, size int64, typeflag byte) []byte {
	h := make([]byte, 512)
	copy(h[0:100], name)
	copy(h[100:108], "0000755\x00")
	copy(h[108:116], "0000000\x00")
	copy(h[116:124], "0000000\x00")
	copy(h[124:136], fmt.Sprintf("%011o\x00", size))
	copy(h[136:148], "00000000000\x00")
	copy(h[148:156], "        ")
	h[156] = typeflag
	copy(h[257:263], "ustar\x00")
	copy(h[263:265], "00")
	var sum int
	for _, b := range h {
		sum += int(b)
	}
	copy(h[148:156], fmt.Sprintf("%06o\x00 ", sum))
	return h
}

func gzipBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(raw); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func TestExtractExecutableBytes_RejectsOversizedTarEntry(t *testing.T) {
	archive := gzipBytes(t, tarHeaderBlock("9routerx_linux_amd64", maxArchiveEntryBytes+1, tar.TypeReg))
	_, err := extractExecutableBytes(archive, "9routerx_linux_amd64.tar.gz")
	if err == nil || !strings.Contains(err.Error(), "over the") {
		t.Fatalf("err = %v, want an over-the-limit refusal", err)
	}
}

func TestExtractExecutableBytes_RejectsTooManyEntries(t *testing.T) {
	var raw []byte
	for i := 0; i <= maxArchiveEntries; i++ {
		raw = append(raw, tarHeaderBlock(fmt.Sprintf("docs/file-%d", i), 0, tar.TypeReg)...)
	}
	_, err := extractExecutableBytes(gzipBytes(t, raw), "9routerx.tar.gz")
	if err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("err = %v, want an entry-count refusal", err)
	}
}

func TestExtractExecutableBytes_RejectsSymlinkZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "9routerx"}
	hdr.SetMode(os.ModeSymlink | 0o777)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatalf("create header: %v", err)
	}
	if _, err := w.Write([]byte("../../etc/passwd")); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if _, err := extractExecutableBytes(buf.Bytes(), "9routerx_linux_amd64.zip"); err == nil ||
		!strings.Contains(err.Error(), "symlink") {
		t.Fatalf("err = %v, want a symlink refusal", err)
	}
}

func TestExtractExecutableBytes_MalformedArchiveIsNotRawFallback(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		url  string
	}{
		{"truncated gzip", []byte{0x1f, 0x8b, 0x08, 0x00}, "9routerx_linux_amd64.tar.gz"},
		{"truncated zip", []byte("PK\x03\x04"), "9routerx_windows_amd64.zip"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractExecutableBytes(tc.data, tc.url)
			if err == nil {
				t.Fatalf("got %d bytes, want a refusal instead of the raw download", len(got))
			}
		})
	}
}

func TestExtractExecutableBytes_RejectsForeignTargetFormat(t *testing.T) {
	archive := updateAssetTarGz(t, foreignExecutablePayload(t, 2048))
	_, err := extractExecutableBytes(archive, "9routerx_"+runtime.GOOS+"_"+runtime.GOARCH+".tar.gz")
	if err == nil {
		t.Fatal("an executable for another platform must not be installed")
	}
}

func TestExtractExecutableBytes_RawNonExecutableIsRejected(t *testing.T) {
	if _, err := extractExecutableBytes(bytes.Repeat([]byte("text"), 1024), "9routerx_linux_amd64"); err == nil {
		t.Fatal("a raw non-executable download must be refused")
	}
}

func TestExecutableMatchesTarget(t *testing.T) {
	switch runtime.GOOS {
	case "linux", "darwin", "windows":
	default:
		t.Skipf("no strict format check on %s", runtime.GOOS)
	}
	target := targetExecutablePayload(t, 0)
	if err := executableMatchesTarget(target); err != nil {
		t.Errorf("target payload rejected: %v", err)
	}
	foreign := foreignExecutablePayload(t, 0)
	if err := executableMatchesTarget(foreign); err == nil {
		t.Error("foreign-platform payload accepted")
	}
}
