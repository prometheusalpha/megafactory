# megafactory — Java IDE plan

Java IDE native viết bằng Go trên MyGo (`github.com/egoist/mygo` v0.8.3, native UI) và jdtls.

## Quyết định đã chốt

| # | Vấn đề | Quyết định |
|---|---|---|
| 1 | Platform | macOS trước; Linux/Windows sau |
| 2 | jdtls + java-debug | App tự tải về app data dir ở lần chạy đầu (cần JDK 21+ để chạy jdtls) |
| 3 | Git | Gọi `git` CLI (blame/diff/checkout khớp hành vi git thật, không thêm dependency) |
| 4 | Blame lề trái | Bản đầu tắt soft wrap khi bật cột blame |
| 5 | XML | Chỉ highlight, không LemMinX |
| 6 | AI provider | OpenCode Go — OpenAI-compatible, base `https://opencode.ai/zen/go/v1`, dùng `/chat/completions` (không có FIM `suffix`) |
| 7 | Spring Boot | Không cần gì đặc biệt để chạy/debug; chỉ thêm config type nhẹ (profiles, env) + highlight `.properties` |
| 8 | Mở project từ lịch sử | Thay project trong window hiện tại |
| 9 | Theme | Mặc định là màu riêng của app; theme khác chọn trong Settings, embed từ repo gốc (bắt đầu với Catppuccin), không tự đọc theme Ghostty. Có System/Light/Dark |
| 10 | File bị ignore | Không ẩn, hiện nhạt theo `git check-ignore` (tôn trọng `.gitignore`) |
| 11 | Focus khi click file trong tree | Tạm thời: editor lấy focus; phím tắt tree với file chỉ dùng khi đi bằng phím mũi tên |

## Kiến trúc

```
main.go               app shell: sidebar | tabs+editor | bottom panel (Run/Debug/Terminal/Git)
internal/workspace/   fs tree, file ops, fs watch
internal/lsp/         JSON-RPC stdio client → jdtls
internal/tabs/        documents: editor.Buffer + editor.State per file, dirty/save
internal/git/         `git` CLI wrapper
internal/runcfg/      run configurations (.megafactory/run.json)
internal/runner/      process + console output
internal/dap/         DAP client → java-debug
internal/build/       maven/gradle sync + goals/tasks
internal/ai/          OpenAI-compatible client cho inline suggestion
internal/recent/      lịch sử project
internal/setup/       tải + verify jdtls, java-debug
```

Settings (`settings.json`) và state window (`state.json`) nằm ở `~/Library/Application Support/megafactory/`.

Nguyên tắc: mọi I/O (jdtls, git, AI, process) chạy ngoài UI thread, đẩy kết quả về bằng `Window.Update`, bỏ kết quả của buffer version cũ.

## Phase 1 — Khung app + Explorer

- [ ] 1.1 Layout 3 vùng bằng `ui.Split`: sidebar, editor area, bottom panel (ẩn/hiện được)
- [x] 1.2 Mở folder (dialog), lưu state window
- [x] 1.3 File tree bằng `ui.TreeBase`, lazy-load thư mục, file bị git ignore (và `.git`) hiện nhạt
- [x] 1.4 Fs watch: tree tự cập nhật khi file đổi ngoài app (`mygo.WatchFiles` hints, bỏ qua `.git`)
- [x] 1.5 Context menu tree: New File, New Folder, Rename (inline), Delete (confirm → Thùng rác), Copy, Cut, Paste, Duplicate, Copy Path, Reveal in Finder
- [x] 1.6 Tabs editor: mở/đóng, đánh dấu dirty, Cmd+S, Cmd+W, cảnh báo đóng tab chưa lưu (cả khi đóng window); tab chưa sửa tự đọc lại khi file đổi ngoài app
- [ ] 1.7 Context menu tab: Close, Close Others, Close to the Right, Close All, Copy Path, Reveal in Tree
- [ ] 1.8 Highlight Java/XML/JSON/Markdown bằng `editor.LanguageFor`; tokenizer cho `.properties`
- [ ] 1.9 Recent projects: lưu danh sách, palette Cmd+Shift+O, chọn → thay project trong window hiện tại (đóng tab, hỏi lưu file dirty)
- [ ] 1.10 Test bằng `ui.NewTester` cho tree ops + tabs

## Phase 2 — jdtls

- [ ] 2.1 `internal/setup`: tải jdtls + java-debug jar về app data dir, verify checksum, hiện tiến độ
- [ ] 2.2 Kiểm tra JDK 21+ (`JAVA_HOME` / `/usr/libexec/java_home`), báo lỗi rõ nếu thiếu
- [ ] 2.3 LSP client: spawn jdtls, workspace data dir riêng theo project, `initialize` (bundles = java-debug)
- [ ] 2.4 Sync document: `didOpen` / `didChange` (incremental) / `didSave` / `didClose`
- [ ] 2.5 Diagnostics → `SetDiagnostics` (gạch sóng + hover + mark scrollbar)
- [ ] 2.6 Toggle inline error: message cuối dòng qua `SetInlays`
- [ ] 2.7 Completion (`OnCompletion`, snippet), hover (`OnHover`), go to definition (`OnDefinition`, mở tab file đích)
- [ ] 2.8 Status bar: trạng thái jdtls (starting / importing / ready / error)
- [ ] 2.9 Maven/Gradle import qua jdtls; nút Sync → `java.projectConfiguration.update`
- [ ] 2.10 Tự gợi ý Sync khi `pom.xml` / `build.gradle(.kts)` thay đổi
- [ ] 2.11 Panel Maven goals / Gradle tasks (chạy qua runner ở Phase 4)

