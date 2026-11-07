# Kế hoạch: ba đường nối từ CLIProxyAPI

Ba pha, làm lần lượt. Mỗi pha xong khi test của pha đó xanh và hành vi cũ giữ nguyên khi cấu hình mới tắt. Không đụng dashboard, plugin, catalog từ xa, Gin, Postgres, Redis, hay fingerprint cloak.

Thứ tự: thinking trước, vì nó đụng mọi provider. Session affinity sau, vì nó chỉ đụng router. Buffer stream sau cùng, vì nó ngồi trên luồng stream đã ổn định.

## Ngoài phạm vi

- Suffix `-high` trên tên model. Trong repo này đó là ID upstream thật (`gemini-3.8-flash-high`, Devin). Không tách suffix khỏi tên model.
- Chữ ký thought của Antigravity (`antigravityRestoreThoughtSignatures`). Đó là sửa lịch sử tool, không phải mức reasoning.
- WebSocket full-duplex và Gemini Interactions.
- Guardian subagent đổi model (`X-Openai-Subagent: guardian`).
- Import auth mới. `internal/importcliproxy` giữ nguyên.

---

## Pha 1 — Thinking chuẩn, rồi mới dịch

### Hiện trạng

Reasoning bị đọc và ghi ở nhiều chỗ, mỗi chỗ một dạng:

- `internal/api/claude_tier_reasoning.go` ghi `reasoning_effort` vào `Raw` khi alias Claude chưa có lựa chọn của client.
- `internal/translator/claudeopenai` đổi `thinking.budget_tokens` ↔ `reasoning_effort`.
- `codexNormalizeReasoning` trong `internal/providers/providers.go` gộp `request.Reasoning`, `body.reasoning` và `reasoning_effort`, rồi ép mặc định `low`.
- `xai.go`, `openai_upstream.go`, `kiro_request.go`, `cursor.go`, `perplexity_web.go` tự đọc `reasoning_effort`.
- `internal/bridge/claude_reasoning.go` đã có nhãn chuẩn: `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`.

`canonical.Request.Reasoning` là `map[string]any`, không có hợp đồng.

### Hợp đồng

Thêm `internal/thinking`. Một struct:

```go
type Config struct {
    Effort       string // none|minimal|low|medium|high|xhigh|max, rỗng = không đặt
    BudgetTokens int    // 0 = không đặt
    Set          bool   // client hoặc alias đã chọn một mức
}
```

`Normalize(request)` chạy một lần, sau khi resolve public model, trước `Execute` và `ExecuteStream`:

1. Đọc, theo thứ tự ưu tiên: `request.Reasoning`, `Raw.reasoning.effort`, `Raw.reasoning_effort`, `Raw.thinking` (type `enabled`/`adaptive` hoặc `budget_tokens` > 0), `generationConfig.thinkingConfig` nếu đã có.
2. Lựa chọn của client thắng alias. Đây là quy tắc sẵn trong `claudeRequestHasReasoningPreference`.
3. Chỉ khi chưa có lựa chọn, alias Claude (`applyClaudeTierReasoningEffort`) điền effort.
4. Ghi kết quả vào `request.Reasoning` với đúng hai khóa `effort` và `budget_tokens`. Bật `Metadata["thinking_normalized"]`.
5. Không xóa `Raw`. Adapter pass-through vẫn thấy body gốc. Adapter nào đã hiểu cờ thì không đọc lại `reasoning_effort` để ghi đè.

`Apply(providerType, upstreamModel, body, cfg)` là chỗ duy nhất biến struct thành JSON upstream:

| Provider | Dạng gửi |
| --- | --- |
| codex | `reasoning.effort` + `summary: auto`. `none` xóa reasoning. Giữ `CodexWireReasoningEffort`. |
| claude | `thinking` với budget khi có, không thì bỏ qua |
| gemini, antigravity | `generationConfig.thinkingConfig` chỉ khi client đặt budget. Không đổi tên model |
| xai | `reasoning.effort` qua `xaiWireReasoningEffort` |
| openai-compatible và phần còn lại | `reasoning_effort` khi effort khác rỗng |

GLM và chỗ đang xóa `reasoning_effort` vẫn xóa sau `Apply`. Mặc định Codex `low` khi client không chọn giữ nguyên, nằm trong nhánh Codex của `Apply`, không nằm trong `Normalize`.

### File

- Thêm `internal/thinking/types.go`, `normalize.go`, `apply.go`, `normalize_test.go`.
- Gọi `Normalize` trong `Server.execute` sau `prepareClientRequest` và trong nhánh WebSocket sau khi request đã parse. Một chỗ trên router (`requestForProvider`) gọi lại là đủ nếu HTTP và WebSocket đều đi qua đó. Chọn router để fallback dùng cùng một config.
- `applyClaudeTierReasoningEffort` chỉ điền config khi `Set == false`, không ghi thẳng vào `Raw`.
- `codexBody` bỏ đoạn tự gộp effort. Gọi `thinking.Apply("codex", ...)`.
- Các adapter còn lại chỉ đổi đoạn đọc `reasoning_effort`. Không viết lại request builder.

### Test

- Client gửi `thinking.budget_tokens`: alias Claude không ghi đè.
- Alias điền effort khi body trống.
- Codex body có `reasoning.effort`, không còn `reasoning_effort`.
- `none` xóa reasoning của Codex.
- Gemini giữ nguyên ID `gemini-3.8-flash-high` và không thêm thinkingConfig khi client không hỏi.
- Request đã normalize không bị adapter ghi effort khác ở lần fallback.

### Xong khi

