# Báo cáo: Co giãn tự động trên EKS spot

Hệ thống tuyển dụng nhỏ chạy trên AWS EKS, dùng toàn bộ node spot. API về 0 pod khi không dùng, worker chạy theo hàng đợi SQS bằng KEDA ScaledJob, node tăng giảm theo nhu cầu.

**Toàn bộ số đo cũ đã được xoá để đo lại từ đầu.** Các mục kết quả bên dưới để trống (`chưa đo`) cho đến khi có số đo mới trong [TEST_LOG.md](TEST_LOG.md). Phần mô tả hệ thống và cấu hình (mục 2 đến 5) là cấu hình đang chạy, không phải kết quả đo.

## 1. Mục tiêu

| Yêu cầu | Trạng thái |
|---|---|
| API Go từ 20 đến 50 endpoint, giao diện React | Có 32 endpoint miền tuyển dụng và `/slow` (33 route) |
| ALB phía trước, chạy trên EKS | Một ALB chung cho API và frontend |
| Toàn bộ node là spot, có cả amd64 và arm64 | Đạt: API và frontend chạy trên arm64 (bài 11); node spot cả hai kiến trúc (bài 4) |
| Ba tầng co giãn: Karpenter, KEDA với SQS, HPA | Đạt: node 2→4→2 (bài 4); HPA 1→3→1 (bài 5); worker 1→20→1 (bài 6) |
| API scale to zero, cold start dưới 3 giây | Đạt: API về 0 sau 5 phút; cold start 1,79 giây (bài 1); 20/20 lượt dưới 3 giây phía hệ thống (bài 2) |
| Worker bắt đầu xử lý dưới 3 giây | **Đạt khi thêm trigger PostgreSQL và KEDA hỏi mỗi 1 giây:** trung vị `server_s` 1,66 giây, 20/20 dưới 3 giây (bài 3). ScaledJob chỉ dùng SQS không đạt (trung vị 10 giây trở lên); worker nóng đạt (0,39 giây) nhưng không chọn |
| Chịu được 200 user cùng lúc | Đạt: 0 lỗi trên 68.854 request, API và frontend tối đa 2 pod (bài 7) |
| Aurora Serverless v2, 0,5 đến 2 ACU, không tạm dừng | Đạt: ACU 0,5 → đỉnh 2,0 (bài 8); hết dư địa ở 200 user |
| API thêm pod theo CPU (KEDA HTTP + trigger CPU) | Đạt: 10 user đốt CPU làm API lên 2 pod sau khoảng 21 giây (bài 5b) |
| Chart Helm dùng được với image bất kỳ | Đạt: 3 image HTTP, đối chứng sai kiến trúc đúng như mong đợi (bài 10) |
| Số pod tối đa: API 2, frontend 2 | Đạt (bài 7) |
| Chịu mất node spot | Đạt một phần: frontend lỗi 5/151 request trong khoảng 6 giây, API 0 lỗi (bài 9) |
| Route 53 | **Chưa làm** (chưa có tên miền) |

## 2. Kiến trúc

```
Người dùng
    |
   ALB  ──(Host: api.demo.local)──►  Interceptor KEDA ──► API (0 đến 2 pod) ──► Aurora Serverless v2
    |                                                          |
    └──────────────────────────────► Frontend nginx (1 đến 2 pod)
                                                               └──► SQS ──► Worker (KEDA ScaledJob: 0 đến 20 Job)
```

- **Ứng dụng:** API Go, frontend React phục vụ bằng nginx, worker Go chấm điểm hồ sơ.
- **Hạ tầng (Terraform):** EKS 1.33, Aurora PostgreSQL 17.9, SQS kèm hàng đợi lỗi (DLQ), ECR, VPC 10.0.0.0/16 trên 3 vùng khả dụng, vùng `ap-southeast-1`.
- **Chart Helm `charts/app`:** một chart dùng chung cho API, frontend và worker; đổi chế độ co giãn bằng một giá trị (`hpa`, `http`, `sqs`, `none`).
- **Truy cập:** địa chỉ DNS của ALB, chỉ HTTP. Chưa có HTTPS và chưa có Route 53 vì chưa có tên miền; ALB mở cho mọi địa chỉ.

## 3. Ứng dụng

