# megafactory — Java IDE plan

Java IDE native viết bằng Go trên MyGo (`github.com/egoist/mygo` v0.8.3, native UI) và jdtls.

## Quyết định đã chốt

| # | Vấn đề | Quyết định |
|---|---|---|
| 1 | Platform | macOS trước; Linux/Windows sau |
| 2 | jdtls + java-debug | App tự tải về app data dir ở lần chạy đầu (jdtls 1.63.0 cần JDK 25 để chạy; code của project vẫn phân tích theo level của nó, vd 21, qua `java.configuration.runtimes`) |
| 3 | Git | Gọi `git` CLI (blame/diff/checkout khớp hành vi git thật, không thêm dependency) |
| 4 | Blame lề trái | Bản đầu tắt soft wrap khi bật cột blame |
| 5 | XML | Chỉ highlight, không LemMinX |
| 6 | AI provider | OpenCode Go — OpenAI-compatible, base `https://opencode.ai/zen/go/v1`, dùng `/chat/completions` (không có FIM `suffix`) |
| 7 | Spring Boot | Không cần gì đặc biệt để chạy/debug; chỉ thêm config type nhẹ (profiles, env) + highlight `.properties` |
| 8 | Mở project từ lịch sử | Thay project trong window hiện tại |
| 9 | Theme | Mặc định là màu riêng của app; theme khác chọn trong Settings, embed từ repo gốc (bắt đầu với Catppuccin), không tự đọc theme Ghostty. Có System/Light/Dark |
| 10 | File bị ignore | Không ẩn, hiện nhạt theo `git check-ignore` (tôn trọng `.gitignore`) |
| 11 | Focus khi click file trong tree | Tạm thời: editor lấy focus; phím tắt tree với file chỉ dùng khi đi bằng phím mũi tên |
| 12 | Breakpoint lưu ở đâu | `.idea/megafactory_breakpoints.json`, dòng tính từ 1 như giao thức; sửa text thì dời breakpoint theo dòng |
| 13 | Output của chương trình đang debug | `console: internalConsole` → ra Debug console của panel, không mở terminal cho java-debug (adapter không hỏi IDE mở terminal) |
| 14 | Restart | java-debug 0.53.2 không có request `restart` của DAP → Restart = stop rồi chạy lại đúng config đó |

## Kiến trúc

```
main.go               composition root: đọc settings + state, mở window, đưa app cái window và desktop nó cần; watch fs
internal/app/         cả app: sidebar | tabs+editor | bottom panel (Run/Debug/Terminal/Git), tree, git, java, run, settings, theme
internal/app/assets/  icons, fonts, theme .conf (embed theo package)
internal/workspace/   fs tree, file ops, fs watch
internal/lsp/         JSON-RPC stdio client → jdtls
internal/tabs/        documents: editor.Buffer + editor.State per file, dirty/save
internal/git/         `git` CLI wrapper
internal/runcfg/     run configurations: model, .idea/megafactory_run.json, argv
internal/runner/      process lifetime, stdout/stderr lines, File.java:123 links
internal/javalang/    LSP client → jdtls, resolveMainClass/resolveClasspath
internal/dap/         DAP client → java-debug
internal/build/       maven/gradle sync + goals/tasks
internal/ai/          OpenAI-compatible client cho inline suggestion
internal/recent/      lịch sử project
internal/keymap/      action + phím mặc định + override trong settings.json
internal/search/      Go to File: liệt kê file (git ls-files, fallback walk) + chấm điểm tên kiểu dbx Quick Open; Find in Files: regexp song song theo số core, có cancel
internal/setup/       tải + verify jdtls, java-debug
```

Settings (`settings.json`) và state window (`state.json`) nằm ở `~/Library/Application Support/megafactory/`.

Nguyên tắc: mọi I/O (jdtls, git, AI, process) chạy ngoài UI thread, đẩy kết quả về bằng `Window.Update`, bỏ kết quả của buffer version cũ.

## Phase 1 — Khung app + Explorer

