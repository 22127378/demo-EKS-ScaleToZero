# Nhật ký kiểm thử

Toàn bộ số đo cũ đã được xoá để đo lại từ đầu bằng cấu hình hiện tại. Mỗi bài dưới đây có **mục đích**, **cách test**, **kết quả không chứng minh** và một bảng kết quả đang để trống (`chưa đo`). Điền số vào đúng bảng ngay sau khi đo, và lưu dữ liệu thô vào thư mục `docs/evidence/` hoặc `docs/results/` đã nêu.

## Trước khi đo

- Chạy `aws sts get-caller-identity`: phải ra mã tài khoản. Phiên hết hạn giữa bài làm hỏng dữ liệu.
- Cấu hình hiện tại (ghi lại khi đo, nếu khác thì ghi vào bảng kết quả):
  - API: `scaledownPeriod`, ảnh `demo-api` tag, tối đa 2 pod.
  - Frontend: HPA 1 đến 2 pod, CPU 50%.
  - Worker: pool `default`, `pollingInterval`, `cooldownPeriod`, startup probe, tối đa 20 pod, `queueLength` 5.
  - Image prepuller đã trỏ đúng tag trong ECR.
- Đo trên cụm ổn định (đủ 4 node, mọi pod Ready). Mỗi bài đo ít nhất 5 lượt nếu là bài thời gian.

## Số liệu nằm ở file nào

| Bài | Dữ liệu thô sẽ lưu ở |
|---|---|
| 1. API scale to zero | `docs/evidence/scale-to-zero/` |
| 2. Cold start API | `docs/results/cold-start-api.csv` |
| 3. Cold start worker | `docs/evidence/worker-cold-start/results.csv` |
| 4. Karpenter | `docs/evidence/karpenter/` |
| 5. HPA | `docs/evidence/hpa/` |
| 6. KEDA với SQS | `docs/evidence/keda-sqs/` |
| 7. 200 user cùng lúc | `docs/results/load-*.txt` và `docs/evidence/load-*/` |
| 8. Aurora ACU | `docs/evidence/aurora-acu/` |
| 9. Mất node spot | `docs/evidence/spot-loss/` |
| 10. Chart với image bất kỳ | `docs/evidence/plug-image/result.txt` |
| 11. Kiến trúc arm64 | `docs/evidence/arm64/` |

## 1. API scale to zero

> **Mục đích:** Chứng minh API tự về 0 pod sau thời gian không có request và tự bật lại khi có request mới.
> **Cách test:** để API không nhận request, ghi số pod theo thời gian; gửi một request khi API ở 0 pod, ghi thời gian trả lời; lấy sự kiện KEDA (`KEDAScaleTargetActivated`, `KEDAScaleTargetDeactivated`) và cấu hình `scaledownPeriod`. Lệnh trong [DEMO.md](DEMO.md) phần 3.
> **Kết quả KHÔNG chứng minh:** không chứng minh cold start dưới 3 giây ở mọi lần (xem bài 2).

| Mốc | Thời điểm | Kết quả |
|---|---|---|
| API ở 0 pod, gửi request | chưa đo | chưa đo |
| KEDA bật API lên 1 pod | chưa đo | chưa đo |
| KEDA đưa API về 0 pod (`scaledownPeriod` = chưa ghi) | chưa đo | chưa đo |

## 2. Cold start API

> **Mục đích:** Đo thời gian từ lúc gửi request khi API ở 0 pod đến lúc có phản hồi, so với yêu cầu dưới 3 giây.
> **Cách test:** `COLD_PATH='/positions?limit=10' COLD_JQ='has("items")' scripts/test-cold-start.sh 20 direct docs/results/cold-start-api.csv` (20 lượt, mỗi lượt chờ API về 0). Cần `scaledownPeriod` ngắn để đỡ chờ, rồi trả lại giá trị thật.
> **Kết quả KHÔNG chứng minh:** đo từ một máy, một vị trí mạng; không đại diện cho mọi người dùng. Phần mạng của máy đo (khoảng vài phần trăm kết nối chậm hơn 1 giây) lẫn trong số đo.