| Thành phần | Chi tiết |
|---|---|
| API | Go, 32 route miền tuyển dụng (vị trí, ứng viên, hồ sơ, chấm điểm, thống kê) cùng `GET /slow` phục vụ bài tải. Cổng 8080, kiểm tra sức khoẻ `/healthz`, không dùng startup probe |
| Frontend | React (Vite, TypeScript), một trang tuyển dụng. Mỗi thao tác hiện thời gian trả lời và gắn nhãn cold start hoặc warm; bấm Score thì có đồng hồ riêng đến lúc worker ghi điểm |
| Worker | Go, nhận tối đa 10 message mỗi lần (long polling 20 giây), chấm điểm ngay (không còn độ trễ giả lập; biến `PROCESS_TIME` chỉ dùng khi thử nghiệm) rồi ghi điểm và xoá message; lỗi thì không xoá, sau 3 lần thất bại vào DLQ |
| Điểm hồ sơ | Điểm giả lập: `50 + (mã hồ sơ × 37 mod 51)`, không phải mô hình thật |

**Tài nguyên mỗi pod:**
| Pod | CPU xin | RAM xin | RAM tối đa |
|---|---|---|---|
| API | 250m | 64 MiB | 256 MiB |
| Frontend | 100m | 64 MiB | 256 MiB |
| Worker | 100m | 64 MiB | 256 MiB |

**Kích thước image** (ECR, đã nén, từng kiến trúc; chưa ghép từng giá trị với từng kiến trúc):
| Image | Kích thước |
|---|---|
| `demo-api:0.3.4` | khoảng 5,1 đến 5,5 MB |
| `demo-worker:0.3.0` | khoảng 5,1 đến 5,5 MB |
| `demo-web:0.3.4` | khoảng 20,1 đến 20,9 MB |

## 4. Hạ tầng và cấu hình co giãn

**Node (toàn bộ spot):**
| Nhóm | Số node | Loại | Ghi chú |
|---|---|---|---|
| Hệ thống (EKS managed) | 2, cố định | `t3.medium` (cho phép `t3a.medium`, `t3.large`, `m5.large`) | chạy Karpenter, KEDA, CoreDNS, bộ điều khiển ALB |
| Karpenter `default` | tối thiểu 2 (1 amd64 + 1 arm64), tăng theo nhu cầu | t3, t4g, c7g, m7g... | giới hạn 16 vCPU; chạy API, frontend, worker và pod giữ chỗ |
| Karpenter `worker` | 0 | | giới hạn 12 vCPU; hiện không dùng (xem 6.5) |

Karpenter 1.14.1, gom node thừa sau 1 phút (`consolidateAfter`).

**Ba tầng co giãn:**
| Tầng | Công cụ | Áp dụng cho | Co giãn theo | Giới hạn | Cấu hình đang chạy |
|---|---|---|---|---|---|
| Node | Karpenter | mọi pod không còn chỗ chạy | pod chưa xếp được | `default` 16 vCPU | |
| Pod | KEDA HTTP Add-on | API | request đồng thời, mục tiêu 100 mỗi pod | 0 đến 2 pod | `scaledownPeriod` mặc định 300 giây, **đang đặt 10 giây** cho demo |
| Pod | HPA | Frontend | CPU 50% so với mức pod xin | 1 đến 2 pod | cửa sổ ổn định khi giảm 300 giây |
| Pod | KEDA ScaledJob với SQS | Worker | độ dài hàng đợi: mỗi Kubernetes Job lấy tối đa 10 message rồi kết thúc | 0 đến 20 Job chạy cùng lúc | `pollingInterval` 1 giây, `activeDeadlineSeconds` 300, chạy ở pool `worker` riêng (4 vCPU, có pod giữ chỗ riêng) |

**Cơ sở dữ liệu:** Aurora Serverless v2, 0,5 đến 2 ACU, không tạm dừng. **Hàng đợi:** SQS `demo-jobs`, thời gian ẩn message 60 giây, tối đa 3 lần nhận trước khi vào DLQ.

**Giá trị demo cần trả lại:** API `scaledownPeriod` về 300 nếu đã hạ xuống cho demo nhanh; worker giữ `pollingInterval` 1 giây, frontend giữ 300.

### 4.1 Tài nguyên từng dịch vụ

Chụp trên cụm đang chạy, lúc API và worker ở 0 pod (số liệu này chưa lưu lại trong kho mã). "Xin" là mức Kubernetes giữ chỗ cho pod; "dùng thực" là mức đang dùng (`kubectl top`), chỉ là một lần chụp, không phải trung bình.

