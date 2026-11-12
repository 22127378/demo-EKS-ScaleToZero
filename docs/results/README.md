# Số liệu đo (docs/results)

Mỗi thư mục là một nhóm bài đo. Kết quả đã được đưa vào [../BAO_CAO.md](../BAO_CAO.md) (chương 5); cách đọc từng cột và từng dòng nằm ở Phụ lục B của báo cáo. Giờ trong các file là giờ máy đo (UTC+7), trừ khi ghi khác.

## api/ (cold start của API, trigger CPU)
| File | Mục trong báo cáo | Ghi gì |
|---|---|---|
| `cold-start-api.csv` | 5.1 | 20 lượt cold start API (30/10/2026): mỗi dòng một lượt. `total_s` tổng thời gian request, `connect_s` thời gian mở kết nối TCP, `ttfb_s` đến byte đầu tiên, `pod_created_after_request_s` pod được tạo sau request bao lâu, `created_to_scheduled_s` và `created_to_ready_s` từ lúc tạo pod đến lúc được xếp và đến lúc Ready (độ phân giải 1 giây). Lượt 12 không có dữ liệu pod |
| `keda-http-cpu.txt` | 5.8 | Thử trigger CPU của API: giờ bắt đầu và kết thúc tải (10 user gọi `/burn`), sự kiện HPA (kích thước mới 2 vì CPU), rồi số pod API khoảng 7 giây một lần |
| `keda-http-cpu-burn.js` | 5.8 | Kịch bản k6 đã dùng: 10 user gọi `GET /burn?ms=1000`, để số request đồng thời thấp mà CPU cao |

## worker/ (cold start worker, nhiều phiếu, hồ sơ mồ côi)
Cột của các file cold start: `api_reply_s` thời gian API trả lời lệnh Score; `total_s` từ lúc bấm đến lúc điểm hiện (đồng hồ máy đo, gồm mạng); `server_s` thời gian xử lý trong hệ thống (`doneAt` trừ `requestedAt` trong database); `keda_job_after_s` từ lúc bấm đến lúc Job đầu tiên xuất hiện (độ phân giải 1 giây, có thể hơi âm); `jobs_created` số Job cho một lần Score (1 là lý tưởng, nhiều hơn là Job rỗng dư).

| File | Mục trong báo cáo | Thuộc về | Chu kỳ hỏi | Ghi gì |
|---|---|---|---|---|
| `worker-cold-start-keda-poll30.csv` | 5.2 | hệ thống (KEDA với SQS) | 30 giây | ScaledJob chỉ có trigger SQS, 20 lượt (2/11/2026). Dựng lại từ số liệu đã in lúc đo; theo nhật ký cũ, chuỗi này không có nghỉ ngẫu nhiên trước mỗi lượt. Trung vị thời gian xử lý 12,19 giây, 0/20 dưới 3 giây, 6 Job dư. Bản hỏi mỗi 1 giây (bản demo) chỉ còn số tổng hợp trong báo cáo |
| `worker-cold-start-keda-postgres.csv` | 5.5 | đề xuất A (chưa duyệt, chỉ để tham khảo) | 1 giây | thêm trigger PostgreSQL vào cùng ScaledJob, 20 lượt (3/11/2026). Trung vị 1,66 giây, 20/20 dưới 3 giây, 23 Job dư |
| `worker-cold-start-keda-postgres-split.csv` | 5.5 | đề xuất B (chưa duyệt, chỉ để tham khảo) | PostgreSQL 1 giây, SQS 60 giây | tách thành hai ScaledJob, 20 lượt (3/11/2026). Trung vị 1,78 giây, 20/20 dưới 3 giây, mọi lượt đúng 1 Job (0 Job dư) |
| `worker-cold-start-split-10.csv` | 5.5 | đề xuất B | PostgreSQL 1 giây, SQS 60 giây | cùng đề xuất, 10 lượt (4/11/2026). Trung vị 1,39 giây, 10/10 dưới 3 giây, mọi lượt đúng 1 Job |
| `worker-cold-start-keda-postgres-poll30.csv` | 5.5 | đề xuất A | 30 giây | cùng đề xuất, hỏi 30 giây (3/11/2026). Trung vị 6,74 giây, 2/20 dưới 3 giây, 3 Job dư |
| `fake-backlog-orphan.txt` | 5.5 | đề xuất A | 30 giây | 3 hồ sơ chờ chấm không có phiếu trong SQS: nhật ký các Job rỗng xuất hiện (3/11/2026) |

