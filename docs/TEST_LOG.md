# Nhật ký kiểm thử

Nhật ký đo cho thiết kế cuối cùng: API scale to zero (KEDA HTTP), frontend (HPA), **worker ScaledJob** (KEDA SQS, tối đa 20 Job), Aurora Serverless v2. Mỗi bài có **mục đích**, **cách test**, **kết quả không chứng minh** và bảng kết quả. Số liệu nằm trong `docs/results/`.

## Trước khi đo

- Chạy `aws sts get-caller-identity`: phải ra mã tài khoản. Phiên hết hạn giữa bài làm hỏng dữ liệu.
- Cấu hình hiện tại (ghi lại khi đo, nếu khác thì ghi vào bảng kết quả):
  - API: `scaledownPeriod`, ảnh `demo-api` tag, tối đa 2 pod.
  - Frontend: HPA 1 đến 2 pod, CPU 50%.
  - Worker: KEDA ScaledJob ở pool `worker`, `pollingInterval` 1 giây, 10 message mỗi Job, tối đa 20 Job, `activeDeadlineSeconds` 300.
  - Image prepuller đã trỏ đúng tag trong ECR.
- Đo trên cụm ổn định (đủ 4 node, mọi pod Ready). Mỗi bài đo ít nhất 5 lượt nếu là bài thời gian.

## Số liệu nằm ở file nào

| Bài | Dữ liệu thô sẽ lưu ở |
|---|---|
| 1. API scale to zero | `docs/results/api/scale-to-zero.txt` |
| 2. Cold start API | `docs/results/api/cold-start-api.csv` |
| 3. Cold start worker | `docs/results/worker/` |
| 4. Karpenter | `docs/results/nodes-and-pods/karpenter.txt` |
| 5. HPA | `docs/results/nodes-and-pods/hpa.txt` |
| 6. KEDA với SQS | `docs/results/worker/` (chỉ phương án worker nóng còn file thô) |
| 7. 200 user cùng lúc | `docs/results/load-200-users/` (chỉ giữ lần đo sau tối ưu) |
| 8. Aurora ACU | `docs/results/aurora/` |
| 9. Mất node spot | `docs/results/spot-loss/` |
| 10. Chart với image bất kỳ | `docs/results/compatibility/plug-image.txt` |
| 11. Kiến trúc arm64 | `docs/results/compatibility/arm64.txt` |

## 1. API scale to zero

> **Mục đích:** Chứng minh API tự về 0 pod sau thời gian không có request và tự bật lại khi có request mới.
> **Cách test:** để API không nhận request, ghi số pod theo thời gian; gửi một request khi API ở 0 pod, ghi thời gian trả lời; lấy sự kiện KEDA (`KEDAScaleTargetActivated`, `KEDAScaleTargetDeactivated`) và cấu hình `scaledownPeriod`. Lệnh trong [DEMO.md](DEMO.md) phần 3.
> **Kết quả KHÔNG chứng minh:** không chứng minh cold start dưới 3 giây ở mọi lần (xem bài 2).

| Mốc | Thời điểm | Kết quả |
|---|---|---|
Đo ngày 2/11/2026 (giờ máy đo), dữ liệu `docs/results/api/scale-to-zero.txt`:

| Mốc | Thời điểm | Kết quả |
|---|---|---|
| API ở 0 pod, gửi request | 13:14:18 | HTTP 200 sau **1,79 giây** (cold start) |
| KEDA bật API lên 1 pod | 13:14:18 | sự kiện `KEDAScaleTargetActivated`: 0 → 1 |
| Request thứ hai ngay sau đó | 13:14:20 | HTTP 200 sau 0,14 giây (warm) |
| KEDA đưa API về 0 pod (`scaledownPeriod` = 300 giây) | 13:19:19 | sự kiện `KEDAScaleTargetDeactivated`: 1 → 0, **khoảng 5 phút 1 giây** sau request cuối |

## 2. Cold start API

> **Mục đích:** Đo thời gian từ lúc gửi request khi API ở 0 pod đến lúc có phản hồi, so với yêu cầu dưới 3 giây.
> **Cách test:** `COLD_PATH='/positions?limit=10' COLD_JQ='has("items")' scripts/test-cold-start.sh 20 direct docs/results/api/cold-start-api.csv` (20 lượt, mỗi lượt chờ API về 0). Cần `scaledownPeriod` ngắn để đỡ chờ, rồi trả lại giá trị thật.
> **Kết quả KHÔNG chứng minh:** đo từ một máy, một vị trí mạng; không đại diện cho mọi người dùng. Phần mạng của máy đo (khoảng vài phần trăm kết nối chậm hơn 1 giây) lẫn trong số đo.

Đo ngày 30/10/2026 (giờ VN), qua ALB, `/positions?limit=10`, `scaledownPeriod` 10 giây, API 32 endpoint (image 0.4.0). Dữ liệu thô: `docs/results/api/cold-start-api.csv`.

| Chỉ số | Tổng thời gian (`total_s`) | Bỏ 6 lượt mất gói lúc kết nối (`connect_s` từ 0,5 giây) | Phía hệ thống (`total_s` trừ `connect_s`) |
|---|---|---|---|
| Số lượt | 20 (cả 20 HTTP 200) | 14 | 20 |
| Nhỏ nhất | 1,43 giây | 1,43 giây | 1,34 giây |
| **Trung vị** | **2,40 giây** | **2,19 giây** | **2,08 giây** |
| p95 | 3,54 giây | 2,96 giây | 2,80 giây |
| Lớn nhất | 5,03 giây | 2,99 giây | 2,89 giây |
| Số lượt dưới 3 giây | **17/20** | 14/14 | **20/20** |
| Đo trên node đã có image sẵn hay node mới | node đã có image sẵn | | |