| Dịch vụ | Số pod | CPU xin | RAM xin | CPU dùng thực | RAM dùng thực |
|---|---|---|---|---|---|
| Karpenter | 2 | 200m | 512 MiB | 15m | khoảng 177 MiB |
| KEDA (operator, metrics, webhook, HTTP add-on) | 9 | 350m | 464 MiB | khoảng 25m | khoảng 172 MiB |
| aws-load-balancer-controller | 2 | 100m | 128 MiB | 4m | 53 MiB |
| aws-node (VPC CNI) | 4 | 200m | không đặt | 16m | 219 MiB |
| CoreDNS | 2 | 200m | 140 MiB | 3m | 23 MiB |
| kube-proxy | 4 | 400m | không đặt | 4m | 53 MiB |
| eks-pod-identity-agent | 4 | 0 | 0 | 4m | 37 MiB |
| metrics-server | 2 | 50m | 96 MiB | 6m | 42 MiB |
| image-prepuller | 2 | 2m | 8 MiB | 0 | 0 |
| Pod giữ chỗ (amd64 + arm64) | 2 | **1000m** | **1024 MiB** | 0 | 0 |
| Frontend | 1 | 100m | 64 MiB | 1m | 3 MiB |
| `hpa-demo` (bản sao còn sót từ bài HPA) | 1 | 250m | 64 MiB | 1m | 4 MiB |
| **Tổng** | | **khoảng 2850m** | **khoảng 2500 MiB** | **khoảng 80m** | **khoảng 780 MiB** |

- Dùng thực rất nhỏ so với mức xin; phần lớn tài nguyên bị giữ chỗ chứ không dùng.
- Pod giữ chỗ chiếm khoảng một phần ba CPU xin và 1 GiB RAM xin dù không làm gì: đó là cái giá của chỗ trống dự phòng.
- Hạ tầng hệ thống (Karpenter, KEDA, kube-system) xin khoảng 1,5 vCPU trên hai node hệ thống `t3.medium` (tổng 4 vCPU).
- Bảng chỉ có CPU và RAM, không phải chi phí tiền theo từng dịch vụ (xem mục 7).

### 4.2 Giám sát và log

| Thành phần | Vai trò | Nơi chạy |
|---|---|---|
| Prometheus + kube-state-metrics | số liệu pod, node, HPA, KEDA (operator, metrics, HTTP interceptor), Karpenter; giữ 6 giờ, không ổ đĩa | node hệ thống |
| Grafana | giao diện xem số liệu và log, chỉ truy cập qua port-forward | node hệ thống |
| Loki (một bản) | nhận log, ghi vào bucket S3 `demo-logs-<mã tài khoản>` (mã hoá, chặn truy cập công khai, xoá sau 7 ngày) | node hệ thống |
| Alloy (DaemonSet) | đọc log container của `demo`, `keda`, `karpenter` trên từng node, kể cả node Karpenter | mọi node |
| Alloy (sự kiện) | chuyển sự kiện Kubernetes (Kubernetes chỉ giữ khoảng 1 giờ) thành log | node hệ thống |

Đã kiểm tra thông suốt: tất cả mục thu thập của Prometheus ở trạng thái `up`, số liệu `keda_scaler_*` có mặt, Loki nhận log của `demo` (782 dòng trong 10 phút lúc kiểm tra) và 81 sự kiện. Log của KEDA và Karpenter ít (hai thành phần này im lặng khi không co giãn) nên có thể trống trong một cửa sổ ngắn.

Hạn chế: Loki chạy một bản không lưu ổ đĩa, nên log chưa ghi lên S3 mất khi pod Loki bị khởi động lại; chưa có số liệu AWS (SQS, Aurora ACU, ALB) vì chưa có CloudWatch exporter; chưa có dashboard dựng sẵn, hiện xem bằng Explore. Chi phí tài nguyên của bộ này chưa đo (ước tính xin khoảng 0,3 vCPU và 0,9 GiB RAM, cộng Alloy khoảng 20m CPU và 64 MiB mỗi node).

### 4.3 Dải địa chỉ mạng

Đọc từ cụm đang chạy (vùng `ap-southeast-1`). Pod dùng IP thật của VPC (VPC CNI của AWS), không có dải IP pod riêng.

| Thành phần | Dải | Số địa chỉ | Dùng cho |
|---|---|---|---|
| VPC | `10.0.0.0/16` | khoảng 65.000 | toàn bộ cụm |
| Subnet riêng, `ap-southeast-1a` | `10.0.0.0/20` | 4.096 (đang trống 4.056) | node và **pod** |
| Subnet riêng, `ap-southeast-1b` | `10.0.16.0/20` | 4.096 (đang trống 4.073) | node và **pod** |
| Subnet riêng, `ap-southeast-1c` | `10.0.32.0/20` | 4.096 (đang trống 4.072) | node và **pod** |
| Subnet công cộng, 3 vùng | `10.0.200.0/24`, `10.0.201.0/24`, `10.0.202.0/24` | 256 mỗi subnet | ALB và NAT gateway |
| Địa chỉ dịch vụ (ClusterIP) | `172.20.0.0/16` | | địa chỉ ảo của Service, không phải IP pod |

