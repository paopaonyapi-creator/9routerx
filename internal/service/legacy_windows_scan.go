// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"9routerx/proxy/internal/daemon"
)

func discoverWindowsLegacy(p daemon.ProcessInfo) ([]LegacyEntry, []string) {
	// CIM and task XML avoid localized LIST column names. Enumerate before any writes.
	script := fmt.Sprintf(`$ErrorActionPreference='Stop'
$entries=@(); $warnings=@(); $systemScope=$%t
try {
 Get-CimInstance Win32_Service | Where-Object { $_.PathName -match '9router|node' } | ForEach-Object {
  if ($_.ProcessId -eq %d -and $_.ProcessId -gt 0) { $systemScope=$true }
  $entries += @{Kind='Windows service';Name=$_.Name;Command=$_.PathName;Snapshot=$_.PathName;Path=''}
 }
} catch { $warnings += "services: $_" }
try {
 Get-ScheduledTask | ForEach-Object {
  $task=$_; $actions=@($task.Actions | ForEach-Object { '"'+$_.Execute+'" '+$_.Arguments }) -join ' '
  if ($actions -match '9router|node' -and @($task.Actions).Count -ne 1) { $warnings += ('Task has multiple actions; inspect manually: '+$task.TaskPath+$task.TaskName) }
  if ($actions -match '9router|node' -and @($task.Actions).Count -eq 1) {
   $xml=Export-ScheduledTask -TaskName $task.TaskName -TaskPath $task.TaskPath
   $entries += @{Kind='scheduled task';Name=($task.TaskPath+$task.TaskName);Command=$actions;Snapshot=$xml;Path=''}
  }
 }
} catch { $warnings += "tasks: $_" }
$keys=@('HKCU\Software\Microsoft\Windows\CurrentVersion\Run')
if ($systemScope) { $keys += 'HKLM\Software\Microsoft\Windows\CurrentVersion\Run' }
foreach ($key in $keys) {
 try {
  $item=Get-Item ('Registry::'+$key)
  foreach ($name in $item.GetValueNames()) {
   $value=[string]$item.GetValue($name)
   if ($value -match '9router|node') { $entries += @{Kind='registry Run';Name=$name;Command=$value;Snapshot=$value;Path=$key} }
  }
 } catch { $warnings += "Run key ${key}: $_" }
}
try {
 $shell=New-Object -ComObject WScript.Shell
 $folder=[Environment]::GetFolderPath('Startup')
 Get-ChildItem -LiteralPath $folder -File | Where-Object { $_.Name -notlike '*.9router-disabled' } | ForEach-Object {
  $file=$_; $command=''
  if ($file.Extension -eq '.lnk') { $link=$shell.CreateShortcut($file.FullName);$command='"'+$link.TargetPath+'" '+$link.Arguments
  } else { $command=Get-Content -LiteralPath $file.FullName -Raw }
  if ($command -match '9router|node') {
   $entries += @{Kind='startup file';Name=$file.Name;Command=$command;Path=$file.FullName;Snapshot=(Get-FileHash -LiteralPath $file.FullName).Hash}
  }
 }
} catch { $warnings += "Startup: $_" }
@{Entries=@($entries);Warnings=@($warnings)} | ConvertTo-Json -Compress -Depth 5`, p.SystemScope, p.PID)
	out, err := legacyCommand("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	if err != nil {
		return nil, []string{err.Error()}
	}
	var result struct {
		Entries  []LegacyEntry
		Warnings []string
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(out), "\ufeff")), &result); err != nil {
		return nil, []string{"Windows autostart enumeration: " + err.Error()}
	}
	return result.Entries, result.Warnings
}