**Ý nghĩa các cột của file CSV:** `total_s` là tổng thời gian cả request; `connect_s` là thời gian mở kết nối TCP tới ALB; `ttfb_s` là thời gian đến byte đầu tiên (gồm chờ pod API bật); `pod_created_after_request_s` là pod được tạo sau request bao lâu; `created_to_scheduled_s` và `created_to_ready_s` là từ lúc tạo pod đến lúc được xếp và đến lúc Ready (độ phân giải 1 giây).

**Lưu ý trung thực:** 6 trong 20 lượt (1, 5, 10, 12, 14, 18) có `connect_s` từ 0,7 đến 3,0 giây, tức là gói SYN đầu tiên tới ALB bị mất rồi thử lại: **mạng của máy đo**, không phải API. Tổng thời gian có 3 lượt vượt 3 giây (3,3 / 3,5 / 5,0 giây) đều là các lượt này hoặc gần nó; phía hệ thống cả 20 lượt đều dưới 3 giây. Trước khi đổi danh sách cho phép IP của ALB, cùng bài đo này có 5 trên 20 lượt hỏng nặng (kẹt 30 giây hoặc không kết nối được) vì máy đo ra internet bằng nhiều IP khác nhau qua Cloudflare.

## 3. Cold start worker

> **Mục đích:** Đo thời gian từ lúc bấm Score (message vào hàng đợi) khi worker ở 0 pod đến lúc điểm hiện, và độ ổn định khi nhiều Job được tạo cùng lúc.
> **Cách test:** (a) Score qua API, `scripts/score-cold-start.sh` (20 lượt, API đã thức, worker ở 0 pod, đọc `requestedAt`/`doneAt` từ API, hỏi `/screening` mỗi 0,25 giây) với KEDA hỏi hàng đợi mỗi 1, 10, 30 giây; (b) đợt dồn: `POST /jobs {"count":100}` rồi đo thời gian từ lúc pod được xếp (`Scheduled`) đến lúc container chạy (`Started`) từ sự kiện Kubernetes lưu trong Loki.
> **Kết quả KHÔNG chứng minh:** công việc chấm điểm là giả lập (gần như 0 giây); đo từ một máy, một vị trí mạng; chưa đo trên cụm khác.

**(a) Cold start worker, KEDA ScaledJob, 20 lượt** (2/11/2026, qua ALB, API đã thức, worker ở 0 pod; chu kỳ 1 giây: file thô không còn lưu, chỉ còn số tổng hợp dưới đây):

| Cột | Ý nghĩa | Nhỏ nhất | Trung vị | p95 | Lớn nhất |
|---|---|---|---|---|---|
| `api_reply_s` | API trả lời lệnh Score (chưa gồm việc chấm điểm) | 0,17 | 0,20 | 2,17 | 2,26 |
| `total_s` | từ lúc bấm đến lúc điểm hiện, đồng hồ máy đo (gồm mạng) | 7,73 | **10,74** | 14,25 | 14,32 |
| `server_s` | `doneAt` trừ `requestedAt` trong database (không gồm mạng của máy đo) | 7,11 | **10,18** | 12,52 | 13,05 |
| `keda_job_after_s` | từ lúc bấm đến lúc KEDA tạo Job đầu tiên (độ phân giải 1 giây) | 6,0 | 9,6 | 16,3 | 16,8 |
| `jobs_created` | số Job cho một lần Score | 3 | 3 | 3 | 3 |

- Dưới 3 giây: **0/20** ở cả `total_s` lẫn `server_s`. Lượt có đúng 1 Job: **0/20**.
- `total_s` trừ `server_s`: trung vị 0,47 giây (mạng và API trả lời). `server_s` trừ `keda_job_after_s`: trung vị 0,96 giây (từ lúc Job tồn tại đến lúc điểm được ghi).
- Từ Loki, 66 Job khởi động trong khoảng đo: **20 Job xử lý message, 46 Job rỗng** (`messages: 0`). Pod khởi động (xếp lên node đến container chạy): trung vị **0,4 giây**, chậm nhất 1 giây.
- **Kết luận:** thời gian chờ nằm hầu hết ở bước KEDA phát hiện việc (6 đến 17 giây), không phải khởi động pod (khoảng 0,4 giây) hay chấm điểm.

**(a2) Chu kỳ hỏi hàng đợi 10 và 30 giây** (dữ liệu `docs/results/worker/worker-cold-start-keda-poll10.csv`, `...poll30.csv`, các cột như (a); hai file này dựng lại từ số liệu đã in lúc đo):

| Chu kỳ | Số lượt | `total_s` trung vị | `server_s` nhỏ nhất / trung vị / lớn nhất | Dưới 3 giây | Lượt đúng 1 Job |
|---|---|---|---|---|---|
| 1 giây | 20 | 10,74 | 7,11 / 10,18 / 13,05 | 0 | 0/20 (luôn 3 Job) |
| 10 giây | 20 | 17,71 | 5,52 / 17,35 / 37,09 | 0 | 5/20 |
| 30 giây | 20 | 12,66 | 4,07 / 12,19 / 42,37 | 0 | 14/20 |

