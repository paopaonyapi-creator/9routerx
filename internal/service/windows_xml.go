// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package service

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"os"
	"unicode/utf16"
)

// schtasks /tr has a 261-character limit; XML keeps long config paths intact.
func createWindowsTaskXML(exe string, args []string) error {
	user := windowsTaskUser()
	if user == "" {
		return fmt.Errorf("service.Install: cannot resolve user for scheduled-task XML")
	}
	escape := func(s string) string { var b bytes.Buffer; _ = xml.EscapeText(&b, []byte(s)); return b.String() }
	doc := `<?xml version="1.0" encoding="UTF-16"?><Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><Triggers><LogonTrigger><Enabled>true</Enabled><UserId>` + escape(user) + `</UserId></LogonTrigger></Triggers><Principals><Principal id="User"><UserId>` + escape(user) + `</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals><Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><ExecutionTimeLimit>PT0S</ExecutionTimeLimit></Settings><Actions Context="User"><Exec><Command>` + escape(exe) + `</Command><Arguments>` + escape(windowsArguments(args)) + `</Arguments></Exec></Actions></Task>`
	file, err := os.CreateTemp("", "9routerx-task-*.xml")
	if err != nil {
		return err
	}
	path := file.Name()
	defer os.Remove(path)
	units := utf16.Encode([]rune(doc))
	encoded := make([]byte, 2+2*len(units))
	encoded[0], encoded[1] = 0xff, 0xfe
	for i, unit := range units {
		binary.LittleEndian.PutUint16(encoded[2+2*i:], unit)
	}
	if _, err := file.Write(encoded); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	_, err = run("schtasks.exe", "/create", "/tn", windowsTaskName, "/xml", path, "/f")
	return err
}
func windowsArguments(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return windowsCommandLine(args[0], args[1:])
}
