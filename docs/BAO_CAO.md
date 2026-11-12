# Hệ thống tuyển dụng co giãn tự động trên Amazon EKS dùng toàn bộ node spot: thiết kế và đánh giá thời gian khởi động lạnh

Báo cáo đồ án, tháng 11/2026

## Đọc nhanh

Phần này dành cho người không làm kỹ thuật. Các phần sau đi vào chi tiết.

- Hệ thống là một trang web tuyển dụng nhỏ chạy trên đám mây của Amazon. Để tiết kiệm tiền, mọi máy chủ đều thuê loại giá rẻ (máy spot), và phần xử lý của trang tự tắt hẳn khi không có ai dùng.
- Thứ báo cáo đo kỹ nhất là thời gian chờ khi trang đang tắt mà có khách đầu tiên đến (khởi động lạnh, cold start). Yêu cầu của đề tài là dưới 3 giây.
- Phần trang web (API): kết quả khoảng 2 giây, đạt yêu cầu.
- Phần chấm điểm hồ sơ chạy ngầm: chưa đạt. Từ lúc bấm nút đến lúc thấy điểm mất khoảng 10 giây. Nguyên nhân là công cụ điều khiển (KEDA) dựa vào số đếm của hàng đợi việc, và số đếm đó cập nhật chậm.
- Hệ thống còn bật dư vài lần chạy không có việc để làm (Job dư). Chúng ngắn và không làm sai kết quả.
- Báo cáo có thử thêm hai ý tưởng cải tiến phần chấm điểm: đếm việc cả qua cơ sở dữ liệu (đề xuất A), và tách thành hai bộ điều khiển (đề xuất B). Cả hai chưa được duyệt nên không áp dụng vào hệ thống, và chỉ được đo để có thêm góc nhìn. Ở các lần đo, hai ý tưởng này cho điểm sau khoảng 1,7 giây.
- Các phần còn lại (thêm bớt máy chủ, tăng giảm số pod, 200 người dùng cùng lúc, cơ sở dữ liệu, mất một máy spot) chạy đúng thiết kế trong các lần đo. Riêng khi mất một máy spot, trang web lỗi ngắn khoảng 6 giây vì frontend chỉ có một pod.

## Tóm tắt

Đồ án dựng một hệ thống tuyển dụng nhỏ trên Amazon EKS, trong đó mọi node đều là máy spot. API Go tự về 0 pod khi không ai dùng, frontend React co giãn bằng HPA, và worker chấm điểm hồ sơ chạy theo hàng đợi SQS thông qua KEDA ScaledJob. Dữ liệu nằm trong Aurora Serverless v2. Báo cáo đo kỹ nhất thời gian khởi động lạnh (cold start) của API và của worker, vì yêu cầu của đề tài là dưới 3 giây khi pod đang ở 0.

Với API, chuỗi 20 lượt cho trung vị 2,40 giây tính đầu cuối, trong đó 17 lượt dưới 3 giây. Bỏ thời gian mở kết nối ở máy đo thì cả 20 lượt dưới 3 giây (trung vị 2,08 giây).

Worker của hệ thống dùng KEDA với trigger SQS. Khi KEDA hỏi hàng đợi mỗi 1 giây (cấu hình bản demo), điểm hiện sau trung vị 10,18 giây và không lượt nào dưới 3 giây, nên yêu cầu của worker chưa đạt. Nguyên nhân là số đếm hàng đợi SQS cập nhật chậm. Hệ thống cũng tạo Job rỗng: 40 Job dư trong 20 lượt.

Báo cáo còn đo hai ý tưởng cải tiến để có thêm góc nhìn: thêm trigger PostgreSQL vào cùng ScaledJob (đề xuất A), và tách thành hai ScaledJob (đề xuất B). Cả hai chưa được duyệt và không áp dụng vào hệ thống. Ở các lượt đo, cả hai cho điểm sau khoảng 1,7 giây.

Ngoài cold start, báo cáo ghi kết quả các phần còn lại. Karpenter thêm máy mới sau 24 giây và cả 5 pod sẵn sàng sau 37 giây. HPA tăng từ 1 lên 3 pod trong 45 giây. Với 200 người dùng cùng lúc, hệ thống xử lý 70.111 request không lỗi, với tối đa 2 pod mỗi bên. Aurora tăng từ 0,5 lên 2,0 ACU và chạm trần. Khi một máy spot bị tắt đột ngột, frontend lỗi khoảng 6 giây (5 trên 151 request) còn API không lỗi.

Từ khóa: Kubernetes, EKS, spot, Karpenter, KEDA, scale to zero, cold start, SQS, Aurora Serverless v2.

## Một số từ cần biết

| Từ | Hiểu đơn giản |
|---|---|
| Cụm (cluster), EKS | nhóm máy chủ do Amazon quản lý, nơi chạy toàn bộ ứng dụng. EKS là tên dịch vụ |
| Máy spot | máy chủ cho thuê giá rẻ từ phần công suất Amazon đang dư. Đổi lại, Amazon có thể lấy máy về bất cứ lúc nào |
| Node | một máy chủ trong cụm |
| Pod | một bản đang chạy của một ứng dụng, giống một nhân viên đang trực quầy. Nhiều pod là nhiều nhân viên |
| Về 0 pod (scale to zero) | khi không có khách, ứng dụng tắt hẳn để không tốn tiền, có khách thì bật lại |
| Khởi động lạnh (cold start) | thời gian chờ khi ứng dụng đang tắt và phải bật lên để phục vụ khách đầu tiên |
| API | phần xử lý phía sau trang web: nhận yêu cầu, đọc và ghi dữ liệu |
| Worker | phần làm việc nền. Ở đây là chấm điểm hồ sơ |
| SQS | hàng đợi của Amazon, giống quầy phát số: việc mới được xếp vào, worker lấy ra lần lượt |
| KEDA | công cụ tự bật và tắt ứng dụng theo lượng việc thực tế, ví dụ số việc đang xếp hàng |
| Karpenter | công cụ tự thêm máy chủ khi thiếu chỗ và bỏ bớt khi thừa |
| HPA | công cụ có sẵn của Kubernetes, tăng giảm số pod theo mức dùng CPU |
| Job | một lần chạy ngắn của worker: bật lên, làm vài việc rồi tắt |
| Job dư (Job rỗng) | Job được bật lên nhưng không còn việc nào để lấy, nên tắt ngay |
| Chu kỳ hỏi (`pollingInterval`) | cứ sau bao nhiêu giây KEDA kiểm tra hàng đợi một lần |
| Aurora, ACU | cơ sở dữ liệu của Amazon. ACU là đơn vị dung lượng nó dùng, tự tăng giảm theo tải |
| ALB | cổng vào chung, nhận khách rồi chuyển tới đúng ứng dụng |
| Trung vị | giá trị ở giữa khi xếp các lần đo từ nhỏ đến lớn: một nửa số lần nhanh hơn, một nửa chậm hơn |
| p95 | giá trị mà 95% số lần đo không vượt quá. Cho biết những lần chậm nhất chậm cỡ nào |

## Mục lục

1. Giới thiệu
2. Các khái niệm cần biết
3. Thiết kế và triển khai
4. Cách đo
5. Kết quả
6. Rủi ro và lý do giữ thiết kế hiện tại
7. Kết luận và hướng phát triển
- Phụ lục A: cách tái lập
- Phụ lục B: dữ liệu thô và cách đọc số liệu

## 1. Giới thiệu

### 1.1 Bối cảnh và mục tiêu

Máy spot rẻ hơn máy thuê thông thường, nhưng Amazon có thể lấy lại chúng bất cứ lúc nào. Một hệ thống chạy hoàn toàn trên máy spot cần co giãn nhanh và chịu được việc mất máy. Đề tài dựng một hệ thống như vậy rồi đo xem phần nào đạt yêu cầu, phần nào chưa.

### 1.2 Yêu cầu và bằng chứng

Bảng cho biết từng yêu cầu của đề tài đã đạt chưa. Cột cuối ghi bằng chứng còn lưu trong kho mã: "Cấu hình" là thứ đọc được từ mã dựng hạ tầng hoặc chart Helm, "Dữ liệu thô" là file đo trong `docs/results/`.

| Yêu cầu | Kết quả | Bằng chứng hiện có |
|---|---|---|
| API Go có 20 đến 50 endpoint, giao diện React | 32 endpoint miền tuyển dụng và `/slow` | Cấu hình (mã nguồn) |
| ALB phía trước, chạy trên EKS | Một ALB dùng chung cho API và frontend | Cấu hình |
| Toàn bộ node là spot, có amd64 và arm64 | Đạt: máy spot, cả hai kiến trúc; API và frontend chạy được trên arm64 | Cấu hình; mục 5.7 và 5.12 |
| Ba tầng co giãn: Karpenter, KEDA với SQS, HPA | Đạt: máy từ 2 lên 4 rồi về 2; HPA từ 1 lên 3 pod rồi về 1; worker chạy theo hàng đợi SQS | Mục 5.2, 5.7 và 5.8 |
| API về 0 pod, cold start dưới 3 giây | Đạt ở chuỗi 20 lượt: phía hệ thống 20/20 lượt dưới 3 giây, trung vị 2,08 giây | Mục 5.1 |
| Worker bắt đầu xử lý dưới 3 giây | Chưa đạt: điểm hiện sau khoảng 10 giây (trung vị 10,18 giây) khi KEDA hỏi hàng đợi mỗi 1 giây. Hai đề xuất cải tiến chưa được duyệt cho kết quả dưới 3 giây, chỉ ghi để tham khảo | Mục 5.2 và 5.5 |
| Chịu 200 người dùng cùng lúc, API và frontend tối đa 2 pod | Đạt: 70.111 request, 0 lỗi, tối đa 2 pod mỗi bên | Mục 5.9 |
| Aurora Serverless v2, 0,5 đến 2 ACU, không tạm dừng | Đạt: ACU từ 0,5 lên 2,0; hết dư địa ở 200 người dùng | Cấu hình; mục 5.10 |
| API thêm pod theo CPU | Đạt: API lên 2 pod sau 21 giây dưới tải CPU | Mục 5.8 |
| Chart Helm dùng được với image bất kỳ | Đạt: 3 image HTTP chạy được, image sai kiến trúc bị từ chối đúng như mong đợi | Mục 5.12 |
| Số pod tối đa: API 2, frontend 2 | Đạt | Mục 5.9 |
| Chịu mất một máy spot | Đạt một phần: frontend lỗi 5 trên 151 request trong khoảng 6 giây, API không lỗi | Mục 5.11 |
| Route 53 | Chưa làm vì chưa có tên miền | Không có |

### 1.3 Dữ liệu dùng trong báo cáo

Chương 5 chỉ dựa vào các file đo còn lưu trong `docs/results/`. Về cold start có chuỗi API (`cold-start-api.csv`) và các chuỗi worker: hệ thống hỏi mỗi 30 giây (`worker-cold-start-keda-poll30.csv`), đề xuất A hỏi mỗi 1 giây và 30 giây (`worker-cold-start-keda-postgres.csv`, `worker-cold-start-keda-postgres-poll30.csv`), đề xuất B (`worker-cold-start-keda-postgres-split.csv` 20 lượt, `worker-cold-start-split-10.csv` 10 lượt). Bản hỏi mỗi 1 giây của hệ thống chỉ còn bảng tổng hợp trong nhật ký cũ. Về các phần còn lại có file đo của Karpenter, HPA, trigger CPU của API, 200 người dùng, Aurora, mất một máy spot, chạy image bất kỳ và arm64, và bài thử hồ sơ mồ côi. Mọi thống kê được tính lại từ các file này.

Bản chỉ SQS hỏi mỗi 1 giây chỉ còn bảng tổng hợp, được ghi rõ ở mục 5.2. Các số đo cũ khác không còn file thô nên không đưa vào báo cáo.

## 2. Các khái niệm cần biết

Phần này giải thích những khái niệm cần để đọc chương 5. Các từ viết tắt khác có trong bảng thuật ngữ ở đầu báo cáo.

Máy spot và Karpenter. Máy spot là máy chủ thừa công suất của Amazon. Amazon có thể lấy lại chúng và báo trước khoảng hai phút. Karpenter thêm máy khi có ứng dụng chưa tìm được chỗ chạy và bỏ máy khi thừa. Dự án chỉ cho phép dùng máy spot.

KEDA. KEDA nhìn một con số bên ngoài (số việc đang xếp hàng, số yêu cầu đang đến, mức CPU) rồi quyết định bật hay tắt bao nhiêu pod. Dự án dùng ba kiểu:
- Cho API: KEDA HTTP Add-on. Một cổng trung gian đứng trước API và giữ yêu cầu của khách trong lúc API đang bật từ 0 lên 1 pod.
- Cho API: thêm trigger CPU, tăng pod khi CPU vượt 70% mức đã xin.
- Cho worker: ScaledJob. Cứ sau mỗi chu kỳ hỏi, KEDA lấy con số cần xem, chia cho số message mỗi Job nhận (`messagesPerJob`), làm tròn lên, trừ số Job đang chạy, rồi bật số Job còn thiếu, tối đa `maxReplicaCount`. Job đã chạy xong không được trừ. ScaledJob của hệ thống chỉ nhìn một con số: độ dài hàng đợi SQS.

HPA. Là công cụ có sẵn của Kubernetes. Ở đây nó tăng giảm số pod của frontend theo mức CPU.

SQS. Con số "độ dài hàng đợi" (`ApproximateNumberOfMessages`) chỉ là số xấp xỉ. Sau khi một việc được thêm vào hoặc lấy ra, con số này có thể chậm một lúc mới đổi, và độ chậm không cố định. KEDA dựa vào con số này nên nó quyết định cả lúc nào bật Job lẫn bật bao nhiêu Job (mục 5.2 đến 5.4).

Aurora Serverless v2. Là cơ sở dữ liệu PostgreSQL tự tăng giảm dung lượng trong khoảng ACU đã đặt. Dự án đặt từ 0,5 đến 2 ACU và không cho tạm dừng, để truy vấn đầu tiên không phải chờ đánh thức.

Địa chỉ IP của pod. Mỗi pod cần một địa chỉ IP riêng, và mỗi máy chỉ giữ được một số địa chỉ nhất định (số card mạng ảo nhân với số địa chỉ mỗi card trừ 1, cộng 2). Khi số địa chỉ giữ sẵn hết, pod mới phải chờ máy xin thêm từ Amazon.

## 3. Thiết kế và triển khai

### 3.1 Kiến trúc tổng thể

Hệ thống có ba phần chính. Frontend là trang web người dùng thấy. API xử lý yêu cầu và đọc ghi dữ liệu. Worker chấm điểm hồ sơ ở nền. Thêm vào đó là cơ sở dữ liệu Aurora và hàng đợi SQS.

```
Người dùng
    |
   ALB  ──(Host: api.demo.local)──►  Interceptor KEDA ──► API (0 đến 2 pod) ──► Aurora Serverless v2
    |                                                          |
    └──────────────────────────────► Frontend nginx (1 đến 2 pod)
                                                               └──► SQS ──► Worker (KEDA ScaledJob, 0 đến 20 Job) ──► Aurora
```

Phiên bản các thành phần, lấy từ `terraform/variables.tf`:

