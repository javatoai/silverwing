//go:build darwin

package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"silverwing-go/internal/process"
)

const (
	darwinProcPIDBSDInfo  = 3
	darwinProcPIDPathSize = 4096
	darwinProcZombie      = 5 // XNU bsd/sys/proc.h: SZOMB
)

// darwinProcBSDInfo mirrors XNU's public struct proc_bsdinfo. In particular,
// pbi_start_tvsec/usec are the final fields; PROC_PIDTBSDINFO is the flavor
// that supplies process identity timestamps (PROC_PIDTASKINFO does not).
// uid_t/gid_t are uint32 on Darwin, and MAXCOMLEN is 16 in the XNU ABI.
type darwinProcBSDInfo struct {
	flags     uint32
	status    uint32
	xstatus   uint32
	pid       uint32
	ppid      uint32
	uid       uint32
	gid       uint32
	ruid      uint32
	rgid      uint32
	svuid     uint32
	svgid     uint32
	reserved  uint32
	comm      [16]byte
	name      [32]byte
	nfiles    uint32
	pgid      uint32
	pjobc     uint32
	tdev      uint32
	tpgid     uint32
	nice      int32
	startSec  uint64
	startUsec uint64
}

type darwinProcPIDInfoFunc func(pid int32, flavor int32, arg uint64, buffer unsafe.Pointer, bufferSize int32) int32
type darwinProcPIDPathFunc func(pid int32, buffer unsafe.Pointer, bufferSize uint32) int32

type darwinProcAPI struct {
	pidInfo darwinProcPIDInfoFunc
	pidPath darwinProcPIDPathFunc
}

var (
	darwinProcAPILoadOnce sync.Once
	darwinProcAPIValue    *darwinProcAPI
	darwinProcAPILoadErr  error
	// Keep libproc loaded while registered function pointers remain in use.
	darwinProcLibrary uintptr
)

func loadDarwinProcAPI() (*darwinProcAPI, error) {
	darwinProcAPILoadOnce.Do(func() {
		handle, err := purego.Dlopen("/usr/lib/libproc.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			darwinProcAPILoadErr = fmt.Errorf("加载 macOS libproc：%w", err)
			return
		}
		pidInfoAddress, err := purego.Dlsym(handle, "proc_pidinfo")
		if err != nil {
			_ = purego.Dlclose(handle)
			darwinProcAPILoadErr = fmt.Errorf("解析 macOS proc_pidinfo：%w", err)
			return
		}
		pidPathAddress, err := purego.Dlsym(handle, "proc_pidpath")
		if err != nil {
			_ = purego.Dlclose(handle)
			darwinProcAPILoadErr = fmt.Errorf("解析 macOS proc_pidpath：%w", err)
			return
		}
		api := &darwinProcAPI{}
		purego.RegisterFunc(&api.pidInfo, pidInfoAddress)
		purego.RegisterFunc(&api.pidPath, pidPathAddress)
		darwinProcLibrary = handle
		darwinProcAPIValue = api
	})
	return darwinProcAPIValue, darwinProcAPILoadErr
}

func queryUnix(ctx context.Context, port int) ([]PortProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if port < 1 || port > 65535 {
		return nil, errors.New("端口必须在 1 到 65535 之间")
	}

	endpointsByPID := make(map[int][]PortEndpoint)
	nameByPID := make(map[int]string)
	for _, protocol := range []string{"TCP", "UDP"} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		selector := "-i" + protocol + ":" + strconv.Itoa(port)
		result, err := process.NewRunner().Run(ctx, process.Spec{
			Executable: "lsof",
			Arguments:  []string{"-nP", "-F", "pcnftT", selector},
			Timeout:    20 * time.Second,
		})
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(result.Stderr) != "" {
			return nil, errors.New(strings.TrimSpace(result.Stderr))
		}
		if result.ExitCode == 1 && strings.TrimSpace(result.Stdout) == "" {
			continue
		}
		if result.ExitCode != 0 {
			return nil, fmt.Errorf("lsof %s 查询失败，退出码 %d", protocol, result.ExitCode)
		}
		parseDarwinLsof(result.Stdout, protocol, port, endpointsByPID, nameByPID)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if len(endpointsByPID) == 0 {
		return []PortProcess{}, nil
	}
	pidList := make([]int, 0, len(endpointsByPID))
	for pid := range endpointsByPID {
		pidList = append(pidList, pid)
	}
	sort.Ints(pidList)
	items := make([]PortProcess, 0, len(pidList))
	for _, pid := range pidList {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		endpoints := endpointsByPID[pid]
		sort.Slice(endpoints, func(i, j int) bool {
			if endpoints[i].Protocol != endpoints[j].Protocol {
				return endpoints[i].Protocol == "TCP"
			}
			if endpoints[i].Address != endpoints[j].Address {
				return endpoints[i].Address < endpoints[j].Address
			}
			if endpoints[i].Port != endpoints[j].Port {
				return endpoints[i].Port < endpoints[j].Port
			}
			return endpoints[i].State < endpoints[j].State
		})

		details, inspectErr := inspectPortProcess(ctx, pid)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if inspectErr != nil {
			details = PortProcess{PID: pid, Name: nameByPID[pid]}
		} else if details.Name == "" {
			details.Name = nameByPID[pid]
		}
		first := endpoints[0]
		details.Protocol = first.Protocol
		details.Address = first.Address
		details.Port = first.Port
		details.State = first.State
		details.Endpoints = endpoints
		items = append(items, details)
	}
	return items, nil
}