- Với chu kỳ 10 giây, `server_s` gom thành các cụm cách nhau gần đúng 10 giây (khoảng 6,5, 17, 27, 37 giây): mỗi lần KEDA hỏi mà số đếm của SQS còn cũ thì chờ thêm một chu kỳ. Với chu kỳ 30 giây, 4 lượt khoảng 42 giây (lỡ một nhịp).
- `server_s` trừ `keda_job_after_s` trung vị khoảng 1 đến 1,5 giây ở mọi chu kỳ: tạo Job, khởi động pod và chấm điểm không phụ thuộc chu kỳ.
- **Chưa giải thích được:** trung vị 30 giây (12,2 giây) thấp hơn 10 giây (17,4 giây) dù chu kỳ dài hơn; mỗi chu kỳ chỉ đo một lần. Chỉ kết luận được là **không chu kỳ nào đưa cold start xuống dưới 3 giây**, và chu kỳ dài hơn thì ít Job dư hơn nhưng chậm hơn.
- **Lưu ý về độ tin cậy:** ở chu kỳ 30 giây, 16/20 lượt có `keda_job_after_s` chỉ từ 10,1 đến 11,0 giây (và 4 lượt khoảng 40,7 giây = nhóm đó cộng 30 giây). Với thời điểm bấm ngẫu nhiên, thời gian chờ phải rải đều trong một chu kỳ; kết quả gom lại cho thấy nhịp của script đo (khoảng 90 giây mỗi lượt, gần 3 lần 30 giây) có khả năng bị khoá pha với nhịp hỏi của KEDA. Vì vậy trung vị 12,2 giây **không đại diện** và không so sánh được giữa các chu kỳ. Đây là suy luận, chưa kiểm chứng (cần thêm độ lệch ngẫu nhiên giữa các lượt). Một lần đo chu kỳ 60 giây bị dừng và không được giữ.
- Nguyên nhân gốc đã tìm ra (thử riêng): `GetQueueAttributes` của SQS trả số đếm cũ khoảng 6 giây (5,4 đến 6,4 giây) khi hàng đợi bị hỏi mỗi giây; hàng đợi mới hiện số đếm sau 0,2 giây; `ReceiveMessage` không bị ảnh hưởng. Dữ liệu thô của thử nghiệm này không còn lưu.

**(a3) Thêm trigger PostgreSQL cho ScaledJob** (3/11/2026; trigger đếm hồ sơ `queued` trong database, giữ cả trigger SQS; KEDA lấy giá trị lớn nhất của hai trigger; script đo có nghỉ ngẫu nhiên 0 đến 30 giây trước mỗi lượt bấm để không khoá pha với nhịp hỏi). Dữ liệu `docs/results/worker/worker-cold-start-keda-postgres.csv` (hỏi mỗi **1 giây**) và `worker-cold-start-keda-postgres-poll30.csv` (hỏi mỗi **30 giây**), 20 lượt mỗi file, các cột như (a):

| Chu kỳ | Cột | Nhỏ nhất | Trung vị | p95 | Lớn nhất |
|---|---|---|---|---|---|
| 1 giây | `total_s` | 1,64 | **2,12** | 4,00 | 4,64 |
| 1 giây | `server_s` | 1,17 | **1,66** | 2,06 | 2,41 |
| 1 giây | `keda_job_after_s` | -0,80 | 0,30 | 2,30 | 3,10 |
| 30 giây | `total_s` | 3,01 | **7,50** | 29,19 | 31,02 |
| 30 giây | `server_s` | 1,68 | **6,74** | 28,52 | 30,37 |
| 30 giây | `keda_job_after_s` | 1,10 | 5,50 | 27,40 | 29,20 |

- **Chu kỳ 1 giây:** `server_s` **20/20 dưới 3 giây**; `total_s` 15/20 dưới 3 giây (5 lượt vượt 3 giây chủ yếu vì `api_reply_s` cao 1,2 đến 2,4 giây, tức API trả lời chậm, không phải worker). So với KEDA chỉ dùng SQS ở (a) (trung vị `server_s` 10,18 giây, 0/20 dưới 3 giây), nhanh hơn khoảng 6 lần. Số Job mỗi Score: **2 Job ở 17 lượt, 3 Job ở 3 lượt, không lượt nào đúng 1 Job**: Job đầu xử lý message, các Job sau là Job rỗng (`messages: 0`).
- **Chu kỳ 30 giây:** `server_s` rải từ 1,7 đến 30,4 giây (13/20 lượt dưới 10 giây, 2/20 dưới 3 giây), đúng dạng "chờ nhịp hỏi kế tiếp" nên trigger database **mất lợi thế** khi KEDA hỏi thưa. Số Job mỗi Score: **1 Job ở 17 lượt, 2 Job ở 3 lượt**: Job dư ít hơn nhiều so với chu kỳ 1 giây. Trung vị 6,7 giây thấp hơn mức kỳ vọng nếu thời điểm bấm rải đều trong 30 giây (khoảng 15 giây); với 20 lượt tôi chưa giải thích được.
- `keda_job_after_s` có thể nhỏ hơn 0 (nhỏ nhất -0,8 giây) vì thời điểm tạo Job của Kubernetes chỉ có độ phân giải 1 giây, làm tròn so với giờ bấm.
- `total_s` trừ `server_s` trung vị 0,5 giây; `server_s` trừ `keda_job_after_s` trung vị 1,3 giây (tạo Job, khởi động pod, chấm điểm).
- **Job dư** nhiều khả năng đến từ trigger SQS (số đếm cũ, Job thoát với 0 message), nhưng chưa tách riêng từng trigger. Cách kiểm chứng: tắt trigger SQS rồi đo lại. Đã đề xuất tách hai ScaledJob (database 1 giây, SQS 60 giây) chưa thực hiện.
- Rủi ro và hạn chế: xem phần trả lời rủi ro; chưa thử database lỗi, mật khẩu xoay vòng, hay hồ sơ `queued` mồ côi (có thể tạo Job rỗng liên tục).