| Thành phần | Phiên bản |
|---|---|
| Kubernetes (EKS) | 1.33 |
| Karpenter (chart) | 1.14.1 |
| KEDA (chart) / HTTP Add-on (chart) | 2.21.0 / 0.16.0 |
| AWS Load Balancer Controller (chart) | 3.6.2 |
| metrics-server (chart) | 3.14.0 |
| Prometheus, Grafana, Loki, Alloy (chart) | 29.39.0, 10.5.15, 7.5.0, 1.14.4 |
| Aurora PostgreSQL | 17.9 |

Hệ thống chạy ở vùng `ap-southeast-1` (Singapore), trong một mạng riêng `10.0.0.0/16` trải trên 3 vùng khả dụng. Người dùng vào bằng địa chỉ DNS của ALB qua HTTP. ALB chỉ nhận các địa chỉ trong biến `INBOUND_CIDRS`, mặc định là địa chỉ của máy chạy script.

### 3.2 Ứng dụng

| Thành phần | Chi tiết |
|---|---|
| API | Go, 32 route miền tuyển dụng (vị trí, ứng viên, hồ sơ, chấm điểm, thống kê) cùng `GET /slow` cho bài tải. Cổng 8080, kiểm tra sức khỏe `/healthz`. Mỗi pod có pool tối đa 10 kết nối tới database. Xin 250m CPU, 64 MiB RAM, giới hạn RAM 256 MiB |
| Frontend | React (Vite, TypeScript) phục vụ bằng nginx. Nginx chuyển `/api/` tới interceptor KEDA với header `Host: api.demo.local`. Xin 100m CPU, 64 MiB RAM |
| Worker | Go, chạy ở chế độ `WORKER_MODE=job`: nhận tối đa `JOB_BATCH` message (10), chấm điểm rồi thoát. Một Job chờ message tối đa `JOB_WAIT_SECONDS` (mặc định 2 giây) trước khi bỏ cuộc. Xin 100m CPU, 64 MiB RAM |

Điểm hồ sơ là điểm giả lập, tính bằng `50 + (mã hồ sơ × 37 mod 51)`. Đó không phải mô hình thật.

Khi người dùng bấm nút Score ở một hồ sơ, mọi thứ diễn ra như sau:
1. Trang web gửi yêu cầu tới API. API ghi vào cơ sở dữ liệu rằng hồ sơ này đang chờ chấm (`screening_status = 'queued'`), bỏ một phiếu việc vào hàng đợi SQS, rồi trả lời "đã nhận" (mã 202). Nếu bỏ phiếu vào hàng đợi lỗi, API trả hồ sơ về trạng thái cũ.
2. KEDA theo dõi hàng đợi. Thấy độ dài tăng, nó bật một Job.
3. Pod worker khởi động, lấy phiếu việc, chấm điểm, ghi điểm vào cơ sở dữ liệu (`UPDATE` theo mã hồ sơ, chỉ khi hồ sơ còn ở trạng thái `queued`) rồi xóa phiếu. Nhờ điều kiện đó, nhận lại một phiếu cũ không làm đổi gì.
4. Trang web cứ 0,25 giây hỏi lại một lần cho đến khi thấy điểm.

Phiếu việc bị lỗi không bị xóa, SQS đưa lại sau một lúc. Đến lần nhận thứ 8, worker đánh dấu hồ sơ là `failed` kèm lý do (cột `screening_error`), và phiếu đi vào hàng đợi lỗi (DLQ).

### 3.3 Máy chủ và ba tầng co giãn

Bảng dưới liệt kê các nhóm máy chủ:

| Nhóm node | Số node | Cấu hình |
|---|---|---|
| Hệ thống (EKS managed) | 2, cố định | `t3.medium` (cho phép `t3a.medium`, `t3.large`, `m5.large`); chạy Karpenter, KEDA, CoreDNS, bộ điều khiển ALB, giám sát |
| Karpenter `default` | tối thiểu 2 (1 amd64, 1 arm64) | Giới hạn 16 vCPU; node 2 hoặc 4 vCPU; chạy API, frontend, pod giữ chỗ |
| Karpenter `worker` | tối thiểu 1 | Giới hạn 12 vCPU; chỉ node 4 vCPU; có taint nên chỉ chạy worker |

Hai nhóm Karpenter chỉ dùng máy spot, thuộc họ amd64 (c5, c6i, m5, m6i, t3) hoặc arm64 (c6g, c7g, m6g, m7g, t4g), bộ nhớ trên 3.000 MiB. Máy thừa bị bỏ sau 1 phút.

Việc tăng giảm diễn ra ở ba tầng:

| Tầng | Công cụ | Áp dụng cho | Co giãn theo | Giới hạn |
|---|---|---|---|---|
| Node | Karpenter | mọi pod chưa có chỗ | pod chưa xếp được | `default` 16 vCPU, `worker` 12 vCPU |
| Pod | KEDA HTTP Add-on | API | request đồng thời (100 mỗi pod) và CPU 70% | 0 đến 2 pod, về 0 sau 300 giây rảnh |
| Pod | HPA | Frontend | CPU 50% so với mức xin | 1 đến 2 pod |
| Pod | KEDA ScaledJob | Worker | độ dài hàng đợi SQS; 10 message mỗi Job | 0 đến 20 Job cùng lúc |

Chart Helm có PodDisruptionBudget cho API và frontend. Frontend không về 0 nên lúc nào cũng còn ít nhất một pod.

### 3.4 Cách giảm thời gian chờ

Dựng một máy mới mất hàng chục giây, nên hệ thống giữ sẵn chỗ trống:
- Pool `default` có hai pod giữ chỗ, mỗi kiến trúc một cái, và pool `worker` có một cái (`placeholder`, image `pause`, xin 500m CPU và 512 MiB, độ ưu tiên -10). Khi một pod thật cần chỗ, nó đẩy pod giữ chỗ ra. Pod giữ chỗ không còn chỗ chạy sẽ làm Karpenter dựng máy mới thay. Nhờ vậy lúc nào cũng có chỗ trống cho API, frontend và worker.
- DaemonSet `image-prepuller` tải sẵn image của API, worker và frontend về mọi máy Karpenter, nên pod mới không phải tải.
- API không dùng startup probe. Chú thích trong `apps/api.values.yaml` ghi lại phép đo: 20 lần cold start cho trung vị 2,97 giây khi có startup probe và 2,06 giây khi không có. Readiness probe vẫn bật.
- Mạng đặt `WARM_IP_TARGET=10` và `MINIMUM_IP_TARGET=30`, nên mỗi máy giữ sẵn 30 địa chỉ IP ngay khi khởi động. Pool `worker` chỉ dùng máy 4 vCPU, chứa được tối đa 58 pod (so với 17 ở máy `t3.medium`), để một đợt nhiều Job không làm hết địa chỉ.

### 3.5 Dữ liệu và hàng đợi

Aurora chạy PostgreSQL 17.9, một instance ghi, từ 0,5 đến 2 ACU, không tạm dừng. Dữ liệu được mã hóa, nằm trong mạng con riêng và chỉ nhận kết nối từ nhóm bảo mật của các máy chủ. RDS tạo mật khẩu trong Secrets Manager và đổi 7 ngày một lần. API và worker đọc mật khẩu qua EKS Pod Identity, giữ trong bộ nhớ 1 phút, và nếu đọc lỗi thì dùng lại mật khẩu gần nhất. Cơ chế này mới có test đơn vị, chưa thử với một lần đổi mật khẩu thật.

Bảng `applications` có thêm chỉ mục `applications_queued_idx`, chỉ chứa các hàng đang chờ chấm. Hệ thống không dùng chỉ mục này. Nó có sẵn cho đề xuất A ở mục 3.6.

Hàng đợi SQS `demo-jobs` giữ phiếu 14 ngày, ẩn phiếu 60 giây sau mỗi lần nhận, chờ phiếu mới tối đa 20 giây mỗi lần hỏi (long polling), và cho nhận tối đa 8 lần trước khi chuyển sang DLQ. Một cảnh báo CloudWatch kêu khi DLQ có phiếu.

### 3.6 Worker: hệ thống và hai đề xuất cải tiến

Hệ thống dùng một ScaledJob với một trigger SQS. KEDA đọc độ dài hàng đợi, bật Job, và mỗi Job lấy tối đa 10 phiếu rồi tắt. Thiết kế này đúng yêu cầu "KEDA với SQS" của đề tài. Chu kỳ hỏi nằm ở `pollingInterval: 1` trong `apps/worker.values.yaml`. Đây là bản demo nên mới dùng 1 giây.

Trong lúc đo, nhóm thử thêm hai ý tưởng để điểm hiện nhanh hơn. Cả hai chưa được duyệt và không được áp dụng vào hệ thống. Báo cáo đo chúng chỉ để có thêm góc nhìn, và gọi là đề xuất A và đề xuất B.

| | Hệ thống (KEDA với SQS) | Đề xuất A | Đề xuất B |
|---|---|---|---|
| Ý tưởng | KEDA nhìn độ dài hàng đợi SQS | thêm một cách đếm việc thứ hai: số hồ sơ đang chờ chấm trong cơ sở dữ liệu (trigger PostgreSQL), trong cùng ScaledJob | tách thành hai ScaledJob, mỗi cái một trigger |
| Số ScaledJob | 1 | 1 | 2 (`worker` và `worker-sqs`) |
| Trigger | SQS | SQS và PostgreSQL, lấy số lớn hơn | `worker`: PostgreSQL. `worker-sqs`: SQS |
| Chu kỳ hỏi đã đo | 1 giây và 30 giây | 1 giây và 30 giây | PostgreSQL 1 giây, SQS 60 giây |
| Số Job tối đa (`maxReplicaCount`) | 20 | 20 | 20 mỗi ScaledJob, tổng có thể 40 |
| Tình trạng | đang áp dụng | chưa duyệt, không áp dụng. Trigger PostgreSQL có sẵn trong chart nhưng tắt (`postgres.enabled: false`) | chưa duyệt, không áp dụng. Chỉ có mã tham chiếu trong `docs/reference/worker-split/` |

Trigger PostgreSQL của đề xuất A và B chạy truy vấn sau:

```sql
SELECT count(*) FROM applications
WHERE screening_status = 'queued'
  AND screening_requested_at > now() - interval '5 minutes'
```

Cửa sổ 5 phút để một hồ sơ mồ côi (phiếu bị mất hoặc worker chết giữa chừng) không làm KEDA bật Job rỗng mãi. Chú thích trong `charts/app/values.yaml` ghi một lần đo trước khi có cửa sổ này: 3 hồ sơ mồ côi đã làm bật 7 Job rỗng trong 3,5 phút.

Mã của đề xuất B nằm ở `docs/reference/worker-split/`, kèm README. Thư mục này chỉ để đọc: không script, workflow CI/CD hay Helm release nào đọc nó, và nó không được áp dụng lên cụm. Mã được viết lại từ cài đặt tạm dùng cho lần đo ngày 3/11/2026 và mới chỉ chạy qua `helm lint` và `helm template`, chưa từng chạy trên cụm. Vì vậy số đo của đề xuất B (mục 5.5) là của cài đặt tạm, không phải của mã tham chiếu.

### 3.7 Vận hành

- Trạng thái của Terraform (danh sách tài nguyên đã dựng) lưu trong bucket S3 `demo-tfstate-<mã tài khoản>`, có bật lưu phiên bản và khóa. Bucket do `scripts/bootstrap-state.sh` tạo và nằm ngoài Terraform, nên lệnh hủy không xóa nó. `scripts/up.sh` chưa tự gọi script tạo bucket.
- `scripts/up.sh` dựng hạ tầng, build và đẩy image, rồi triển khai ứng dụng. `scripts/down.sh` hủy theo thứ tự: gỡ ứng dụng (ALB biến mất), gỡ máy do Karpenter tạo, hủy hạ tầng, rồi liệt kê phần còn sót. Lần dựng đầu phải chạy hai lượt: khi kho ECR còn trống, `prepull_images` phải là `[]`, và chỉ đặt lại giá trị sau khi đã đẩy image.
- CI/CD dùng GitHub Actions. `ci.yml` chạy khi có pull request và khi push vào `main` (Go, web, Helm, Terraform, build image). `cd.yml` chạy khi push vào `main` có lọc đường dẫn, hoặc chạy tay. Nó đăng nhập AWS bằng OIDC (role `demo-github-actions`), build và đẩy image lên ECR, rồi `helm upgrade` các ứng dụng. CD chỉ triển khai ứng dụng, còn `terraform apply` vẫn chạy tay. CD triển khai đúng mã trên GitHub, nên thay đổi chưa push không nằm trong đó.
- Giám sát bật mặc định (`enable_monitoring`, `enable_logging`): Prometheus và Grafana, Loki ghi log lên S3 (giữ 7 ngày), Alloy thu log và sự kiện của Kubernetes.
- Chi phí chạy theo giờ gồm phí điều khiển EKS (khoảng 0,10 USD mỗi giờ), NAT gateway, ALB, Aurora ở mức tối thiểu 0,5 ACU, 2 máy hệ thống và các máy dự phòng. Báo cáo chưa có số chi phí thật vì chưa đối chiếu bảng giá và chưa có hóa đơn.

## 4. Cách đo

### 4.1 Điều kiện đo

Mọi phép đo chạy từ một máy ngoài cụm, đi qua ALB, và máy đo phải nằm trong `INBOUND_CIDRS`. Vì vậy số đo gồm cả độ trễ mạng của máy đo. Chương 5 tách phần đó ra khi có thể.

| Phép đo | Ngày | Công cụ | Ghi chú |
|---|---|---|---|
| Cold start API, 20 lượt | 30/10/2026 (ngày tạo file) | `scripts/test-cold-start.sh` | qua ALB, đường `/positions?limit=10` (có đọc cơ sở dữ liệu). Theo ghi chép lúc đo, `scaledownPeriod` được hạ xuống 10 giây để đỡ chờ |
| Cold start worker của hệ thống (chỉ SQS), hỏi 1 giây | 2/11/2026 | `scripts/score-cold-start.sh` | chỉ còn bảng tổng hợp trong nhật ký cũ |
| Cold start worker của hệ thống, hỏi 30 giây | 2/11/2026 | `scripts/score-cold-start.sh` | dựng lại từ số đã in lúc đo. Theo nhật ký cũ, chuỗi này không có khoảng nghỉ ngẫu nhiên trước mỗi lượt |
| Cold start worker của đề xuất A, hỏi 1 giây và 30 giây | 3/11/2026 | `scripts/score-cold-start.sh` | có khoảng nghỉ ngẫu nhiên trước mỗi lượt |
| Cold start worker của đề xuất B (tách hai ScaledJob), 20 lượt và 10 lượt | 3/11/2026 và 4/11/2026 | `scripts/score-cold-start.sh` | có khoảng nghỉ ngẫu nhiên trước mỗi lượt |
| Karpenter, HPA, trigger CPU, 200 người dùng, Aurora, mất một máy spot, image bất kỳ và arm64 | 2/11/2026 | script trong `scripts/` và `docs/results/` | xem từng mục trong chương 5 |
| Hồ sơ mồ côi (đề xuất A) | 3/11/2026 | chạy tay | chu kỳ hỏi 30 giây |