## nodes-and-pods/ (Karpenter và HPA)
| File | Mục trong báo cáo | Ghi gì |
|---|---|---|
| `karpenter.txt` | 5.7 | Tạo 5 pod xin 1500m CPU: số máy nhóm `default` và số pod sẵn sàng mỗi khoảng 7 giây, giờ tạo, giờ xong (37 giây), giờ xóa, máy về 2 sau 126 giây |
| `hpa.txt` | 5.8 | Bản sao API với HPA 1 đến 3 pod: mỗi dòng giờ, số bản HPA muốn (`replicas`) và số pod Running; cuối file là các sự kiện `SuccessfulRescale`. Cột phần trăm CPU bị cắt khi lấy mẫu nên chỉ hiện `cpu:` |

## load-200-users/ (200 người dùng cùng lúc)
Chỉ giữ lần đo sau khi gộp truy vấn đếm và thêm bộ nhớ đệm cho thống kê.
| File | Mục trong báo cáo | Ghi gì |
|---|---|---|
| `load-200-vus-after.txt` | 5.9 | Đầu ra k6 đầy đủ: ngưỡng p95 và p99 đạt, tổng 70.111 request, 0 lỗi, thời gian trả lời (trung vị, p90, p95) và kết quả từng kiểm tra |
| `load-200-pods-after.csv` | 5.9 | Số pod API và frontend khoảng 7 giây một lần trong lúc chạy (tối đa 2 mỗi bên) |

## aurora/ (cơ sở dữ liệu)
| File | Mục trong báo cáo | Ghi gì |
|---|---|---|
| `aurora-cloudwatch-before.txt` | 5.10 | CloudWatch quanh lần chạy 200 người dùng đầu (trước tối ưu): mỗi phút ACU, CPU, số kết nối (trung bình và cao nhất) |
| `aurora-cloudwatch-after.txt` | 5.10 | Như trên, quanh lần chạy sau tối ưu |
| `aurora-explain-analyze.txt` | 5.10 | `EXPLAIN ANALYZE` trên cơ sở dữ liệu thật (1.122 ứng viên, 15 vị trí, 340 hồ sơ): mọi truy vấn dưới 1 mili giây |
| `aurora-per-endpoint.csv` | 5.10 | Tải database của từng endpoint ở 200 request/giây trong 1 phút: CPU trung bình, ACU, kết nối, số request, tốc độ, p95, lỗi |
| `aurora-per-endpoint-one.js` | 5.10 | Kịch bản k6 dùng cho file trên (gọi đúng một endpoint với tốc độ cố định) |

## spot-loss/ (mất một máy spot)
| File | Mục trong báo cáo | Ghi gì |
|---|---|---|
| `spot-loss-summary.txt` | 5.11 | Máy và instance bị tắt, giờ tắt (14:00:59), giờ kết thúc, số request lỗi (frontend 5/151, API 0/143) và các dòng log của Karpenter (giờ UTC) |
| `spot-loss-probe.csv` | 5.11 | Mỗi request của frontend và API trong lúc tắt máy: giờ, đối tượng (`fe` hoặc `api`), mã HTTP, thời gian. 5 dòng 502 hoặc 503 là gián đoạn của frontend |
| `spot-loss-timeline.csv` | 5.11 | Trạng thái cụm khoảng 10 giây một lần: máy sẵn sàng, pod frontend, pod API, pod giữ chỗ |

## compatibility/ (image bất kỳ và arm64)
| File | Mục trong báo cáo | Ghi gì |
|---|---|---|
| `plug-image.txt` | 5.12 | Đầu ra `scripts/test-plug-image.sh`: nginx, http-echo, hello-app đều HTTP 200; phép đối chứng (image chỉ amd64 ép lên arm64) không chạy, đúng như mong đợi |
| `arm64.txt` | 5.12 | Image api, web, worker có manifest `linux/arm64`; ép API và frontend lên máy arm64 và request thành công (worker chưa đo lại) |