**(b) Đợt dồn 100 message:** dữ liệu thô của đợt này không còn trong thư mục làm việc, có trong lịch sử git (commit `cba758f`, thư mục `docs/evidence/worker-burst/`).

| Cấu hình | Số pod | Trung vị khởi động | Chậm nhất | Pod trên 3 giây | Lỗi cấp IP |
|---|---|---|---|---|---|
| Pool `default`, kho IP 3, 5 message mỗi Job | 28 | 41 giây | 111 giây | 20 | 97 |
| Kho IP 10 và 12 | 43 | 2 giây | 39 giây | 20 | 44 |
| + 10 message mỗi Job | 28 | 1 giây | 29 giây | 3 | 6 |
| Pool `worker` riêng, kho IP 10 và 12, node mới | 20 | 12 giây | 40 giây | 10 | 21 |
| Pool `worker` riêng, kho IP 10 và 30, node mới | 36 | 2 giây | 30 giây | 9 | 15 |
| **Pool `worker` riêng, kho IP 10 và 30, node ổn định** | 30 | **1 giây** | 30 giây | **3** | **4** |

**Kết luận:** thời gian khởi động pod bình thường dưới 1 giây; phần chậm duy nhất là pod chờ cấp IP khi nhiều Job được tạo cùng lúc (sự kiện `FailedCreatePodSandBox ... failed to assign an IP address`, thử lại khoảng mỗi 12 đến 15 giây). Tăng kho IP và giảm số pod mỗi lượt giảm số pod chậm từ 71% xuống 10%, nhưng chưa về 0. Trong các đợt dồn, frontend và API gần như không lỗi (tối đa 1 request lỗi mỗi bên trong khoảng 110 đến 160 request).

**Phương án so sánh, đã thử và không chọn: worker nóng** (Deployment luôn có 1 pod, KEDA ScaledObject `minReplicaCount: 1`), 20 lượt, 2/11/2026, dữ liệu `docs/results/worker/worker-cold-start-warm.csv`: `total_s` trung vị **0,39 giây** (nhỏ nhất 0,35; lớn nhất 0,69), `server_s` trung vị 0,04 giây, **20/20 dưới 3 giây**, không tạo Job nào (`jobs_created` bằng 0, `keda_job_after_s` để trống). Cái giá: một pod worker chạy liên tục. Đây là phương án duy nhất trong các phương án đã đo đưa cold start worker xuống dưới 3 giây.

## 4. Karpenter: tầng node

> **Mục đích:** Chứng minh Karpenter tạo node spot mới khi có pod không còn chỗ chạy và dọn node thừa khi hết cần.
> **Cách test:** [DEMO.md](DEMO.md) phần 4 (5 pod, mỗi pod xin 1500m CPU, `rollout status`, rồi xoá namespace); ghi số node, NodeClaim và nhật ký Karpenter (lấy theo label, vì chỉ pod leader ghi sự kiện).
> **Kết quả KHÔNG chứng minh:** không chứng minh xử lý khi spot hết hàng hoặc bị thu hồi (xem bài 9).

Đo ngày 2/11/2026, dữ liệu `docs/results/nodes-and-pods/karpenter.txt`:

| Chỉ số | Giá trị |
|---|---|
| Số node `default` trước, đỉnh, sau | 2 → **4** → 2 |
| Thời gian đến khi cả 5 pod sẵn sàng | **37 giây** từ lúc tạo Deployment (node mới xuất hiện sau khoảng 24 giây) |
| Thời gian về số node ban đầu sau khi xoá | **126 giây** sau khi xoá namespace |
| Loại node và kiến trúc được tạo | 2 node spot: `t3.xlarge` (amd64) và `m6g.xlarge` (arm64) |

## 5. HPA: tầng pod

> **Mục đích:** Chứng minh HPA thêm pod khi CPU vượt ngưỡng 50% và giảm khi hết tải.
> **Cách test:** [DEMO.md](DEMO.md) phần 6 (bản sao API có endpoint `/burn`, tải nhẹ: đốt CPU 1 giây, nghỉ 2 giây). Ghi số pod, CPU và sự kiện `SuccessfulRescale`.
> **Kết quả KHÔNG chứng minh:** frontend nginx dùng rất ít CPU nên không dùng làm bằng chứng HPA; chỉ ghi nhận frontend thật lên 2 pod nếu quan sát được trong bài 7.

Đo ngày 2/11/2026 (bản sao API image 0.5.1, HPA 1 đến 3 pod, mục tiêu CPU 50%, mỗi pod xin 250m, tải 150 giây gọi `/burn?ms=1000` rồi nghỉ 2 giây, `scaleDownStabilizationSeconds` 60), dữ liệu thô `docs/results/nodes-and-pods/hpa.txt`:

