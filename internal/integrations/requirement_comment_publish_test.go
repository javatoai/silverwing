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
	"sync"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

type commentFixture struct {
	s      *Service
	config domain.AppConfig
	root   string
	p      map[string]any
	draft  domain.RequirementCommentDraft
}

func buildCommentCLI(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "main.go")
	cli := filepath.Join(root, "meegle")
	if runtime.GOOS == "windows" {
		cli += ".exe"
	}
	if err := os.WriteFile(src, []byte(commentCLISource), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", cli, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %s %v", out, err)
	}
	return cli
}

func newCommentFixture(t *testing.T, cli string) *commentFixture {
	t.Helper()
	root := t.TempDir()
	t.Setenv("SW_COMMENT_FIXTURE", root)
	paths := storage.NewPaths(filepath.Join(root, "home"))
	store := storage.NewConfigStore(paths)
	cfg := domain.DefaultAppConfig()
	cfg.MeegleExecutablePath = &cli
	cfg.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "p", SimpleName: "fixture"}}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	f := &commentFixture{s: NewService(paths, store, nil), config: cfg, root: root, p: map[string]any{"url": "https://project.feishu.cn/fixture/userstory/detail/101"}}
	f.edit(t, "load", nil)
	return f
}

func (f *commentFixture) payload(op string) map[string]any {
	p := map[string]any{"url": f.p["url"], "operation": op, "draftID": f.draft.ID, "revision": f.draft.Revision, "scope": f.draft.Scope, "expectedIdentity": f.draft.Identity}
	return p
}
func (f *commentFixture) edit(t *testing.T, op string, extra map[string]any) {
	t.Helper()
	p := f.payload(op)
	if op == "load" && f.draft.Identity == "" {
		delete(p, "expectedIdentity")
	}
	for k, v := range extra {
		p[k] = v
	}
	v, err := f.s.Execute(context.Background(), "requirements.comment-draft", p)
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	f.draft = v.(domain.RequirementCommentDraft)
}
func (f *commentFixture) mode(t *testing.T, mode string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.root, "mode"), []byte(mode), 0600); err != nil {
		t.Fatal(err)
	}
}
func (f *commentFixture) publish(t *testing.T, progress func(domain.CommentPublishProgress)) (domain.CommentPublishResult, map[string]any) {
	t.Helper()
	p := f.payload("")
	p["requestID"] = commentRandomID()
	v, err := f.s.PublishRequirementComment(context.Background(), p, progress)
	if err != nil {
		t.Fatal(err)
	}
	r := v.(domain.CommentPublishResult)
	f.draft = r.Draft
	return r, p
}
func (f *commentFixture) counts(t *testing.T) (int, int) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.root, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	creates, uploads := 0, 0
	for _, line := range strings.Split(string(raw), "\n") {
		var args []string
		_ = json.Unmarshal([]byte(line), &args)
		if len(args) > 1 && args[0] == "comment" && args[1] == "add" && !strings.Contains(line, "--dry-run") {
			creates++
		}
		if len(args) > 1 && args[0] == "attachment" {
			uploads++
		}
	}
	return creates, uploads
}