Đo ngày 30/10/2026 (giờ VN), qua ALB, `/positions?limit=10`, `scaledownPeriod` 10 giây, API 32 endpoint (image 0.4.0). Dữ liệu thô: `docs/results/cold-start-api.csv`.

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

> **Mục đích:** Đo thời gian từ lúc có message khi worker ở 0 pod đến khi worker xử lý xong, và độ ổn định khi nhiều Job được tạo cùng lúc.
> **Cách test:** (a) Score đơn lẻ qua API khi worker ở 0 pod, đọc `requestedAt` và `doneAt` từ API; (b) đợt dồn: `POST /jobs {"count":100}` rồi đo thời gian từ lúc pod được xếp (`Scheduled`) đến lúc container chạy (`Started`) từ sự kiện Kubernetes lưu trong Loki, kèm lưu lượng API và frontend chạy song song qua ALB.
> **Kết quả KHÔNG chứng minh:** mỗi cấu hình chỉ đo một đến hai lần; chưa thử 20 Job cùng lúc lặp lại nhiều lần; chưa đo trên cụm khác.

**(a) Score đơn lẻ (API đã thức, worker ở 0 pod, có API tạo Job ngay):**

| Cấu hình | Từ lúc yêu cầu đến lúc có điểm |
|---|---|
| Deployment, pool `worker` (chưa có node) | khoảng 55 giây pod Ready (đo trước khi dọn) |
| Deployment, pool `default`, polling 1 giây | 6,2 đến 9,3 giây pod Ready |
| ScaledJob, chưa có API đánh thức | 8,5 đến 9,4 giây |
| **ScaledJob + API tạo Job ngay, pool `default`** | **1,0 đến 1,2 giây** (3 lần), mỗi lần 1 Job |
| ScaledJob + API tạo Job ngay, pool `worker` riêng | 1,25 và 1,86 giây (2 lần) |
| Cả API và worker cùng ngủ (pool `default`) | 3,3 / 5,0 / 3,6 giây từ lúc bấm, phần worker khoảng 1,2 đến 1,6 giây |

**(b) Đợt dồn 100 message:** xem bảng đầy đủ và log trong `docs/evidence/worker-burst/` (`README.md`, `summary-by-burst.txt`, `worker-pod-events.log`).

| Cấu hình | Số pod | Trung vị khởi động | Chậm nhất | Pod trên 3 giây | Lỗi cấp IP |
|---|---|---|---|---|---|
| Pool `default`, kho IP 3, 5 message mỗi Job | 28 | 41 giây | 111 giây | 20 | 97 |
| Kho IP 10 và 12 | 43 | 2 giây | 39 giây | 20 | 44 |
| + 10 message mỗi Job | 28 | 1 giây | 29 giây | 3 | 6 |
| Pool `worker` riêng, kho IP 10 và 12, node mới | 20 | 12 giây | 40 giây | 10 | 21 |
| Pool `worker` riêng, kho IP 10 và 30, node mới | 36 | 2 giây | 30 giây | 9 | 15 |
| **Pool `worker` riêng, kho IP 10 và 30, node ổn định** | 30 | **1 giây** | 30 giây | **3** | **4** |

**Kết luận:** thời gian khởi động pod bình thường dưới 1 giây; phần chậm duy nhất là pod chờ cấp IP khi nhiều Job được tạo cùng lúc (sự kiện `FailedCreatePodSandBox ... failed to assign an IP address`, thử lại khoảng mỗi 12 đến 15 giây). Tăng kho IP và giảm số pod mỗi lượt giảm số pod chậm từ 71% xuống 10%, nhưng chưa về 0. Trong các đợt dồn, frontend và API gần như không lỗi (tối đa 1 request lỗi mỗi bên trong khoảng 110 đến 160 request).

## 4. Karpenter: tầng node