### 4.2 Các chỉ số

Chuỗi API (`cold-start-api.csv`):
- `total_s`: tổng thời gian của yêu cầu, đo ở máy khách.
- `connect_s`: thời gian mở kết nối tới ALB.
- `ttfb_s`: thời gian cho tới khi nhận byte đầu tiên, gồm cả thời gian chờ API bật.
- `pod_created_after_request_s`: pod được tạo sau yêu cầu bao lâu.
- `created_to_scheduled_s` và `created_to_ready_s`: từ lúc tạo pod đến lúc được xếp lên máy, và đến lúc sẵn sàng. Độ phân giải 1 giây.
- "Phía hệ thống" là `total_s` trừ `connect_s`, tức bỏ thời gian mở kết nối ở máy đo.

Chuỗi worker (`score-cold-start.sh`):
- `api_reply_s`: thời gian API trả lời lệnh Score (mã 202), đo ở máy khách.
- `total_s`: từ lúc bấm đến lúc điểm hiện, theo đồng hồ máy đo, hỏi lại mỗi 0,25 giây.
- `server_s`: `doneAt` trừ `requestedAt` trong cơ sở dữ liệu, không gồm độ trễ mạng của máy đo.
- `keda_job_after_s`: từ lúc bấm đến lúc Job đầu tiên xuất hiện. Dấu thời gian tạo Job có độ phân giải 1 giây nên giá trị có thể hơi âm.
- `jobs_created`: số Job được bật cho một lần Score. 1 là lý tưởng, lớn hơn nghĩa là có Job dư.
- Phần dư phía máy khách do báo cáo tự định nghĩa, không phải cột của file: `total_s` trừ `server_s` trừ `api_reply_s`. Nó gồm bước hỏi lại kết quả (0,25 giây) và độ trễ mạng. Ở đề xuất A hỏi 1 giây, trung vị là 0,24 giây.

Các phân vị trong báo cáo tính bằng nội suy tuyến tính trên 20 mẫu. Script cũng in một bản tóm tắt theo cách chọn hạng gần nhất, nên p95 do script in có thể lệch nhẹ so với bảng trong báo cáo.

### 4.3 Tách thời gian chấm điểm thành hai đoạn

Từ lúc việc xuất hiện đến lúc có điểm gồm hai đoạn:

```
server_s  =  W  +  C
W = thời gian chờ KEDA phát hiện việc và tạo Job   (đo bằng keda_job_after_s)
C = thời gian từ lúc Job được tạo đến lúc điểm được ghi   (server_s trừ keda_job_after_s)
```

Cách tách này cho thấy đoạn nào làm các thiết kế khác nhau (mục 5.3 và 5.5).

Hai cột có gốc thời gian khác nhau. `keda_job_after_s` tính từ lúc bấm, còn `server_s` tính từ lúc API ghi hồ sơ (`requestedAt`). Hiệu của chúng vì vậy thấp hơn C thật một khoảng bằng thời gian API xử lý trước khi ghi, và âm khi API trả lời chậm. Ví dụ lượt 5 của đề xuất A hỏi 1 giây có `api_reply_s` 2,39 giây và hiệu -1,10 giây. Trung vị ít bị ảnh hưởng vì phần lớn lượt có `api_reply_s` khoảng 0,2 giây.

### 4.4 Giới hạn của cách đo

- Chỉ một máy đo, một vị trí mạng, 20 lượt mỗi chuỗi. Với mẫu nhỏ như vậy, các giá trị cao như p95 chỉ dựa vào một hai lần đo.
- Việc chấm điểm là giả lập và gần như không tốn thời gian. Các phép đo vì vậy cho thấy thời gian phát hiện việc và khởi động, không phải thời gian xử lý thật.
- `score-cold-start.sh` đợi 25 giây sau khi có điểm rồi mới đếm Job. Với chu kỳ hỏi 1 giây, 25 giây là đủ. Với chu kỳ 30 giây, Job dư tạo ở lần kiểm tra kế tiếp có thể chưa xuất hiện, nên số Job dư của các chuỗi 30 giây là con số thấp nhất có thể.

## 5. Kết quả

Mọi bảng dưới đây ghi kết quả đo. Với thời gian, số càng nhỏ càng tốt. Yêu cầu của đề tài là dưới 3 giây.

### 5.1 Trang web (API) khi đang tắt

Bảng này cho biết mất bao lâu để API trả lời khách đầu tiên khi nó đang ở 0 pod. Đây là chuỗi 20 lượt (`cold-start-api.csv`), đường `/positions?limit=10`, đơn vị giây.

| Chỉ số | Tổng thời gian (`total_s`) | Bỏ 6 lượt mở kết nối chậm (`connect_s` từ 0,5 giây) | Phía hệ thống (`total_s` trừ `connect_s`) |
|---|---|---|---|
| Số lượt | 20 | 14 | 20 |
| Nhỏ nhất | 1,43 | 1,43 | 1,34 |
| Trung vị | 2,40 | 2,19 | 2,08 |
| p95 | 3,62 | 2,97 | 2,81 |
| Lớn nhất | 5,03 | 2,99 | 2,89 |
| Dưới 3 giây | 17/20 | 14/14 | 20/20 |

Kết quả từng lượt, nguyên văn file `cold-start-api.csv` (đổi tên cột theo ý nghĩa, làm tròn 2 chữ số, đơn vị giây):

| Lượt | Đường gọi | Mã HTTP | Tổng thời gian | Thời gian mở kết nối | Thời gian đến byte đầu tiên | Pod được tạo sau request | Từ tạo pod đến được xếp | Từ tạo pod đến Ready | Ghi chú |
|---|---|---|---|---|---|---|---|---|---|
| 1 | direct | 200 | 3,29 | 1,22 | 3,29 | 1,70 | 0 | 1 | mở kết nối chậm (từ 0,5 giây) |
| 2 | direct | 200 | 2,99 | 0,18 | 2,98 | 0,30 | 0 | 2 |  |
| 3 | direct | 200 | 1,87 | 0,05 | 1,86 | 0,14 | 0 | 1 |  |
| 4 | direct | 200 | 2,13 | 0,26 | 2,13 | 0,33 | 0 | 1 |  |
| 5 | direct | 200 | 3,54 | 1,13 | 3,49 | 1,63 | 0 | 1 | mở kết nối chậm (từ 0,5 giây) |
| 6 | direct | 200 | 2,96 | 0,07 | 2,91 | -0,10 | 0 | 2 |  |
| 7 | direct | 200 | 2,50 | 0,09 | 2,50 | 0,54 | 0 | 1 |  |
| 8 | direct | 200 | 2,30 | 0,08 | 2,29 | 0,24 | 0 | 1 |  |
| 9 | direct | 200 | 1,43 | 0,09 | 1,43 | 0,29 | 0 | 0 |  |
| 10 | direct | 200 | 2,86 | 0,72 | 2,86 | 0,72 | 0 | 1 | mở kết nối chậm (từ 0,5 giây) |
| 11 | direct | 200 | 2,05 | 0,29 | 2,05 | 0,84 | 0 | 0 |  |
| 12 | direct | 200 | 5,03 | 3,02 | 5,03 |  |  |  | mở kết nối chậm (từ 0,5 giây); không có dữ liệu pod |
| 13 | direct | 200 | 2,62 | 0,37 | 2,62 | 0,20 | 0 | 1 |  |
| 14 | direct | 200 | 2,71 | 0,77 | 2,71 | 1,08 | 0 | 1 | mở kết nối chậm (từ 0,5 giây) |
| 15 | direct | 200 | 2,04 | 0,20 | 2,03 | 0,44 | 0 | 1 |  |
| 16 | direct | 200 | 2,01 | 0,05 | 2,01 | 0,30 | 0 | 1 |  |
| 17 | direct | 200 | 2,13 | 0,05 | 2,13 | 0,41 | 0 | 1 |  |
| 18 | direct | 200 | 2,87 | 1,25 | 2,83 | 0,98 | 0 | 1 | mở kết nối chậm (từ 0,5 giây) |
| 19 | direct | 200 | 2,25 | 0,12 | 2,24 | 0,43 | 0 | 1 |  |
| 20 | direct | 200 | 2,29 | 0,04 | 2,25 | 0,30 | 0 | 1 |  |

Các lượt có tổng thời gian từ 3 giây trở lên:

| Lượt | Tổng thời gian | Thời gian mở kết nối | Phía hệ thống | Ghi chú |
|---|---|---|---|---|
| 1 | 3,29 | 1,22 | 2,07 | kết nối chậm (mất gói SYN tới ALB, độ trễ mạng của máy đo) |
| 5 | 3,54 | 1,13 | 2,41 | kết nối chậm |
| 12 | 5,03 | 3,02 | 2,00 | kết nối chậm |

Thời gian từ lúc có yêu cầu đến lúc pod sẵn sàng (19 lượt có dữ liệu pod):

| Giai đoạn | Giá trị (giây) |
|---|---|
| Từ lúc có request đến lúc pod được tạo (`pod_created_after_request_s`) | trung vị 0,41 (từ -0,10 đến 1,70) |
| Từ lúc tạo pod đến lúc được xếp lên node (`created_to_scheduled_s`) | 0 ở cả 19 lượt |
| Từ lúc tạo pod đến lúc Ready (`created_to_ready_s`) | trung vị 1, lớn nhất 2 (độ phân giải 1 giây) |

### 5.2 Phần chấm điểm (worker) của hệ thống

Hệ thống dùng KEDA với SQS. Bản demo đặt chu kỳ hỏi 1 giây, tức KEDA kiểm tra hàng đợi mỗi giây một lần. Bảng dưới là kết quả 20 lượt bấm Score khi worker đang tắt. Đơn vị là giây.

Tên các cột trong báo cáo, và cột tương ứng trong file:

| Tên trong báo cáo | Cột trong file | Ý nghĩa |
|---|---|---|
| Thời gian API trả lời | `api_reply_s` | từ lúc bấm đến lúc API trả lời "đã nhận" (mã 202), đo ở máy khách |
| Thời gian chờ điểm của người dùng | `total_s` | từ lúc bấm đến lúc điểm hiện, theo đồng hồ máy đo, gồm cả mạng |
| Thời gian xử lý trong hệ thống | `server_s` | từ lúc hồ sơ được ghi vào hàng đợi đến lúc có điểm, đo trong cơ sở dữ liệu, không gồm mạng của máy đo |
| Thời gian đến khi KEDA tạo Job | `keda_job_after_s` | từ lúc bấm đến lúc Job đầu tiên xuất hiện. Độ phân giải 1 giây |
| Số Job mỗi lần chấm | `jobs_created` | 1 là lý tưởng. Lớn hơn nghĩa là có Job dư |

Hệ thống, hỏi mỗi 1 giây (chỉ còn bảng tổng hợp trong nhật ký cũ, nên không có p25, p75 và số liệu từng lượt):

| Chỉ số | Nhỏ nhất | Trung vị | p95 | Lớn nhất | Giải thích |
|---|---|---|---|---|---|
| Thời gian API trả lời | 0,17 | 0,20 | 2,17 | 2,26 | API ghi hồ sơ, bỏ phiếu vào hàng đợi rồi trả lời, khoảng 0,2 giây. Vài lượt chậm hơn là do API |
| Thời gian chờ điểm của người dùng | 7,73 | 10,74 | 14,25 | 14,32 | người dùng phải chờ gần 11 giây. Không lượt nào dưới 3 giây |
| Thời gian xử lý trong hệ thống | 7,11 | 10,18 | 12,52 | 13,05 | cả 20 lượt từ 7 giây trở lên, nên 0/20 lượt đạt yêu cầu dưới 3 giây |
| Thời gian đến khi KEDA tạo Job | 6,0 | 9,6 | 16,3 | 16,8 | KEDA mất 6 đến 17 giây mới nhận ra có việc, vì con số độ dài hàng đợi cập nhật chậm |
| Số Job mỗi lần chấm | 3 | 3 | 3 | 3 | cả 20 lượt đều bật 3 Job: 1 Job có việc và 2 Job dư. Tổng cộng 40 Job dư |

Hệ thống, hỏi mỗi 30 giây (file `worker-cold-start-keda-poll30.csv`, dựng lại từ số đã in lúc đo; theo nhật ký cũ chuỗi này không có khoảng nghỉ ngẫu nhiên giữa các lượt, nên trung vị không đại diện):

| Chỉ số | Nhỏ nhất | p25 | Trung vị | p75 | p95 | Lớn nhất | Giải thích |
|---|---|---|---|---|---|---|---|
| Thời gian API trả lời | 0,19 | 0,20 | 0,22 | 0,24 | 0,38 | 0,48 | API nhanh ở cả 20 lượt |
| Thời gian chờ điểm của người dùng | 4,49 | 12,30 | 12,66 | 12,84 | 42,42 | 42,81 | người dùng thường chờ khoảng 12 giây, có 4 lượt chờ tới khoảng 42 giây |
| Thời gian xử lý trong hệ thống | 4,07 | 11,99 | 12,19 | 12,36 | 42,20 | 42,37 | 15 lượt quanh 12 giây, 4 lượt quanh 42 giây (lỡ một lần KEDA kiểm tra nên chờ thêm đúng một chu kỳ 30 giây). Không lượt nào dưới 3 giây |
| Thời gian đến khi KEDA tạo Job | 2,80 | 10,55 | 10,85 | 10,93 | 40,80 | 40,90 | 14 lượt từ 10,1 đến 11,0 giây, 4 lượt từ 40,6 đến 40,9 giây, 2 lượt lẻ (2,8 và 8,9 giây) |
| Số Job mỗi lần chấm | 1 | 1 | 1 | 2 | 2 | 2 | 14 lượt có 1 Job, 6 lượt có 2 Job (6 Job dư) |

Kết quả từng lượt, hệ thống hỏi mỗi 30 giây (nguyên văn file `worker-cold-start-keda-poll30.csv`, đổi tên cột theo ý nghĩa):

| Lượt | Thời gian API trả lời | Thời gian chờ điểm của người dùng | Thời gian xử lý trong hệ thống | Thời gian đến khi KEDA tạo Job | Số Job mỗi lần chấm | Giải thích |
|---|---|---|---|---|---|---|
| 1 | 0,21 | 4,49 | 4,07 | 2,8 | 1 | bình thường |
| 2 | 0,23 | 42,81 | 42,37 | 40,9 | 1 | lỡ một lần KEDA kiểm tra nên chờ thêm một chu kỳ 30 giây |
| 3 | 0,20 | 12,71 | 12,20 | 10,8 | 2 | 1 Job dư |
| 4 | 0,23 | 12,65 | 12,08 | 10,7 | 2 | 1 Job dư |
| 5 | 0,33 | 42,23 | 41,92 | 40,6 | 1 | lỡ một lần KEDA kiểm tra nên chờ thêm một chu kỳ 30 giây |
| 6 | 0,20 | 12,82 | 12,34 | 11,0 | 1 | bình thường |
| 7 | 0,24 | 12,05 | 11,49 | 10,1 | 1 | bình thường |
| 8 | 0,25 | 12,34 | 12,10 | 10,6 | 1 | bình thường |
| 9 | 0,24 | 12,70 | 12,28 | 10,9 | 2 | 1 Job dư |
| 10 | 0,20 | 12,36 | 12,06 | 10,6 | 1 | bình thường |
| 11 | 0,23 | 42,39 | 42,08 | 40,6 | 2 | 1 Job dư; lỡ một lần KEDA kiểm tra nên chờ thêm một chu kỳ 30 giây |
| 12 | 0,20 | 12,36 | 12,04 | 10,7 | 2 | 1 Job dư |
| 13 | 0,21 | 12,67 | 12,22 | 10,9 | 2 | 1 Job dư |
| 14 | 0,20 | 11,79 | 11,53 | 10,2 | 1 | bình thường |
| 15 | 0,21 | 12,71 | 12,40 | 10,9 | 1 | bình thường |
| 16 | 0,37 | 10,63 | 10,06 | 8,9 | 1 | bình thường |
| 17 | 0,48 | 42,40 | 42,19 | 40,8 | 1 | lỡ một lần KEDA kiểm tra nên chờ thêm một chu kỳ 30 giây |
| 18 | 0,19 | 12,61 | 12,18 | 10,9 | 1 | bình thường |
| 19 | 0,24 | 12,90 | 12,32 | 10,9 | 1 | bình thường |
| 20 | 0,21 | 12,19 | 11,84 | 10,4 | 1 | bình thường |