| Chỉ số | Giá trị |
|---|---|
| Số pod | 1 → 2 → 3 → 1 |
| Thời điểm (tải bắt đầu 13:28:32) | 2 pod lúc 13:29:02 (khoảng 30 giây); 3 pod lúc 13:29:17 (khoảng 45 giây); về 1 pod lúc khoảng 13:32:10, **khoảng 70 giây sau khi tải kết thúc** (13:31:02) |
| Sự kiện HPA | `SuccessfulRescale` lên 2 và lên 3 (lý do: CPU trên mục tiêu), xuống 1 (lý do: mọi chỉ số dưới mục tiêu) |
| CPU đỉnh so với mức xin | **chưa ghi** (cột phần trăm CPU bị cắt khi lấy mẫu); chỉ biết CPU vượt 50% đủ để lên mức tối đa 3 pod |

Lưu ý: ngay lúc đầu HPA báo `FailedGetResourceMetric` một lần (chưa có số đo CPU của pod mới), rồi tự hết.

**(b) API: KEDA HTTP thêm trigger CPU** (2/11/2026). Đã bật `scaling.http.cpu` (ngưỡng 70% mức xin 250m) cho API: chart tạo ScaledObject riêng có hai trigger, `external-push` (request đồng thời, mục tiêu 100 mỗi pod, kiêm việc 0 ↔ 1) và `cpu`. Dữ liệu thô `docs/results/api/keda-http-cpu.txt` và `keda-http-cpu-burn.js`.

- **Cách test:** 10 user gọi `GET /burn?ms=1000` (mỗi request đốt CPU 1 giây) trong 2 phút, qua ALB, khi API đang 1 pod. Số request đồng thời chỉ 4 đến 10, **xa dưới mức 100** của trigger request, nên chỉ trigger CPU mới có thể thêm pod. Có 1.071 request.
- **Kết quả:** API lên **2 pod sau khoảng 21 giây** (tải bắt đầu 15:41:13, 2 pod lúc 15:41:34). Sự kiện của HPA: `New size: 2; reason: cpu resource utilization (percentage of request) above target`. Sau tải (kết thúc 15:43:15), API về **0 pod lúc khoảng 15:48** (khoảng 5 phút, đúng `scaledownPeriod` 300 giây).
- **Sau khi bật:** cold start từ 0 pod vẫn **1,97 giây**, lượt thứ hai 0,13 giây (một lần đo).
- **Chưa chứng minh:** phần trăm CPU đỉnh không được ghi (chỉ có sự kiện và số pod); một lần đo; chưa so sánh với cấu hình không có CPU trong cùng điều kiện (nhưng với 4 đến 10 request đồng thời, trigger request không thể đạt mức 100).

## 6. KEDA với SQS: tầng worker

> **Mục đích:** Chứng minh worker (KEDA ScaledJob) tăng từ 0 lên tối đa 20 Job theo độ dài hàng đợi, không mất message, rồi về 0.
> **Cách test:** gửi 500 message (`POST /jobs`, 5 lần count 100); ghi số worker, node, hàng đợi mỗi vài giây; lấy độ dài hàng đợi lỗi; đồng thời gửi một request mỗi giây vào frontend và API để xem worker có làm chậm hai thành phần này không.
> **Kết quả KHÔNG chứng minh:** khối lượng công việc thật (worker chỉ chấm điểm giả lập); một lần đo; chưa lấy số đếm nhận/xoá của SQS.

Đo ngày 2/11/2026 (ScaledJob, `pollingInterval` 1 giây, 10 message mỗi Job, tối đa 20 Job, pool `worker` riêng); dữ liệu thô của lần đo này không còn lưu:

| Chỉ số | Giá trị |
|---|---|
| Số worker lớn nhất | **20 pod** |
| Số node tạo thêm | **0**: node `worker` có sẵn (1 node) đủ chỗ cho 20 pod |
| Hàng đợi | 500 message → 0 sau **75 giây** |
| SQS gửi / nhận / xoá | gửi 500 (5 lần `{"enqueued":100}`); hàng đợi về 0 và hàng đợi lỗi giữ nguyên 0 nên không mất message; số đếm nhận/xoá của SQS **chưa lấy** |
| Hàng đợi lỗi (DLQ) | **0** trước và sau |
| Thời gian về 0 pod | khoảng **1 phút 40 giây** sau khi gửi |
| Request frontend/API không phải 200 trong lúc đó | **0/76** lượt thử (mỗi giây một lượt cho frontend và API `/healthz`) |

Lưu ý: bài này chạy trên node `worker` riêng, nên chưa chứng minh worker không làm chậm API/frontend khi chung pool `default`.

**Phương án so sánh, đã thử và không chọn: worker nóng** (2/11/2026, dữ liệu `docs/results/worker/keda-sqs-3000.csv`): gửi 3.000 message (mỗi message 1 giây xử lý); một pod nóng xử lý hết trong khoảng 17 giây, KEDA thêm pod lên 20 (sau khi hàng đợi đã cạn vì số đếm cũ vài giây), về 1 pod sau khoảng 4 phút 15 giây, hàng đợi lỗi 0, 0/256 request frontend/API lỗi, không tạo node mới.

## 7. 200 user cùng lúc