## Phase 3 — Git

- [ ] 3.1 `internal/git`: wrapper `git` CLI, detect repo, branch hiện tại trên status bar
- [ ] 3.2 Gutter changes: `DiffBase` = `git show HEAD:<path>`, cập nhật sau commit/checkout
- [ ] 3.3 Inline blame (bật/tắt): `git blame --porcelain`, inlay cuối dòng có con trỏ (tác giả, thời gian, summary)
- [ ] 3.4 Blame lề trái (bật/tắt): cột tự vẽ cạnh editor, đồng bộ qua `ScrollY`/`LineTop`; tắt soft wrap khi bật
- [ ] 3.5 Panel Changes: danh sách file modified/added/deleted/untracked
- [ ] 3.6 Diff view (`editor.DiffView`, split/unified) cho file trong Changes và "Compare with HEAD" từ tab
- [ ] 3.7 Branch picker có search: local, remote, tag
- [ ] 3.8 Checkout: branch local, remote (tạo tracking branch), tag/commit (detached), tạo branch mới từ HEAD
- [ ] 3.9 Xử lý working tree dirty khi checkout: báo lỗi git rõ ràng, gợi ý stash

## Phase 4 — Terminal + Run

- [ ] 4.1 Terminal bằng `plugins/terminal` (libghostty-vt), cwd = root project
- [ ] 4.2 Nhiều tab terminal, đóng/mở, rename
- [ ] 4.3 Run configurations: model + lưu `.megafactory/run.json`
- [ ] 4.4 Config type **Application**: main class, module, program args, VM options, env vars, working dir, JDK
- [ ] 4.5 Config type **Spring Boot**: như Application + active profiles (`-Dspring.profiles.active`)
- [ ] 4.6 Config type **Maven goal** / **Gradle task** / **Shell**
- [ ] 4.7 Dialog Edit Configurations (thêm, sửa, xoá, nhân bản) + dropdown chọn config trên toolbar
- [ ] 4.8 Resolve main class + classpath qua java-debug (`vscode.java.resolveMainClass`, `vscode.java.resolveClasspath`)
- [ ] 4.9 Tự tạo config từ gutter/context menu "Run 'Main'" trên class có `main` hoặc `@SpringBootApplication`
- [ ] 4.10 Runner: start/stop/rerun, console stdout/stderr có màu, link `File.java:123` trong stack trace mở file

## Phase 5 — Debug

- [ ] 5.1 Breakpoints: `OnGutterClick` + `GutterMarks`, lưu theo project, bám dòng khi edit
- [ ] 5.2 DAP client: `vscode.java.startDebugSession` → port → kết nối TCP, `launch` theo run config
- [ ] 5.3 Dừng tại breakpoint: mở file, tô dòng hiện tại (`LineBackgrounds`)
- [ ] 5.4 Toolbar: Continue, Step Over, Step Into, Step Out, Pause, Stop, Restart
- [ ] 5.5 Panel Call Stack (threads + frames, click để nhảy tới frame)
- [ ] 5.6 Panel Variables: cây lazy-load (`variables` theo `variablesReference`)
- [ ] 5.7 Watch: thêm/sửa/xoá biểu thức, `evaluate` context `watch` mỗi lần dừng
- [ ] 5.8 Debug console: output + evaluate biểu thức tại frame hiện tại

## Phase 6 — AI inline suggestion

- [ ] 6.1 Settings: base URL (mặc định OpenCode Go), API key (lưu bằng MyGo secrets/Keychain), model, bật/tắt
- [ ] 6.2 Lấy danh sách model từ `/v1/models`, chỉ cho chọn model dùng `/chat/completions`
- [ ] 6.3 Prompt kiểu FIM qua chat: gửi prefix + suffix quanh con trỏ, yêu cầu chỉ trả phần chèn giữa; hậu xử lý bỏ code fence, cắt phần trùng suffix
- [ ] 6.4 Trigger: debounce ~300ms sau khi gõ, huỷ request cũ, bỏ kết quả nếu buffer version đã đổi
- [ ] 6.5 Hiện bằng `SetSuggestion`, Tab nhận, Esc bỏ
- [ ] 6.6 Giới hạn context (số dòng prefix/suffix) + timeout, báo lỗi auth/quota trên status bar

## Phase 7 — Hoàn thiện

- [ ] 7.1 Markdown preview cạnh editor bằng `ui/markdown`, cập nhật theo lúc gõ
- [ ] 7.2 JSON: format document, báo lỗi parse thành diagnostic
- [ ] 7.3 Rà soát context menu editor (`ContextMenu`): Go to Definition, Run/Debug, Compare with HEAD, Copy Path
- [ ] 7.4 Shortcut tổng hợp (Cmd+P mở file nhanh, Cmd+Shift+O project, …)
- [ ] 7.5 `go tool mygo vet .`, `CGO_ENABLED=0 go test ./...`, race test cho shared state
- [ ] 7.6 Build + đóng gói app macOS bằng `mygo` CLI

## Rủi ro đã biết

- `editor.Inlay` chỉ có `{Offset, Text}` → blame và error inline cùng một màu.
- Editor không có API custom gutter → cột blame tự vẽ, khó khớp với fold/soft wrap.
- OpenCode Go không có endpoint FIM → chất lượng suggestion phụ thuộc prompt; model list thay đổi thường xuyên.
