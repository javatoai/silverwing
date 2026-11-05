package larkcli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"silverwing-go/internal/process"
)

// Child processes are this isolated test executable, never the installed CLI.
func TestMain(m *testing.M) {
	if os.Getenv("SW_NOTICE_FAKE_LARK") == "1" {
		args := os.Args[1:]
		if len(args) > 1 && args[0] == "auth" && args[1] == "status" {
			fmt.Println(`{"appId":"cli_a95dcece863a9cce","identities":{"user":{"userName":"本机测试姓名","openId":"ou_test"},"bot":{"status":"ready"}}}`)
			os.Exit(0)
		}
		if len(args) > 1 && args[0] == "im" && args[1] == "+messages-send" {
			flags := map[string]string{}
			for i := 0; i+1 < len(args); i++ {
				if len(args[i]) > 2 && args[i][:2] == "--" {
					flags[args[i]] = args[i+1]
				}
			}
			if flags["--as"] != "bot" || flags["--profile"] != "cli_a95dcece863a9cce" || flags["--user-id"] != "ou_test" || flags["--chat-id"] != "" {
				os.Exit(93)
			}
			for i, arg := range args {
				if arg == "--content" && i+1 < len(args) && args[i+1] == os.Getenv("SW_NOTICE_EXPECTED_CARD") && json.Valid([]byte(args[i+1])) {
					fmt.Println(`{"ok":true,"data":{"message_id":"om_isolated_child"}}`)
					os.Exit(0)
				}
			}
			fmt.Fprintln(os.Stderr, `{"ok":false,"error":{"code":"BAD_TEST_CARD","message":"card changed in process transport"}}`)
		}
		os.Exit(92)
	}
	os.Exit(m.Run())
}

type isolatedProcessRunner struct{ card string }

func (r *isolatedProcessRunner) Run(ctx context.Context, s process.Spec) (process.Result, error) {
	if s.Env == nil {
		s.Env = map[string]string{}
	}
	s.Env["SW_NOTICE_FAKE_LARK"] = "1"
	s.Env["SW_NOTICE_EXPECTED_CARD"] = r.card
	return process.NewRunner().Run(ctx, s)
}
func TestNotificationJSONSurvivesRealProcessAndWindowsShim(t *testing.T) {
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	raw := `{"schema":"2.0","text":"中文与空格 \"quoted\" %PATH% ! & | < > ^ $()","elements":[{"text":"下一行\nemoji🙂"}]}`
	for _, shim := range []bool{false, true} {
		if shim && runtime.GOOS != "windows" {
			continue
		}
		t.Run(fmt.Sprint("shim=", shim), func(t *testing.T) {
			path := exe
			if shim {
				base := filepath.Join(t.TempDir(), "中文 空格")
				if e = os.MkdirAll(base, 0700); e != nil {
					t.Fatal(e)
				}
				path = filepath.Join(base, "lark-cli.cmd")
				pkg := filepath.Join(base, "node_modules", "@larksuite", "cli")
				if e = os.MkdirAll(filepath.Join(pkg, "bin"), 0700); e != nil {
					t.Fatal(e)
				}
				binary, e := os.ReadFile(exe)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(filepath.Join(pkg, "bin", "lark-cli.exe"), binary, 0700); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(filepath.Join(pkg, "package.json"), []byte(`{"name":"@larksuite/cli","bin":{"lark-cli":"scripts/run.js"}}`), 0600); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(path, []byte("@echo off\r\nrem @larksuite/cli/scripts/run.js\r\n\""+exe+"\" %*\r\n"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			runner := &isolatedProcessRunner{card: raw}
			client := fakeClient(&fakeRunner{})
			client.Runner = runner
			client.Config.LarkExecutablePath = &path
			id, e := client.Identity(context.Background())
			if e != nil || id.Name != "本机测试姓名" {
				t.Fatal(id, e)
			}
			answer := client.Send(context.Background(), "ou_test", raw, "uat-card-isolated")
			if answer.MessageID != "om_isolated_child" {
				t.Fatal(answer)
			}
		})
	}
}