func (s *PortService) queryPlatform(ctx context.Context, port int) ([]PortProcess, error) {
	return queryUnix(ctx, port)
}

func (s *PortService) inspectPortProcess(ctx context.Context, pid int) (PortProcess, error) {
	return inspectPortProcess(ctx, pid)
}

func (s *PortService) forceTerminatePortProcess(ctx context.Context, item PortProcess) error {
	return forceTerminatePortProcess(ctx, item)
}

func inspectPortProcess(ctx context.Context, pid int) (PortProcess, error) {
	if err := ctx.Err(); err != nil {
		return PortProcess{}, err
	}
	if pid <= 0 {
		return PortProcess{}, errors.New("进程 ID 无效")
	}
	api, err := loadDarwinProcAPI()
	if err != nil {
		return PortProcess{}, err
	}
	info, err := readDarwinProcessInfo(api, pid)
	if err != nil {
		return PortProcess{}, err
	}
	if info.startSec == 0 || info.startUsec >= 1_000_000 {
		return PortProcess{}, errors.New("macOS 未返回有效的进程启动时间")
	}
	started := time.Unix(int64(info.startSec), int64(info.startUsec)*int64(time.Microsecond)).UTC()
	startedAt := started.Format(time.RFC3339Nano)
	path := darwinProcessPath(api, int32(pid))
	name := cString(info.name[:])
	if name == "" {
		name = cString(info.comm[:])
	}
	if name == "" && path != "" {
		name = filepath.Base(path)
	}
	commandLine, user := darwinProcessCommandAndUser(ctx, pid)
	if err := ctx.Err(); err != nil {
		return PortProcess{}, err
	}
	// Confirm the native start identity again after optional ps/path reads. This
	// keeps the identity returned by inspectPortProcess tied to the PID observed
	// at the end of inspection, which is also used immediately before SIGKILL.
	latest, err := readDarwinProcessInfo(api, pid)
	if err != nil {
		return PortProcess{}, err
	}
	if latest.startSec != info.startSec || latest.startUsec != info.startUsec {
		return PortProcess{}, errors.New("进程身份在读取详情期间已变化")
	}
	return PortProcess{
		PID:                  pid,
		Name:                 name,
		Path:                 path,
		StartedAt:            startedAt,
		StartedAtEpochMillis: started.UnixMilli(),
		StartTimeDisplay:     started.In(time.Local).Format("2006年01月02日 15:04:05"),
		CommandLine:          commandLine,
		User:                 user,
		CanTerminate:         pid != os.Getpid(),
	}, nil
}

func readDarwinProcessInfo(api *darwinProcAPI, pid int) (darwinProcBSDInfo, error) {
	var info darwinProcBSDInfo
	read := api.pidInfo(int32(pid), darwinProcPIDBSDInfo, 0, unsafe.Pointer(&info), int32(unsafe.Sizeof(info)))
	if read != int32(unsafe.Sizeof(info)) {
		// Signal zero is an existence/permission probe only; it does not send a
		// terminating signal. The shared await loop uses os.ErrNotExist to
		// distinguish a process that exited from an identity-read failure.
		if probeErr := syscall.Kill(pid, 0); errors.Is(probeErr, syscall.ESRCH) {
			return darwinProcBSDInfo{}, os.ErrNotExist
		}
		return darwinProcBSDInfo{}, fmt.Errorf("读取 PID %d 的 macOS 进程身份失败（proc_pidinfo 返回 %d）", pid, read)
	}
	if info.status == darwinProcZombie {
		return darwinProcBSDInfo{}, os.ErrNotExist
	}
	return info, nil
}

func darwinProcessPath(api *darwinProcAPI, pid int32) string {
	buffer := make([]byte, darwinProcPIDPathSize)
	length := api.pidPath(pid, unsafe.Pointer(&buffer[0]), uint32(len(buffer)))
	if length <= 0 || int(length) > len(buffer) {
		return ""
	}
	return cString(buffer[:length])
}

func cString(value []byte) string {
	if index := strings.IndexByte(string(value), 0); index >= 0 {
		value = value[:index]
	}
	return string(value)
}

func darwinProcessCommandAndUser(ctx context.Context, pid int) (string, string) {
	result, err := process.NewRunner().Run(ctx, process.Spec{
		Executable: "ps",
		Arguments:  []string{"-p", strconv.Itoa(pid), "-o", "user=", "-o", "command="},
		Timeout:    5 * time.Second,
	})
	if err != nil || result.ExitCode != 0 {
		return "", ""
	}
	line := strings.TrimSpace(result.Stdout)
	if line == "" {
		return "", ""
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", ""
	}
	user := fields[0]
	commandLine := strings.TrimSpace(strings.TrimPrefix(line, user))
	return commandLine, user
}

func forceTerminatePortProcess(ctx context.Context, item PortProcess) error {
	if item.PID <= 0 || item.PID == os.Getpid() {
		return errors.New("不能结束自身或系统进程")
	}
	if item.StartedAt == "" {
		return errors.New("无法确认进程启动时间，拒绝结束进程")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := inspectPortProcess(ctx, item.PID)
	if err != nil {
		return fmt.Errorf("结束前无法复核 macOS 进程身份：%w", err)
	}
	if current.StartedAt != item.StartedAt {
		return errors.New("进程身份已变化，拒绝结束进程")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := syscall.Kill(item.PID, syscall.SIGKILL); err != nil {
		return fmt.Errorf("向 PID %d 提交强制结束请求：%w", item.PID, err)
	}
	return nil
}
