// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package service

import (
	"encoding/binary"
	"encoding/xml"
	"os"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestLongWindowsTaskUsesXML(t *testing.T) {
	stubHost(t, hostStub{goos: "windows"})
	stubWindowsTaskUser(t, "user & account")
	exe := `C:\Program Files\9RouterX\9routerx.exe`
	args := []string{"--data-dir=C:\\" + strings.Repeat("long data & ", 30)}
	var createdPath string
	stubRunFunc(t, func(name string, argv ...string) (string, error) {
		if len(argv) > 0 && argv[0] == "/create" {
			createdPath = argAfter(argv, "/xml")
			if createdPath == "" {
				t.Fatal("long command passed to /tr")
			}
			body, err := os.ReadFile(createdPath)
			if err != nil {
				t.Fatal(err)
			}
			if len(body) < 2 || body[0] != 0xff || body[1] != 0xfe {
				t.Fatal("missing UTF-16LE BOM")
			}
			var units []uint16
			for i := 2; i < len(body); i += 2 {
				units = append(units, binary.LittleEndian.Uint16(body[i:]))
			}
			body = []byte(strings.Replace(string(utf16.Decode(units)), `encoding="UTF-16"`, `encoding="UTF-8"`, 1))
			var task struct {
				Principals struct {
					Principal struct{ UserId, LogonType, RunLevel string }
				}
				Actions struct {
					Exec struct{ Command, Arguments string }
				}
			}
			if err := xml.Unmarshal(body, &task); err != nil {
				t.Fatal(err)
			}
			if task.Actions.Exec.Command != exe || task.Actions.Exec.Arguments != windowsArguments(args) {
				t.Fatalf("argv drift: %+v", task)
			}
			p := task.Principals.Principal
			if p.UserId != "user & account" || p.LogonType != "InteractiveToken" || p.RunLevel != "LeastPrivilege" {
				t.Fatal("unsafe principal", p)
			}
		}
		return "", nil
	})
	if _, err := installWindowsTask(exe, args); err != nil {
		t.Fatal(err)
	}
	if createdPath == "" {
		t.Fatal("missing create")
	}
	if _, err := os.Stat(createdPath); !os.IsNotExist(err) {
		t.Fatal("temporary XML not cleaned")
	}
}
