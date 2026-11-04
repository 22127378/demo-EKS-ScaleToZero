# Số liệu đo (docs/results)

Mỗi thư mục là một nhóm bài trong [../TEST_LOG.md](../TEST_LOG.md). Giờ trong các file là giờ máy đo (UTC+7) ngày 2/11/2026, trừ khi ghi khác. Dưới đây là mỗi file ghi gì.

## api/ (API về 0 pod, cold start, thêm pod theo CPU)
| File | Bài | Ghi gì |
|---|---|---|
| `cold-start-api.csv` | 2 | 20 lượt cold start API (30/10/2026): mỗi dòng một lượt. `total_s` tổng thời gian request, `connect_s` thời gian mở kết nối TCP, `ttfb_s` đến byte đầu tiên, `pod_created_after_request_s` pod được tạo sau request bao lâu, `created_to_scheduled_s`/`created_to_ready_s` từ lúc tạo pod đến lúc được xếp và đến lúc Ready (độ phân giải 1 giây). |
| `scale-to-zero.txt` | 1 | Một lượt API về 0 pod: request lúc API 0 pod (1,79 giây), request tiếp theo (0,14 giây), API về 0 pod sau 5 phút, hai sự kiện KEDA `Activated`/`Deactivated`. Đây là **bản rút gọn** (ghi chú trong file). |
| `keda-http-cpu.txt` | 5b | Thử trigger CPU của API: giờ bắt đầu và kết thúc tải (10 user gọi `/burn`), sự kiện HPA (`New size: 2` vì CPU), rồi số pod API và số liệu HPA mỗi khoảng 7 giây. |
| `keda-http-cpu-burn.js` | 5b | Kịch bản k6 đã dùng: 10 user gọi `GET /burn?ms=1000`, để số request đồng thời thấp mà CPU cao. |

## worker/ (cold start worker và KEDA SQS)
| File | Bài | Ghi gì |
|---|---|---|
| `worker-cold-start-warm.csv` | 3 (phương án so sánh, không chọn) | 20 lượt bấm Score khi có **worker nóng** (Deployment luôn 1 pod; phương án đã thử, không chọn). `api_reply_s` API trả lời lệnh Score; `total_s` từ lúc bấm đến lúc điểm hiện (gồm mạng); `server_s` `doneAt` trừ `requestedAt` trong database; `keda_job_after_s` để trống và `jobs_created` bằng 0 vì không còn Job. |
| `keda-sqs-3000.csv` | 6 (phương án so sánh, không chọn) | Gửi 3.000 message (mỗi message 1 giây xử lý), mỗi khoảng 7 giây một dòng: `visible` message đang chờ, `inflight` đang được xử lý, `worker_pods` số pod worker, `worker_nodes` số node worker. |
| `worker-cold-start-keda-postgres.csv` | 3 | ScaledJob với **trigger PostgreSQL** (đếm hồ sơ `queued`) cộng trigger SQS, KEDA hỏi mỗi **1 giây**, 20 lượt, có nghỉ ngẫu nhiên trước mỗi lượt. Cùng các cột với file worker nóng. Trung vị `server_s` 1,66 giây, 20/20 dưới 3 giây, 0/20 lượt đúng 1 Job (Job rỗng dư vẫn còn). |
| `worker-cold-start-keda-postgres-poll30.csv` | 3 | Như trên nhưng KEDA hỏi mỗi **30 giây**. Trung vị `server_s` 6,74 giây (rải từ 1,7 đến 30,4 giây), 17/20 lượt đúng 1 Job. |
| `worker-cold-start-keda-postgres-split.csv` | 3 | Thử nghiệm **tách 2 ScaledJob**: `worker` (trigger PostgreSQL, hỏi mỗi 1 giây) và `worker-sqs` (trigger SQS, hỏi mỗi 60 giây, lưới an toàn), 20 lượt, có nghỉ ngẫu nhiên. Trung vị `server_s` 1,78 giây, 20/20 dưới 3 giây, **20/20 lượt đúng 1 Job**. Cấu hình này đã được gỡ về 1 ScaledJob sau khi đo; script chỉ chờ 25 giây trước khi đếm Job nên Job dư muộn của `worker-sqs` có thể không được tính. |
| `worker-cold-start-keda-poll10.csv` | 3 | Worker ScaledJob (về 0 pod), thiết kế đang dùng: KEDA hỏi hàng đợi mỗi **10 giây**, 20 lượt. Cùng các cột với file worker nóng; `keda_job_after_s` là từ lúc bấm đến lúc Job đầu tiên xuất hiện, `jobs_created` là số Job cho một lần Score (1 là lý tưởng, nhiều hơn là Job rỗng dư). Trung vị `server_s` 17,35 giây, 5/20 lượt đúng 1 Job, 0/20 dưới 3 giây. |
| `worker-cold-start-keda-poll30.csv` | 3 | Như trên với chu kỳ **30 giây**, 20 lượt. Trung vị `server_s` 12,19 giây (có 4 lượt khoảng 42 giây do lỡ một nhịp hỏi), 14/20 lượt đúng 1 Job, 0/20 dưới 3 giây. |