So sánh hệ thống ở hai chu kỳ hỏi:

| Chỉ số | Hỏi mỗi 1 giây | Hỏi mỗi 30 giây |
|---|---|---|
| Thời gian xử lý trong hệ thống, trung vị | 10,18 | 12,19 |
| Thời gian xử lý trong hệ thống, lớn nhất | 13,05 | 42,37 |
| Thời gian xử lý dưới 3 giây | 0/20 | 0/20 |
| Thời gian đến khi KEDA tạo Job, trung vị | 9,6 | 10,85 |
| Số lượt đúng 1 Job | 0/20 | 14/20 |
| Job dư trong 20 lượt | 40 | 6 |
| Số lần KEDA hỏi SQS mỗi ngày khi rảnh | khoảng 86.400 | khoảng 2.880 |

### 5.3 Thời gian chờ của hệ thống nằm ở đâu

Theo cách tách ở mục 4.3, thời gian xử lý gồm hai đoạn: W là thời gian KEDA mất để nhận ra có việc và bật Job, C là thời gian từ lúc Job được bật đến lúc có điểm. Đơn vị là giây (trung vị):

| Đoạn | Hỏi mỗi 1 giây | Hỏi mỗi 30 giây |
|---|---|---|
| W: thời gian đến khi KEDA tạo Job | 9,6 | 10,85 |
| W nhỏ nhất đến lớn nhất | 6,0 đến 16,8 | 2,80 đến 40,90 |
| C: từ lúc tạo Job đến lúc có điểm | 0,96 | 1,39 |
| Thời gian xử lý trong hệ thống | 10,18 | 12,19 |
| Phần cộng thêm ở máy khách (thời gian chờ điểm trừ thời gian xử lý) | 0,47 | 0,42 |

### 5.4 Job dư của hệ thống

Job dư là Job được bật nhưng không còn việc nên tắt ngay. Bảng cho biết có bao nhiêu Job dư trong 20 lượt, theo chu kỳ hỏi:

| Chu kỳ hỏi | Lượt có Job dư | Số Job mỗi lần chấm | Tổng Job dư | Nguồn |
|---|---|---|---|---|
| 1 giây | 20/20 | 3 ở 20 lượt | 40 | mục 5.2, chỉ còn bảng tổng hợp |
| 30 giây | 6/20 | 1 Job ở 14 lượt, 2 Job ở 6 lượt | 6 | mục 5.2, có file thô |

### 5.5 Góc nhìn thêm: hai đề xuất cải tiến (chưa duyệt, không áp dụng)

Hai đề xuất này không phải một phần của hệ thống. Chúng được đo chỉ để xem nếu thay đổi cách KEDA nhận ra việc thì thời gian chờ thay đổi thế nào. Cách đọc các cột giống mục 5.2.

Đề xuất A là thêm cách đếm việc thứ hai (số hồ sơ đang chờ chấm trong cơ sở dữ liệu) vào cùng ScaledJob. Kết quả từng lượt khi KEDA hỏi mỗi 1 giây (nguyên văn file `worker-cold-start-keda-postgres.csv`, đổi tên cột theo ý nghĩa):

| Lượt | Thời gian API trả lời | Thời gian chờ điểm của người dùng | Thời gian xử lý trong hệ thống | Thời gian đến khi KEDA tạo Job | Số Job mỗi lần chấm | Giải thích |
|---|---|---|---|---|---|---|
| 1 | 0,25 | 2,81 | 2,04 | 0,2 | 2 | 1 Job dư |
| 2 | 0,23 | 1,67 | 1,17 | -0,8 | 2 | 1 Job dư; âm do làm tròn 1 giây, đọc là xấp xỉ 0 |
| 3 | 0,22 | 2,11 | 1,71 | 0,7 | 2 | 1 Job dư |
| 4 | 0,20 | 1,64 | 1,30 | 0,3 | 2 | 1 Job dư |
| 5 | 2,39 | 4,64 | 2,00 | 3,1 | 3 | 2 Job dư; API trả lời chậm (2,39 giây) làm các cột tính từ lúc bấm đều tăng |
| 6 | 0,21 | 2,49 | 1,97 | 0,8 | 3 | 2 Job dư |
| 7 | 0,19 | 2,49 | 2,00 | 0,8 | 2 | 1 Job dư |
| 8 | 0,18 | 2,02 | 1,60 | 0,5 | 2 | 1 Job dư |
| 9 | 0,22 | 2,05 | 1,83 | 0,1 | 2 | 1 Job dư |
| 10 | 1,81 | 3,70 | 1,49 | 1,8 | 2 | 1 Job dư; API trả lời chậm (1,81 giây) làm các cột tính từ lúc bấm đều tăng |
| 11 | 0,20 | 1,65 | 1,34 | 0,1 | 2 | 1 Job dư |
| 12 | 1,20 | 3,63 | 2,01 | 1,4 | 2 | 1 Job dư; API trả lời chậm (1,20 giây) làm các cột tính từ lúc bấm đều tăng |
| 13 | 0,22 | 1,95 | 1,59 | 0,1 | 2 | 1 Job dư |
| 14 | 0,26 | 3,00 | 2,41 | 0,7 | 2 | 1 Job dư; thời gian xử lý lớn nhất của chuỗi |
| 15 | 1,94 | 4,00 | 2,06 | 2,3 | 2 | 1 Job dư; API trả lời chậm (1,94 giây) làm các cột tính từ lúc bấm đều tăng |
| 16 | 0,19 | 2,12 | 1,53 | 0,1 | 2 | 1 Job dư |
| 17 | 0,22 | 2,08 | 1,54 | 0,1 | 2 | 1 Job dư |
| 18 | 0,20 | 1,65 | 1,37 | -0,2 | 3 | 2 Job dư; âm do làm tròn 1 giây, đọc là xấp xỉ 0 |
| 19 | 0,23 | 1,70 | 1,34 | -0,3 | 2 | 1 Job dư; âm do làm tròn 1 giây, đọc là xấp xỉ 0 |
| 20 | 0,22 | 2,23 | 1,85 | 0,3 | 2 | 1 Job dư |

Đề xuất A, hỏi mỗi 1 giây, tổng hợp và giải thích từng dòng:

| Chỉ số | Nhỏ nhất | p25 | Trung vị | p75 | p95 | Lớn nhất | Giải thích |
|---|---|---|---|---|---|---|---|
| Thời gian API trả lời | 0,18 | 0,20 | 0,22 | 0,25 | 1,96 | 2,39 | phần lớn lượt khoảng 0,2 giây. Bốn lượt chậm (1,2 đến 2,39 giây) là do API, không liên quan KEDA |
| Thời gian chờ điểm của người dùng | 1,64 | 1,89 | 2,12 | 2,86 | 4,03 | 4,64 | dài hơn thời gian xử lý khoảng 0,5 giây. Có 5 lượt từ 3 giây trở lên, 4 lượt trong đó do API chậm |
| Thời gian xử lý trong hệ thống | 1,17 | 1,46 | 1,66 | 2,00 | 2,08 | 2,41 | cả 20 lượt dưới 3 giây |
| Thời gian đến khi KEDA tạo Job | -0,80 | 0,10 | 0,30 | 0,80 | 2,34 | 3,10 | KEDA nhận ra việc gần như ngay. Giá trị âm do làm tròn 1 giây. Các lượt từ 1,4 giây trở lên là lượt API chậm |
| Số Job mỗi lần chấm | 2 | 2 | 2 | 2 | 3 | 3 | không lượt nào đúng 1 Job: 17 lượt có 2 Job, 3 lượt có 3 Job. Tổng 23 Job dư |

Kết quả từng lượt của đề xuất A khi KEDA hỏi mỗi 30 giây (nguyên văn file `worker-cold-start-keda-postgres-poll30.csv`, đổi tên cột theo ý nghĩa):

| Lượt | Thời gian API trả lời | Thời gian chờ điểm của người dùng | Thời gian xử lý trong hệ thống | Thời gian đến khi KEDA tạo Job | Số Job mỗi lần chấm | Giải thích |
|---|---|---|---|---|---|---|
| 1 | 0,26 | 29,19 | 28,52 | 27,4 | 1 | bình thường |
| 2 | 0,23 | 31,02 | 30,37 | 29,2 | 1 | bình thường |
| 3 | 0,26 | 6,84 | 6,48 | 5,2 | 1 | bình thường |
| 4 | 1,93 | 6,71 | 4,53 | 5,2 | 2 | 1 Job dư; API trả lời chậm (1,93 giây) làm các cột tính từ lúc bấm đều tăng |
| 5 | 0,24 | 25,58 | 25,11 | 23,8 | 1 | bình thường |
| 6 | 0,21 | 10,21 | 9,93 | 8,4 | 1 | bình thường |
| 7 | 0,25 | 3,42 | 2,86 | 1,6 | 1 | bình thường |
| 8 | 0,40 | 5,25 | 4,86 | 3,5 | 1 | bình thường |
| 9 | 0,23 | 5,35 | 4,82 | 3,6 | 1 | bình thường |
| 10 | 0,22 | 20,36 | 19,88 | 18,2 | 1 | bình thường |
| 11 | 0,23 | 3,32 | 3,09 | 1,7 | 1 | bình thường |
| 12 | 0,43 | 6,80 | 6,18 | 5,1 | 1 | bình thường |
| 13 | 0,24 | 7,26 | 6,99 | 5,8 | 1 | bình thường |
| 14 | 1,19 | 3,01 | 1,68 | 1,1 | 1 | API trả lời chậm (1,19 giây) làm các cột tính từ lúc bấm đều tăng |
| 15 | 0,31 | 6,90 | 6,39 | 4,9 | 1 | bình thường |
| 16 | 0,23 | 21,64 | 21,24 | 19,7 | 2 | 1 Job dư |
| 17 | 0,23 | 25,13 | 24,54 | 23,1 | 2 | 1 Job dư |
| 18 | 0,23 | 21,38 | 20,86 | 19,1 | 1 | bình thường |
| 19 | 0,21 | 7,90 | 7,46 | 6,3 | 1 | bình thường |
| 20 | 0,22 | 7,73 | 3,58 | 2,1 | 1 | bình thường |

Đề xuất A, hỏi mỗi 30 giây, tổng hợp:

| Chỉ số | Nhỏ nhất | p25 | Trung vị | p75 | p95 | Lớn nhất | Giải thích |
|---|---|---|---|---|---|---|---|
| Thời gian API trả lời | 0,21 | 0,23 | 0,23 | 0,27 | 1,22 | 1,93 | API nhanh, hai lượt chậm hơn 1 giây |
| Thời gian chờ điểm của người dùng | 3,01 | 6,37 | 7,50 | 21,45 | 29,28 | 31,02 | không lượt nào dưới 3 giây |
| Thời gian xử lý trong hệ thống | 1,68 | 4,75 | 6,74 | 20,95 | 28,61 | 30,37 | rải từ 1,7 đến 30,4 giây tùy lúc bấm so với lần KEDA kiểm tra. Chỉ 2 lượt dưới 3 giây |
| Thời gian đến khi KEDA tạo Job | 1,10 | 3,58 | 5,50 | 19,25 | 27,49 | 29,20 | rải gần hết một chu kỳ 30 giây |
| Số Job mỗi lần chấm | 1 | 1 | 1 | 1 | 2 | 2 | 17 lượt có 1 Job, 3 lượt có 2 Job (3 Job dư) |

Đề xuất B là tách thành hai ScaledJob: một ScaledJob chỉ có trigger PostgreSQL hỏi mỗi 1 giây, và một ScaledJob chỉ có trigger SQS hỏi mỗi 60 giây làm lưới an toàn. Kết quả từng lượt, đợt 20 lượt (nguyên văn file `worker-cold-start-keda-postgres-split.csv`, đổi tên cột theo ý nghĩa):

| Lượt | Thời gian API trả lời | Thời gian chờ điểm của người dùng | Thời gian xử lý trong hệ thống | Thời gian đến khi KEDA tạo Job | Số Job mỗi lần chấm | Giải thích |
|---|---|---|---|---|---|---|
| 1 | 0,24 | 2,10 | 1,57 | 0,0 | 1 | bình thường |
| 2 | 0,38 | 3,86 | 1,60 | 0,2 | 1 | chờ thêm 1,88 giây ngoài worker ở bước hỏi lại kết quả, chưa giải thích |
| 3 | 0,23 | 2,52 | 1,87 | 0,5 | 1 | bình thường |
| 4 | 0,24 | 2,11 | 1,83 | -0,3 | 1 | âm do làm tròn 1 giây, đọc là xấp xỉ 0 |
| 5 | 2,46 | 4,74 | 1,96 | 2,6 | 1 | API trả lời chậm (2,46 giây) làm các cột tính từ lúc bấm đều tăng |
| 6 | 0,26 | 1,75 | 1,48 | 0,0 | 1 | bình thường |
| 7 | 0,28 | 2,55 | 1,93 | 0,4 | 1 | bình thường |
| 8 | 0,26 | 2,50 | 1,84 | 0,2 | 1 | bình thường |
| 9 | 0,25 | 2,20 | 1,76 | 0,2 | 1 | bình thường |
| 10 | 0,44 | 2,61 | 1,79 | 0,3 | 1 | bình thường |
| 11 | 0,19 | 2,41 | 1,93 | 0,1 | 1 | bình thường |
| 12 | 0,24 | 2,14 | 1,88 | 0,1 | 1 | bình thường |
| 13 | 0,20 | 6,11 | 1,61 | -0,3 | 1 | chờ thêm 4,30 giây ngoài worker ở bước hỏi lại kết quả, chưa giải thích; âm do làm tròn 1 giây, đọc là xấp xỉ 0 |
| 14 | 0,18 | 2,06 | 1,68 | 0,9 | 1 | bình thường |
| 15 | 2,81 | 6,07 | 1,59 | 3,2 | 1 | API trả lời chậm (2,81 giây) làm các cột tính từ lúc bấm đều tăng; chờ thêm 1,67 giây ngoài worker ở bước hỏi lại kết quả, chưa giải thích |
| 16 | 0,21 | 2,38 | 1,63 | 0,7 | 1 | bình thường |
| 17 | 0,23 | 8,13 | 2,15 | 0,9 | 1 | chờ thêm 5,75 giây ngoài worker ở bước hỏi lại kết quả, chưa giải thích |
| 18 | 0,20 | 1,75 | 1,09 | 0,0 | 1 | bình thường |
| 19 | 0,26 | 2,64 | 1,99 | 0,9 | 1 | bình thường |
| 20 | 0,20 | 2,14 | 1,77 | 0,7 | 1 | bình thường |

