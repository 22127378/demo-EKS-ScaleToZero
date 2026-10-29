# Báo cáo: Co giãn tự động trên EKS spot

Hệ thống tuyển dụng nhỏ chạy trên AWS EKS, dùng toàn bộ node spot. API về 0 pod khi không dùng, worker chạy theo hàng đợi SQS, node tăng giảm theo nhu cầu.

Mọi con số trong báo cáo lấy từ [TEST_LOG.md](TEST_LOG.md) và dữ liệu thô trong `docs/results/` và `docs/evidence/`. Báo cáo ghi cả những điểm chưa đạt.

## 1. Mục tiêu

| Yêu cầu | Ghi chú |
|---|---|
| API Go từ 20 đến 50 endpoint, giao diện React | Có 32 endpoint (miền tuyển dụng) |
| ALB phía trước, chạy trên EKS | Dùng một ALB chung cho API và frontend |
| Toàn bộ node là spot, có cả amd64 và arm64 | |
| Ba tầng co giãn: Karpenter, KEDA với SQS, HPA | |
| API scale to zero, cold start dưới 3 giây | |
| Chịu được 200 user cùng lúc | |
| Aurora Serverless v2, tối thiểu 0,5 và tối đa 2 ACU, không tạm dừng | |
| Chart Helm dùng được với image bất kỳ | |
| Số pod tối đa: API 2, frontend 2 | |

## 2. Kiến trúc

```
Người dùng
    |
   ALB  ──(Host: api.demo.local)──►  Interceptor KEDA ──► API (0 đến 2 pod) ──► Aurora Serverless v2
    |                                                          |
    └──────────────────────────────► Frontend nginx (1 đến 2 pod)
                                                               └──► SQS ──► Worker (0 đến 20 pod)
```

- **Ứng dụng:** API Go (32 endpoint), frontend React với nginx, worker Go chấm điểm hồ sơ.
- **Hạ tầng:** EKS 1.33, Aurora PostgreSQL 17, SQS kèm hàng đợi lỗi (DLQ), ECR, tất cả dựng bằng Terraform.
- **Chart Helm `charts/app`:** một chart dùng chung cho API, frontend và worker; đổi chế độ co giãn bằng một giá trị (`hpa`, `http`, `sqs`, `none`).
- **Chưa làm:** Route 53, vì chưa có tên miền. Hiện truy cập bằng địa chỉ DNS của ALB.

## 3. Ba tầng co giãn

| Tầng | Công cụ | Áp dụng cho | Co giãn theo | Giới hạn |
|---|---|---|---|---|
| Node | Karpenter | mọi pod không còn chỗ chạy | pod ở trạng thái chưa xếp được | pool `default` tối đa 16 vCPU, pool `worker` tối đa 12 vCPU |
| Pod | KEDA HTTP Add-on | API | số request đồng thời (mục tiêu 100 mỗi pod) | 0 đến 2 pod, về 0 sau 300 giây không có request |
| Pod | HPA | Frontend | CPU 50% so với mức pod xin | 1 đến 2 pod |
| Pod | KEDA với SQS | Worker | độ dài hàng đợi (mục tiêu 5 message mỗi pod) | 0 đến 20 pod |

## 4. Giảm thời gian chờ: chỗ trống dự phòng (over-provisioning)

Tạo một node mới mất khoảng 40 đến 50 giây, quá dài so với mục tiêu 3 giây, nên hệ thống giữ sẵn chỗ trống:

- **Hai node spot luôn chạy** thuộc pool `default`: một amd64 và một arm64. Mỗi node có một pod giữ chỗ (`placeholder`, xin 500m CPU và 512 MiB RAM, độ ưu tiên thấp, không bị Karpenter gom). Mỗi node 2 vCPU còn trống khoảng 1,28 vCPU cho pod API hoặc frontend mới.
- **Cách hoạt động:** khi chỗ trống hết, pod thật đẩy pod giữ chỗ ra, pod giữ chỗ chưa xếp được khiến Karpenter tạo node thay thế, nên chỗ trống tự được bù lại.
- **Tải sẵn image:** một DaemonSet chạy các image API và frontend trên mọi node, nên pod mới không mất thời gian kéo image.
- **Bỏ `startupProbe` của API:** kubelet chạy hai probe nối tiếp, nên bỏ một probe giảm trung vị cold start từ 2,97 xuống 2,06 giây (20 lượt mỗi cấu hình).
- **Cấu hình VPC CNI** (`WARM_IP_TARGET=3`): node giữ sẵn địa chỉ IP.
- **Interceptor của KEDA giữ request** trong lúc API bật từ 0 lên 1, nên người dùng chỉ thấy chậm hơn chứ không nhận lỗi.

**Cái giá:** hai node dự phòng chạy liên tục kể cả khi không ai dùng. Cùng với hai node hệ thống (chạy Karpenter, KEDA, CoreDNS, bộ điều khiển ALB), lúc nhàn rỗi luôn có **4 node**, đều là spot.

