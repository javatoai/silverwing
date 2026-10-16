//go:build windows

package tools

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"silverwing-go/internal/process"
)

func (s *PortService) queryPlatform(ctx context.Context, port int) ([]PortProcess, error) {
	spec := powerShellSpec(portQueryWindowsScript)
	spec.Env = map[string]string{"SILVERWING_TOOL_PORT": strconv.Itoa(port)}
	result, err := s.Runner.Run(ctx, spec)
	if err != nil {
		return nil, err
	}
	if err = result.RequireSuccess(); err != nil {
		return nil, err
	}
	var items []PortProcess
	if err = json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &items); err != nil {
		return nil, fmt.Errorf("解析端口详情：%w", err)
	}
	return items, nil
}

func (s *PortService) inspectPortProcess(ctx context.Context, pid int) (PortProcess, error) {
	spec := powerShellSpec(`[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false);$item=Get-Process -Id ` + strconv.Itoa(pid) + ` -ErrorAction SilentlyContinue;if($null -eq $item){[Console]::Out.Write('null');exit 0};$started='';try{$started=$item.StartTime.ToUniversalTime().ToString('o')}catch{};[Console]::Out.Write((ConvertTo-Json -InputObject @{pid=$item.Id;startedAt=$started} -Compress))`)
	spec.Timeout = 5 * time.Second
	result, err := s.Runner.Run(ctx, spec)
	if err != nil {
		return PortProcess{}, err
	}
	if err = result.RequireSuccess(); err != nil {
		return PortProcess{}, err
	}
	if strings.TrimSpace(result.Stdout) == "null" {
		return PortProcess{}, os.ErrNotExist
	}
	var item PortProcess
	if err = json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &item); err != nil {
		return item, err
	}
	return item, nil
}

func (s *PortService) forceTerminatePortProcess(ctx context.Context, item PortProcess) error {
	if item.PID <= 0 || item.PID == os.Getpid() || uint64(item.PID) > uint64(^uint32(0)) {
		return errors.New("不能结束自身或无效的系统进程")
	}
	started, err := time.Parse(time.RFC3339Nano, item.StartedAt)
	if err != nil {
		return errors.New("进程身份无效")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Bind identity validation and termination to one kernel process object;
	// reopening by PID after checking StartTime could target a reused PID.
	handle, err := syscall.OpenProcess(syscall.PROCESS_TERMINATE|0x1000, false, uint32(item.PID))
	if err != nil {
		return fmt.Errorf("打开待结束进程：%w", err)
	}
	defer syscall.CloseHandle(handle)
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return fmt.Errorf("读取进程启动身份：%w", err)
	}
	if !started.Equal(time.Unix(0, creation.Nanoseconds())) {
		return errors.New("进程身份已变化，拒绝结束进程")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return syscall.TerminateProcess(handle, 1)
}

func powerShellSpec(script string) process.Spec {
	units := utf16.Encode([]rune(script))
	bytes := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(bytes[2*i:], unit)
	}
	return process.Spec{Executable: "powershell.exe", Arguments: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(bytes)}, Timeout: 20 * time.Second}
}

const portQueryWindowsScript = `$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)
try{
 $port=[int]::Parse($env:SILVERWING_TOOL_PORT)
 $rows=[Collections.Generic.List[object]]::new()
 Get-NetTCPConnection -ErrorAction Stop | Where-Object {$_.LocalPort -eq $port} | ForEach-Object {$rows.Add(@{pid=[int]$_.OwningProcess;protocol='TCP';address=[string]$_.LocalAddress;port=[int]$_.LocalPort;state=[string]$_.State})}
 Get-NetUDPEndpoint -ErrorAction Stop | Where-Object {$_.LocalPort -eq $port} | ForEach-Object {$rows.Add(@{pid=[int]$_.OwningProcess;protocol='UDP';address=[string]$_.LocalAddress;port=[int]$_.LocalPort;state=''})}
 $owners=@{}
 $ownerIds=@($rows | ForEach-Object {$_.pid} | Select-Object -Unique)
 if($ownerIds.Count -gt 0){
  Get-CimInstance -ClassName Win32_Process -ErrorAction SilentlyContinue | Where-Object {$ownerIds -contains $_.ProcessId} | ForEach-Object {
   $entry=$_;$started='';$userName='';$exePath=[string]$entry.ExecutablePath
   try{if($entry.CreationDate){$started=$entry.CreationDate.ToUniversalTime().ToString('o')}}catch{}
   try{$native=Get-Process -Id $entry.ProcessId -ErrorAction Stop;$started=$native.StartTime.ToUniversalTime().ToString('o');if($native.Path){$exePath=$native.Path}}catch{}
   try{$owner=Invoke-CimMethod -InputObject $entry -MethodName GetOwner -ErrorAction Stop;if($owner.ReturnValue -eq 0){$userName=if($owner.Domain){$owner.Domain+'\'+$owner.User}else{$owner.User}}}catch{}
   $owners[[string]$entry.ProcessId]=@{name=[string]$entry.Name;path=$exePath;commandLine=[string]$entry.CommandLine;user=$userName;startedAt=$started}
  }
 }
 $records=@($rows.ToArray() | ForEach-Object {
  $owner=$owners[[string]$_.pid]
  @{pid=$_.pid;protocol=$_.protocol;address=$_.address;port=$_.port;state=$_.state;name=[string]$owner.name;path=[string]$owner.path;commandLine=[string]$owner.commandLine;user=[string]$owner.user;startedAt=[string]$owner.startedAt}
 })
 [Console]::Out.WriteLine((ConvertTo-Json -InputObject $records -Depth 6 -Compress))
}catch{[Console]::Error.WriteLine($_.Exception.Message);exit 2}`
