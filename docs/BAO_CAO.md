# Báo cáo: Co giãn tự động trên EKS spot

Hệ thống tuyển dụng nhỏ chạy trên AWS EKS, dùng toàn bộ node spot. API về 0 pod khi không dùng, worker chạy theo hàng đợi SQS, node tăng giảm theo nhu cầu.

**Toàn bộ số đo cũ đã được xoá để đo lại từ đầu.** Các mục kết quả bên dưới để trống (`chưa đo`) cho đến khi có số đo mới trong [TEST_LOG.md](TEST_LOG.md). Phần mô tả hệ thống và cấu hình (mục 2 đến 5) là cấu hình đang chạy, không phải kết quả đo.

## 1. Mục tiêu

| Yêu cầu | Trạng thái |
|---|---|
| API Go từ 20 đến 50 endpoint, giao diện React | Có 32 endpoint miền tuyển dụng và `/slow` (33 route) |
| ALB phía trước, chạy trên EKS | Một ALB chung cho API và frontend |
| Toàn bộ node là spot, có cả amd64 và arm64 | Cấu hình đã đặt; kết quả chưa đo lại |
| Ba tầng co giãn: Karpenter, KEDA với SQS, HPA | Cấu hình đã đặt; kết quả chưa đo lại |
| API scale to zero, cold start dưới 3 giây | Chưa đo lại |
| Chịu được 200 user cùng lúc | Chưa đo lại |
| Aurora Serverless v2, 0,5 đến 2 ACU, không tạm dừng | Cấu hình đã đặt; ACU chưa đo lại |
| Chart Helm dùng được với image bất kỳ | Chưa đo lại |
| Số pod tối đa: API 2, frontend 2 | Cấu hình đã đặt; chưa đo lại |
| Chịu mất node spot | Chưa đo lại |
| Route 53 | **Chưa làm** (chưa có tên miền) |

## 2. Kiến trúc