## 5. Kết quả đo

### 5.1 API scale to zero

Cấu hình thật: chờ 300 giây không có request thì về 0 pod.

| Mốc | Thời điểm | Kết quả |
|---|---|---|
| API đang ở 0 pod | 16:41:45 | gửi một request, HTTP 200 sau 2,375 giây |
| KEDA bật API lên 1 pod | 16:41:46 | sự kiện `KEDAScaleTargetActivated` |
| KEDA đưa API về 0 pod | 16:46:46 | sự kiện `KEDAScaleTargetDeactivated`, **đúng 300 giây** sau |

### 5.2 Cold start (20 lượt, API 32 endpoint, endpoint chạm cơ sở dữ liệu)

| Chỉ số | Giá trị |
|---|---|
| Trung vị | 2,16 giây |
| p95 | 2,60 giây |
| Lớn nhất | 3,07 giây |
| Số lượt dưới 3 giây | **19/20** |

Kiểm tra lại nhanh trên cluster dựng lại với **2 lượt**: 2,21 và 2,13 giây, cùng mức với bài 20 lượt (2 mẫu chỉ để xác nhận, không thay cho 20 lượt). Khoảng 1 giây là pod khởi động; phần còn lại là KEDA phát hiện request, interceptor chuyển tiếp và mạng. Kết luận trung thực: **phần lớn đạt dưới 3 giây, nhưng có 1/20 lượt vượt**, nên không nói "luôn dưới 3 giây".

### 5.3 Karpenter: tầng node

Tạo 3 pod, mỗi pod xin 1200m CPU (lớn hơn chỗ trống hiện có), rồi xoá.

- Số node pool `default` đi từ **2 lên 4**; cả 3 pod sẵn sàng sau **44 giây**.
- Sau khi xoá, Karpenter tự gom và về **2 node** sau khoảng **191 giây**.
- Toàn bộ node là spot, gồm cả amd64 (t3) và arm64 (t4g, c7g). Nhật ký do chính Karpenter ghi.

### 5.4 KEDA với SQS: tầng worker

Gửi 500 message vào hàng đợi.

- Worker đi từ **0 lên 20 pod**; Karpenter tạo thêm khoảng 3 node `worker`.
- Bộ đếm của SQS: gửi 504, nhận 504, xoá 504; **hàng đợi lỗi (DLQ) bằng 0**, tức không mất message nào. (Cửa sổ đo CloudWatch rộng hơn bài chạy nên con số 504 gồm cả vài message từ các lần thử trước.)
- Hàng đợi hết sau khoảng 81 giây; worker về **0 pod** sau khoảng 153 giây kể từ lúc gửi.

### 5.5 HPA: thêm pod khi CPU cao

Frontend nginx dùng quá ít CPU nên khó kích hoạt HPA ở tải nhẹ. Vì vậy dùng một bản sao của API (endpoint `/burn`, tối đa 3 pod, cửa sổ giảm 60 giây) với tải nhẹ có chủ đích (đốt CPU 1 giây rồi nghỉ 2 giây):

- HPA đi từ **1 lên 2 rồi 3 pod**, mỗi bước có sự kiện `SuccessfulRescale`, rồi giảm về **1 pod** sau khi hết tải và hết cửa sổ ổn định.
- Bài đốt CPU nặng trước đó (CPU khoảng 780%) cũng cho thấy 1 lên 3 pod; xem TEST_LOG mục 5.1b.
- Trong bài 200 user có database (mục 5.6), **frontend thật cũng lên 2 pod** và dừng ở tối đa 2.

### 5.6 Chịu 200 user cùng lúc

Ba bài k6, mỗi bài có 200 user cùng lúc (5 phút qua ALB; 3 phút có truy vấn Aurora; 3 phút có các endpoint miền tuyển dụng).

| Chỉ số | Kết quả |
|---|---|
| Lỗi | **0** ở cả ba bài |
| p95 | từ 120 đến 314 ms |
| p99 | từ 306 đến 327 ms |
| Aurora | chạm mức tối đa đã đặt (2,0 ACU), CPU lên tới 92,6% ở bài nặng nhất |

Bài thứ tư (2 phút, có gọi database): 47.379 request, 0 lỗi; Aurora đi từ 0,5 lên **2,0 ACU** (CPU database 83%) và API, frontend đều lên 2 pod rồi dừng. (Lượt đầu không bật gọi database giữ ACU ở 0,5, vì tải không chạm database.)

Kết luận: **đạt, nhưng gần trần của cấu hình hiện tại**. Cơ sở dữ liệu là điểm nghẽn tiếp theo nếu tải tăng.

### 5.7 Mất một node spot

Tắt đột ngột (`terminate-instances`) node đang chạy pod frontend, trong lúc gửi request liên tục qua ALB.