Đề xuất B, đợt 20 lượt, tổng hợp và giải thích từng dòng:

| Chỉ số | Nhỏ nhất | p25 | Trung vị | p75 | p95 | Lớn nhất | Giải thích |
|---|---|---|---|---|---|---|---|
| Thời gian API trả lời | 0,18 | 0,21 | 0,24 | 0,27 | 2,48 | 2,81 | phần lớn lượt khoảng 0,2 giây. Hai lượt chậm (2,46 và 2,81 giây) là do API |
| Thời gian chờ điểm của người dùng | 1,75 | 2,13 | 2,46 | 2,95 | 6,21 | 8,13 | có 5 lượt từ 3 giây trở lên. Worker không phải nguyên nhân: thời gian xử lý của các lượt đó vẫn bình thường |
| Thời gian xử lý trong hệ thống | 1,09 | 1,61 | 1,78 | 1,89 | 2,00 | 2,15 | cả 20 lượt dưới 3 giây |
| Thời gian đến khi KEDA tạo Job | -0,30 | 0,08 | 0,25 | 0,75 | 2,63 | 3,20 | KEDA nhận ra việc gần như ngay vì trigger PostgreSQL hỏi mỗi 1 giây. Hai lượt cao nhất là hai lượt API chậm |
| Số Job mỗi lần chấm | 1 | 1 | 1 | 1 | 1 | 1 | cả 20 lượt đúng 1 Job, không có Job dư |

Kết quả từng lượt, đợt 10 lượt (nguyên văn file `worker-cold-start-split-10.csv`, đo ngày 4/11, đổi tên cột theo ý nghĩa):

| Lượt | Thời gian API trả lời | Thời gian chờ điểm của người dùng | Thời gian xử lý trong hệ thống | Thời gian đến khi KEDA tạo Job | Số Job mỗi lần chấm | Giải thích |
|---|---|---|---|---|---|---|
| 1 | 2,29 | 3,70 | 1,41 | 2,3 | 1 | API trả lời chậm (2,29 giây) làm các cột tính từ lúc bấm đều tăng |
| 2 | 0,23 | 1,62 | 1,36 | 0,1 | 1 | bình thường |
| 3 | 0,19 | 1,60 | 0,99 | -0,3 | 1 | âm do làm tròn 1 giây, đọc là xấp xỉ 0 |
| 4 | 0,25 | 2,05 | 1,42 | 0,1 | 1 | bình thường |
| 5 | 1,17 | 2,64 | 1,21 | 0,9 | 1 | API trả lời chậm (1,17 giây) làm các cột tính từ lúc bấm đều tăng |
| 6 | 0,27 | 4,11 | 1,84 | 0,5 | 1 | chờ thêm 2,00 giây ngoài worker ở bước hỏi lại kết quả, chưa giải thích |
| 7 | 0,27 | 1,69 | 1,39 | -0,1 | 1 | âm do làm tròn 1 giây, đọc là xấp xỉ 0 |
| 8 | 0,19 | 2,08 | 1,68 | 0,2 | 1 | bình thường |
| 9 | 0,26 | 1,67 | 1,33 | -0,3 | 1 | âm do làm tròn 1 giây, đọc là xấp xỉ 0 |
| 10 | 2,00 | 3,41 | 1,39 | 1,6 | 1 | API trả lời chậm (2,00 giây) làm các cột tính từ lúc bấm đều tăng |

Đề xuất B, đợt 10 lượt, tổng hợp:

| Chỉ số | Nhỏ nhất | p25 | Trung vị | p75 | p95 | Lớn nhất | Giải thích |
|---|---|---|---|---|---|---|---|
| Thời gian API trả lời | 0,19 | 0,23 | 0,26 | 0,95 | 2,16 | 2,29 | ba lượt chậm hơn 1 giây (lượt 1, 5 và 10: 2,29, 1,17 và 2,00 giây) là do API |
| Thời gian chờ điểm của người dùng | 1,60 | 1,67 | 2,06 | 3,22 | 3,93 | 4,11 | 3 lượt từ 3 giây trở lên: lượt 1 và 10 do API chậm, lượt 6 còn chờ thêm 2,00 giây ngoài worker ở bước hỏi lại kết quả, chưa giải thích |
| Thời gian xử lý trong hệ thống | 0,99 | 1,34 | 1,39 | 1,42 | 1,77 | 1,84 | cả 10 lượt dưới 3 giây |
| Thời gian đến khi KEDA tạo Job | -0,30 | -0,05 | 0,15 | 0,80 | 1,98 | 2,30 | KEDA nhận ra việc gần như ngay |
| Số Job mỗi lần chấm | 1 | 1 | 1 | 1 | 1 | 1 | cả 10 lượt đúng 1 Job |

Job dư của đề xuất theo chu kỳ hỏi SQS:

| Thiết kế | Chu kỳ hỏi của trigger SQS | Lượt có Job dư | Tổng Job dư | Nguồn |
|---|---|---|---|---|
| Đề xuất A | 1 giây | 20/20 | 23 | file thô, mục 5.5 |
| Đề xuất A | 30 giây | 3/20 | 3 | file thô, mục 5.5 |
| Đề xuất B (đợt 20 lượt) | 60 giây | 0/20 | 0 | file thô, mục 5.5 |
| Đề xuất B (đợt 10 lượt) | 60 giây | 0/10 | 0 | file thô, mục 5.5 |

Một bài thử riêng cho trigger PostgreSQL của đề xuất A, chạy trước khi có cửa sổ 5 phút. Bài thử đánh dấu 3 hồ sơ ở trạng thái chờ chấm trong cơ sở dữ liệu nhưng không có phiếu nào trong SQS (hồ sơ "mồ côi"), với chu kỳ hỏi 30 giây. Nguồn: `worker/fake-backlog-orphan.txt`, đo ngày 3/11.

| Giờ | Sự kiện |
|---|---|
| 14:31:25 | đánh dấu 3 hồ sơ chờ chấm (mã 2, 3, 4), không có phiếu trong SQS |
| 14:31:36 | Job rỗng thứ 1 xuất hiện |
| 14:32:04 | Job rỗng thứ 2 |
| 14:32:35 | Job rỗng thứ 3 |
| 14:33:04 | Job rỗng thứ 4 |
| 14:33:35 | Job rỗng thứ 5 |
| 14:34:04 | Job rỗng thứ 6 |
| 14:34:33 | Job rỗng thứ 7 |
| 14:34:57 | trả 3 hồ sơ về trạng thái ban đầu |
| 14:35:02 | thêm 1 Job nữa xuất hiện (trong 75 giây sau khi trả hồ sơ) |

Cứ mỗi nhịp hỏi 30 giây, KEDA lại bật một Job rỗng trong lúc 3 hồ sơ mồ côi còn ở trạng thái chờ chấm: 7 Job trong khoảng 3,5 phút. Đây là lý do truy vấn của trigger có cửa sổ 5 phút (mục 3.6).

### 5.6 Tổng hợp phần chấm điểm

| Tiêu chí | Hệ thống (KEDA với SQS, hỏi 1 giây) | Đề xuất A (hỏi 1 giây) | Đề xuất B |
|---|---|---|---|
| Tình trạng | đang áp dụng | chưa duyệt, không áp dụng | chưa duyệt, không áp dụng |
| Thời gian xử lý: trung vị, lớn nhất (giây) | 10,18 và 13,05 | 1,66 và 2,41 | 1,78 và 2,15 |
| Dưới 3 giây | 0/20 | 20/20 | 20/20 |
| Job dư trong 20 lượt | 40 | 23 | 0 |
| Số ScaledJob | 1 | 1 | 2 |
| Số Job tối đa | 20 | 20 | 20 mỗi ScaledJob (tổng có thể 40) |
| Có phụ thuộc cơ sở dữ liệu để bật Job nhanh không | không | không | có |
| Số lần hỏi SQS mỗi ngày khi rảnh | khoảng 86.400 | khoảng 86.400 | khoảng 1.440 |
| Số lần hỏi cơ sở dữ liệu mỗi ngày khi rảnh | 0 | khoảng 86.400 | khoảng 86.400 |

### 5.7 Thêm và bớt máy chủ (Karpenter)

Bài thử tạo 5 pod, mỗi pod xin nhiều CPU hơn chỗ còn trống, để buộc Karpenter thêm máy. Sau đó xóa các pod để xem máy thừa có được bỏ bớt không. Nguồn: `nodes-and-pods/karpenter.txt`, đo ngày 2/11, lấy mẫu khoảng 7 giây một lần.

| Giờ | Sự kiện | Số máy nhóm `default` | Số pod sẵn sàng |
|---|---|---|---|
| 13:24:08 | trước bài thử | 2 | |
| 13:24:15 | tạo 5 pod | 2 | 1 |
| 13:24:39 | máy mới xuất hiện (24 giây sau khi tạo pod) | 4 | 1 |
| 13:24:52 | cả 5 pod sẵn sàng (37 giây sau khi tạo pod) | 4 | 5 |
| 13:25:18 | xóa các pod | 4 | 0 |
| 13:27:07 | bắt đầu bỏ bớt máy | 3 | 0 |
| 13:27:23 | về lại 2 máy (126 giây sau khi xóa pod) | 2 | 0 |

### 5.8 Thêm và bớt pod theo CPU

HPA. Bài thử cài một bản sao của API (từ 1 đến 3 pod, mục tiêu CPU 50%) rồi tạo tải trong 150 giây. Nguồn: `nodes-and-pods/hpa.txt`, đo ngày 2/11. Cột "Số bản HPA muốn" là số pod mà HPA quyết định cần, "Số pod thật" là số pod đang chạy.

| Giờ | Sự kiện | Số bản HPA muốn | Số pod thật |
|---|---|---|---|
| 13:28:32 | bắt đầu tải (150 giây) | 0 (chưa có số đo CPU) | 1 |
| 13:28:46 | HPA có số đo CPU | 1 | 1 |
| 13:29:02 | pod thứ hai chạy (30 giây sau khi bắt đầu tải) | 1 | 2 |
| 13:29:17 | pod thứ ba chạy (45 giây sau khi bắt đầu tải) | 2 | 3 |
| 13:29:31 | HPA đạt mức tối đa | 3 | 3 |
| 13:31:02 | tải kết thúc | 3 | 3 |
| 13:32:18 | giảm về 1 pod (76 giây sau khi tải kết thúc) | 3 | 1 |
| 13:32:35 | HPA cũng xuống 1 | 1 | 1 |

Sự kiện HPA ghi trong file (tuổi tính từ lúc chụp):

| Tuổi sự kiện | Loại | Lý do | Nội dung |
|---|---|---|---|
| 7 phút 24 giây | Warning | `FailedGetResourceMetric` | chưa có số đo CPU của pod mới (tự hết) |
| 7 phút 9 giây | Normal | `SuccessfulRescale` | kích thước mới 2: CPU vượt mục tiêu |
| 6 phút 54 giây | Normal | `SuccessfulRescale` | kích thước mới 3: CPU vượt mục tiêu |
| 3 phút 54 giây | Normal | `SuccessfulRescale` | kích thước mới 1: mọi chỉ số dưới mục tiêu |

Trigger CPU của API. 10 người dùng ảo gọi `/burn?ms=1000` (mỗi yêu cầu đốt CPU 1 giây) trong 2 phút (script `api/keda-http-cpu-burn.js`). Số yêu cầu đồng thời chỉ khoảng 10, xa mức 100 của trigger theo số yêu cầu, nên chỉ trigger CPU mới thêm được pod. Nguồn: `api/keda-http-cpu.txt`, đo ngày 2/11.

| Giờ | Sự kiện | Số pod API |
|---|---|---|
| 15:41:13 | bắt đầu tải | 1 |
| 15:41:34 | pod thứ hai chạy (21 giây sau khi bắt đầu tải) | 2 |
| 15:43:15 | tải kết thúc | 2 |
| 15:47:57 | vẫn còn 2 pod | 2 |
| 15:48:04 | API về 0 pod (4 phút 49 giây sau khi tải kết thúc) | 0 |

Sự kiện HPA do KEDA tạo ra: `SuccessfulRescale`, kích thước mới 2, vì CPU vượt mục tiêu (tuổi sự kiện 6 phút 43 giây tính từ lúc chụp).

### 5.9 200 người dùng cùng lúc

Công cụ k6 giả lập 200 người dùng cùng lúc vào trang web và API qua ALB trong 3 phút. Đây là lần đo sau khi gộp các truy vấn đếm và thêm bộ nhớ đệm cho thống kê. Nguồn: `load-200-users/load-200-vus-after.txt` và `load-200-pods-after.csv`, đo ngày 2/11.

| Chỉ số | Giá trị | Giải thích |
|---|---|---|
| Số người dùng ảo cùng lúc | 200 | |
| Thời gian chạy | 3 phút 6,8 giây | 3 phút tải, cộng thời gian kết thúc |
| Tổng số request | 70.111 | |
| Request mỗi giây | 375,4 | |
| Request lỗi | 0 (0,00%) | |
| Kiểm tra đạt | 70.111 trên 70.111 (100%) | gồm `seed` 201, `frontend` 200, `api /` 200, `api /db` 200, `api /slow` 200, `positions` 200, `applicants` 200, `stats` 200, `create applicant` 201 |
| Thời gian trả lời chung: trung vị / p90 / p95 / lớn nhất | 84,02 ms / 2,06 s / 2,07 s / 3,01 s | p90 và p95 cao vì `/slow` được thiết kế để chờ 2 giây |
| Nhóm request nhẹ: trung vị / p95 / lớn nhất | 74,85 ms / 175,12 ms / 1,74 s | không tính `/slow` |
| API: trung vị / p95 | 67,85 ms / 83 ms | |
| Frontend: trung vị / p95 | 84,9 ms / 161,53 ms | |
| Số vòng lặp hoàn thành | 9.870 | |

Số pod trong lúc chạy (nguyên văn file `load-200-pods-after.csv`, đổi tên cột theo ý nghĩa). API tăng từ 0 lên 2 pod, frontend từ 1 lên 2 pod, không bên nào vượt 2.