Mọi request chat, responses, messages đi qua một `Normalize`. Test cũ của Codex, Claude tier và Antigravity vẫn xanh.

---

## Pha 2 — Session affinity cho subagent và rebind

### Hiện trạng

`routing.session-affinity` và TTL đã có. `sessionIDFromRequest` lấy `X-Session-ID`, `X-Opencode-Session`, `prompt_cache_key`, `metadata.user_id`. `preferSession` đưa credential đã gắn lên đầu. `bindSession` ghi đè binding mỗi lần request thành công, kể cả khi thành công nhờ failover. Key là `modelID` + hash(client key, team, session).

Chưa có: session cha, và chế độ giữ binding khi lỗi chỉ là tạm.

### Hành vi

Thêm hai khóa trong `RoutingConfig`, chỉ có tác dụng khi `session-affinity: true`:

- `session-affinity-subagents: true` (mặc định true).
- `session-affinity-rebind-on: any` (mặc định `any`, đúng hành vi hiện tại). Giá trị còn lại: `quota`.

Session cha, cùng model:

- Đọc `metadata.parent_session_id` và header `X-Parent-Session-ID`.
- Binding của chính session con thắng.
- Không có binding con thì dùng credential của cha nếu credential đó còn trong danh sách eligible của cùng public model.
- Tra cứu cha không gia hạn TTL của cha.
- Hash cha cùng client key và team. Session của key khác không kế thừa.

Rebind `quota`:

- 429, 401, 403, hết quota: failover thành công thì `bindSession` gắn credential mới, như hiện tại.
- 408, 500, 502, 503, 504, stream rỗng trước byte nội dung: request đó dùng credential khác, binding cũ giữ nguyên.
- `any`: mọi failover thành công đều gắn lại. Đây là default để không đổi production đang chạy.

Không làm kế thừa khác model.

### File

- `internal/config/config.go`: hai field, validate `rebind-on`.
- `internal/api/protocol.go`: trả thêm parent session id từ header và metadata.
- `internal/router/router.go`: `preferSession` nhận parent key. `bindSession` nhận lý do rời credential cũ.
- `Execute` và `ExecuteStream`: nếu rời credential sticky vì lỗi tạm và mode là `quota`, không gọi `bindSession`.
- `config.example.yaml` và README: ba dòng mô tả. Không thêm trang docs mới.

### Test

- Con có parent id dính credential của cha. Binding con sẵn có thì không đổi.
- Tra cứu con không đổi `ExpiresAt` của cha.
- Mode `quota`: 502 rồi credential khác thành công, request sau vẫn về credential đầu. 429 thì gắn credential mới.
- Mode `any`: 502 thành công vẫn gắn credential mới.
- Affinity tắt: cả hai khóa không có tác dụng.
- Hai client key không dùng chung binding.

### Xong khi

Test session hiện có trong `router_test.go` và `account_runtime_test.go` vẫn xanh. Default YAML không đổi hành vi.

---

## Pha 3 — Giữ stream cho đến khi biết đó không phải overload

### Hiện trạng

`ExecuteStream` failover được khi `ExecuteStream` của adapter trả error trước khi có channel. Sau đó `wrapEvents` chuyển mọi event ra HTTP. Comment ở `wrapEvents` nói rõ: stream đã bắt đầu thì không failover, kể cả khi SSE báo model at capacity.

Codex có thể trả HTTP 200, vài frame handshake, rồi mới báo quá tải. Lúc đó header đã commit.

### Hành vi

Thêm `upstream.codex.stream-bootstrap-buffering`, mặc định `false`.

Khi bật, một lớp nằm giữa adapter và `wrapEvents`, chỉ cho Codex:

- Giữ event chưa phải nội dung: SSE `response.created`, `response.in_progress`, heartbeat, item rỗng.
- `text_delta`, `reasoning_delta`, `tool_call_delta`, hoặc SSE có output thật: xả phần đang giữ, rồi stream bình thường.
- Trước nội dung, nếu payload là overload, rate limit, hoặc `ModelAtCapacitySSE`: đóng attempt như một `ProviderError` 429. Vòng credential hiện có chạy tiếp. Client chưa nhận byte nào.
- Ngân sách: 48 event hoặc 1 MiB. Hết ngân sách thì xả, không chờ thêm, không failover nữa.
- Không có timeout tường. Suy luận dài không bị cắt.
- Chat Completions và Claude không đi qua buffer này ở pha này. Chỉ Responses SSE của Codex, vì đó là chỗ HTTP 200 giấu lỗi.

### File

- `internal/config/config.go`: một bool.
- `internal/router/bootstrap_buffer.go` + test.
- `ExecuteStream` bọc channel trước `wrapEvents` khi cờ bật và provider là `codex`.
- Không sửa parser SSE của Codex ngoài việc để event overload đi ra như hiện tại. Buffer quyết định failover.

### Test

- Overload sau handshake, trước text: client không nhận body, credential thứ hai được gọi.
- Text sau handshake: client nhận handshake rồi text, một credential.
- Quá 48 event handshake: xả và không failover.
- Cờ tắt: event đầu đi thẳng, kể cả overload trong stream.

### Xong khi

Một test router chứng minh failover trước byte đầu. Request không bật cờ giữ nguyên đường cũ.

---

## Cách làm

Mỗi pha một nhánh nhỏ, test của package liên quan (`go test` trên `internal/thinking`, `internal/router`, `internal/providers`, `internal/api`). Không gộp ba pha vào một diff.

Pha 1 không cần migration SQLite. Pha 2 và 3 chỉ thêm khóa YAML có default an toàn. Dashboard chưa chỉnh ở cả ba pha.