- Karpenter nhận thông báo sau khoảng 2 giây và dọn node. Frontend (1 pod) **lỗi khoảng 12 giây** (2 request 502, 1 request quá hạn), rồi tự trở lại HTTP 200.
- Sau khoảng **2 phút 33 giây** có node thay thế, đủ 4 node Ready và cả hai pod giữ chỗ chạy lại.
- API không nằm trên node bị tắt nên 0 lỗi; điều này không chứng minh API chịu được mất node.
- Chưa thử mất cả hai node hệ thống. Chi tiết: TEST_LOG mục 5.4.

### 5.8 Chart Helm với image bất kỳ

Cài `nginx`, `http-echo`, `hello-app` (chỉ amd64) bằng cùng chart: cả 3 trả HTTP 200; ép image amd64 lên arm64 thì pod đúng là không chạy. Chỉ thử dịch vụ HTTP. Chi tiết: TEST_LOG mục 5.5.

## 6. Trạng thái so với yêu cầu

| Yêu cầu | Trạng thái |
|---|---|
| API Go 32 endpoint, frontend React, ALB | **Đạt** |
| Ba tầng co giãn (Karpenter, KEDA với SQS, HPA) | **Đạt**, có bằng chứng ở mục 5 |
| API scale to zero | **Đạt** |
| Cold start dưới 3 giây | **Phần lớn đạt**: 19/20 lượt, một lượt 3,07 giây |
| 200 user cùng lúc | **Đạt**, Aurora gần hết dư địa |
| Toàn bộ node spot, amd64 và arm64 | **Đạt** |
| Aurora Serverless v2 không tạm dừng | **Đạt** (tối thiểu 0,5 ACU) |
| Chart dùng được với image bất kỳ | **Đạt** với image web HTTP (bằng chứng ở mục 5.8); xem điều kiện ở mục 7 |
| Chịu mất node spot | **Đạt một phần**: tự hồi phục, nhưng frontend gián đoạn khoảng 12 giây (mục 5.7) |
| Route 53 | **Chưa làm** (chưa có tên miền) |
| Huỷ toàn bộ hạ tầng | Đã chạy thật hai lần bằng `scripts/down.sh`; script đã sửa sau lần thứ hai |

## 7. Hạn chế

- **Mỗi bài chỉ đo một lần** (cold start đo 20 lượt), từ một máy; kết quả có thể khác ở vị trí địa lý khác.
- **Mạng của máy đo:** khoảng 5 đến 10% kết nối tốn hơn 1 giây kể cả tới đích không liên quan hệ thống, nên một số lượt chậm không do hệ thống.
- **"Image bất kỳ"** nghĩa là một dịch vụ HTTP bình thường. Image khác có thể cần đổi cổng, đường dẫn kiểm tra sức khoẻ (`/healthz` mặc định), và kiến trúc CPU (image chỉ có amd64 không chạy trên node arm64). Chưa thử image dùng volume, TCP thuần hay gRPC.
- **Spot:** đã thử mất một node đột ngột (mục 5.7); chưa thử mất cả hai node hệ thống cùng lúc.
- **Worker** chỉ giả lập công việc (chấm điểm), không phải khối lượng xử lý nặng thật.
- **Tải sẵn image đang hỏng trên node mới:** `image-prepuller` dùng tag `demo-web:0.3.0` đã không còn trong ECR nên báo `ImagePullBackOff` trên node mới. Đã sửa sau các bài đo (tag đổi sang api 0.3.4, web 0.3.3, pod prepull `Running`); các số đo cold start được lấy trước khi sửa.
- **Hai số liệu không có file đối chiếu** trong `docs/` (đo trực tiếp lúc viết nhật ký, chưa lưu): phần mạng của máy đo và biểu đồ Aurora lúc có tải.

## 8. Chi phí và vận hành

- **Chạy nền:** phí điều khiển EKS (khoảng 0,10 USD mỗi giờ), NAT gateway, ALB, Aurora tối thiểu 0,5 ACU, 2 node hệ thống và 2 node dự phòng. Các khoản này tính theo giờ, kể cả khi hệ thống rảnh, nên cách tiết kiệm đơn giản nhất là **huỷ toàn bộ khi không dùng** (`scripts/down.sh`), rồi dựng lại bằng `scripts/up.sh` (khoảng 25 đến 40 phút).
- **Dữ liệu:** huỷ hạ tầng thì Aurora và dữ liệu bị xoá vĩnh viễn; dựng lại sẽ có cơ sở dữ liệu rỗng (có nút nạp dữ liệu mẫu trên giao diện).

## 9. Cách xem lại kết quả

- Nhật ký chi tiết từng bài đo, kèm mục đích, cách test và ý nghĩa kết quả: [TEST_LOG.md](TEST_LOG.md).
- Dữ liệu thô: `docs/results/` (cold start, 200 user) và `docs/evidence/` (scale to zero, HPA, Karpenter, KEDA với SQS).
- Kịch bản chạy demo trực tiếp: [DEMO.md](DEMO.md).