| Giờ | Số pod API | Số pod frontend |
|---|---|---|
| 14:14:38 | 0 | 1 |
| 14:14:47 | 1 | 1 |
| 14:14:54 | 1 | 1 |
| 14:15:01 | 2 | 1 |
| 14:15:11 | 2 | 2 |
| 14:15:18 | 2 | 2 |
| 14:15:25 | 2 | 2 |
| 14:15:32 | 2 | 2 |
| 14:15:40 | 2 | 2 |
| 14:15:47 | 2 | 2 |
| 14:15:54 | 2 | 2 |
| 14:16:01 | 2 | 2 |
| 14:16:09 | 2 | 2 |
| 14:16:17 | 2 | 2 |
| 14:16:24 | 2 | 2 |
| 14:16:32 | 2 | 2 |
| 14:16:39 | 2 | 2 |
| 14:16:46 | 2 | 2 |
| 14:16:53 | 2 | 2 |
| 14:17:01 | 2 | 2 |
| 14:17:08 | 2 | 2 |
| 14:17:15 | 2 | 2 |
| 14:17:22 | 2 | 2 |
| 14:17:29 | 2 | 2 |
| 14:17:37 | 2 | 2 |
| 14:17:44 | 2 | 2 |
| 14:17:51 | 2 | 2 |
| 14:17:58 | 2 | 2 |

### 5.10 Cơ sở dữ liệu Aurora

Aurora chạy ở mức 0,5 ACU lúc rảnh và tự tăng khi có tải, tối đa 2 ACU. Hai bảng dưới là số đo CloudWatch (mỗi dòng một phút) quanh hai lần chạy 200 người dùng. Nguồn: `aurora/aurora-cloudwatch-before.txt` và `aurora-cloudwatch-after.txt`, đo ngày 2/11.

Lần 1, trước khi gộp truy vấn đếm và thêm bộ nhớ đệm (13:40 là lúc bắt đầu tải):

| Phút | ACU trung bình | ACU cao nhất | CPU trung bình (%) | CPU cao nhất (%) | Số kết nối cao nhất |
|---|---|---|---|---|---|
| 13:38 | 0,50 | 0,5 | 21,0 | 30,9 | 0 |
| 13:39 | 0,50 | 0,5 | 21,5 | 30,6 | 0 |
| 13:40 | 0,71 | 2,0 | 34,1 | 95,0 | 20 |
| 13:41 | 1,18 | 1,5 | 47,2 | 63,6 | 20 |
| 13:42 | 1,31 | 1,5 | 51,0 | 69,7 | 20 |
| 13:43 | 1,17 | 1,5 | 38,6 | 64,4 | 20 |
| 13:44 | 1,00 | 1,0 | 22,0 | 26,1 |  |

Lần 2, sau khi gộp truy vấn đếm và thêm bộ nhớ đệm (14:14 là lúc bắt đầu tải):

| Phút | ACU trung bình | ACU cao nhất | CPU trung bình (%) | CPU cao nhất (%) | Số kết nối cao nhất |
|---|---|---|---|---|---|
| 14:12 | 0,50 | 0,5 | 20,7 | 29,8 | 0 |
| 14:13 | 0,50 | 0,5 | 21,3 | 30,5 | 0 |
| 14:14 | 0,59 | 2,0 | 29,0 | 89,7 | 10 |
| 14:15 | 1,09 | 2,0 | 44,9 | 93,2 | 20 |
| 14:16 | 1,20 | 1,5 | 48,8 | 63,1 | 20 |
| 14:17 | 1,19 | 2,0 | 43,4 | 63,9 | 20 |
| 14:18 | 1,02 | 1,5 | 23,5 | 26,8 |  |

So sánh hai lần:

| Chỉ số | Lần 1 (trước) | Lần 2 (sau) |
|---|---|---|
| ACU cao nhất | 2,0 | 2,0 |
| CPU cao nhất (%) | 95,0 | 93,2 |
| Số kết nối cao nhất | 20 | 20 |

Tải của từng endpoint. Mỗi endpoint được gọi trong 1 phút ở khoảng 200 request mỗi giây, cách nhau một phút nghỉ. Nguồn: `aurora/aurora-per-endpoint.csv` (script `aurora-per-endpoint-one.js`).

| Endpoint | Phút đo | CPU trung bình (%) | ACU trung bình | Số kết nối cao nhất | Số request | Request mỗi giây | Thời gian trả lời p95 | Tỷ lệ lỗi |
|---|---|---|---|---|---|---|---|---|
| healthz: không đọc dữ liệu (đối chứng) | 14:24 | 20,73 | 0,50 | 0 | 10.825 | 196,6 | 77,29ms | 0,00% |
| db: tăng bộ đếm một dòng (`/db`) | 14:26 | 58,43 | 1,50 | 10 | 11.001 | 199,7 | 91,4ms | 0,00% |
| positions: danh sách vị trí | 14:28 | 29,57 | 2,00 | 10 | 11.001 | 199,6 | 120,68ms | 0,00% |
| applicants: danh sách ứng viên | 14:30 | 55,15 | 2,00 | 10 | 11.001 | 199,7 | 121,24ms | 0,00% |
| stats: thống kê tổng quan (có bộ nhớ đệm 5 giây) | 14:32 | 22,52 | 1,00 | 10 | 11.001 | 199,8 | 78,39ms | 0,00% |
| create: tạo một ứng viên (ghi) | 14:34 | 77,26 | 1,92 | 10 | 11.000 | 199,8 | 84,21ms | 0,00% |

Thời gian chạy các truy vấn chính trên cơ sở dữ liệu thật (1.122 ứng viên, 15 vị trí, 340 hồ sơ). Nguồn: `aurora/aurora-explain-analyze.txt`.

| Truy vấn | Bước ngoài cùng của kế hoạch chạy | Thời gian thực thi (mili giây) |
|---|---|---|
| truy vấn 1 | Aggregate | 0,186 |
| truy vấn 2 | Limit | 0,211 |
| truy vấn 3 | Aggregate | 0,020 |
| truy vấn 4 | Result | 0,955 |
| truy vấn 5 | HashAggregate | 0,672 |

### 5.11 Mất một máy spot

Bài thử tắt đột ngột một máy đang chạy pod frontend, như thể Amazon lấy lại máy, trong khi liên tục gửi yêu cầu vào frontend và API. Nguồn: `spot-loss/spot-loss-summary.txt`, `spot-loss-probe.csv` và `spot-loss-timeline.csv`, đo ngày 2/11.

| Chỉ số | Giá trị |
|---|---|
| Máy bị tắt | `ip-10-0-43-172.ap-southeast-1.compute.internal` (instance `i-093cbbc31ff7a72e3`) |
| Giờ tắt | 14:00:59 |
| Request tới frontend | 151, trong đó 5 request không phải HTTP 200 (2 lần 502, 3 lần 503) |
| Request tới API | 143, không có request lỗi |
| Các request lỗi của frontend | từ 14:01:01 đến 14:01:07 (khoảng 6 giây) |
| Karpenter nhận thông báo gián đoạn | 14:01:01 |
| Karpenter xóa NodeClaim | 14:02:18 |
| Số máy sẵn sàng | 5 cho đến 14:01:53, 4 từ 14:02:03 |
| Kết thúc đo | 14:02:46 |

Các request lỗi:

| Giờ | Đối tượng | Mã HTTP | Thời gian trả lời (giây) |
|---|---|---|---|
| 14:01:01 | frontend | 502 | 0,13 |
| 14:01:02 | frontend | 502 | 3,41 |
| 14:01:06 | frontend | 503 | 0,13 |
| 14:01:07 | frontend | 503 | 0,12 |
| 14:01:07 | frontend | 503 | 0,15 |

Trạng thái cụm theo thời gian (nguyên văn file `spot-loss-timeline.csv`, đổi tên cột theo ý nghĩa):

| Giờ | Số máy sẵn sàng | Pod frontend đang chạy | Pod frontend đang chờ | Pod API đang chạy | Pod giữ chỗ đang chạy |
|---|---|---|---|---|---|
| 14:01:07 | 5 | 1 | 0 | 1 | 3 |
| 14:01:19 | 5 | 1 | 0 | 1 | 3 |
| 14:01:30 | 5 | 1 | 0 | 1 | 3 |
| 14:01:40 | 5 | 1 | 0 | 1 | 3 |
| 14:01:53 | 5 | 1 | 0 | 1 | 3 |
| 14:02:03 | 4 | 1 | 0 | 1 | 3 |
| 14:02:13 | 4 | 1 | 0 | 1 | 3 |
| 14:02:23 | 4 | 1 | 0 | 1 | 3 |
| 14:02:34 | 4 | 1 | 0 | 1 | 3 |
| 14:02:44 | 4 | 1 | 0 | 1 | 3 |

### 5.12 Chạy image bất kỳ và kiến trúc arm64

Chart Helm dùng chung cho mọi ứng dụng. Bài thử cài ba image của bên thứ ba bằng cùng một chart và gọi thử từ trong cụm. Một phép thử đối chứng ép image chỉ có amd64 chạy trên máy arm64, và phải thất bại. Nguồn: `compatibility/plug-image.txt`, đo ngày 2/11.

| Image | Cổng | Kiến trúc script phát hiện | Chạy trên máy | Kết quả HTTP trong cụm |
|---|---|---|---|---|
| `nginx:1.27` | 80 | `auto` (đa kiến trúc) | amd64 | 200 |
| `hashicorp/http-echo:latest` | 5678 | `auto` (đa kiến trúc) | amd64 | 200 |
| `gcr.io/google-samples/hello-app:1.0` | 8080 | `amd64` (chỉ có amd64) | amd64 | 200 |
| đối chứng: `hello-app` ép lên arm64 | 8080 | | arm64 | pod không sẵn sàng (`CrashLoopBackOffError`), đúng như mong đợi |

Kết quả cuối của script: `RESULT: all checks passed`. Hai image đa kiến trúc rơi vào máy amd64 do Karpenter chọn, nên bài này chưa chứng minh chúng chạy được trên arm64.

Kiến trúc arm64 của chính ứng dụng. Nguồn: `compatibility/arm64.txt`, đo ngày 2/11, image tag 0.5.1.

| Ứng dụng | Image có arm64 | Chạy được trên máy arm64 | Kết quả |
|---|---|---|---|
| API (`demo-api`) | có (`linux/amd64` và `linux/arm64`) | có, máy `ip-10-0-7-94` | HTTP 200 sau 2,44 giây (gồm cả thời gian bật API từ 0 pod) |
| Frontend (`demo-web`) | có | có, cùng máy | HTTP 200 sau 0,14 giây |
| Worker (`demo-worker`) | có | chưa đo lại | file ghi "chưa đo lại với thiết kế worker chạy sẵn" |

## 6. Rủi ro và lý do giữ thiết kế hiện tại

### 6.1 Cách đánh giá

Mỗi rủi ro có nguyên nhân, hậu quả, mức độ và mức bằng chứng. Mức độ là đánh giá của nhóm thực hiện:
- Cao: làm hệ thống không đạt yêu cầu của đề tài, hoặc ngừng một chức năng mà không có cảnh báo.
- Trung bình: gây chậm hoặc tốn tài nguyên trong một số điều kiện, hoặc có thể gây sự cố khi tải lớn.
- Thấp: ảnh hưởng nhỏ, có giới hạn rõ và tự hết.

Bằng chứng có ba mức. "Đã đo" là có số liệu ở chương 5. "Đã thấy" là quan sát được lúc làm nhưng chưa thành số đo. "Suy luận" là rút ra từ thiết kế và mã, chưa thử.

### 6.2 Rủi ro của hệ thống (KEDA với SQS)

| Rủi ro | Vì sao xảy ra | Hậu quả | Mức | Bằng chứng | Cách giảm |
|---|---|---|---|---|---|
| Điểm hiện chậm, không đạt dưới 3 giây | KEDA dựa vào độ dài hàng đợi SQS, mà con số này cập nhật chậm | Trung vị 10,18 giây khi hỏi mỗi 1 giây và 12,19 giây khi hỏi mỗi 30 giây | Cao so với yêu cầu | Đã đo | Đề xuất A (mục 5.5) hoặc B (mã tham chiếu), hoặc giữ một worker luôn chạy sẵn (không về 0). Chưa duyệt |
| Job dư | Con số độ dài hàng đợi vẫn báo còn việc sau khi Job đã xong. Job đã xong lại không được trừ khỏi số Job đang chạy | 40 Job dư trong 20 lượt khi hỏi mỗi 1 giây, 6 khi hỏi mỗi 30 giây. Mỗi Job dư chiếm một pod, một địa chỉ IP và một chỗ trong trần 20 Job trong vài giây. Không làm sai kết quả, không làm Score chậm ở tải thường | Thấp ở tải thường; có thể trung bình khi dồn nhiều Job | Đã đo ở tải thường. Tác động khi dồn tải: suy luận | Hỏi thưa hơn (30 giây giảm còn 6 Job dư nhưng điểm hiện chậm hơn); trần 20 Job; pod xong bị dọn sau 120 giây |
| Hỏi hàng đợi liên tục | Bản demo hỏi SQS mỗi giây | Khoảng 86.400 lần hỏi mỗi ngày dù không ai dùng | Thấp | Tính từ cấu hình | Hỏi thưa hơn khi không phải bản demo |
| Độ chậm của số đếm SQS không cố định | SQS cập nhật con số theo kiểu nhất quán cuối cùng | Khó đoán trước thời gian chờ và số Job dư | Trung bình | Đã đo: thời gian đến khi KEDA tạo Job nằm từ 6,0 đến 16,8 giây giữa các lượt (mục 5.2) | Không có cách chắc chắn nếu chỉ dùng SQS |

### 6.3 Rủi ro của hai đề xuất, nếu sau này được duyệt

Đề xuất A (thêm trigger PostgreSQL vào cùng ScaledJob):

| Rủi ro | Vì sao xảy ra | Hậu quả | Mức | Bằng chứng | Cách giảm |
|---|---|---|---|---|---|
| Job dư nhiều hơn mong đợi | Trigger SQS hỏi mỗi giây nên thường trúng lúc số đếm còn cũ | 23 Job dư trong 20 lượt khi hỏi mỗi 1 giây | Thấp ở tải thường | Đã đo | Hỏi thưa hơn (30 giây còn 3 Job dư, nhưng điểm hiện chậm, trung vị 6,74 giây) |
| Tải truy vấn nền lên cơ sở dữ liệu | KEDA hỏi Aurora mỗi giây | Khoảng 86.400 truy vấn mỗi ngày dù không ai dùng | Thấp | Suy luận từ cấu hình, chưa đo tải thật lên Aurora | Hỏi thưa hơn khi không phải bản demo |
| Cold start phụ thuộc chu kỳ hỏi | Việc chờ đến lần KEDA kiểm tra kế tiếp | Nếu đặt chu kỳ thưa thì không đạt dưới 3 giây (30 giây: trung vị 6,74 giây) | Cao so với yêu cầu, chỉ khi đổi chu kỳ | Đã đo | Giữ chu kỳ 1 giây |
| Mật khẩu cơ sở dữ liệu đổi 7 ngày một lần | KEDA đọc mật khẩu bằng thông tin kết nối riêng | Nếu KEDA mất quyền đọc, trigger PostgreSQL ngừng và điểm hiện chậm như hệ thống hiện tại (khoảng 10 giây), không có cảnh báo | Trung bình | Suy luận, chưa thử đổi mật khẩu thật | Thêm cảnh báo khi trigger lỗi; tài khoản cơ sở dữ liệu chỉ đọc riêng cho KEDA |

Đề xuất B (tách hai ScaledJob):