- [x] 1.1 Layout 3 vùng: sidebar, editor area, bottom panel (ẩn/hiện được) — card + divider tự vẽ kiểu IntelliJ islands, không dùng `ui.Split`
- [x] 1.2 Mở folder (dialog), lưu state window
- [x] 1.3 File tree bằng `ui.TreeBase`, lazy-load thư mục, file bị git ignore (và `.git`) hiện nhạt
- [x] 1.4 Fs watch: tree tự cập nhật khi file đổi ngoài app (`mygo.WatchFiles` hints, bỏ qua `.git`)
- [x] 1.5 Context menu tree: New File, New Folder, Rename (inline), Delete (confirm → Thùng rác), Copy, Cut, Paste, Duplicate, Copy Path, Reveal in Finder
- [x] 1.6 Tabs editor: mở/đóng, đánh dấu dirty, Cmd+S, Cmd+W, cảnh báo đóng tab chưa lưu (cả khi đóng window); tab chưa sửa tự đọc lại khi file đổi ngoài app
- [x] 1.7 Context menu tab: Close, Close Others, Close to the Right, Close All, Copy Path, Reveal in Tree
- [x] 1.8 Highlight Java/XML/JSON/Markdown bằng `editor.LanguageFor`; tokenizer cho `.properties` (key, `=`/`:`, escape, dòng nối `\`, `${placeholder}` của Spring)
- [x] 1.9 Recent projects: lưu danh sách, palette Cmd+Shift+O, chọn → thay project trong window hiện tại (đóng tab, hỏi lưu file dirty)
- [x] 1.10 Test bằng `ui.NewTester` cho tree ops + tabs
- [x] 1.11 Go to File (Cmd+P): popup tìm file theo tên, chấm điểm exact/initials/prefix/word-prefix/substring/fuzzy như dbx, highlight ký tự khớp, file ignored không hiện, query rỗng hiện file của các tab đang mở trước
- [x] 1.12 Keymap chỉnh được: Close Tab ⌘W, Close Other Tabs ⌘⇧W, Next/Prev Tab ⌃Tab/⌃⇧Tab, Go to File ⌘P, Find in Files ⌘⇧F; Settings → Keyboard shortcuts (bấm rồi nhấn phím, báo trùng, Reset); menu File dựng lại theo keymap, Close Window không còn phím
- [x] 1.13 Find in Files (⌘⇧F): dialog, toggle Match case / Whole word / Regex, tìm trên đĩa (không thấy sửa chưa lưu), tối đa 500 kết quả, Enter/click mở file và chọn đúng đoạn khớp; lấy chữ đang chọn làm query

## Phase 2 — jdtls

- [x] 2.1 `internal/setup`: tải jdtls + java-debug jar về app data dir, verify checksum, hiện tiến độ (jdtls 1.63.0 milestone, java-debug 0.53.2 từ Open VSX; tự tải lần đầu mở project Java)
- [x] 2.2 `internal/jdk`: tìm JDK 25+ để chạy jdtls (`JAVA_HOME` → `/usr/libexec/java_home` → `java` trên PATH, đọc `release`), báo lỗi rõ nếu thiếu/cũ; `Installed()` liệt kê mọi JDK (8…25) làm runtimes cho project (JDK 21 → `JavaSE-21`)
- [x] 2.2b `setup.JDK`: Temurin 25 pin theo hệ (mac/linux, arm64/amd64), `Find` tìm cả trong `Roots` (thư mục servers của app); 2.8 nối vào app: `Find` báo thiếu JDK 25 thì tải về `servers/` và thêm vào `Roots`
- [x] 2.3 `internal/lsp` (JSON-RPC stdio) + `internal/jdtls`: spawn jdtls, workspace data dir riêng theo project, `initialize` (bundles = java-debug, runtimes). Đã chạy jdtls thật: code Java 21 sạch lỗi, tính năng Java 25 báo lỗi ở project level 21
- [x] 2.4 Sync document: `didOpen` / `didChange` (incremental, một range UTF-16) / `didSave` / `didClose`; xếp hàng tuần tự, gửi ngoài UI thread
- [x] 2.5 Diagnostics → `SetDiagnostics` (gạch sóng + hover + mark scrollbar); panel Problems gộp mọi file, click nhảy đúng dòng
- [x] 2.6 Toggle inline error: message cuối dòng qua `SetInlays`, bật/tắt trong Settings (`inlineErrors`)
- [x] 2.7 Completion (`OnCompletion`, snippet), hover (`OnHover`), go to definition (`OnDefinition`, mở tab file đích)
- [x] 2.8 Status bar: trạng thái jdtls (starting / importing / ready / error), tải jdk 25 vào `servers/` khi máy thiếu
- [x] 2.9 Maven/Gradle import qua jdtls; nút Sync → `workspace/didChangeWatchedFiles` (jdtls 1.63.0 không có lệnh `java.projectConfiguration.update`)
- [x] 2.10 Tự gợi ý Sync khi `pom.xml` / `build.gradle(.kts)` thay đổi
- [x] 2.11 Panel Build liệt kê Maven goals / Gradle tasks (chạy qua runner ở Phase 4)

## Phase 3 — Git

- [x] 3.1 `internal/git`: wrapper `git` CLI, detect repo, branch hiện tại trên status bar
- [x] 3.2 Gutter changes: `DiffBase` = `git show HEAD:<path>`, cập nhật sau commit/checkout
- [x] 3.3 Inline blame (bật/tắt): `git blame --porcelain`, inlay cuối dòng có con trỏ (tác giả, thời gian, summary)
- [x] 3.4 Blame lề trái (bật/tắt): cột tự vẽ cạnh editor, đồng bộ qua `ScrollY`/`LineTop`; tắt soft wrap khi bật
- [x] 3.5 Panel Changes: danh sách file modified/added/deleted/untracked
- [x] 3.6 Diff view (`editor.DiffView`, split/unified) cho file trong Changes và "Compare with HEAD" từ tab
- [x] 3.7 Branch picker có search: local, remote, tag
- [x] 3.8 Checkout: branch local, remote (tạo tracking branch), tag/commit (detached), tạo branch mới từ HEAD
- [x] 3.9 Xử lý working tree dirty khi checkout: báo lỗi git rõ ràng, gợi ý stash

## Phase 4 — Terminal + Run

- [x] 4.1 Terminal bằng `plugins/terminal` (libghostty-vt), cwd = root project
- [x] 4.2 Nhiều tab terminal, đóng/mở, rename
- [x] 4.3 Run configurations: model + lưu `.idea/megafactory_run.json`
- [x] 4.4 Config type **Application**: main class, module, program args, VM options, env vars, working dir, JDK
- [x] 4.5 Config type **Spring Boot**: như Application + active profiles (`-Dspring.profiles.active`)
- [x] 4.6 Config type **Maven goal** / **Gradle task** / **Shell**
- [x] 4.7 Dialog Edit Configurations (thêm, sửa, xoá, nhân bản) + dropdown chọn config trên toolbar
- [x] 4.8 Resolve main class + classpath qua java-debug (`vscode.java.resolveMainClass`, `vscode.java.resolveClasspath`)
- [x] 4.9 Tự tạo config từ gutter/context menu "Run 'Main'" trên class có `main` hoặc `@SpringBootApplication`
- [x] 4.10 Runner: start/stop/rerun, console stdout/stderr có màu, link `File.java:123` trong stack trace mở file

## Phase 5 — Debug

- [x] 5.1 Breakpoints: `OnGutterClick` + `GutterMarks`, lưu theo project, bám dòng khi edit
- [x] 5.2 DAP client: `vscode.java.startDebugSession` → port → kết nối TCP, `launch` theo run config
- [x] 5.3 Dừng tại breakpoint: mở file, tô dòng hiện tại (`LineBackgrounds`)
- [x] 5.4 Toolbar: Continue, Step Over, Step Into, Step Out, Pause, Stop, Restart
- [x] 5.5 Panel Call Stack (threads + frames, click để nhảy tới frame)
- [x] 5.6 Panel Variables: cây lazy-load (`variables` theo `variablesReference`)
- [x] 5.7 Watch: thêm/sửa/xoá biểu thức, `evaluate` context `watch` mỗi lần dừng
- [x] 5.8 Debug console: output + evaluate biểu thức tại frame hiện tại

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