Subnet vẫn còn khoảng 4.000 địa chỉ trống mỗi vùng, nên **không phải subnet hết IP**. Giới hạn nằm ở **số IP mà từng node giữ được** (giải thích dưới đây).

**Giới hạn IP của từng node.** Mỗi pod cần một IP riêng. Mỗi loại EC2 chỉ gắn được một số card mạng ảo (ENI), và mỗi ENI chỉ có một số IP:
| Loại node | ENI | IP mỗi ENI | IP phụ cho pod | Số pod tối đa |
|---|---|---|---|---|
| `t3.medium` | 3 | 6 | 15 | 17 |
| `c6g.large` | 3 | 10 | 27 | 29 |

Node giữ sẵn một "kho" IP trống (`WARM_IP_TARGET`, hiện 10; `MINIMUM_IP_TARGET`, hiện 12). Pod mới lấy IP từ kho ngay lập tức. Khi kho cạn (ví dụ dồn nhiều pod worker cùng lúc), node phải gọi API của EC2 để xin thêm IP, mất vài giây; trong lúc đó pod không có mạng và Kubernetes thử lại sau khoảng 10 giây, nên pod chờ 11 đến 40 giây (lỗi `failed to assign an IP address to container`). Biện pháp đã làm: tăng kho IP dự phòng (3 lên 10) và cho mỗi Job lấy 10 message thay vì 5 để tạo ít pod hơn. Cách còn lại: dùng node lớn hơn (nhiều IP hơn) hoặc bật prefix delegation.

## 5. Giảm thời gian chờ: chỗ trống dự phòng (over-provisioning)

Tạo một node mới mất thời gian đáng kể, nên hệ thống giữ sẵn chỗ trống (hiệu quả cần đo lại):

- **Hai node spot luôn chạy** thuộc pool `default`: một amd64 và một arm64. Mỗi node có một pod giữ chỗ (`placeholder`, image `pause`, xin 500m CPU và 512 MiB RAM, độ ưu tiên -10, không bị Karpenter gom).
- **Cách hoạt động:** khi chỗ trống hết, pod thật đẩy pod giữ chỗ ra, pod giữ chỗ chưa xếp được khiến Karpenter tạo node thay, nên chỗ trống tự được bù lại.
- **Tải sẵn image:** DaemonSet `image-prepuller` chạy image API, worker và frontend trên mọi node Karpenter. Tag trong `prepull_images` phải khớp với ECR.
- **Bỏ startup probe của API và worker.**
- **VPC CNI `WARM_IP_TARGET=3`:** node giữ sẵn địa chỉ IP.
- **Interceptor của KEDA giữ request** trong lúc API bật từ 0 lên 1, nên người dùng chỉ thấy chậm hơn chứ không nhận lỗi.
- **Aurora hết dư địa ở 200 user:** ACU chạm trần 2,0 và CPU đỉnh khoảng 95% (không lỗi). Thêm tải ghi sẽ cần tăng ACU tối đa.

**Cái giá:** hai node dự phòng chạy liên tục kể cả khi không ai dùng. Cùng hai node hệ thống, lúc nhàn rỗi luôn có 4 node, đều là spot.

## 6. Kết quả đo

Chi tiết từng bài (mục đích, cách test, phạm vi không chứng minh, dữ liệu thô) nằm trong [TEST_LOG.md](TEST_LOG.md). Đo ngày 30/10 và 2/11/2026.