> **Mục đích:** Chứng minh hệ thống chịu 200 người dùng ảo cùng lúc, và API/frontend không vượt 2 pod.
> **Cách test:** `docker run --rm -v "$PWD/scripts:/s:ro" grafana/k6 run -e BASE_URL=http://$ALB -e VUS=200 -e DURATION=<thời lượng> [-e WITH_SLOW=1] [-e WITH_DB=1] [-e WITH_DOMAIN=1] /s/loadtest.js`. `WITH_SLOW=1` cần để có đủ request đồng thời (mục tiêu 100 mỗi pod); `WITH_DB=1` và `WITH_DOMAIN=1` cần để chạm database. Ghi số pod mỗi 5 giây.
> **Kết quả KHÔNG chứng minh:** không chứng minh trên 200 user; request nhanh chỉ tạo khoảng 10 request đồng thời nên không đủ để API lên 2 pod nếu thiếu `WITH_SLOW`.

Đo ngày 2/11/2026 từ một máy ngoài cụm, qua ALB, 200 user ảo. Kết quả bên dưới là lần đo **đầu** (trước tối ưu ở 8b); file thô của lần này không còn lưu, chỉ giữ file của lần đo sau tối ưu: `docs/results/load-200-users/load-200-vus-after.txt` (kết quả k6) và `load-200-pods-after.csv` (số pod mỗi 5 giây).

| Bài (cờ bật) | Thời lượng | Số request | Request/giây | Trung vị | p95 | Lỗi | API pod tối đa | Frontend pod tối đa |
|---|---|---|---|---|---|---|---|---|
| `VUS=200 WITH_SLOW=1 WITH_DB=1 WITH_DOMAIN=1` | 3 phút | 68.854 (9.693 vòng lặp) | 373 | 85,5 ms | 2,06 giây (gồm `/slow` cố ý chờ 2 giây); nhóm request nhẹ: **314 ms**, p99 413 ms | **0** (0,00%, mọi kiểm tra đạt 68.854/68.854) | **2** | **2** |

- Ngưỡng k6 đều đạt: lỗi dưới 1%; nhóm nhẹ p95 dưới 500 ms và p99 dưới 3 giây; frontend p95 331 ms; API p95 306 ms. Request chậm nhất 3,01 giây.
- Số pod API và frontend tối đa không vượt 2 trong suốt bài (lấy mẫu mỗi 5 giây).
- Chưa chứng minh: chỉ một lần chạy 3 phút (không phải 5 phút mặc định), một máy đo, không lặp lại.

## 8. Aurora ACU

> **Mục đích:** Cho thấy Aurora co giãn dung lượng trong khoảng 0,5 đến 2 ACU khi có tải chạm database.
> **Cách test:** chạy bài 7 với `WITH_DB=1 WITH_DOMAIN=1`; ngay sau đó đọc CloudWatch `ServerlessDatabaseCapacity`, `CPUUtilization`, `DatabaseConnections` của `demo-aurora` (lệnh trong [DEMO.md](DEMO.md) phần 9).
> **Kết quả KHÔNG chứng minh:** CloudWatch chỉ có độ phân giải 1 phút; chạy không bật gọi database thì ACU đứng yên và không chứng minh gì.

Đo ngay sau bài 7, cùng khoảng thời gian (CloudWatch, giờ máy đo, mỗi dòng là một phút; dữ liệu thô `docs/results/aurora/aurora-cloudwatch-before.txt`):

| Phút | ACU (trung bình / đỉnh) | CPU database (trung bình / đỉnh) | Kết nối |
|---|---|---|---|
| 13:38 và 13:39 (trước tải) | 0,5 / 0,5 | 21% / 31% | 0 |
| 13:40 (tải bắt đầu) | 0,71 / **2,0** | 34% / **95%** | 20 |
| 13:41 | 1,18 / 1,5 | 47% / 64% | 20 |
| 13:42 | 1,31 / 1,5 | 51% / 70% | 20 |
| 13:43 | 1,17 / 1,5 | 39% / 64% | 20 |
| 13:44 (tải kết thúc) | 1,0 / 1,0 | 22% / 26% | không có số liệu |

Aurora tăng từ 0,5 lên đỉnh 2,0 ACU rồi giảm dần khi hết tải (chưa quan sát về 0,5 vì Aurora giảm chậm sau tải). Số kết nối giữ 20 (nhiều khả năng là pool kết nối của API; chưa kiểm tra).

**(b) Sau khi gộp truy vấn đếm và cache thống kê** (2/11/2026). Thay đổi: mỗi danh sách (ứng viên, vị trí, hồ sơ) chạy 1 truy vấn thay vì 2 (`count(*) OVER ()`), thống kê tổng hợp lưu trong bộ nhớ API 5 giây (`STATS_CACHE_SECONDS=5`), image API 0.5.2. Đo lại đúng bài 7 (200 user, 3 phút, cùng cờ). Dữ liệu `docs/results/load-200-users/load-200-vus-after.txt`, `docs/results/load-200-users/load-200-pods-after.csv`, `docs/results/aurora/aurora-explain-analyze.txt`, `aurora-cloudwatch-after.txt`.

Trước khi sửa, `EXPLAIN ANALYZE` trên database thật (1.122 ứng viên, 15 vị trí, 340 hồ sơ) cho thấy **mọi truy vấn chạy dưới 1 ms** (0,02 đến 0,9 ms). Truy vấn không phải điểm nghẽn ở khối lượng dữ liệu này; tải đến từ số truy vấn mỗi giây.