> **Mục đích:** Chứng minh Karpenter tạo node spot mới khi có pod không còn chỗ chạy và dọn node thừa khi hết cần.
> **Cách test:** [DEMO.md](DEMO.md) phần 4 (5 pod, mỗi pod xin 1500m CPU, `rollout status`, rồi xoá namespace); ghi số node, NodeClaim và nhật ký Karpenter (lấy theo label, vì chỉ pod leader ghi sự kiện).
> **Kết quả KHÔNG chứng minh:** không chứng minh xử lý khi spot hết hàng hoặc bị thu hồi (xem bài 9).

| Chỉ số | Giá trị |
|---|---|
| Số node `default` trước, đỉnh, sau | chưa đo |
| Thời gian đến khi cả pod sẵn sàng | chưa đo |
| Thời gian về số node ban đầu sau khi xoá | chưa đo |
| Loại node và kiến trúc được tạo | chưa đo |

## 5. HPA: tầng pod

> **Mục đích:** Chứng minh HPA thêm pod khi CPU vượt ngưỡng 50% và giảm khi hết tải.
> **Cách test:** [DEMO.md](DEMO.md) phần 6 (bản sao API có endpoint `/burn`, tải nhẹ: đốt CPU 1 giây, nghỉ 2 giây). Ghi số pod, CPU và sự kiện `SuccessfulRescale`.
> **Kết quả KHÔNG chứng minh:** frontend nginx dùng rất ít CPU nên không dùng làm bằng chứng HPA; chỉ ghi nhận frontend thật lên 2 pod nếu quan sát được trong bài 7.

| Chỉ số | Giá trị |
|---|---|
| Số pod 1 → ? → ? → về | chưa đo |
| CPU đỉnh so với mức xin | chưa đo |
| Thời gian giữa các bước | chưa đo |

## 6. KEDA với SQS: tầng worker

> **Mục đích:** Chứng minh worker tăng từ 0 lên tối đa 20 pod theo độ dài hàng đợi, không mất message, rồi về 0.
> **Cách test:** gửi 500 message (`POST /jobs`, 5 lần count 100); ghi số worker, node, hàng đợi mỗi vài giây; lấy bộ đếm SQS (gửi, nhận, xoá) và độ dài hàng đợi lỗi. Đồng thời gửi request vào frontend và API để thấy worker có làm chậm hai thành phần này không (worker dùng chung pool `default`).
> **Kết quả KHÔNG chứng minh:** khối lượng công việc thật; worker chỉ chấm điểm giả lập (công thức cố định), không phải xử lý nặng thật.

| Chỉ số | Giá trị |
|---|---|
| Số worker lớn nhất | chưa đo |
| Số node tạo thêm | chưa đo |
| SQS gửi / nhận / xoá | chưa đo |
| Hàng đợi lỗi | chưa đo |
| Thời gian hàng đợi hết, về 0 pod | chưa đo |
| Request frontend/API không phải 200 trong lúc đó | chưa đo |

## 7. 200 user cùng lúc

> **Mục đích:** Chứng minh hệ thống chịu 200 người dùng ảo cùng lúc, và API/frontend không vượt 2 pod.
> **Cách test:** `docker run --rm -v "$PWD/scripts:/s:ro" grafana/k6 run -e BASE_URL=http://$ALB -e VUS=200 -e DURATION=<thời lượng> [-e WITH_SLOW=1] [-e WITH_DB=1] [-e WITH_DOMAIN=1] /s/loadtest.js`. `WITH_SLOW=1` cần để có đủ request đồng thời (mục tiêu 100 mỗi pod); `WITH_DB=1` và `WITH_DOMAIN=1` cần để chạm database. Ghi số pod mỗi 5 giây.
> **Kết quả KHÔNG chứng minh:** không chứng minh trên 200 user; request nhanh chỉ tạo khoảng 10 request đồng thời nên không đủ để API lên 2 pod nếu thiếu `WITH_SLOW`.

