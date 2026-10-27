package integrations

import (
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func buildCommentEditCLI(t *testing.T) string {
	t.Helper()
	source := strings.Replace(commentCLISource, `if a[0]=="attachment" {`, `if a[0]=="attachment"&&a[1]=="+download" {name:="original-image.png";if strings.Contains(a[2],"attachment"){name="original.txt"};raw,e:=os.ReadFile(filepath.Join(root,name));if e!=nil{fail(e.Error())};if mode=="download-fail"{fail("download failed")};if e=os.WriteFile(flag("--output"),raw,0600);e!=nil{fail(e.Error())};return};if a[0]=="attachment" {`, 1)
	source = strings.Replace(source, `flag("--action")!="create"`, `(flag("--action")!="create"&&flag("--action")!="update")`, 1)
	source = strings.Replace(source, `p["action"]="create"`, `p["action"]=flag("--action");if flag("--action")=="update"{p["comment_id"]=flag("--comment-id")}`, 1)
	source = strings.Replace(source, `if mode=="create-fail"`, commentEditCLIUpdate+`;if mode=="create-fail"`, 1)
	source = strings.Replace(source, `if mode=="read-fail"||mode=="restart"`, `if mode=="read-fail"||mode=="restart"||(mode=="update-read-fail"&&func()bool{_,e:=os.Stat(filepath.Join(root,"posted"));return e==nil}())`, 1)
	root := t.TempDir()
	src, cli := filepath.Join(root, "main.go"), filepath.Join(root, "meegle")
	if runtime.GOOS == "windows" {
		cli += ".exe"
	}
	if err := os.WriteFile(src, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", cli, src).CombinedOutput(); err != nil {
		t.Fatalf("build edit fixture: %s %v", out, err)
	}
	return cli
}

const commentEditCLIUpdate = `if flag("--action")=="update" {
 if flag("--comment-id")!="original-1" {fail("wrong edit ID")}
 if _,ok:=p["file_token"];!ok{fail("missing explicit attachment state")}
 if mode=="permission-denied"||mode=="invalid-token" {emit(map[string]any{"error":map[string]string{"code":"FORBIDDEN","message":mode}});return}
 if mode=="update-not-applied"{fail("transport outcome unknown")}
 rawBefore,e:=os.ReadFile(filepath.Join(root,"comment"));if e!=nil{fail(e.Error())};var c map[string]any;json.Unmarshal(rawBefore,&c)
 os.WriteFile(filepath.Join(root,"posted"),raw,0600)
 c["content"]=p["content"];c["updated_at"]=time.Now().UTC().Format(time.RFC3339)
 if token,ok:=p["file_token"].(string);ok&&token!=""{c["file_url"]="https://project.feishu.cn/files/"+token}else{delete(c,"file_url")}
 if mode=="update-rewritten"{c["content"]=regexp.MustCompile("!\\[[^\\]]*\\]\\([^)]+\\)<!--([^>]+)-->").ReplaceAllString(p["content"].(string),"![](https://project.feishu.cn/goapi/v5/platform/file/stream/download/$1)")}
 out,_:=json.Marshal(c);os.WriteFile(filepath.Join(root,"comment"),out,0600)
 if mode=="update-read-fail"||mode=="update-lost-response"{fail("transport lost after update")}
 if mode=="update-wrong-id"{emit(map[string]string{"comment_id":"another-comment"});return}
 emit(map[string]string{"comment_id":"original-1"});return
}`

func seedEditComment(t *testing.T, f *commentFixture, assets bool, author, parent string) {
	t.Helper()
	c := map[string]any{"comment_id": "original-1", "content": "原始 **Markdown**", "creator": map[string]string{"user_key": author, "name": "原作者"}, "created_at": "2020-01-01 10:00:00"}
	if parent != "" {
		c["parent_id"] = parent
	}
	if assets {
		c["content"] = "前文\n\n![第一张](https://project.feishu.cn/goapi/v5/platform/file/stream/download/original-a)\n\n中间正文\n\n![第二张](https://project.feishu.cn/goapi/v5/platform/file/stream/download/original-b)\n\n后文\n\n`![代码](local.png)`"
		c["file_url"] = "https://project.feishu.cn/goapi/v5/platform/file/stream/download/original-attachment"
		file, err := os.Create(filepath.Join(f.root, "original-image.png"))
		if err != nil {
			t.Fatal(err)
		}
		if err = png.Encode(file, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
		if err = os.WriteFile(filepath.Join(f.root, "original.txt"), []byte("原附件"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(c)
	if err := os.WriteFile(filepath.Join(f.root, "comment"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func editPayload(f *commentFixture, op string) map[string]any {
	p := f.payload(op)
	p["editCommentID"] = "original-1"
	return p
}

func editDraft(t *testing.T, f *commentFixture, op string, extra map[string]any) {
	t.Helper()
	p := editPayload(f, op)
	for k, v := range extra {
		p[k] = v
	}
	v, err := f.s.Execute(context.Background(), "requirements.comment-draft", p)
	if err != nil {
		t.Fatalf("edit %s: %v", op, err)
	}
	f.draft = v.(domain.RequirementCommentDraft)
}

func updateDraft(t *testing.T, f *commentFixture, progress func(domain.CommentPublishProgress)) (domain.CommentPublishResult, map[string]any) {
	t.Helper()
	p := editPayload(f, "")
	p["requestID"] = commentRandomID()
	v, err := f.s.UpdateRequirementComment(context.Background(), p, progress)
	if err != nil {
		t.Fatal(err)
	}
	r := v.(domain.CommentPublishResult)
	f.draft = r.Draft
	return r, p
}

func commentEditCalls(t *testing.T, f *commentFixture) (updates, creates, uploads, probes int) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.root, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		var a []string
		_ = json.Unmarshal([]byte(line), &a)
		if len(a) < 2 {
			continue
		}
		if a[0] == "attachment" && a[1] == "+upload" {
			uploads++
		}
		if a[0] == "comment" && a[1] == "add" && !strings.Contains(line, "--dry-run") {
			if strings.Contains(line, `"update"`) {
				updates++
			} else {
				creates++
			}
		}
		if a[0] == "auth" || a[0] == "config" || a[0] == "user" && a[1] == "me" {
			probes++
		}
		if a[0] == "comment" && a[1] == "delete" {
			t.Fatal("edit called delete")
		}
	}
	return
}

func TestCommentEditCLI(t *testing.T) {
	cli := buildCommentEditCLI(t)
	for _, mode := range []string{"text", "other-author", "reply", "multi", "add-image", "add-attachment", "remove-image", "remove-attachment", "replace-attachment", "update-rewritten", "permission-denied", "invalid-token", "upload-fail", "update-lost-response", "update-read-fail", "update-not-applied", "update-wrong-id", "remote-change", "remote-change-after-upload", "no-change", "empty", "duplicate", "draft-isolation", "config-change", "account-change"} {
		t.Run(mode, func(t *testing.T) {
			f := newCommentFixture(t, cli)
			creation := f.draft
			assets := mode == "multi" || mode == "remove-image" || mode == "remove-attachment" || mode == "replace-attachment" || mode == "update-rewritten" || mode == "upload-fail" || mode == "remote-change-after-upload"
			author, parent := "user-1", ""
			if mode == "other-author" {
				author = "other-user"
			}
			if mode == "reply" {
				parent = "parent-99"
			}
			seedEditComment(t, f, assets, author, parent)
			editDraft(t, f, "load", nil)
			if f.draft.Edit == nil || f.draft.Edit.CommentID != "original-1" || f.draft.HasChanges() {
				t.Fatal("invalid initial edit snapshot")
			}
			if strings.Contains(f.draft.Content, "查看评论附件") {
				t.Fatal("reader presentation leaked into editor")
			}
			_, _, _, probes := commentEditCalls(t, f)
			if mode == "no-change" {
				r, _ := updateDraft(t, f, nil)
				if r.Status != "failed" {
					t.Fatal(r.Status)
				}
				return
			}
			content := f.draft.Content + "\n\n新增 **说明**"
			if mode == "empty" {
				content = ""
			}
			editDraft(t, f, "save", map[string]any{"content": content})
			if mode == "add-image" {
				file, e := os.Create(filepath.Join(f.root, "new.png"))
				if e != nil {
					t.Fatal(e)
				}
				_ = png.Encode(file, image.NewRGBA(image.Rect(0, 0, 3, 3)))
				_ = file.Close()
				editDraft(t, f, "add-image", map[string]any{"sourcePath": filepath.Join(f.root, "new.png")})
			}
			if mode == "remove-image" {
				editDraft(t, f, "remove", map[string]any{"assetID": f.draft.Images[0].ID})
			}
			if mode == "remove-attachment" || mode == "replace-attachment" {
				editDraft(t, f, "remove", map[string]any{"assetID": f.draft.Attachment.ID})
			}
			if mode == "replace-attachment" || mode == "add-attachment" {
				path := filepath.Join(f.root, "replacement.md")
				_ = os.WriteFile(path, []byte("replacement"), 0600)
				editDraft(t, f, "add-attachment", map[string]any{"sourcePath": path})
			}
			if mode == "remote-change" {
				raw, _ := os.ReadFile(filepath.Join(f.root, "comment"))
				var c map[string]any
				_ = json.Unmarshal(raw, &c)
				c["content"] = "远端新改动"
				raw, _ = json.Marshal(c)
				_ = os.WriteFile(filepath.Join(f.root, "comment"), raw, 0600)
			}
			f.mode(t, mode)
			progress := func(e domain.CommentPublishProgress) {
				if mode == "remote-change-after-upload" && e.Stage == "上传附件" && e.Completed == e.Total {
					seedEditComment(t, f, false, author, parent)
				}
				if mode == "config-change" && e.Stage == "保存修改" {
					cfg := f.config
					proxy := "http://127.0.0.1:9999"
					cfg.CommandProxyURL = &proxy
					_ = f.s.config.Save(cfg)
				}
				if mode == "account-change" && e.Stage == "保存修改" {
					_ = os.WriteFile(filepath.Join(f.root, "account"), []byte("other-account"), 0600)
					if _, _, err := f.s.RequirementIdentity(context.Background(), f.config); err != nil {
						t.Fatal(err)
					}
				}
			}
			r, p := updateDraft(t, f, progress)
			u, c, upl, afterProbes := commentEditCalls(t, f)
			if c != 0 || mode != "account-change" && afterProbes != probes {
				t.Fatalf("edit created a comment or polled identity: create=%d probes=%d", c, afterProbes-probes)
			}
			wantUpdate := 1
			switch mode {
			case "remote-change", "remote-change-after-upload", "upload-fail", "empty", "config-change", "account-change":
				wantUpdate = 0
				if r.Status != "failed" || !r.Draft.HasContent() && mode != "empty" {
					t.Fatalf("failure lost draft: %s %s", r.Status, r.Message)
				}
			case "permission-denied", "invalid-token":
				if r.Status != "failed" || r.Draft.Pending() || !r.Draft.HasContent() {
					t.Fatalf("rejection lost editable draft: %s", r.Status)
				}
			case "update-read-fail", "update-not-applied", "update-wrong-id":
				if r.Status != "unknown" || !r.Draft.Pending() {
					t.Fatalf("ambiguous result became editable: %s %s", r.Status, r.Message)
				}
				v, err := f.s.UpdateRequirementComment(context.Background(), editPayloadWithRequest(f), nil)
				if err != nil {
					t.Fatal(err)
				}
				if v.(domain.CommentPublishResult).Status != "unknown" {
					t.Fatal("pending update was retried")
				}
				if mode == "update-read-fail" {
					f.s = NewService(f.s.paths, f.s.config, nil)
					f.mode(t, "normal")
					editDraft(t, f, "load", nil)
					v, err = f.s.Execute(context.Background(), "requirements.comments/check", editPayload(f, ""))
					if err != nil {
						t.Fatal(err)
					}
					if v.(domain.CommentPublishResult).Status != "updated" {
						t.Fatal("restart did not reconcile original ID")
					}
				}
			default:
				if r.Status != "updated" || r.CommentID != "original-1" || r.Draft.HasContent() {
					t.Fatalf("not updated: %s %s", r.Status, r.Message)
				}
				if mode == "duplicate" {
					v, err := f.s.UpdateRequirementComment(context.Background(), p, nil)
					if err != nil || v.(domain.CommentPublishResult).Status != "updated" {
						t.Fatal("idempotent result failed", err)
					}
				}
			}
			if u != wantUpdate {
				t.Fatalf("got %d updates, expected %d", u, wantUpdate)
			}
			if mode == "remote-change" && (!r.Draft.Edit.Conflict || upl != 0) {
				t.Fatal("remote conflict uploaded or lost conflict")
			}
			if r.Status == "updated" && assets {
				raw, _ := os.ReadFile(filepath.Join(f.root, "posted"))
				var posted map[string]string
				_ = json.Unmarshal(raw, &posted)
				if strings.Contains(posted["content"], "/download/original-") || strings.Contains(posted["content"], "查看评论附件") {
					t.Fatal("reused old resources or reader decoration")
				}
				if mode == "remove-attachment" && posted["file_token"] != "" {
					t.Fatal("attachment removal was not explicit")
				}
				if !strings.Contains(posted["content"], "中间正文") || !strings.Contains(posted["content"], "`![代码](local.png)`") {
					t.Fatal("source positions/code not preserved")
				}
			}
			if mode == "draft-isolation" {
				loaded, err := f.s.readCommentDraft(creation.Identity, creation.Item)
				if err != nil || loaded.ID != creation.ID || loaded.Revision != creation.Revision || loaded.Edit != nil {
					t.Fatal("edit changed creation draft", err)
				}
			}
			endUpdates, endCreates, _, _ := commentEditCalls(t, f)
			if endUpdates != wantUpdate || endCreates != 0 {
				t.Fatal("repeated update or creation")
			}
		})
	}
}

func editPayloadWithRequest(f *commentFixture) map[string]any {
	p := editPayload(f, "")
	p["requestID"] = commentRandomID()
	return p
}