| Chỉ số | Trước | Sau |
|---|---|---|
| Số request / request mỗi giây | 68.854 / 373 | 70.111 / 375 |
| Lỗi | 0 | 0 |
| p95 nhóm request nhẹ | 314 ms | **175 ms** |
| p95 frontend / API | 331 / 306 ms | **162 / 83 ms** |
| ACU đỉnh | 2,0 | 2,0 |
| ACU trung bình các phút có tải | 1,18 / 1,31 / 1,17 | 1,09 / 1,20 / 1,19 |
| CPU database trung bình các phút có tải | 47% / 51% / 39% | 45% / 49% / 43% |
| CPU database đỉnh | 95% | 93% |
| Kết nối | 20 | 20 |

**Kết luận trung thực:** độ trễ giảm rõ (p95 nhẹ từ 314 xuống 175 ms) nhưng **ACU và CPU database gần như không đổi** (chênh vài phần trăm, nằm trong sai số của một lần đo). Hai thay đổi này chưa giải quyết được mức tải lên database. Chưa tìm ra nguyên nhân: nghi vấn còn lại là các phần chưa xem là `GET /db` (cập nhật một dòng đếm lượt truy cập, mọi user cùng ghi), `POST` tạo ứng viên, chi phí mỗi kết nối/TLS và mức CPU nền 20% ở 0,5 ACU lúc rảnh. Một lần đo mỗi bên, chưa lặp lại.

**(c) Tải database của từng endpoint** (2/11/2026, API 0.5.2, mỗi endpoint đúng 1 phút ở 200 request mỗi giây, qua ALB, cách nhau một phút nghỉ để CloudWatch không trộn số; `docs/results/aurora/aurora-per-endpoint.csv` và `aurora-per-endpoint-one.js`). CPU và ACU lấy từ CloudWatch theo phút tương ứng; lúc rảnh CPU khoảng 21 đến 22%.

| Endpoint | CPU database (trung bình phút đó) | ACU | p95 | Lỗi |
|---|---|---|---|---|
| `/healthz` (không chạm database, đối chứng) | 20,7% | 0,5 | 77 ms | 0% |
| `/stats/summary` (đã cache 5 giây) | 22,5% | 1,0 | 78 ms | 0% |
| `/positions?limit=10` | 29,6% | 2,0 | 121 ms | 0% |
| `/applicants?limit=10` | **55,2%** | 2,0 | 121 ms | 0% |
| `/db` (tăng bộ đếm một dòng) | **58,4%** | 1,5 | 91 ms | 0% |
| `POST /applicants` (ghi một dòng) | **77,3%** | 1,9 | 84 ms | 0% |

- **Kết luận có căn cứ:** các endpoint **ghi** và danh sách ứng viên (có `ILIKE` và 1.122 dòng) tốn CPU database nhiều nhất; `/stats/summary` gần như không tốn gì nhờ cache; `/healthz` là mức nền. Tạo ứng viên ở 200 yêu cầu mỗi giây dùng 77% CPU dù từng câu lệnh chỉ khoảng 1 ms: tải đến từ **số lệnh mỗi giây** chứ không từ độ nặng của câu lệnh.
- ACU cuối mỗi phút còn dư từ phút trước (Aurora giảm chậm), nên cột CPU đáng tin hơn cột ACU khi so sánh các endpoint.
- Chưa chứng minh: một lần đo mỗi endpoint; 200 yêu cầu mỗi giây đồng đều không giống tải người dùng thật; chưa tách riêng nguyên nhân bên trong `/applicants` (đếm, sắp xếp hay `ILIKE`).

## 9. Mất một node spot

> **Mục đích:** Đo người dùng thấy gián đoạn bao lâu và cụm hồi phục sau bao lâu khi một node spot bị lấy lại đột ngột.
> **Cách test:** `scripts/spot-loss-test.sh` (**tắt một EC2 thật**, chỉ chạy khi cụm ổn định). Ghi request frontend và API mỗi 0,5 giây, trạng thái cụm mỗi 3 giây, nhật ký Karpenter.
> **Kết quả KHÔNG chứng minh:** đây là tắt máy đột ngột, không phải thông báo gián đoạn spot 2 phút; pod API không nằm trên node bị tắt thì không chứng minh gì cho API; chưa thử mất cả hai node hệ thống.

Đo ngày 2/11/2026: tắt đột ngột instance `i-093cbbc31ff7a72e3` (node `default` amd64) lúc 14:00:59. Dữ liệu thô `docs/results/spot-loss/spot-loss-probe.csv`, `spot-loss-timeline.csv`, `spot-loss-summary.txt`.

| Chỉ số | Giá trị |
|---|---|
| Khoảng frontend lỗi, số request không phải 200 | **5/151** request frontend lỗi (2 lần 502, 3 lần 503), từ 14:01:01 đến 14:01:07, tức khoảng **6 giây** |
| API | **0/143** request lỗi (`/healthz`; API không nằm trên node bị tắt) |
| Thời gian đến khi frontend trả lại 200 | khoảng **8 giây** sau lúc tắt máy (14:01:07 là request lỗi cuối); frontend còn 1 pod trên node khác nên không mất hẳn |
| Karpenter nhận biết | thông báo gián đoạn tới lúc 14:01:01, khoảng 2 giây sau khi tắt máy; NodeClaim xoá lúc 14:02:18 |
| Thời gian đến khi đủ node và pod giữ chỗ | node về 4 sau khoảng 1 phút; script dừng lúc 14:02:46 (khoảng 1 phút 47 giây). Khi kiểm tra lại sau đó đã đủ **5 node** và cả 3 pod giữ chỗ chạy |
| Pod nào nằm trên node bị tắt | `fe-7dfbf75c57-zgznj` (frontend), `placeholder-amd64` và các pod hệ thống (aws-node, kube-proxy, prepuller, alloy, pod-identity-agent) |