| Bài (cờ bật) | Thời lượng | Số request | Request/giây | Trung vị | p95 | Lỗi | API pod tối đa | Frontend pod tối đa |
|---|---|---|---|---|---|---|---|---|
| chưa đo | | | | | | | | |

## 8. Aurora ACU

> **Mục đích:** Cho thấy Aurora co giãn dung lượng trong khoảng 0,5 đến 2 ACU khi có tải chạm database.
> **Cách test:** chạy bài 7 với `WITH_DB=1 WITH_DOMAIN=1`; ngay sau đó đọc CloudWatch `ServerlessDatabaseCapacity`, `CPUUtilization`, `DatabaseConnections` của `demo-aurora` (lệnh trong [DEMO.md](DEMO.md) phần 9).
> **Kết quả KHÔNG chứng minh:** CloudWatch chỉ có độ phân giải 1 phút; chạy không bật gọi database thì ACU đứng yên và không chứng minh gì.

| Phút | ACU | CPU database | Kết nối |
|---|---|---|---|
| chưa đo | | | |

## 9. Mất một node spot

> **Mục đích:** Đo người dùng thấy gián đoạn bao lâu và cụm hồi phục sau bao lâu khi một node spot bị lấy lại đột ngột.
> **Cách test:** `scripts/spot-loss-test.sh` (**tắt một EC2 thật**, chỉ chạy khi cụm ổn định). Ghi request frontend và API mỗi 0,5 giây, trạng thái cụm mỗi 3 giây, nhật ký Karpenter.
> **Kết quả KHÔNG chứng minh:** đây là tắt máy đột ngột, không phải thông báo gián đoạn spot 2 phút; pod API không nằm trên node bị tắt thì không chứng minh gì cho API; chưa thử mất cả hai node hệ thống.

| Chỉ số | Giá trị |
|---|---|
| Khoảng frontend lỗi, số request không phải 200 | chưa đo |
| Thời gian đến khi frontend trả lại 200 | chưa đo |
| Thời gian đến khi đủ node và pod giữ chỗ | chưa đo |
| Pod nào nằm trên node bị tắt | chưa ghi |

## 10. Chart Helm với image bất kỳ

> **Mục đích:** Chứng minh cùng chart chạy được image bên thứ ba chỉ bằng cách đổi image, cổng và tham số.
> **Cách test:** `scripts/test-plug-image.sh 2>&1 | tee docs/evidence/plug-image/result.txt` (nginx, http-echo, hello-app chỉ amd64, và phép thử đối chứng ép image amd64 lên arm64).
> **Kết quả KHÔNG chứng minh:** chỉ dịch vụ HTTP; image đa kiến trúc rơi vào node nào là do Karpenter chọn, nên chưa chắc chạy trên arm64; chưa thử volume, TCP, gRPC, registry riêng, non-root.

| Image | Kiến trúc phát hiện | Chạy trên | HTTP |
|---|---|---|---|
| chưa đo | | | |

## 11. Kiến trúc arm64

> **Mục đích:** Chứng minh pod API, frontend và worker thật chạy được trên node arm64.
> **Cách test:** kiểm tra manifest image trong ECR có `linux/arm64`; ép từng ứng dụng `arch=arm64`, gọi một request/message, ghi node và kiến trúc node.
> **Kết quả KHÔNG chứng minh:** hiệu năng arm64 so với amd64 nếu chưa đo cold start riêng trên arm64.

| Ứng dụng | Image có arm64 | Chạy trên node arm64 | Kết quả |
|---|---|---|---|
| chưa đo | | | |

## 12. Hạn chế đã biết (không phải số đo)

- Route 53 chưa làm; chỉ HTTP.
- Frontend và API chỉ có 1 pod nền: mất node là gián đoạn ngắn.
- Vụ 3 pod worker mỗi lần Score chưa giải quyết: cần theo dõi một lần bấm khi worker đang 0 pod.
- Worker chạy ở pool `default` cần bài hồi quy đầy đủ (bài 6) vì chung chỗ với API và frontend.
