package gui

// uiLocalizations holds the ja/ko/zh-TW forms of every log, error and status
// string the Go side pushes to the GUI. The key is the English text from the
// T(zh, en) call, which is stable and unique across all call sites. Keeping
// the translations in one table means the 70+ call sites stay untouched and
// every string can be reviewed in one place.
//
// zh-CN needs no entry: T() returns its Chinese argument verbatim. ja/ko fall
// back to English and zh-TW falls back to the Chinese argument when an entry
// is missing, so a new message can never render blank.
var uiLocalizations = map[string]struct{ ja, ko, tw string }{
	"① Check the panel":             {"① パネル本体を確認", "① 패널 본체 확인", "① 檢查面板本體"},
	"WWEmote ① One-click setup -- ": {"WWEmote ① ワンクリック設定 —— ", "WWEmote ① 원클릭 구성 —— ", "WWEmote ① 一鍵設定 —— "},
	"Output is written only to this panel's user/ folder; other mods under Mods/ are not touched.": {"出力はこのパネルの user/ フォルダーにのみ書き込まれ、Mods/ 内の他の mod には触れません。", "출력은 이 패널의 user/ 폴더에만 기록되며 Mods/ 아래의 다른 mod는 건드리지 않습니다.", "輸出僅寫入本面板的 user/ 資料夾；Mods/ 下其他 mod 一個位元組都不動。"},
	"Cannot read the exclusion rules in d3dx.ini":                                                  {"d3dx.ini の除外ルールを読み取れません", "d3dx.ini의 제외 규칙을 읽을 수 없습니다", "無法讀取 d3dx.ini 中的排除規則"},
	"Restart WWEmoteTool.exe and configure again.":                                                 {"WWEmoteTool.exe を再起動してから、もう一度設定してください。", "WWEmoteTool.exe를 다시 시작한 뒤 다시 구성하세요.", "重新啟動 WWEmoteTool.exe 再點一次設定。"},
	"Cannot write to the panel's user folder":                                                      {"パネルの user フォルダーに書き込めません", "패널의 user 폴더에 쓸 수 없습니다", "無法寫入面板的 user 資料夾"},
	"Cannot read the character data":                                                               {"キャラクターデータを読み取れません", "캐릭터 데이터를 읽을 수 없습니다", "角色資料讀不出來"},
	"  Panel: ":                                                                                    {"  パネル: ", "  패널: ", "  面板: "},
	"  Exclusions: %s (%d rules)":                                                                  {"  除外: %s (%d 件)", "  제외: %s (%d개)", "  排除規則: %s（%d 條）"},
	"  Option: DISABLED_ mods are ignored":                                                         {"  オプション: DISABLED_ の mod を無視", "  옵션: DISABLED_ mod 무시", "  選項: 已忽略 DISABLED_ 狀態的 mod"},
	"  Option: stale config files will be removed":                                                 {"  オプション: 古い設定ファイルを削除", "  옵션: 오래된 설정 파일 제거", "  選項: 將刪除失效設定檔"},
	"① Panel is in place":                                                                          {"① パネル本体の準備完了", "① 패널 준비 완료", "① 面板本體已就位"},
	"② Scan and write the user layer":                                                              {"② ユーザー層をスキャンして書き込み", "② 사용자 계층 스캔 및 기록", "② 掃描並寫入使用者層"},
	"Cannot read the chosen folder":                                                                {"選択したフォルダーを読み取れません", "선택한 폴더를 읽을 수 없습니다", "讀不了所選資料夾"},
	"Check the path and that nothing else is using it, then configure again.":                      {"パスを確認し、他のプログラムが使用していないことを確認してから、もう一度設定してください。", "경로를 확인하고 다른 프로그램이 사용 중이 아닌지 확인한 뒤 다시 구성하세요.", "確認路徑無誤、未被其他程式佔用，再點一次設定。"},
	"  Scope: scanning only ":                                                                      {"  範囲: スキャン対象は ", "  범위: 스캔 대상은 ", "  範圍: 僅掃描 "},
	"Sets configured outside this scope are kept; cleanup is limited to this scope.":               {"この範囲外で設定済みのセットは保持され、削除もこの範囲内に限定されます。", "이 범위 밖에서 구성된 세트는 유지되며 정리도 이 범위 내로 제한됩니다.", "範圍外已設定的套保持原樣；清理也僅限本範圍。"},
	"  Indexed %d ini files":                                                                       {"  %d 個の ini をインデックス", "  ini %d개 색인", "  已索引 %d 個 ini"},
	"  Skipped %d folder(s) ending in _skipemote (they still work in the game, only expression setup is skipped)": {"  _skipemote で終わるフォルダーを %d 個スキップしました（ゲーム内では引き続き有効、表情設定のみ対象外）", "  _skipemote로 끝나는 폴더 %d개를 건너뛰었습니다 (게임에서는 계속 작동하며 표정 설정만 제외)", "  已略過 %d 個以 _skipemote 結尾的資料夾（這些 mod 在遊戲中仍生效，僅不配置表情）"},
	"There is not a single ini in that Mods folder":                                                               {"その Mods フォルダーに ini が一つもありません", "해당 Mods 폴더에 ini가 하나도 없습니다", "該 Mods 資料夾中一個 ini 都沒有"},
	"Make sure you picked the Mods folder under WWMI.":                                                            {"WWMI 配下の Mods フォルダーを選択したか確認してください。", "WWMI 아래의 Mods 폴더를 선택했는지 확인하세요.", "請確認選的是 WWMI 下面的那個 Mods 資料夾。"},
	"%d ini files claim to be %s mods but do not match":                                                           {"%d 個の ini が %s の mod を名乗っていますが内容が一致しません", "%d개의 ini가 %s mod라고 하지만 내용이 일치하지 않습니다", "%d 個 ini 聲稱是 %s 的 mod，但內容對不上"},
	"Cannot read the old config for %s":                                                                           {"%s の古い設定を読み取れません", "%s의 이전 설정을 읽을 수 없습니다", "%s 的舊設定讀不出來"},
	"Cannot write the delta data for %s":                                                                          {"%s の差分データを書き込めません", "%s의 델타 데이터를 쓸 수 없습니다", "寫不進 %s 的適配資料"},
	"  -- %s: scoped mods removed, their config pruned":                                                           {"  -- %s: 範囲内の mod が削除され設定も整理しました", "  -- %s: 범위 내 mod가 제거되어 설정도 정리했습니다", "  -- %s: 範圍內 mod 已移除，已清理其設定"},
	"  -- %s: all scoped mods removed, config deleted":                                                            {"  -- %s: 範囲内の mod がすべて削除され設定も削除しました", "  -- %s: 범위 내 mod가 모두 제거되어 설정도 삭제했습니다", "  -- %s: 範圍內 mod 已全部移除，已刪除設定"},
	"  -- %s: not installed, skipped":                                                                             {"  -- %s: 未インストールのためスキップ", "  -- %s: 설치되지 않아 건너뜀", "  -- %s: 未安裝，跳過"},
	"  -- %d set(s) not configured; see the ! lines above for the failed mods.":                                   {"  -- %d セット未設定。失敗した mod は上の ! 行を参照。", "  -- %d개 세트 미설정. 실패한 mod는 위 ! 줄 참조.", "  -- %d 套未設定，失敗 mod 見上方 ! 行。"},
	"  -- %s: configured mod was renamed or is missing, skipping expression bindings":                             {"  -- %s: 設定済み mod が改名済みまたは見つからないため、表情バインドをスキップ", "  -- %s: 구성된 mod가 이름이 바뀌었거나 없어 표현 바인딩을 건너뜀", "  -- %s: 已設定的 mod 已改名或缺失，略過表情綁定"},
	"  -- %s: mod marked _skipemote, expression setup skipped as requested":                                       {"  -- %s: mod に _skipemote が付いているため、指示どおり表情設定をスキップ", "  -- %s: mod에 _skipemote가 있어 요청대로 표정 설정을 건너뜀", "  -- %s: mod 已標記 _skipemote，依要求略過表情設定"},
	"%s mod data is inconsistent; skipped":                                                                        {"%s の mod データに不整合があるためスキップ", "%s mod 데이터가 일치하지 않아 건너뜀", "%s 的 mod 資料有問題，跳過生成"},
	"%s: this mod format (finished shape keys) is not supported yet":                                              {"%s: この mod 形式（ベイク済みシェイプキー）はまだ対応していません", "%s: 이 mod 형식(베이크된 셰이프 키)은 아직 지원되지 않습니다", "%s：暫不支援此格式的 mod（自帶烘焙形態鍵）"},
	"No usable delta could be baked for %s":                                                                       {"%s の mod から表情差分を生成できませんでした", "%s의 mod에서 사용할 수 있는 표정 차분을 만들 수 없습니다", "%s 的 mod 無法生成表情差量"},
	"  OK %s: %d sets, %d bytes":                                                                                  {"  OK %s: %d セット, %d バイト", "  OK %s: %d세트, %d바이트", "  OK %s: %d 套, %d 位元組"},
	"  Removed %d stale config files":                                                                             {"  %d 個の古い設定ファイルを削除", "  오래된 설정 파일 %d개 제거", "  已刪除 %d 個失效設定檔"},
	"  Stale check: nothing to remove":                                                                            {"  古い設定のチェック: 削除対象なし", "  오래된 설정 확인: 제거할 항목 없음", "  失效設定檢查: 無需刪除"},
	"The delta data just written cannot be read back":                                                             {"書き込んだばかりの差分データを読み戻せません", "방금 쓴 델타 데이터를 다시 읽을 수 없습니다", "剛寫好的適配資料讀不回來"},
	"Cannot write the config file the game reads":                                                                 {"ゲームが読み込む設定ファイルを書き込めません", "게임이 읽는 설정 파일을 쓸 수 없습니다", "寫不發給遊戲的設定檔"},
	"Wrote %s (%d bytes)":                                                                                         {"%s を書き出しました (%d バイト)", "%s 기록함 (%d바이트)", "已寫出 %s（%d 位元組）"},
	"  No mod to configure, skipped (expression setup left unchanged).":                                           {"  設定する mod がないためスキップしました (表情設定は変更なし)。", "  구성할 mod가 없어 건너뜀 (표정 설정 변경 없음).", "  沒有需要設定的 mod，已略過（表情設定未變更）。"},
	"② User layer written":                                                                                        {"② ユーザー層を書き込みました", "② 사용자 계층 기록 완료", "② 已寫入使用者層"},
	"③ Verify the change scope":                                                                                   {"③ 変更範囲を確認", "③ 변경 범위 확인", "③ 複核變更範圍"},
	"  OK  Scope: inside user/ only (%d delta buf + 2 ini)":                                                       {"  OK  範囲: user/ 内のみ (差分 buf %d + ini 2)", "  OK  범위: user/ 내부만 (델타 buf %d + ini 2)", "  OK  範圍: 僅在 user/ 內（差量 buf %d + ini 2）"},
	"③ Verified":         {"③ 確認完了", "③ 확인 완료", "③ 複核通過"},
	"No Mods path given": {"Mods パスが入力されていません", "Mods 경로가 입력되지 않았습니다", "未填寫 Mods 路徑"},
	"Click Browse and pick the Mods folder under WWMI.":                           {"Browse を押して WWMI 配下の Mods フォルダーを選択してください。", "Browse를 눌러 WWMI 아래의 Mods 폴더를 선택하세요.", "請點 Browse 選到 WWMI 下面的那個 Mods 資料夾。"},
	"That path cannot be resolved":                                                {"そのパスを解決できません", "해당 경로를 확인할 수 없습니다", "該路徑無法解析"},
	"Try a different folder.":                                                     {"別のフォルダーをお試しください。", "다른 폴더를 사용해 보세요.", "換一個資料夾再試。"},
	"That path does not exist, or is not a folder":                                {"そのパスは存在しないか、フォルダーではありません", "해당 경로가 없거나 폴더가 아닙니다", "該路徑不存在，或者不是資料夾"},
	"Click Browse and pick it again.":                                             {"Browse を押してもう一度選択してください。", "Browse를 눌러 다시 선택하세요.", "請點 Browse 重新選一次。"},
	"There is no d3dx.ini in that folder":                                         {"そのフォルダーに d3dx.ini がありません", "해당 폴더에 d3dx.ini가 없습니다", "該資料夾中沒有 d3dx.ini"},
	"Pick the WWMI root, the Mods folder inside it, or a mod folder inside Mods.": {"WWMI ルート、その中の Mods フォルダー、または Mods 内の mod フォルダーを選択してください。", "WWMI 루트, 그 안의 Mods 폴더, 또는 Mods 안의 mod 폴더를 선택하세요.", "請選 WWMI 根目錄、其下的 Mods 資料夾，或 Mods 內的某個 mod 資料夾。"},
	" is missing; the exclusion rules (exclude_recursive) cannot be read, so disabled mods cannot be told apart. Expected layout: <WWMI>/d3dx.ini + <WWMI>/Mods/; you gave ": {" がありません。除外ルール (exclude_recursive) が読めず、無効化された mod を判別できません。想定する構成: <WWMI>/d3dx.ini + <WWMI>/Mods/。指定されたのは ", "이(가) 없습니다. 제외 규칙(exclude_recursive)을 읽을 수 없어 비활성화된 mod를 구분할 수 없습니다. 예상 구조: <WWMI>/d3dx.ini + <WWMI>/Mods/; 입력값: ", " 不存在；排除規則（exclude_recursive）讀不到，無法判別哪些 mod 已停用。正確配置應為：<WWMI>/d3dx.ini + <WWMI>/Mods/；你給的是 "},
	"Cannot find the panel files (WWEmote.ini / chars)":                  {"パネルファイルが見つかりません (WWEmote.ini / chars)", "패널 파일을 찾을 수 없습니다 (WWEmote.ini / chars)", "找不到面板檔案（WWEmote.ini / chars）"},
	"Put WWEmoteTool.exe inside the unpacked panel folder, then run it.": {"展開したパネルフォルダーに WWEmoteTool.exe を入れてから実行してください。", "압축을 푼 패널 폴더에 WWEmoteTool.exe를 넣은 뒤 실행하세요.", "請把 WWEmoteTool.exe 放進解壓出來的面板資料夾再執行。"},
	"Pick your Mods folder (the Mods under WWMI)":                        {"Mods フォルダーを選択してください (WWMI 配下の Mods)", "Mods 폴더를 선택하세요 (WWMI 아래의 Mods)", "請選擇 Mods 資料夾（WWMI 下面的那個 Mods）"},
	"Setup is already running.\nWait for it to finish, or click Cancel.": {"設定は既に実行中です。\n完了するまで待つか、キャンセルを押してください。", "구성이 이미 실행 중입니다.\n완료될 때까지 기다리거나 취소를 누르세요.", "設定正在進行中。\n等它結束，或點取消。"},
	"Setup failed": {"設定に失敗しました", "구성 실패", "設定失敗"},
	"cancelled":    {"キャンセルしました", "취소됨", "已取消"},
	"aborted":      {"中止しました", "중단됨", "已中止"},
	"Setup complete -- switch back to the game to see it.": {"設定完了 —— ゲームに戻って効果を確認してください。", "구성 완료 —— 게임으로 돌아가 결과를 확인하세요.", "設定完成 —— 回到遊戲即可看到效果。"},
	"(abort requested...)":                                 {"(中止を要求しました...)", "(중단 요청...)", "（已請求中止…）"},
	"...":                                                  {"...", "...", "..."},
	"Stopped at ":                                          {"停止位置: ", "중단 위치: ", "停止於 "},
	"Click Diagnostics at the bottom left and send the report to the author (local paths removed).": {"左下の Diagnostics を押してレポートを作者に送ってください（ローカルパスは除去済み）。", "왼쪽 하단 Diagnostics를 눌러 보고서를 작성자에게 보내세요(로컬 경로는 제거됨).", "請點左下角 Diagnostics 產生報告並發給作者（已去除本機路徑）。"},
}

// localizedText returns the text for the active language. zhText/enText come
// from the call site; ja/ko/zh-TW are looked up by the English text. Missing
// entries fall back so a translation gap degrades to a readable language,
// never to an empty string.
func localizedText(zhText, enText string) string {
	switch savedLanguage() {
	case "zh-CN":
		return zhText
	case "zh-TW":
		if e, ok := uiLocalizations[enText]; ok && e.tw != "" {
			return e.tw
		}
		return zhText
	case "ja":
		if e, ok := uiLocalizations[enText]; ok && e.ja != "" {
			return e.ja
		}
		return enText
	case "ko":
		if e, ok := uiLocalizations[enText]; ok && e.ko != "" {
			return e.ko
		}
		return enText
	default:
		return enText
	}
}