| Rủi ro | Nguyên nhân | Hậu quả | Mức | Bằng chứng | Giảm thiểu |
|---|---|---|---|---|---|
| Trần Job nhân đôi | Hai ScaledJob không trừ Job của nhau, và mỗi cái có `maxReplicaCount` 20 | Khi hàng đợi tồn đọng, tổng đến 40 Job: vượt yêu cầu 20 và vượt sức chứa pool `worker` (12 vCPU, tối đa 3 node, 58 pod mỗi node); kho IP cạn nhanh hơn | Trung bình | Đã thấy (cả hai MAX bằng 20 lúc kiểm tra cụm) | Đặt trần riêng cho `worker-sqs` (`safetyMax` trong mã tham chiếu) |
| Đường nhanh phụ thuộc database | Cold start 1,78 giây (mục 5.5) chỉ có khi trigger PostgreSQL chạy | Nếu trigger lỗi (mật khẩu xoay vòng, database chậm), cold start thành hơn một phút (chu kỳ 60 giây) mà không có cảnh báo | Cao | Suy luận | Cảnh báo khi trigger lỗi; thử xoay mật khẩu thật; tài khoản database riêng cho KEDA |
| Lưới dự phòng chậm | `worker-sqs` hỏi mỗi 60 giây | Message không có hồ sơ `queued`, hoặc message quay lại hàng đợi sau khi Job lỗi, chờ tối đa 60 giây | Trung bình | Suy luận | Rút ngắn chu kỳ của lưới (đổi lại Job dư tăng) |
| Job dư vẫn có thể xảy ra ở `worker-sqs` | Số đếm SQS cũ với độ trễ không cố định | Job rỗng xuất hiện muộn, tách khỏi lần Score gây ra nó, khó truy nguyên | Thấp | Không thấy Job dư nào trong 30 lượt (20 và 10, mục 5.5); không chứng minh được bằng 0 | Đếm Job bằng log KEDA |
| Phần chậm phía máy khách chưa giải thích | 4 trong 5 lượt có thời gian chờ điểm từ 3 giây trở lên còn phần dư 1,67 đến 5,75 giây (mục 5.5) | Chưa biết có liên quan đến thiết kế hay không | Thấp | Đã đo, chưa giải thích | Xem log ALB và API đúng thời điểm các lượt đó |
| Vận hành phức tạp hơn | Hai ScaledJob, hai nhóm Job trong log và số liệu, template có vòng lặp | Dễ coi `worker-sqs` là thừa rồi xóa nó; dễ để hai cấu hình lệch nhau | Trung bình | Đã thấy | Ghi chú rõ trong chart và tài liệu |
| Mã tham chiếu chưa chạy trên cụm | Mã được viết lại từ cài đặt tạm của lần đo; chỉ kiểm bằng `helm lint` và `helm template` | Hành vi thật của mã tham chiếu chưa được xác nhận | Trung bình | Đã thấy | Đo lại trên cụm trước khi dùng |

### 6.4 Vì sao giữ thiết kế hiện tại và chưa áp dụng hai đề xuất

Quyết định: hệ thống giữ KEDA với SQS. Hai đề xuất A và B chưa được duyệt nên không áp dụng; đề xuất B chỉ còn là mã tham chiếu trong `docs/reference/worker-split/`. Lý do:

1. Yêu cầu của đề tài là "KEDA với SQS". Hai đề xuất đi xa hơn yêu cầu đó và chưa qua duyệt.
2. Thiết kế hiện tại đơn giản hơn. Một ScaledJob, một trigger, một nơi chỉnh chu kỳ hỏi, một trần 20 Job, và không phụ thuộc cơ sở dữ liệu để bật Job.
3. Hai đề xuất còn nhiều điều chưa kiểm chứng: chưa thử khi cơ sở dữ liệu lỗi, chưa thử đổi mật khẩu thật, chưa đo khi dồn nhiều Job, và mã của đề xuất B chưa từng chạy trên cụm. Đề xuất B còn có hai ScaledJob không biết nhau, nên tổng số Job có thể lên 40, vượt trần 20.
4. Tác hại của Job dư nhỏ và có giới hạn. Một Job rỗng chờ phiếu tối đa `JOB_WAIT_SECONDS` (2 giây) rồi tắt. Nó không mở kết nối cơ sở dữ liệu, vì worker chỉ kết nối ở lần dùng đầu tiên (hàm `Pool` trong `src/internal/db/db.go`) mà Job rỗng không có phiếu nào để chấm, nên không làm tăng tải Aurora. Nó không lấy phiếu nào, nên không gây mất hay xử lý lặp. Số Job cùng lúc bị chặn ở 20 (`maxReplicaCount`), pod xong được dọn sau 120 giây (`ttlSecondsAfterFinished`), và chart chỉ giữ 3 Job thành công và 5 Job lỗi gần nhất.

Điều chưa đạt: điểm hiện sau khoảng 10 giây nên chưa đạt yêu cầu dưới 3 giây ở phần chấm điểm. Hai đề xuất cho thấy hướng có thể đạt (khoảng 1,7 giây ở các lượt đo). Nếu được duyệt, mỗi đề xuất có rủi ro riêng ở mục 6.3.

Về chu kỳ 1 giây. Bản demo dùng 1 giây vì đây là bản demo. Chu kỳ 1 giây làm KEDA hỏi hàng đợi mỗi giây và tạo nhiều Job dư (40 trong 20 lượt). Chu kỳ 30 giây giảm Job dư còn 6 nhưng thời gian chờ không tốt hơn (trung vị 12,19 giây). Hệ thống thật nên chọn chu kỳ theo mức Job dư chấp nhận được.

Khi nào nên xem lại quyết định:
- Khi phần chấm điểm bắt buộc phải dưới 3 giây. Lúc đó cần duyệt một trong hai đề xuất, chia lại số Job tối đa nếu chọn đề xuất B, và đo đường dự phòng khi cơ sở dữ liệu lỗi.
- Khi dồn nhiều việc cùng lúc (hàng trăm phiếu), nơi Job dư có thể tranh địa chỉ IP và chỗ trong trần 20 với Job có việc. Chưa đo; cách kiểm tra là gửi 500 phiếu, đếm Job dư và sự kiện `FailedCreatePodSandBox`.
- Khi Job dư làm nhiễu giám sát hoặc tốn chi phí đáng kể.

### 6.5 Rủi ro chung

- Một hồ sơ bị kẹt ở trạng thái chờ chấm trong lúc phiếu bị ẩn (chờ thử lại, 60 giây mỗi lần) sẽ làm trigger PostgreSQL của hai đề xuất bật Job rỗng lặp lại cho tới hết cửa sổ 5 phút. Cách sửa là thêm trạng thái `processing`, chưa làm. Suy luận.
- Hồ sơ chờ chấm quá 5 phút không còn được trigger PostgreSQL đếm, và nằm ở trạng thái chờ mãi vì chưa có tiến trình dọn.
- Điểm hiện nhanh của worker dựa vào một máy `worker` luôn sẵn nhờ pod giữ chỗ. Mất máy đó thì Job kế tiếp phải chờ máy mới. Chưa đo.
- Mật khẩu cơ sở dữ liệu đổi định kỳ: ứng dụng đã có cơ chế đọc lại (có test đơn vị), nhưng chưa thử với một lần đổi mật khẩu thật.
- Cấu hình Aurora còn ở mức demo: một instance ghi, giữ bản sao lưu 1 ngày (`backup_retention_period = 1`), không snapshot cuối, không bảo vệ xóa, Performance Insights tắt, ứng dụng dùng tài khoản `postgres`, kết nối `sslmode=require` không kiểm tra chứng chỉ, và chưa có cảnh báo cho ACU hay số kết nối.
- Chưa đo với bảng hàng triệu dòng. Theo cách viết truy vấn hiện tại (`ILIKE '%...%'`, phân trang `OFFSET`, `count(*) OVER ()`), tìm kiếm và danh sách sẽ phải quét nhiều dòng và là chỗ chậm đầu tiên. Đây là suy luận từ mã.
- Mỗi pod chạy bước kiểm tra cấu trúc bảng lúc khởi động và giữ một khóa. Nhiều pod cùng lên sẽ xếp hàng chờ khóa, và thêm chỉ mục trên bảng lớn sẽ chạy ngay lúc pod khởi động.
- Frontend và API chỉ có một pod nền, nên mất máy là gián đoạn ngắn.
- Chưa thử mất cả hai máy hệ thống cùng lúc. Máy hệ thống chạy Karpenter, KEDA, cổng trung gian của API và CoreDNS.
- Route 53 chưa làm, nên chỉ có HTTP qua địa chỉ DNS của ALB.

### 6.6 Hạn chế của cách đo

Một máy đo, 20 lượt mỗi chuỗi, và việc chấm điểm là giả lập. Các chuỗi worker đo ở những ngày khác nhau (2/11, 3/11 và 4/11) và không xen kẽ, nên điều kiện (tải của Amazon, trạng thái máy) có thể khác nhau. Đề xuất B được đo bằng một cài đặt tạm, không phải bằng mã tham chiếu.

## 7. Kết luận và hướng phát triển

Hệ thống đạt phần lớn yêu cầu về thiết kế: toàn bộ máy là spot, có ba tầng co giãn, API về 0 pod, và số pod tối đa theo cấu hình. Về thời gian khởi động lạnh, kết quả chia làm hai phần.

Trang web (API): chuỗi 20 lượt cho trung vị 2,08 giây phía hệ thống, và cả 20 lượt dưới 3 giây. Tính đầu cuối còn 3 lượt vượt ngưỡng, nguyên nhân là việc mở kết nối ở máy đo.

Phần chấm điểm (worker): hệ thống dùng KEDA với SQS cho điểm hiện sau trung vị 10,18 giây khi hỏi mỗi 1 giây, và 0/20 lượt dưới 3 giây, nên chưa đạt yêu cầu. Nguyên nhân là con số độ dài hàng đợi SQS cập nhật chậm, không phải thời gian khởi động pod. Hệ thống còn bật Job dư: 40 trong 20 lượt khi hỏi mỗi 1 giây, 6 khi hỏi mỗi 30 giây. Hai đề xuất cải tiến chưa được duyệt, được đo để có thêm góc nhìn: thêm trigger PostgreSQL (đề xuất A, trung vị 1,66 giây, 23 Job dư) và tách hai ScaledJob (đề xuất B, trung vị 1,78 giây, 0 Job dư). Đề xuất B hiện chỉ có mã tham chiếu trong `docs/reference/worker-split/`, không được chạy.

Các phần còn lại hoạt động đúng thiết kế trong các lần đo: Karpenter thêm máy trong 24 giây và bỏ bớt sau 126 giây, HPA tăng đến 3 pod, 200 người dùng cùng lúc không lỗi với tối đa 2 pod mỗi bên, Aurora chạm trần 2 ACU, và trang web chỉ lỗi khoảng 6 giây khi mất một máy spot.

Hướng phát triển, theo thứ tự ưu tiên:
1. Xin duyệt một trong hai đề xuất nếu phần chấm điểm bắt buộc phải dưới 3 giây. Nếu chọn đề xuất B, chia lại số Job tối đa của hai ScaledJob và đo đường dự phòng khi cơ sở dữ liệu lỗi.
2. Thêm trạng thái `processing` để hồ sơ đang chờ thử lại không làm KEDA bật Job rỗng.
3. Tạo tài khoản cơ sở dữ liệu chỉ đọc riêng cho KEDA và thử đổi mật khẩu thật.
4. Tách việc tạo cấu trúc bảng ra một Job riêng, bỏ bước này khỏi lúc pod khởi động.
5. Thêm cảnh báo cho Aurora (ACU chạm trần, số kết nối), bật Performance Insights, và đặt thời gian sao lưu, bảo vệ xóa phù hợp với dữ liệu thật.
6. Đo lại cold start của API và chuỗi worker với nhiều lượt hơn, ghi lại nhịp hỏi của KEDA từ log.

## Phụ lục A. Cách tái lập

Quy trình chi tiết, kèm điều kiện tiên quyết, tiêu chí đạt cho từng bước và cách xử lý sự cố, nằm trong [DEMO.md](DEMO.md). Phần này chỉ tóm tắt các lệnh chính.

Cần `aws` (đã `aws login`), `terraform`, `kubectl`, `helm`, `docker`, `jq`. Mọi lệnh AWS dùng vùng `ap-southeast-1`.

```bash
scripts/bootstrap-state.sh                     # lần đầu: tạo bucket state
scripts/up.sh --plan                           # kiểm tra, không tạo gì
scripts/up.sh                                  # dựng hạ tầng, build image, deploy (25 đến 40 phút)

# cold start API, 20 lượt (cần API_SCALEDOWN thấp để đỡ chờ)
COLD_PATH='/positions?limit=10' COLD_JQ='has("items")' \
  scripts/test-cold-start.sh 20 direct docs/results/api/cold-start-api.csv

# cold start worker, 20 lượt (API đã nóng)
scripts/score-cold-start.sh 20 docs/results/worker/<tên-file>.csv

scripts/down.sh                                # hủy toàn bộ (xóa cả database)
```

Cấu hình trong kho mã đặt `pollingInterval: 1` tại `apps/worker.values.yaml` (bản demo). Đặt `30` để thử chu kỳ hỏi thưa hơn. Hai đề xuất A và B không có lệnh triển khai, vì chúng chưa được duyệt.

## Phụ lục B. Dữ liệu thô và cách đọc số liệu

Phụ lục này giải thích các số trong các file đo: file nào do script nào tạo, mỗi cột đo cái gì, đọc một dòng cụ thể ra sao, các giá trị đặc biệt (ô trống, giá trị âm), và các số trong chương 5 được tính từ file như thế nào.

### B.1 Danh sách file

| File | Tạo bởi | Số dòng | Dùng ở |
|---|---|---|---|
| `docs/results/api/cold-start-api.csv` | `scripts/test-cold-start.sh` | 20 lượt | mục 5.1 |
| `docs/results/nodes-and-pods/karpenter.txt`, `hpa.txt` | script đo Karpenter và HPA | một lần | mục 5.7, 5.8 |
| `docs/results/api/keda-http-cpu.txt`, `keda-http-cpu-burn.js` | bài thử trigger CPU của API và script k6 đã dùng | một lần | mục 5.8 |
| `docs/results/load-200-users/load-200-vus-after.txt`, `load-200-pods-after.csv` | k6 (`scripts/loadtest.js`) và lấy mẫu số pod | một lần; 28 mẫu | mục 5.9 |
| `docs/results/aurora/aurora-cloudwatch-before.txt`, `aurora-cloudwatch-after.txt` | số đo CloudWatch quanh hai lần chạy 200 người dùng | 7 phút mỗi file | mục 5.10 |
| `docs/results/aurora/aurora-per-endpoint.csv`, `aurora-per-endpoint-one.js` | k6 gọi từng endpoint với tốc độ cố định | 6 endpoint | mục 5.10 |
| `docs/results/aurora/aurora-explain-analyze.txt` | `EXPLAIN ANALYZE` trên cơ sở dữ liệu thật | 5 truy vấn | mục 5.10 |
| `docs/results/spot-loss/spot-loss-probe.csv`, `spot-loss-timeline.csv`, `spot-loss-summary.txt` | `scripts/spot-loss-test.sh` | 294 request; 10 mẫu; tóm tắt | mục 5.11 |
| `docs/results/compatibility/plug-image.txt`, `arm64.txt` | `scripts/test-plug-image.sh` và kiểm tra thủ công | một lần | mục 5.12 |
| `docs/results/worker/fake-backlog-orphan.txt` | bài thử hồ sơ mồ côi | một lần | mục 5.5 |
| `docs/results/worker/worker-cold-start-keda-poll30.csv` | `scripts/score-cold-start.sh`, dựng lại từ số đã in lúc đo | 20 lượt | mục 5.2, hệ thống (chỉ SQS) hỏi mỗi 30 giây |
| `docs/results/worker/worker-cold-start-keda-postgres.csv` | `scripts/score-cold-start.sh` | 20 lượt | mục 5.5, đề xuất A hỏi mỗi 1 giây |
| `docs/results/worker/worker-cold-start-keda-postgres-split.csv`, `worker-cold-start-split-10.csv` | `scripts/score-cold-start.sh` | 20 lượt; 10 lượt | mục 5.5, đề xuất B |
| `docs/results/worker/worker-cold-start-keda-postgres-poll30.csv` | `scripts/score-cold-start.sh` | 20 lượt | mục 5.5, đề xuất A hỏi mỗi 30 giây |