| Bài | Nội dung | Kết quả chính |
|---|---|---|
| 1 | API scale to zero | cold start 1,79 giây; về 0 pod sau khoảng 5 phút 1 giây |
| 2 | Cold start API | 20 lượt: phía hệ thống 20/20 dưới 3 giây, trung vị 2,08 giây; đầu-cuối 17/20 (3 lượt chậm do mạng máy đo) |
| 3 | Cold start worker (ScaledJob) | trung vị `server_s` 10,2 giây (hỏi mỗi 1 giây), 17,4 / 12,2 giây ở chu kỳ 10 / 30 giây (số 30 giây có thể bị khoá pha với script đo); 0 lượt dưới 3 giây; mỗi Score tạo 1 đến 3 Job. Worker nóng (không chọn): 0,39 giây |
| 4 | Karpenter | node `default` 2 → 4 → 2; 5 pod sẵn sàng sau 37 giây; dọn node sau 126 giây |
| 5 | HPA; API với trigger CPU | HPA 1 → 2 → 3 → 1 pod; API (KEDA HTTP + CPU) 1 → 2 pod sau 21 giây |
| 6 | KEDA với SQS | 500 message xử lý hết sau 75 giây; worker 0 → 20 → 0 Job; DLQ 0; 0/76 lỗi frontend/API |
| 7 | 200 user cùng lúc | 68.854 request, 0 lỗi, p95 nhẹ 175 ms (sau tối ưu); API và frontend tối đa 2 pod |
| 8 | Aurora ACU | 0,5 → 2,0 ACU, CPU đỉnh 93 đến 95%; endpoint ghi và danh sách ứng viên tốn nhất |
| 9 | Mất một node spot | frontend 5/151 request lỗi trong khoảng 6 giây; API 0 lỗi; cụm đủ node sau khoảng 2 phút |
| 10 | Chart Helm với image bất kỳ | nginx, http-echo, hello-app đều 200; đối chứng sai kiến trúc không chạy |
| 11 | Kiến trúc arm64 | API, frontend và worker đều chạy được trên node arm64 |

## 7. Chi phí và vận hành

- **Khoản tính theo giờ khi cụm chạy:** phí điều khiển EKS (khoảng 0,10 USD mỗi giờ), NAT gateway, ALB, Aurora tối thiểu 0,5 ACU, 2 node hệ thống và 2 node dự phòng. Ước tính thô **vài chục cent mỗi giờ, vài USD mỗi ngày**; đây là ước tính chưa đối chiếu bảng giá chính thức và chưa có hoá đơn thật (Cost Explorer trễ khoảng 24 giờ và credits có thể che khoản phải trả).
- **Tiết kiệm:** huỷ toàn bộ khi không dùng (`scripts/down.sh`), dựng lại bằng `scripts/up.sh` (khoảng 25 đến 40 phút). Huỷ thì Aurora và dữ liệu bị xoá vĩnh viễn; dựng lại có cơ sở dữ liệu rỗng (giao diện có nút nạp dữ liệu mẫu).
- **Phiên AWS:** `aws login` có phiên hết hạn giữa chừng làm hỏng bài đo dài; nên kiểm tra `aws sts get-caller-identity` trước khi chạy bài dài.
- **Cấu hình tag image:** mỗi lần đẩy image mới lên ECR phải cập nhật `prepull_images` (Terraform) và `apps/*.values.yaml`, nếu không tải sẵn image sẽ hỏng.

## 8. Hạn chế và vấn đề còn mở

- **Route 53 chưa làm:** chưa có tên miền; dùng DNS của ALB, chỉ HTTP, ALB mở cho cả internet.
- **Frontend và API chỉ có 1 pod nền,** nên mất node là gián đoạn ngắn.
- **Cold start worker không đạt dưới 3 giây với ScaledJob** (trung vị 10 giây trở lên): KEDA đọc số đếm hàng đợi SQS bị cũ vài giây. Mỗi Score còn tạo 1 đến 3 Job, trong đó có Job rỗng; chu kỳ hỏi dài hơn giảm Job dư nhưng làm chậm hơn. Worker nóng (1 pod luôn chạy) đạt 0,39 giây nhưng đã không chọn.
- **Đợt dồn nhiều Job** có thể hết IP của node, pod khởi động chậm 11 đến 40 giây (giảm còn khoảng 10% pod chậm bằng kho IP lớn hơn).
- **Aurora hết dư địa ở 200 user:** ACU chạm trần 2,0 và CPU đỉnh khoảng 95% (không lỗi). Thêm tải ghi sẽ cần tăng ACU tối đa.
- **"Image bất kỳ"** nghĩa là một dịch vụ HTTP bình thường; chưa thử volume, TCP, gRPC, registry riêng, non-root.
- **Worker** chỉ giả lập công việc (chấm điểm), không phải khối lượng xử lý nặng thật.
- **Spot:** chưa thử mất cả hai node hệ thống cùng lúc.
- **down.sh:** bản đã sửa chưa được dùng trong một lần huỷ thật sau lần sửa cuối.
- **Kho mã:** chưa push lên GitHub (cần đăng nhập).

## 9. Cách xem lại kết quả

- Mục đích, cách test và bảng kết quả từng bài: [TEST_LOG.md](TEST_LOG.md).
- Số liệu nằm ở `docs/results/`.
- Kịch bản chạy demo trực tiếp: [DEMO.md](DEMO.md).