func TestUserCommentPublishCLI(t *testing.T) {
	cli := buildCommentCLI(t)
	for _, mode := range []string{"text", "markdown", "multi", "attachment-only", "rewritten", "rewritten-no-id", "upload-fail", "lost-response", "read-fail", "create-fail", "account-change", "config-change", "duplicate", "save-fail", "restart"} {
		t.Run(mode, func(t *testing.T) {
			f := newCommentFixture(t, cli)
			f.edit(t, "save", map[string]any{"content": "# 评论\n\n**中文** [链接](https://example.com)\n\n`<span>`"})
			if mode == "text" {
				f.edit(t, "save", map[string]any{"content": "你好，已处理"})
			}
			if mode == "multi" || mode == "upload-fail" || mode == "account-change" || strings.HasPrefix(mode, "rewritten") {
				for i := 0; i < 2; i++ {
					path := filepath.Join(f.root, string(rune('a'+i))+".png")
					file, e := os.Create(path)
					if e != nil {
						t.Fatal(e)
					}
					_ = png.Encode(file, image.NewRGBA(image.Rect(0, 0, 4, 4)))
					_ = file.Close()
					f.edit(t, "add-image", map[string]any{"sourcePath": path})
					if filepath.Ext(f.draft.Images[len(f.draft.Images)-1].LocalPath) != ".png" {
						t.Fatal("staged image cannot use native preview")
					}
				}
			}
			if mode == "multi" || mode == "attachment-only" {
				path := filepath.Join(f.root, "说明.txt")
				_ = os.WriteFile(path, []byte("attachment"), 0600)
				f.edit(t, "add-attachment", map[string]any{"sourcePath": path})
				if mode == "attachment-only" {
					f.edit(t, "save", map[string]any{"content": ""})
				}
			}
			if n, u := f.counts(t); n != 0 || u != 0 {
				t.Fatal("draft preparation uploaded or published")
			}
			f.mode(t, mode)
			var events []domain.CommentPublishProgress
			progress := func(e domain.CommentPublishProgress) {
				events = append(events, e)
				if mode == "account-change" && e.Stage == "上传图片" {
					_ = os.WriteFile(filepath.Join(f.root, "account"), []byte("other"), 0600)
					// Model the normal app identity refresh after switching login;
					// publishing reuses this known state instead of polling user/me.
					if state, _, err := f.s.RequirementIdentity(context.Background(), f.config); err != nil || state != requirementIdentityAuthenticated {
						t.Fatal("account switch was not observed", err)
					}
				}
				if mode == "config-change" && e.Stage == "发布评论" {
					cfg := f.config
					v := "http://127.0.0.1:9999"
					cfg.CommandProxyURL = &v
					_ = f.s.config.Save(cfg)
				}
				if mode == "save-fail" && e.Stage == "发布评论" {
					dir, _ := f.s.commentDirectory(f.draft)
					_ = os.Rename(filepath.Join(dir, "draft.json"), filepath.Join(dir, "draft.bak"))
					_ = os.Mkdir(filepath.Join(dir, "draft.json"), 0700)
				}
			}
			r, p := f.publish(t, progress)
			switch mode {
			case "upload-fail", "account-change", "config-change":
				if r.Status != "failed" || !r.Draft.HasContent() {
					t.Fatalf("failed operation lost draft: %+v", r)
				}
				if n, _ := f.counts(t); n != 0 {
					t.Fatal("failure published a comment")
				}
			case "create-fail":
				if r.Status != "unknown" || !r.Draft.Pending() {
					t.Fatalf("failed create can replay: %+v", r)
				}
				f.edit(t, "load", nil)
				_, err := f.s.Execute(context.Background(), "requirements.comment-draft", f.payload("save"))
				if err == nil {
					t.Fatal("pending write allowed editing")
				}
				v, err := f.s.PublishRequirementComment(context.Background(), p, nil)
				if err != nil && !strings.Contains(err.Error(), "草稿已变化") {
					t.Fatal(err)
				}
				if v != nil && v.(domain.CommentPublishResult).Status != "unknown" {
					t.Fatal(v)
				}
				if n, _ := f.counts(t); n != 1 {
					t.Fatal("unknown outcome was automatically resent")
				}
				f.edit(t, "allow-retry", map[string]any{"confirmed": true, "attemptID": f.draft.Attempt.ID})
				f.mode(t, "normal")
				r, _ = f.publish(t, nil)
				if r.Status != "published" {
					t.Fatal(r)
				}
			case "read-fail", "restart":
				if r.Status != "unknown" {
					t.Fatalf("expected unknown: %+v", r)
				}
				f.s = NewService(f.s.paths, f.s.config, nil)
				f.mode(t, "normal")
				f.edit(t, "load", nil)
				v, err := f.s.Execute(context.Background(), "requirements.comments/check", f.payload(""))
				if err != nil {
					t.Fatal(err)
				}
				r = v.(domain.CommentPublishResult)
				if r.Status != "published" {
					t.Fatal(r)
				}
			case "save-fail":
				if r.Status != "unknown" {
					t.Fatal(r)
				}
			default:
				if r.Status != "published" || r.Draft.HasContent() {
					t.Fatalf("publish failed: %+v", r)
				}
				v, err := f.s.PublishRequirementComment(context.Background(), p, nil)
				if err != nil || v.(domain.CommentPublishResult).Status != "published" {
					t.Fatalf("duplicate result %v %v", v, err)
				}
				if n, _ := f.counts(t); n != 1 {
					t.Fatalf("duplicate create count %d", n)
				}
			}
			if mode == "multi" {
				_, u := f.counts(t)
				if u != 3 {
					t.Fatalf("wrong uploads %d", u)
				}
				raw, _ := os.ReadFile(filepath.Join(f.root, "posted"))
				var posted map[string]string
				_ = json.Unmarshal(raw, &posted)
				if strings.Count(posted["content"], "<!--token-") != 2 || !strings.Contains(posted["file_token"], "token-13-") || strings.Contains(posted["content"], "silverwing-materials") {
					t.Fatalf("wrong payload %s", raw)
				}
			}
			if len(events) == 0 || events[0].Stage != "核对目标" {
				t.Fatal("missing progress")
			}
		})
	}
	t.Run("concurrent duplicate", func(t *testing.T) {
		f := newCommentFixture(t, cli)
		f.edit(t, "save", map[string]any{"content": "once"})
		p := f.payload("")
		p["requestID"] = commentRandomID()
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := f.s.PublishRequirementComment(context.Background(), p, nil)
				errs <- e
			}()
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		if n, _ := f.counts(t); n != 1 {
			t.Fatal(n)
		}
	})
	t.Run("draft boundaries and restart intent", func(t *testing.T) {
		f := newCommentFixture(t, cli)
		r, _ := f.publish(t, nil)
		if r.Status != "failed" {
			t.Fatal("empty draft published", r)
		}
		f.edit(t, "save", map[string]any{"content": "本地路径 ![image](C:/private/file.png) 不会自动上传"})
		stale := f.payload("save")
		f.edit(t, "save", map[string]any{"content": "new"})
		if _, err := f.s.Execute(context.Background(), "requirements.comment-draft", stale); err == nil {
			t.Fatal("stale save accepted")
		}
		p := f.payload("")
		p["url"] = "https://project.feishu.cn/fixture/userstory/detail/102"
		p["requestID"] = commentRandomID()
		if _, err := f.s.PublishRequirementComment(context.Background(), p, nil); err == nil {
			t.Fatal("different requirement accepted stale draft")
		}
		path := filepath.Join(f.root, "one.txt")
		_ = os.WriteFile(path, []byte("one"), 0600)
		f.edit(t, "add-attachment", map[string]any{"sourcePath": path})
		p = f.payload("add-attachment")
		p["sourcePath"] = path
		if _, err := f.s.Execute(context.Background(), "requirements.comment-draft", p); err == nil {
			t.Fatal("second attachment accepted")
		}
		f.draft.Attempt = &domain.CommentPublishAttempt{ID: commentRandomID(), State: "publishing", Body: f.draft.Content}
		if err := f.s.saveCommentDraft(&f.draft); err != nil {
			t.Fatal(err)
		}
		f.s = NewService(f.s.paths, f.s.config, nil)
		f.edit(t, "load", nil)
		if !f.draft.Pending() || f.draft.Attempt.State != "unknown" {
			t.Fatal("restart lost durable intent")
		}
		if n, u := f.counts(t); n != 0 || u != 0 {
			t.Fatal("restart resumed remote writes")
		}
		p = f.payload("allow-retry")
		p["attemptID"] = f.draft.Attempt.ID
		if _, err := f.s.Execute(context.Background(), "requirements.comment-draft", p); err == nil {
			t.Fatal("unconfirmed retry accepted")
		}
	})
	t.Run("uploaded receipts reused only for unchanged files", func(t *testing.T) {
		f := newCommentFixture(t, cli)
		f.edit(t, "save", map[string]any{"content": "with files"})
		file := filepath.Join(f.root, "image.png")
		out, _ := os.Create(file)
		_ = png.Encode(out, image.NewRGBA(image.Rect(0, 0, 4, 4)))
		_ = out.Close()
		f.edit(t, "add-image", map[string]any{"sourcePath": file})
		attachment := filepath.Join(f.root, "attachment.txt")
		_ = os.WriteFile(attachment, []byte("one"), 0600)
		f.edit(t, "add-attachment", map[string]any{"sourcePath": attachment})
		f.mode(t, "partial-upload")
		r, _ := f.publish(t, nil)
		if r.Status != "failed" || r.Draft.Images[0].Upload == nil || r.Draft.Attachment.Upload != nil {
			t.Fatal(r)
		}
		f.mode(t, "normal")
		r, _ = f.publish(t, nil)
		if r.Status != "published" {
			t.Fatal(r)
		}
		if n, u := f.counts(t); n != 1 || u != 3 {
			t.Fatalf("upload replay %d %d", n, u)
		}
		// A fresh editor and reader see the posted list without another create.
		v, err := f.s.Execute(context.Background(), "requirements.comments", map[string]any{"url": f.p["url"], "expectedIdentity": f.draft.Identity})
		if err != nil {
			t.Fatal(err)
		}
		if len(v.(RequirementCommentsReadResult).Comments) != 1 {
			t.Fatal(v)
		}
	})
	t.Run("staged file changed and pre-create save failure", func(t *testing.T) {
		for _, mode := range []string{"changed-file", "save-before-create", "cancel"} {
			t.Run(mode, func(t *testing.T) {
				f := newCommentFixture(t, cli)
				f.edit(t, "save", map[string]any{"content": "preserve"})
				if mode == "changed-file" {
					path := filepath.Join(f.root, "attachment.txt")
					_ = os.WriteFile(path, []byte("one"), 0600)
					f.edit(t, "add-attachment", map[string]any{"sourcePath": path})
					_ = os.WriteFile(f.draft.Attachment.LocalPath, []byte("two"), 0600)
				}
				p := f.payload("")
				p["requestID"] = commentRandomID()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				v, err := f.s.PublishRequirementComment(ctx, p, func(e domain.CommentPublishProgress) {
					if mode == "save-before-create" && e.Stage == "核对目标" {
						dir, _ := f.s.commentDirectory(f.draft)
						_ = os.Rename(filepath.Join(dir, "draft.json"), filepath.Join(dir, "draft.bak"))
						_ = os.Mkdir(filepath.Join(dir, "draft.json"), 0700)
					}
					if mode == "cancel" && e.Stage == "发布评论" {
						cancel()
					}
				})
				if err != nil {
					t.Fatal(err)
				}
				r := v.(domain.CommentPublishResult)
				if r.Status != "failed" || !r.Draft.HasContent() {
					t.Fatal(r)
				}
				if n, _ := f.counts(t); n != 0 {
					t.Fatal("invalid files/save/cancel started a create")
				}
			})
		}
	})
}