```
Người dùng
    |
   ALB  ──(Host: api.demo.local)──►  Interceptor KEDA ──► API (0 đến 2 pod) ──► Aurora Serverless v2
    |                                                          |
    └──────────────────────────────► Frontend nginx (1 đến 2 pod)
                                                               └──► SQS ──► Worker (0 đến 20 pod)
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
| Pod | KEDA ScaledJob với SQS | Worker | độ dài hàng đợi: mỗi Kubernetes Job lấy tối đa 10 message rồi kết thúc | 0 đến 20 Job chạy cùng lúc | `pollingInterval` **1 giây**, `cooldownPeriod` **10 giây** (mặc định 5 và 60), startup probe tắt, chạy ở pool `default` |

**Cơ sở dữ liệu:** Aurora Serverless v2, 0,5 đến 2 ACU, không tạm dừng. **Hàng đợi:** SQS `demo-jobs`, thời gian ẩn message 60 giây, tối đa 3 lần nhận trước khi vào DLQ.

**Giá trị demo cần trả lại:** API `scaledownPeriod` về 300, worker `cooldownPeriod` về 60 (và `pollingInterval` về 5 nếu muốn như cũ), frontend giữ 300. Thời gian chờ 10 giây làm pod worker bị tắt trước khi kịp chạy nếu bấm Score liên tiếp.

### 4.1 Tài nguyên từng dịch vụ

Chụp trên cụm đang chạy, lúc API và worker ở 0 pod (dữ liệu thô: `docs/evidence/resource-usage/`). "Xin" là mức Kubernetes giữ chỗ cho pod; "dùng thực" là mức đang dùng (`kubectl top`), chỉ là một lần chụp, không phải trung bình.

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

Node giữ sẵn một "kho" IP trống (`WARM_IP_TARGET`, hiện 10; `MINIMUM_IP_TARGET`, hiện 12). Pod mới lấy IP từ kho ngay lập tức. Khi kho cạn (ví dụ dồn nhiều Job worker cùng lúc), node phải gọi API của EC2 để xin thêm IP, mất vài giây; trong lúc đó pod không có mạng và Kubernetes thử lại sau khoảng 10 giây, nên pod chờ 11 đến 40 giây (lỗi `failed to assign an IP address to container`). Biện pháp đã làm: tăng kho IP dự phòng (3 lên 10) và cho mỗi Job lấy 10 message thay vì 5 để tạo ít pod hơn. Cách còn lại: dùng node lớn hơn (nhiều IP hơn) hoặc bật prefix delegation.

## 5. Giảm thời gian chờ: chỗ trống dự phòng (over-provisioning)

Tạo một node mới mất thời gian đáng kể, nên hệ thống giữ sẵn chỗ trống (hiệu quả cần đo lại):

- **Hai node spot luôn chạy** thuộc pool `default`: một amd64 và một arm64. Mỗi node có một pod giữ chỗ (`placeholder`, image `pause`, xin 500m CPU và 512 MiB RAM, độ ưu tiên -10, không bị Karpenter gom).
- **Cách hoạt động:** khi chỗ trống hết, pod thật đẩy pod giữ chỗ ra, pod giữ chỗ chưa xếp được khiến Karpenter tạo node thay, nên chỗ trống tự được bù lại.
- **Tải sẵn image:** DaemonSet `image-prepuller` chạy image API, worker và frontend trên mọi node Karpenter. Tag trong `prepull_images` phải khớp với ECR.
- **Bỏ startup probe của API và worker.**
- **VPC CNI `WARM_IP_TARGET=3`:** node giữ sẵn địa chỉ IP.
- **Interceptor của KEDA giữ request** trong lúc API bật từ 0 lên 1, nên người dùng chỉ thấy chậm hơn chứ không nhận lỗi.
- **Worker chạy ở pool `default`** để dùng chung chỗ trống (đánh đổi: tranh chỗ với API và frontend).

**Cái giá:** hai node dự phòng chạy liên tục kể cả khi không ai dùng. Cùng hai node hệ thống, lúc nhàn rỗi luôn có 4 node, đều là spot.

## 6. Kết quả đo

Chưa đo lại. Mỗi bài có mục đích, cách test, phạm vi không chứng minh và bảng kết quả trống trong [TEST_LOG.md](TEST_LOG.md):

| Bài | Nội dung | Trạng thái |
|---|---|---|
| 1 | API scale to zero | chưa đo |
| 2 | Cold start API (mục tiêu dưới 3 giây) | chưa đo |
| 3 | Cold start worker | chưa đo |
| 4 | Karpenter thêm và bớt node | chưa đo |
| 5 | HPA | chưa đo |
| 6 | KEDA với SQS (worker 0 đến 20) | chưa đo |
| 7 | 200 user cùng lúc, API và frontend tối đa 2 pod | chưa đo |
| 8 | Aurora ACU (0,5 đến 2) | chưa đo |
| 9 | Mất một node spot | chưa đo |
| 10 | Chart Helm với image bất kỳ | chưa đo |
| 11 | Kiến trúc arm64 | chưa đo |

## 7. Chi phí và vận hành

- **Khoản tính theo giờ khi cụm chạy:** phí điều khiển EKS (khoảng 0,10 USD mỗi giờ), NAT gateway, ALB, Aurora tối thiểu 0,5 ACU, 2 node hệ thống và 2 node dự phòng. Ước tính thô **vài chục cent mỗi giờ, vài USD mỗi ngày**; đây là ước tính chưa đối chiếu bảng giá chính thức và chưa có hoá đơn thật (Cost Explorer trễ khoảng 24 giờ và credits có thể che khoản phải trả).
- **Tiết kiệm:** huỷ toàn bộ khi không dùng (`scripts/down.sh`), dựng lại bằng `scripts/up.sh` (khoảng 25 đến 40 phút). Huỷ thì Aurora và dữ liệu bị xoá vĩnh viễn; dựng lại có cơ sở dữ liệu rỗng (giao diện có nút nạp dữ liệu mẫu).
- **Phiên AWS:** `aws login` có phiên hết hạn giữa chừng làm hỏng bài đo dài; nên kiểm tra `aws sts get-caller-identity` trước khi chạy bài dài.
- **Cấu hình tag image:** mỗi lần đẩy image mới lên ECR phải cập nhật `prepull_images` (Terraform) và `apps/*.values.yaml`, nếu không tải sẵn image sẽ hỏng.

## 8. Hạn chế và vấn đề còn mở

- **Route 53 chưa làm:** chưa có tên miền; dùng DNS của ALB, chỉ HTTP, ALB mở cho cả internet.
- **Frontend và API chỉ có 1 pod nền,** nên mất node là gián đoạn ngắn.
- **Số Job thừa của ScaledJob: một message sinh ra khoảng 3 Job (2 Job thừa thoát ngay vì hàng đợi đã rỗng); đã thử các chiến lược `accurate` (8 và 6 Job) và `default` (3 Job), chưa triệt hẳn. Vụ 3 pod worker mỗi lần Score trước đó:** cần theo dõi một lần bấm Score khi worker đang 0 pod.
- **Worker chạy ở pool `default`** cần bài hồi quy 500 message đầy đủ, vì chung chỗ với API và frontend.
- **"Image bất kỳ"** nghĩa là một dịch vụ HTTP bình thường; chưa thử volume, TCP, gRPC, registry riêng, non-root.
- **Worker** chỉ giả lập công việc (chấm điểm), không phải khối lượng xử lý nặng thật.
- **Spot:** chưa thử mất cả hai node hệ thống cùng lúc.
- **down.sh:** bản đã sửa chưa được dùng trong một lần huỷ thật sau lần sửa cuối.
- **Kho mã:** chưa push lên GitHub (cần đăng nhập).

## 9. Cách xem lại kết quả

- Mục đích, cách test và bảng kết quả từng bài: [TEST_LOG.md](TEST_LOG.md).
- Dữ liệu thô sẽ nằm ở `docs/results/` và `docs/evidence/` sau khi đo.
- Kịch bản chạy demo trực tiếp: [DEMO.md](DEMO.md).