> Hai file `poll10/30` (ScaledJob) được dựng lại từ số liệu đã in ra khi đo (file gốc bị xoá lúc gom kết quả), không đo lại vì cụm đã huỷ. Thống kê tính lại từ file khớp với số đã ghi lúc đó. Lần đo chu kỳ **1 giây** (20 lượt) và lần đo 500 message của bài 6 không còn file thô; chỉ còn số tổng hợp trong TEST_LOG.md.

## nodes-and-pods/ (Karpenter và HPA)
| File | Bài | Ghi gì |
|---|---|---|
| `karpenter.txt` | 4 | Tạo 5 pod xin 1500m CPU: số node `default` và số pod sẵn sàng mỗi 5 giây, giờ tạo Deployment, giờ xong (37 giây), giờ xoá namespace, node về 2 sau 126 giây. |
| `hpa.txt` | 5 | Bản sao API với HPA 1 đến 3 pod: mỗi dòng giờ, số bản sao HPA (`replicas`) và số pod Running; cuối file là các sự kiện `SuccessfulRescale`. Cột phần trăm CPU bị cắt khi lấy mẫu nên chỉ hiện `cpu:`. |

## load-200-users/ (200 user cùng lúc)
Chỉ giữ lần đo **sau** khi gộp truy vấn đếm và cache thống kê.
| File | Bài | Ghi gì |
|---|---|---|
| `load-200-vus-after.txt` | 7 | Đầu ra k6 đầy đủ: ngưỡng p95/p99 đạt, tổng 70.111 request, 0 lỗi, thời gian trả lời (trung vị, p90, p95) và kết quả từng kiểm tra. |
| `load-200-pods-after.csv` | 7 | Số pod API và frontend mỗi 5 giây trong lúc chạy (tối đa 2 mỗi bên). |

## aurora/ (bài 8)
| File | Ghi gì |
|---|---|
| `aurora-cloudwatch-before.txt` | CloudWatch quanh lần đo 200 user **đầu** (trước tối ưu): mỗi phút ACU, CPU, số kết nối (cột: trung bình, đỉnh). |
| `aurora-cloudwatch-after.txt` | Như trên, quanh lần đo 200 user **sau** tối ưu. |
| `aurora-explain-analyze.txt` | `EXPLAIN ANALYZE` trên database thật (1.122 ứng viên, 15 vị trí, 340 hồ sơ): mọi truy vấn dưới 1 ms. |
| `aurora-per-endpoint.csv` | Tải database của từng endpoint ở 200 request/giây trong 1 phút: CPU trung bình, ACU, kết nối, số request, tốc độ, p95, lỗi. |
| `aurora-per-endpoint-one.js` | Kịch bản k6 dùng cho file trên (gọi đúng một endpoint với tốc độ cố định). |

## spot-loss/ (bài 9, tắt node spot)
| File | Ghi gì |
|---|---|
| `spot-loss-summary.txt` | Node và instance bị tắt, giờ tắt (14:00:59), giờ kết thúc, số request lỗi (frontend 5/151, API 0/143) và các dòng Karpenter (nhận thông báo gián đoạn, xoá NodeClaim). |
| `spot-loss-probe.csv` | Mỗi request của frontend và API trong lúc tắt máy: giờ, đối tượng (`fe`/`api`), mã HTTP, thời gian. 5 dòng 502/503 là gián đoạn của frontend. |
| `spot-loss-timeline.csv` | Trạng thái cụm mỗi khoảng 10 giây: node Ready, pod frontend Running/Pending, pod API, pod giữ chỗ. |

## compatibility/ (bài 10 và 11)
| File | Ghi gì |
|---|---|
| `plug-image.txt` | Đầu ra script `test-plug-image.sh`: nginx, http-echo, hello-app đều HTTP 200; phép đối chứng (image chỉ amd64 ép lên arm64) không chạy, đúng như mong đợi. |
| `arm64.txt` | Image api/web/worker có manifest `linux/arm64`; ép API và frontend lên node arm64 và request thành công (worker chưa đo lại với thiết kế worker nóng). |