Chú thích ngắn của từng file cũng có trong `docs/results/README.md`.

### B.2 `cold-start-api.csv`

Mỗi dòng là một lần API về 0 pod rồi bật lại. Script chờ API về 0, gửi một request qua ALB, ghi thời gian, rồi đọc dấu thời gian của pod mới từ Kubernetes.

| Cột | Ý nghĩa | Đơn vị | Ghi chú |
|---|---|---|---|
| `run` | số thứ tự lượt | | 1 đến 20 |
| `mode` | đường gọi | | `direct`: ALB, interceptor KEDA, API (header `Host: api.demo.local`). Cả 20 dòng là `direct` |
| `http` | mã HTTP | | 200 ở cả 20 dòng |
| `total_s` | tổng thời gian của request | giây | đồng hồ máy đo, từ lúc gửi đến lúc nhận xong |
| `connect_s` | thời gian mở kết nối TCP tới ALB | giây | là độ trễ mạng của máy đo, không phải của hệ thống |
| `ttfb_s` | thời gian đến byte đầu tiên | giây | gồm cả chờ pod API bật. Gần bằng `total_s` vì phản hồi nhỏ |
| `pod_created_after_request_s` | pod được tạo sau request bao lâu | giây | dấu thời gian tạo pod của Kubernetes có độ phân giải 1 giây, nên có thể hơi âm (lượt 6: -0,1). Đọc giá trị âm nhẹ là xấp xỉ 0 |
| `created_to_scheduled_s` | từ lúc tạo pod đến lúc được xếp lên node | giây | 0 ở mọi dòng có dữ liệu: pod được xếp trong cùng giây |
| `created_to_ready_s` | từ lúc tạo pod đến lúc Ready | giây | 0 đến 2, độ phân giải 1 giây |

Giá trị đặc biệt. Lượt 12 không có ba cột cuối (script không đọc được thời gian của pod), nên các thống kê về pod dùng 19 dòng.

Đọc hai dòng mẫu:
- Lượt 9: `total_s` 1,426, `connect_s` 0,089, nên phía hệ thống là 1,426 trừ 0,089 bằng 1,337 giây. Pod tạo 0,29 giây sau request và Ready ngay trong giây đó. Đây là lượt nhanh nhất.
- Lượt 12: `total_s` 5,027, `connect_s` 3,025. Phía hệ thống là 5,027 trừ 3,025 bằng 2,003 giây, bình thường. Lượt này chậm ở đầu cuối chỉ vì kết nối TCP mất 3 giây.

Cách ra các số ở bảng 5.1:
- Cột "Phía hệ thống" là `total_s` trừ `connect_s` của từng dòng, rồi lấy thống kê.
- Cột "bỏ 6 lượt" loại các dòng có `connect_s` từ 0,5 giây trở lên (lượt 1, 5, 10, 12, 14, 18).
- "Dưới 3 giây" đếm dòng có giá trị nhỏ hơn 3 (so sánh nghiêm ngặt). Lượt 2 (2,985) và lượt 6 (2,961) dưới 3 giây rất sát, nên con số 17/20 nhạy với cách làm tròn.

### B.3 Các file đo khác

| File | Cách đọc |
|---|---|
| `nodes-and-pods/karpenter.txt` | mỗi dòng là giờ và trạng thái: số máy nhóm `default` (`nodes`) và số pod sẵn sàng (`ready_pods`), lấy mẫu khoảng 7 giây một lần. Các mốc "sau X giây" trong báo cáo là hiệu của hai giờ trong file |
| `nodes-and-pods/hpa.txt` | mỗi dòng có `replicas` (số pod HPA muốn) và `pods` (số pod thật). Cột phần trăm CPU bị cắt khi lấy mẫu nên chỉ còn `cpu:`. Cuối file là sự kiện của HPA, tuổi tính từ lúc chụp |
| `api/keda-http-cpu.txt` | đầu file là giờ bắt đầu và kết thúc tải, rồi sự kiện HPA, rồi bảng `time,api_pods,hpa`. Cột `hpa` bị cắt phần trăm CPU. Hai dòng cuối có `<unknown>` vì API đã về 0 pod |
| `load-200-users/load-200-vus-after.txt` | đầu ra đầy đủ của k6: từng giây trong lúc chạy, rồi bảng tổng kết (kiểm tra, thời gian trả lời theo nhóm, số request) |
| `load-200-users/load-200-pods-after.csv` | số pod API và frontend khoảng 7 giây một lần |
| `aurora/aurora-cloudwatch-*.txt` | ba khối `ServerlessDatabaseCapacity`, `CPUUtilization`, `DatabaseConnections`; mỗi dòng là một phút với hai số: trung bình và cao nhất. Phút cuối của khối kết nối có thể thiếu |
| `aurora/aurora-per-endpoint.csv` | mỗi dòng một endpoint: `cpu_avg_pct` và `acu_avg` là trung bình phút đó theo CloudWatch (ACU còn dư từ phút trước vì Aurora giảm chậm, nên cột CPU đáng tin hơn); `requests`, `req_per_s`, `p95`, `failed` do k6 báo |
| `aurora/aurora-explain-analyze.txt` | đầu ra của `EXPLAIN ANALYZE` cho 5 truy vấn, kèm số dòng của ba bảng ở đầu file. Không ghi tên truy vấn, nên báo cáo gọi theo thứ tự |
| `spot-loss/spot-loss-probe.csv` | mỗi dòng một request: giờ, đối tượng (`fe` hoặc `api`), mã HTTP, thời gian trả lời |
| `spot-loss/spot-loss-timeline.csv` | trạng thái cụm khoảng 10 giây một lần |
| `spot-loss/spot-loss-summary.txt` | máy bị tắt, giờ tắt, số request không phải HTTP 200 theo từng đối tượng, và các dòng log của Karpenter (giờ UTC, cộng 7 giờ ra giờ máy đo) |
| `compatibility/plug-image.txt`, `arm64.txt` | đầu ra của `scripts/test-plug-image.sh` và ghi chép kiểm tra arm64 |
| `worker/fake-backlog-orphan.txt` | nhật ký bài thử hồ sơ mồ côi: giờ và Job mới xuất hiện (giờ tạo Job trong file là UTC) |

Hai dòng lỗi AWS ở đầu `fake-backlog-orphan.txt` (`NonExistentQueue`) do script gọi sai vùng lúc đó và không ảnh hưởng đến kết quả đếm Job.

### B.4 Các chuỗi worker

Ba file worker có cùng cột. Mỗi dòng là một lần bấm Score khi worker đang tắt. Tên cột trong báo cáo và ý nghĩa nằm ở bảng đầu mục 5.2. Quan hệ giữa các cột:
- Thời gian chờ điểm của người dùng (`total_s`) xấp xỉ thời gian API trả lời (`api_reply_s`) cộng thời gian xử lý trong hệ thống (`server_s`) cộng một phần dư phía máy khách. Phần dư gồm bước hỏi lại kết quả (0,25 giây) và độ trễ mạng. Trung vị là 0,19 giây ở file chỉ SQS hỏi 30 giây, 0,24 giây ở file đề xuất A hỏi 1 giây và 0,23 giây ở file đề xuất A hỏi 30 giây.
- `keda_job_after_s` tính từ lúc bấm, còn `server_s` tính từ lúc API ghi hồ sơ. Hiệu `server_s` trừ `keda_job_after_s` vì vậy thấp hơn thời gian thật từ lúc tạo Job đến lúc có điểm, và âm khi API chậm. Ví dụ ở file đề xuất A hỏi 1 giây, lượt 5: `api_reply_s` 2,39, `server_s` 2,0, `keda_job_after_s` 3,1, hiệu -1,10.
- `jobs_created`: file chỉ SQS hỏi 30 giây có 6 lượt (3, 4, 9, 11, 12, 13) có 2 Job, các lượt còn lại có 1. File đề xuất A hỏi 1 giây có cả 20 lượt từ 2 Job trở lên (17 lượt có 2 Job, lượt 5, 6, 18 có 3 Job, tổng 43). File đề xuất A hỏi 30 giây có lượt 4, 16, 17 có 2 Job, các lượt còn lại có 1.

Đọc một vài dòng mẫu:
- File chỉ SQS hỏi 30 giây, lượt 1: `keda_job_after_s` 2,8, `server_s` 4,07, `total_s` 4,49. Đây là lượt nhanh nhất của chuỗi.
- File chỉ SQS hỏi 30 giây, lượt 7: `keda_job_after_s` 10,1, `server_s` 11,49. Đại diện cho nhóm 14 lượt có `keda_job_after_s` từ 10,1 đến 11,0 giây.
- File chỉ SQS hỏi 30 giây, lượt 2: `keda_job_after_s` 40,9, `server_s` 42,37. Lỡ một lần KEDA kiểm tra nên chờ thêm đúng một chu kỳ 30 giây. Bốn lượt (2, 5, 11, 17) khoảng 42 giây. Không có giá trị âm hay ô trống, và `api_reply_s` lớn nhất chỉ 0,48 giây.
- File đề xuất A hỏi 1 giây, lượt 11: `keda_job_after_s` 0,1, `server_s` 1,34, `total_s` 1,65, `jobs_created` 2. Việc được nhận ra gần như ngay nhưng vẫn có một Job dư.
- File đề xuất A hỏi 1 giây, lượt 5: `api_reply_s` 2,39, `keda_job_after_s` 3,1, `total_s` 4,64, `jobs_created` 3. API chậm kéo theo các cột tính từ lúc bấm, và lượt này có 2 Job dư.
- File đề xuất A hỏi 30 giây, lượt 2: `keda_job_after_s` 29,2, `server_s` 30,37. Lần bấm rơi ngay sau một lần KEDA kiểm tra nên chờ gần trọn 30 giây. Lượt 14: `keda_job_after_s` 1,1, `server_s` 1,68, lần bấm rơi ngay trước một lần kiểm tra. Lượt 20 có `total_s` 7,73 nhưng `server_s` 3,58 và `api_reply_s` 0,22, phần dư 3,93 giây chưa giải thích.

Giá trị đặc biệt: `keda_job_after_s` có thể âm. Ở file đề xuất A hỏi 1 giây, nó âm ở lượt 2, 18, 19 (-0,8, -0,2, -0,3), vì dấu thời gian tạo Job có độ phân giải 1 giây và được làm tròn so với giờ bấm. Đọc là xấp xỉ 0.

### B.5 Cách tính thống kê

- Các phân vị (p25, trung vị, p75, p95) tính bằng nội suy tuyến tính trên các giá trị đã sắp xếp: vị trí là (n trừ 1) nhân p. Script `test-cold-start.sh` và `score-cold-start.sh` in thêm tóm tắt bằng cách chọn hạng gần nhất, nên p95 do script in có thể lệch nhẹ.
- Với 20 mẫu, p95 chỉ dựa vào một hai giá trị cao nhất.
- "Dưới 3 giây" là so sánh nghiêm ngặt, tính trên cột đã nêu (`server_s` hoặc `total_s`).
- Mọi số được tính lại từ file, không sao chép từ đầu ra của script.

### B.6 Bảng tra: số trong chương 5 lấy từ đâu

| Bảng hoặc số | File | Cột và phép tính |
|---|---|---|
| Bảng 5.1, cột "Tổng thời gian" | `cold-start-api.csv` | `total_s` |
| Bảng 5.1, cột "Phía hệ thống" | `cold-start-api.csv` | `total_s` trừ `connect_s` |
| Bảng 5.1, cột "bỏ 6 lượt" | `cold-start-api.csv` | `total_s`, chỉ các dòng có `connect_s` dưới 0,5 |
| Bảng giai đoạn của pod, mục 5.1 | `cold-start-api.csv` | ba cột `pod_created_after_request_s`, `created_to_scheduled_s`, `created_to_ready_s` (19 dòng) |
| Bảng hệ thống hỏi 30 giây, mục 5.2 | `worker-cold-start-keda-poll30.csv` | từng cột; bảng từng lượt là nguyên văn các dòng với tên cột đổi theo ý nghĩa |
| Bảng hệ thống hỏi 1 giây, mục 5.2 | nhật ký cũ (không còn file thô) | bảng tổng hợp, không có p25 và p75 |
| Bảng W và C, mục 5.3 | hai file hệ thống (1 giây từ nhật ký cũ) | W là `keda_job_after_s`; C là `server_s` trừ `keda_job_after_s`; cột cuối là `total_s` trừ `server_s`; lấy trung vị |
| Bảng Job dư, mục 5.4 và 5.5 | các file worker | đếm `jobs_created`; Job dư là `jobs_created` trừ 1 của mỗi lượt |
| Các bảng mục 5.7 đến 5.12 | các file trong `docs/results/` tương ứng (B.1) | các dòng giờ, các cột và số liệu nguyên văn; các mốc "sau X giây" là hiệu hai giờ trong file |
| Bảng đề xuất B, mục 5.5 | `worker-cold-start-keda-postgres-split.csv` và `worker-cold-start-split-10.csv` | từng cột; bảng từng lượt là nguyên văn các dòng |
| Bảng đề xuất A, mục 5.5 | `worker-cold-start-keda-postgres.csv` và `...-poll30.csv` | từng cột; bảng từng lượt là nguyên văn các dòng |

### B.7 Điều không nên suy ra từ các file này

- Chuỗi hệ thống hỏi 30 giây không có khoảng nghỉ ngẫu nhiên và được dựng lại từ số đã in, nên trung vị của nó không đại diện. Bản hỏi 1 giây của hệ thống chỉ còn bảng tổng hợp, không có p25, p75 và số liệu từng lượt. Hai chuỗi đo ở hai ngày khác nhau (2/11 và 3/11) và không xen kẽ.
- Không coi hiệu `server_s` trừ `keda_job_after_s` là thời gian khởi động pod. Nó gồm tạo Job, xếp pod, khởi động container, nhận phiếu và ghi điểm, và có sai số như đã nêu ở B.4.
- Không rút ra tỷ lệ Job dư từ 20 lượt. Các con số như 6/20 hay 3/20 là số đếm, không phải ước lượng tỷ lệ chính xác.