const commentCLISource = `package main
import("encoding/json";"os";"fmt";"path/filepath";"strings";"time";"regexp")
func main(){
 root:=os.Getenv("SW_COMMENT_FIXTURE"); a:=os.Args[1:]; b,_:=json.Marshal(a);f,_:=os.OpenFile(filepath.Join(root,"calls"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);f.Write(append(b,'\n'));f.Close()
 emit:=func(v any){b,_:=json.Marshal(v);fmt.Println(string(b))}; flag:=func(k string)string{for i,v:=range a {if v==k&&i+1<len(a){return a[i+1]}};return ""}
 fail:=func(s string){fmt.Fprintln(os.Stderr,s);os.Exit(3)}; modeRaw,_:=os.ReadFile(filepath.Join(root,"mode"));mode:=string(modeRaw);acctRaw,_:=os.ReadFile(filepath.Join(root,"account"));acct:="user-1";if len(acctRaw)>0 {acct=string(acctRaw)}
 _,onceErr:=os.Stat(filepath.Join(root,"single-use-tokens"));singleUse:=onceErr==nil
 next:=func(name string)int{path:=filepath.Join(root,name);raw,_:=os.ReadFile(path);n:=0;fmt.Sscan(string(raw),&n);n++;os.WriteFile(path,[]byte(fmt.Sprint(n)),0600);return n}
 if len(a)==0 {fail("no command")};if a[0]=="--version" {fmt.Println("fixture");return}
 if a[0]=="config" {fmt.Println("fixture-profile");return};if a[0]=="auth" {emit(map[string]any{"authenticated":true,"host":"project.feishu.cn"});return};if a[0]=="user" {emit(map[string]any{"user_key":acct,"name":"测试账号"});return}
 if a[0]=="project" {emit(map[string]any{"projects":[]any{map[string]string{"name":"Fixture","project_key":"p","simple_name":"fixture"}}});return}
 if a[0]=="workitem" {emit(map[string]any{"work_item_id":"101","project_key":"p","work_item_type":"story"});return}
 if a[0]=="attachment" {r:=flag("--resource-type");if mode=="upload-fail"||(mode=="partial-upload"&&r=="13") {fail("upload failed")};token:="token-"+r+"-"+filepath.Base(a[2]);if singleUse{token+=fmt.Sprintf("-%d",next("upload-number"))};emit(map[string]any{"file_token":token,"file_url":"https://project.feishu.cn/files/"+token});return}
 if a[0]=="comment"&&a[1]=="add" {
   raw,e:=os.ReadFile(strings.TrimPrefix(flag("--params"),"@"));if e!=nil {fail(e.Error())};var p map[string]any;json.Unmarshal(raw,&p)
   if flag("--project-key")!="p"||flag("--work-item-id")!="101"||flag("--action")!="create" {fail("wrong target/action")}
   dry:=false;for _,v:=range a {if v=="--dry-run" {dry=true}}
   if dry {p["project_key"]="p";p["work_item_id"]="101";p["action"]="create";emit(map[string]any{"dry_run":true,"tool":"add_comment","params":p});return}
   if mode=="create-fail" {fail("fixture request feedback: request outcome unknown")}
   if mode=="create-business-fail" {emit(map[string]any{"success":false,"error":map[string]string{"message":"fixture request feedback: rejected"}});return}
   commentID:="comment-1"
   if singleUse {
     usedRaw,_:=os.ReadFile(filepath.Join(root,"used-tokens"));used:=map[string]bool{};json.Unmarshal(usedRaw,&used)
     tokens:=[]string{};for _,m:=range regexp.MustCompile("<!--([^>]+)-->").FindAllStringSubmatch(p["content"].(string),-1){tokens=append(tokens,m[1])};if token,ok:=p["file_token"].(string);ok{tokens=append(tokens,token)}
     for _,token:=range tokens {if used[token]{emit(map[string]any{"data":nil,"error":map[string]any{"code":"SERVER_CALL_FAILED","message":"error=ErrWrongParameter,message=wrong parameter,invalid file token,retriable=false","retryable":true},"meta":map[string]any{}});os.Exit(3)}}
     for _,token:=range tokens{used[token]=true};saved,_:=json.Marshal(used);os.WriteFile(filepath.Join(root,"used-tokens"),saved,0600);commentID=fmt.Sprintf("comment-%d",next("create-number"))
   }
   os.WriteFile(filepath.Join(root,"posted"),raw,0600)
   body:=p["content"];if strings.HasPrefix(mode,"rewritten") {body=regexp.MustCompile("!\\[[^\\]]*\\]\\([^)]+\\)<!--([^>]+)-->").ReplaceAllString(body.(string),"![](https://project.feishu.cn/goapi/v5/platform/file/stream/download/$1)")}
   c:=map[string]any{"comment_id":commentID,"content":body,"creator":map[string]string{"user_key":acct,"name":"测试账号"},"created_at":time.Now().UTC().Format(time.RFC3339)};if token,ok:=p["file_token"].(string);ok {c["file_url"]="https://project.feishu.cn/files/"+token};out,_:=json.Marshal(c);os.WriteFile(filepath.Join(root,"comment"),out,0600)
   if mode=="lost-response" {fail("transport lost after create")};if mode=="rewritten-no-id" {emit(map[string]bool{"success":true});return};emit(map[string]string{"comment_id":commentID});return
 }
 if a[0]=="comment"&&a[1]=="list" {raw,e:=os.ReadFile(filepath.Join(root,"comment"));rows:=[]any{};if e==nil {if mode=="read-fail"||mode=="restart" {fail("readback unavailable")};if mode=="read-account-change" {os.WriteFile(filepath.Join(root,"account"),[]byte("other"),0600)};var c any;json.Unmarshal(raw,&c);rows=append(rows,c)};emit(map[string]any{"comments":rows,"pagination":map[string]any{"page_num":1,"has_more":false,"total":len(rows)}});return}
 fail("unexpected command "+strings.Join(a," "))
}
`

func TestCommentUploadReceiptsAndLocalPaths(t *testing.T) {
	for _, raw := range []string{`{}`, `{"file_token":"x"}`, `{"file_token":"x--><script>","file_url":"https://x/a"}`, `{"file_token":"x","file_url":"file:///a"}`} {
		if _, err := parseCommentUpload(raw, true); err == nil {
			t.Fatal(raw)
		}
	}
	d := domain.RequirementCommentDraft{Content: "![local](C:/private/test.png)\n\n`![code](a.png)`", Images: []domain.CommentDraftAsset{{ID: "1", Name: "图[片]"}}}
	body := d.Markdown(func(a domain.CommentDraftAsset) (string, string) { return "https://example.com/a", "token" })
	if !strings.Contains(body, "C:/private/test.png") || !strings.Contains(body, ")<!--token-->") || strings.Count(body, "<!--token-->") != 1 {
		t.Fatal(body)
	}
}
