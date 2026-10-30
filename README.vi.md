# tproxy

**AI gateway tự lưu trữ với model ID ổn định, định tuyến đa provider và trung tâm điều khiển nhúng.**

tproxy nằm giữa ứng dụng của bạn và các nhà cung cấp AI upstream. Client giao tiếp với một endpoint duy nhất tương thích OpenAI, Claude hoặc Gemini; gateway xử lý luân chuyển credential, failover, viết lại phản hồi, theo dõi sử dụng và đăng ký OAuth — không cần thay đổi cấu hình client khi bạn thêm hoặc đổi provider.

[![GitHub](https://img.shields.io/badge/GitHub-ktvdung2812%2FTProxy-181717?logo=github)](https://github.com/ktvdung2812/TProxy)
[![npm](https://img.shields.io/npm/v/@ktvdung1606/tproxy?label=npm)](https://www.npmjs.com/package/@ktvdung1606/tproxy)

---

## Mục lục

- [Tổng quan](#tổng-quan)
- [Tính năng chính](#tính-năng-chính)
- [Kiến trúc](#kiến-trúc)
- [API & provider được hỗ trợ](#api--provider-được-hỗ-trợ)
- [Trung tâm điều khiển](#trung-tâm-điều-khiển)
- [Cài đặt](#cài-đặt)
- [Bắt đầu nhanh](#bắt-đầu-nhanh)
- [Cấu hình](#cấu-hình)
- [Tích hợp client](#tích-hợp-client)
- [Triển khai](#triển-khai)
- [Thao tác CLI](#thao-tác-cli)
- [Bảo mật](#bảo-mật)
- [Phát triển](#phát-triển)
- [Giấy phép](#giấy-phép)

---

## Tổng quan

Quy trình AI hiện đại thường phụ thuộc vào nhiều provider — subscription OpenAI, Claude OAuth, Ollama cục bộ, Gemini API key, Codex, Kimi, Cursor, Kiro và nhiều hơn nữa. Mỗi provider có SDK, luồng xác thực, cách đặt tên model và giới hạn tốc độ riêng.

**tproxy hợp nhất chúng sau một gateway:**

| Vấn đề | Cách tproxy giải quyết |
|--------|------------------------|
| Client hard-code tên model của provider | **Public model ID** với bí danh; tên upstream được viết lại trong phản hồi (bao gồm SSE stream) |
| Một tài khoản bị giới hạn tốc độ | **Luân chuyển credential** với cooldown, round-robin và 18+ chiến lược lập lịch |
| Provider ngừng hoạt động chặn ứng dụng | **Fallback có thứ tự** qua route, combo và chính sách fusion |
| Secrets rải rác trong file env | **Lưu trữ SQLite mã hóa** với client API key đã hash |
| OAuth phức tạp trên server | **Tích hợp OAuth wizard** (trình duyệt PKCE, device flow, profile riêng cho từng provider) |
| Không có tầm nhìn về chi tiêu | **Theo dõi sử dụng, quota, ước tính giá**, request log và audit event |

tproxy được thiết kế cho **máy cục bộ, máy trạm phát triển và server đơn node**. Lưu trữ chỉ dùng SQLite — không cần cụm cơ sở dữ liệu ngoài.

---

## Tính năng chính

### Định tuyến & models

- **Public Model ID (PPM)** — định nghĩa tên ổn định (`td-coder-pro`, `gpt-sol`, …) ánh xạ đến một hoặc nhiều upstream target với ưu tiên và trọng số.
- **Bí danh (Aliases)** — hiển thị nhiều tên cho cùng một model từ phía client.
- **Combos** — chuỗi fallback có thứ tự trên các virtual model.
- **Protocol mapping** — định tuyến trong suốt Claude/GPT placeholder để các công cụ như Claude Code và Codex giữ nguyên tên tier gốc trong khi tproxy viết lại model ID upstream phía server.
- **Auto-combo** — preset không cần cấu hình như `auto`, `auto/coding:fast`, `auto/reasoning:pro`.
- **Fusion routing** — thực thi song song trên nhiều model với xếp hạng kiểu arena.
- **Session affinity** — định tuyến sticky với TTL có thể cấu hình, kế thừa credential của phiên cha cho subagent, và chế độ `quota` chỉ đổi binding khi hết quota hoặc lỗi xác thực.
- **Lập lịch theo số liệu thực** — độ trễ/TTFT, tải hiện tại, tỷ lệ lỗi và quota gần nhất được dùng cho các chiến lược latency-aware, capacity-aware, health-first, least-used và quota-aware.

### Tương thích giao thức

- OpenAI **Chat Completions** và **Responses** (bao gồm SSE và WebSocket xác thực).
- Anthropic **Messages** và **count_tokens**.
- Google **Gemini** `generateContent` / `streamGenerateContent`.
- Embeddings, tạo/chỉnh sửa ảnh, audio (TTS/STT), video job, web search và web fetch bảo vệ SSRF.
- **MCP JSON-RPC** bridge tại `POST /mcp`.

### Providers & xác thực

- Adapter chung: **OpenAI-compatible**, **Anthropic-compatible**, **Gemini**, **Vertex** và **Ollama**.
- OAuth profile: **Codex**, **Claude**, **Kimi**, **xAI**, **Antigravity** (Google Cloud Code), **Copilot**, **Cursor**, **Kiro** và nhiều hơn nữa.
- Provider API-key: Tavily search, ElevenLabs audio, image/video aliases, HTTP plugin tùy chọn.
- **Proxy pool mã hóa** (HTTP/S, SOCKS5) gắn với từng provider hoặc credential.
- Provider **Meta Muse** dùng Meta Model API chính thức (`https://api.meta.ai/v1`),
  xác thực bằng Model API key và tự khám phá các model `muse-spark-*`/
  `muse-wonder-1.0`. Ngoài API key, Muse còn hỗ trợ **đăng nhập bằng tài khoản Meta**
  (OAuth device code — cùng flow của Muse CLI chính thức): sau khi duyệt mã,
  tproxy đổi token tài khoản tại `POST /muse-code/key` để lấy khóa `LLM|`
  gắn với gói subscription, rồi lưu khóa đó làm credential. Token tài khoản chỉ
  được giữ để mint lại khóa định kỳ (~20h). Yêu cầu subscription Muse đang hoạt
  động; tài khoản không có sub sẽ bị từ chối thay vì âm thầm tính phí pay-as-you-go.
  Tài khoản Meta Managed Account/business vẫn phải dùng Model API key.
- **Tự động nhập JSON** tại Providers → Import data hoặc mục Connections của provider. Chọn một hoặc nhiều file 9router, CLIProxyAPI, sub2api, token OAuth ChatGPT hoặc OAuth bundle của tproxy; ứng dụng tự nhận diện từng định dạng và xem trước trước khi lưu. File token ChatGPT được tự chuyển thành tài khoản Codex. Hỗ trợ giới hạn số tài khoản và bỏ qua trùng lặp cho sub2api và ChatGPT. OAuth bundle vẫn cần master key phù hợp. API chung: `POST /api/admin/import?dry_run=true`; các endpoint nhập theo định dạng vẫn được giữ.

Với sub2api, có thể chọn nhiều file JSON cùng lúc. Khi chọn nhiều file hoặc một file chứa nhiều tài khoản, kiểm tra số tài khoản trong từng file và chọn số dòng cần nhập từ đầu file (ban đầu là một). Dashboard mặc định bật **Bỏ qua tài khoản đã tồn tại**, nhận diện theo credential ID hoặc email OAuth trên cùng provider. Provider được chuẩn bị trước khi lưu credential. Các file được xử lý lần lượt; lỗi ở file sau không hoàn tác những file đã nhập thành công.

Endpoint quản trị là `POST /api/admin/import/sub2api`, nhận nội dung JSON export và yêu cầu xác thực quản trị. Các query parameter tùy chọn:

| Tham số | Hành vi |
| --- | --- |
| `dry_run=true` | Kiểm tra và xem trước, không lưu dữ liệu. |
| `max_accounts=N` | Xét `N` dòng tài khoản đầu tiên trước khi lọc trùng; `0` hoặc bỏ trống xét toàn bộ. Không giới hạn các mục proxy. |
| `skip_duplicates=true` | Bỏ qua credential đã lưu thay vì cập nhật. Khi gọi API mà bỏ trống, mặc định là false để giữ tương thích. |

Phản hồi có `counts.skipped` và danh sách `accounts` gồm tên, email, provider/credential ID cùng hành động (`create`, `update`, `skip_duplicate`). Xem trước nhiều file chỉ đối chiếu từng file với cơ sở dữ liệu hiện tại, không lưu file trước đó, nên số trùng giữa các file có thể thay đổi khi nhập thật.

### Vận hành & quản trị

- **Client API key** mặc định dùng tất cả model và kế thừa giới hạn từ các tài khoản khả dụng. Chính sách endpoint, RPM, concurrency, kích thước input, output token, media job và ngân sách hàng ngày của key có thể siết thêm sức chứa dùng chung.
- **Teams** với giới hạn phạm vi và tổng hợp chi phí.
- **Token Saver** pipeline nén (RTK, Caveman, CCR, Headroom, LLMLingua-2).
- **Circuit breaker** cho từng provider (OPEN / DEGRADED / CLOSED).
- **Giới hạn tài khoản** — số request đồng thời, hàng đợi có giới hạn và timeout, thời hạn sử dụng tài khoản; chỉnh riêng cho từng kết nối.
- **Giám sát vận hành** — số liệu trong 5 phút, p95 độ trễ/TTFT, thời gian chờ, token cache và cảnh báo quota, xác thực, hết hạn, tỷ lệ lỗi, route không khả dụng.
- **Tunnel** — Cloudflare Tunnel dùng token và tên miền riêng, cùng tích hợp Tailscale từ dashboard. Cấu hình tại **APIs → Cloudflare → Bật**: nhập token sau `--token` trong lệnh cài connector của Cloudflare và tên miền đã khai báo. Trong **Cloudflare → Tunnels → Routes**, trỏ ứng dụng công khai của tên miền về dịch vụ HTTP hiển thị trong hộp thoại (mặc định `http://127.0.0.1:28120`). Token được mã hóa; tắt/bật lại hoặc khởi động lại vẫn dùng cùng tên miền. Để trống token để dùng lại token đã lưu. Quick Tunnel cũ cần cấu hình token và tên miền trước khi bật lại. Khi chạy `npm run dev`, đặt `__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS=api.example.com` để Vite chấp nhận tên miền riêng.
- **Chính sách lưu giữ** cho usage event, request log, audit trail và OAuth session.
- **Xuất/nhập cấu hình** (YAML/JSON kèm token OAuth; API key vẫn là biến môi trường) cùng backup/khôi phục OAuth bundle mã hóa.

### Trung tâm điều khiển (Dashboard)

Dashboard React nhúng được phục vụ từ cùng một tiến trình — không cần triển khai frontend riêng. Quản lý provider, model, key, log và cài đặt từ trình duyệt.

### Điều khiển quota và chẩn đoán

- **Quota Tracker** có nút làm mới quota, xóa cooldown cho từng tài khoản hoặc nhóm đã chọn. Lọc provider rồi chọn các tài khoản đang hiển thị để thao tác theo provider. Xóa cooldown giữ nguyên lịch sử sử dụng và trạng thái tắt thủ công. Reset quota upstream dùng một **reset credit Codex** còn hiệu lực cho mỗi tài khoản, có xác nhận và chống thực thi trùng khi thử lại. Provider khác hỗ trợ làm mới/xóa cooldown. Kết quả phân biệt rõ thao tác đã áp dụng với lỗi tải lại quota.
- **Providers → Edit connection** chỉnh metadata: `max_concurrent_requests` (mặc định `0`, không giới hạn), `max_queue_size` (mặc định `16`), `queue_timeout_ms` (mặc định `5000`), `account_expires_at` (tùy chọn, RFC3339). Queue size hoặc timeout bằng `0` từ chối ngay khi bận. Concurrency và hạn tài khoản nhập từ sub2api được áp dụng nếu chưa ghi đè. Hạn tài khoản tách biệt với hạn token OAuth.
- **Chẩn đoán chuyển đổi** xem trước request OpenAI/Responses/Claude/Gemini, dạng canonical và payload gửi provider. Chạy offline, che secret, không nạp credential, không lưu prompt và không áp dụng header riêng của tài khoản. API: `POST /api/admin/diagnostics/translate`. Số liệu vận hành: `GET /api/admin/operations`; thao tác quota: `POST /api/admin/quota/actions` (tối đa 100 credential ID chỉ định rõ).
- **Usage** theo dõi cache đọc, cache ghi và reasoning. Tổng input đã bao gồm token cache; tổng output đã bao gồm reasoning, không cộng lại các phần này. TTFT tính từ delta nội dung đầu tiên; request không stream không có mẫu TTFT.

Responses WebSocket hỗ trợ `previous_response_id`, kế thừa thiết lập bị lược bỏ và ghép lại input/output trước đó cho upstream không lưu trạng thái. Gửi `X-Session-ID` ổn định (hoặc `session_id`, `conversation_id`, `prompt_cache_key`) để nối tiếp qua kết nối mới. Lịch sử được tách theo API key/team/session, chỉ giữ trong bộ nhớ 15 phút, tối đa 128 phản hồi / 32 MiB tổng / 2 MiB mỗi ngữ cảnh. Khi hết hạn hoặc khởi động lại server, gửi lại input đầy đủ và bỏ `previous_response_id`; `reset: true` bắt đầu ngữ cảnh mới. Quan sát định tuyến và cảnh báo đang hoạt động cũng bắt đầu lại sau restart; số liệu usage vẫn lưu trong SQLite.

### Giới hạn API key và sức chứa dùng chung

Với cả key cũ và mới, từng trường `policy.limits` bằng `0` hoặc bỏ trống kế thừa giới hạn của nguồn phục vụ. Giới hạn dương của key, team và global tiếp tục siết thêm. Chỉ tính các tài khoản key được phép dùng cho model, route hoặc combo yêu cầu; loại tài khoản bị tắt, hết hạn, lỗi xác thực hoặc đang cooldown. Việc đọc giới hạn không thay đổi rotation và không ghi tổng suy ra vào cấu hình key.

RPM cộng theo provider và mỗi lần thử gọi upstream đều tiêu thụ RPM. Sức chứa request cộng theo tài khoản; request thường và stream dùng chung slot tài khoản, stream chịu thêm trần provider/key/team/global. Ngân sách dùng chi phí ước tính kể từ đầu ngày UTC. Media tính cả job đang tạo và chưa kết thúc; retry có cùng idempotency key dùng lại job đã lưu. Input bytes và output tokens được kiểm tra trên từng request/route, không cộng giữa tài khoản. Trần output được đưa vào request khi upstream hỗ trợ; Codex loại bỏ các trường trần output không hỗ trợ nên không bảo đảm được giới hạn token sinh ra bằng thiết lập này.

Ví dụ, hai tài khoản có concurrency 3 và 5 tạo tám slot dùng chung; tạo thêm key không tăng sức chứa. Nguồn chưa đặt trần vẫn có thể phục vụ theo giới hạn đã biết. Các trạng thái là `finite` (có trần), `unbounded` (chưa đặt trần), `unknown` (thiếu dữ liệu), `unavailable` (không khả dụng); pool rỗng không được hiểu là vô hạn.

Client đọc giới hạn bằng chính API key qua `GET /v1/limits?model=<model-tùy-chọn>`, dùng `Authorization: Bearer <client-api-key>` hoặc `X-Api-Key`. Endpoint luôn yêu cầu client key, kể cả khi bật gọi nội bộ không cần key. Phản hồi có `object: "tproxy.limits"`, `api_key_id`, `effective_limits`, `shared_limits`, `eligible_account_count`, `quota_windows`, `computed_at` và chỉ phản ánh quyền của key đã xác thực. Đọc giới hạn không tiêu thụ RPM inference, không xoay tài khoản và không probe upstream. Model không tồn tại hoặc key không được phép dùng trả pool `unavailable`.

```bash
curl "$TPROXY_BASE_URL/limits?model=gpt-6.1-sol" \
  -H "Authorization: Bearer $TPROXY_API_KEY"
# TPROXY_BASE_URL bao gồm /v1, ví dụ http://127.0.0.1:28120/v1.
```

Để phát hiện hỗ trợ, chỉ chấp nhận HTTP 200 với `object: "tproxy.limits"` và các trường sức chứa hợp lệ. HTTP 404 hoặc payload khác nghĩa là endpoint chưa được hỗ trợ; phân biệt với lỗi xác thực hoặc lỗi tạm thời. Quản trị viên xem các trường tương tự theo từng key qua `GET /api/admin/quota/summary?model=<model-tùy-chọn>`. Khi không truyền model, kết quả là trần sức chứa trên các model được phép; từng request có thể có ít route khả dụng hơn.

Quota upstream giữ riêng từng cửa sổ của từng tài khoản. Với Codex, đọc `window_minutes`: `300` là 5 giờ, `10080` là 7 ngày, không suy luận chỉ từ tên primary/secondary. Thiếu hoặc sai thời lượng/phần đã dùng có `total: 0` (chưa biết), không mặc định còn 100%. Khi số liệu hợp lệ, phần trăm còn lại bằng `100 - used_percent`. Header quota của một request sinh nội dung chỉ thuộc tài khoản phục vụ request đó; không cộng phần trăm giữa các tài khoản hoặc quy đổi thành token, lượt gọi hay USD.

Backend làm mới snapshot quota mỗi 60 giây (Claude: 180 giây), tối đa bốn probe cùng lúc và gộp probe trùng nhau, kể cả khi dashboard đóng. `GET /api/admin/credentials/{id}/quota` đọc snapshot; `POST` yêu cầu làm mới. Probe lỗi giữ dữ liệu trước đó và đánh dấu cũ/chưa biết; kiểm tra phục hồi tài khoản hết quota giữ nguyên trạng thái tắt thủ công.

Triển khai backend trước dashboard; không cần migration SQLite hay cấu hình key. Bộ đếm RPM, slot đang dùng và snapshot quota chỉ có phạm vi một tiến trình, reset khi khởi động lại. Chi tiêu và media job chưa hoàn tất vẫn lưu trong SQLite.

---

## Cài đặt

### Yêu cầu hệ thống

| Thành phần | Phiên bản | Ghi chú |
|-----------|---------|--------|
| **Go** | 1.26+ | Cần để build gateway binary từ source |
| **Node.js** | 18+ | Cần để build dashboard và `npm run dev` |
| **OS** | macOS, Linux | Windows không được hỗ trợ chính thức; dùng WSL2 hoặc Docker |
| **Ổ đĩa** | ~100 MB + SQLite | Tăng theo log sử dụng và credential |

Cổng mặc định:

| Cổng | Dịch vụ |
|------|---------|
| `28120` | Dashboard, `/v1/*` API, `/api/admin/*` |
| `28122` | Go backend (nội bộ, chỉ `npm run dev`) |

### Bắt đầu nhanh

```bash
git clone https://github.com/ktvdung2812/TProxy.git
cd TProxy
cp config.example.yaml config.yaml
cp .env.example .env.run
go run ./cmd/tproxy --print-master-key   # thêm vào .env.run dưới dạng TPROXY_MASTER_KEY
npm install && npm run dev
```

Mở http://127.0.0.1:28120/dashboard/ (mật khẩu: `123123`).

Đường dẫn phát triển đầy đủ và các phương thức triển khai (Docker, npm global, binary Linux), xem [README.md](README.md) bản tiếng Anh.

---

## Tích hợp client

Trỏ bất kỳ client tương thích OpenAI, Anthropic hoặc Gemini vào tproxy:

```text
Base URL:  http://127.0.0.1:28120/v1
API key:   <TPROXY_API_KEY của bạn>
Model:     <public model ID hoặc alias>
```

---

## Bảo mật

- Credential provider và URL proxy pool được **mã hóa khi lưu trữ** với `TPROXY_MASTER_KEY`.
- Client API key được **hash**; chỉ hiển thị plaintext một lần khi tạo.
- Management API và dashboard yêu cầu `TPROXY_MANAGEMENT_SECRET`.
- OAuth token dùng encrypted envelope với refresh deduplication.
- Request log hỗ trợ correlation ID với redaction trường nhạy cảm.

---

## Phát triển

```bash
npm run dev        # Stack dev đầy đủ (Go backend + Vite dashboard)
npm run build      # Build frontend + binary
go test ./...      # Chạy Go test
```

Cấu trúc dự án:

```text
tproxy/
├── cmd/tproxy/          # Binary chính và lệnh con `connect`
├── internal/
│   ├── api/             # HTTP handler, dashboard embedding
│   ├── auth/            # OAuth, token refresh, xác thực provider
│   ├── router/          # Định tuyến model, fallback, fusion, auto-combo
│   ├── providers/       # Adapter upstream
│   ├── store/           # SQLite persistence
│   ├── tunnel/          # Cloudflare / Tailscale
│   └── ...
├── web/                 # React dashboard (Vite + TypeScript)
├── deploy/              # Bundle triển khai production
└── npm/                 # Gói npm CLI wrapper
```

---

## Giấy phép

MIT — xem repository để biết chi tiết.

---

<p align="center">
  <a href="https://github.com/ktvdung2812/TProxy">GitHub</a> ·
  <a href="https://www.npmjs.com/package/@ktvdung1606/tproxy">npm</a> ·
  <a href="docs/IMPLEMENTATION.md">Trạng thái triển khai</a>
</p>