Lưu ý: số node ghi lúc dừng là 4; tôi chưa ghi thời điểm chính xác node thay thế sẵn sàng (thấy đủ 5 ở lần kiểm tra sau).

## 10. Chart Helm với image bất kỳ

> **Mục đích:** Chứng minh cùng chart chạy được image bên thứ ba chỉ bằng cách đổi image, cổng và tham số.
> **Cách test:** `scripts/test-plug-image.sh 2>&1 | tee docs/results/compatibility/plug-image.txt` (nginx, http-echo, hello-app chỉ amd64, và phép thử đối chứng ép image amd64 lên arm64).
> **Kết quả KHÔNG chứng minh:** chỉ dịch vụ HTTP; image đa kiến trúc rơi vào node nào là do Karpenter chọn, nên chưa chắc chạy trên arm64; chưa thử volume, TCP, gRPC, registry riêng, non-root.

Đo ngày 2/11/2026, dữ liệu thô `docs/results/compatibility/plug-image.txt`:

| Image | Kiến trúc phát hiện | Chạy trên | HTTP |
|---|---|---|---|
| `nginx:1.27` (cổng 80) | auto (đa kiến trúc) | node amd64 | 200 |
| `hashicorp/http-echo:latest` (cổng 5678) | auto | node amd64 | 200 |
| `gcr.io/google-samples/hello-app:1.0` (cổng 8080) | amd64 (chỉ có amd64) | node amd64 | 200 |
| Đối chứng: image chỉ amd64 ép lên arm64 | | node arm64 | pod không Ready (`CrashLoopBackOff`, lỗi định dạng thực thi), đúng như mong đợi |

Kết quả script: `RESULT: all checks passed`. Hai image đa kiến trúc rơi vào node amd64 do Karpenter chọn, nên **chưa chứng minh** chúng chạy trên arm64 (xem mục 11 cho ứng dụng của dự án).

## 11. Kiến trúc arm64

> **Mục đích:** Chứng minh pod API, frontend và worker thật chạy được trên node arm64.
> **Cách test:** kiểm tra manifest image trong ECR có `linux/arm64`; ép từng ứng dụng `arch=arm64`, gọi một request/message, ghi node và kiến trúc node.
> **Kết quả KHÔNG chứng minh:** hiệu năng arm64 so với amd64 nếu chưa đo cold start riêng trên arm64.

Đo ngày 2/11/2026 (image tag 0.5.1 trong ECR; ép `arch=arm64` bằng `helm upgrade`, sau đó trả lại `arch=auto`). Dữ liệu thô: `docs/results/compatibility/arm64.txt`.

| Ứng dụng | Image có arm64 | Chạy trên node arm64 | Kết quả |
|---|---|---|---|
| API (`demo-api`) | có (`linux/amd64` và `linux/arm64`) | có, node `ip-10-0-7-94` (arm64) | HTTP 200 sau 2,44 giây (gồm bật API từ 0 pod) |
| Frontend (`demo-web`) | có | có, cùng node arm64 | HTTP 200 sau 0,14 giây |
| Worker (`demo-worker`) | có | có (đo bằng ScaledJob ép `arch=arm64`: Karpenter tạo node `worker` arm64, pod Job chạy được và Score hoàn tất; `server_s` 89,7 giây vì phải tạo node mới) | chưa chụp tên node lúc pod chạy; xác nhận qua `nodeSelector` và node arm64 được tạo |

Lưu ý: hiệu năng arm64 so với amd64 chưa so sánh.

## 12. Hạn chế đã biết (không phải số đo)

- Route 53 chưa làm; chỉ HTTP.
- Frontend và API chỉ có 1 pod nền: mất node là gián đoạn ngắn.
- **Cold start worker (ScaledJob chỉ dùng SQS) không đạt dưới 3 giây:** trung vị 10 giây trở lên ở mọi chu kỳ hỏi đã đo, vì KEDA đọc số đếm hàng đợi SQS bị cũ vài giây. Thêm trigger PostgreSQL và hỏi mỗi 1 giây đạt (trung vị `server_s` 1,66 giây); hỏi mỗi 30 giây thì không (trung vị 6,7 giây). Worker nóng (đã thử, không chọn) cũng đạt.
- Mỗi Score tạo thêm Job rỗng (2 đến 3 Job mỗi lần với trigger PostgreSQL ở chu kỳ 1 giây); chu kỳ hỏi dài giảm Job dư nhưng làm chậm hơn.
- Đợt dồn nhiều Job cùng lúc có thể hết IP của node, pod khởi động chậm 11 đến 40 giây (đã giảm còn khoảng 10% pod chậm bằng kho IP lớn hơn).
- Công việc chấm điểm là giả lập; chưa đo việc xử lý nặng thật (ví dụ 7 triệu dòng).
- Aurora 0,5 đến 2 ACU: khi 200 user cùng lúc, ACU chạm 2,0 và CPU đỉnh khoảng 93 đến 95% (không lỗi); không còn dư địa.
