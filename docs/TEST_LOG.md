# Nhật ký kết quả: API scale to zero, cold start, 200 user và bằng chứng co giãn

Tạo lúc 2026-10-26 15:21 (giờ máy). Tài khoản `728384804953`, vùng `ap-southeast-1`, cluster `demo`.
File này gồm: (1) **API tự về 0 pod khi không dùng và tự bật lại khi có request** (scale to zero); (2) **thời gian bật lại (cold start)**, ghi từng lượt; (3) **200 user cùng lúc**; (4) **bằng chứng ba tầng co giãn** (HPA, Karpenter, KEDA với SQS); (5) tổng kết hệ thống.
Mọi con số lấy từ dữ liệu thật đã lưu trong `docs/results/` hoặc từ truy vấn chạy ngay lúc tạo file. Không có số nào được làm tròn cho đẹp.

## Kết luận ngắn, nói thẳng

- **API scale to zero: đạt.** Với cấu hình thật (chờ 300 giây), API lên 1 pod khi có request và **về 0 pod đúng 300 giây sau request cuối** (xem mục 1), và sự kiện của KEDA cho thấy chu kỳ trước đó cũng giống vậy (xem mục 1).
- **Dưới 3 giây: phần lớn đạt.** Lần 4: 19/20 lượt dưới 3 giây (lượt chậm nhất 3,07 giây, thời gian kết nối TCP của lượt đó chỉ 0,11 giây, nên đó là chậm thật của hệ thống, không phải do mạng).
- **200 user: đạt cả ba bài** (5 phút qua ALB, 3 phút có `/db`, 3 phút có các endpoint domain): 0 lỗi, p95 từ 120 đến 314 ms, p99 từ 306 đến 327 ms. **Lưu ý:** database đã chạm mức tối đa đã đặt (2,0 ACU) và CPU của nó lên tới 92,6% ở bài C, nên 200 user là gần giới hạn của cấu hình hiện tại (xem mục 4).

## Số liệu trong log nằm ở file nào

Tất cả đường dẫn tính từ thư mục gốc của dự án. File **dữ liệu thô** là thứ có thể mở ra đối chiếu với con số trong log.

| Mục trong log | Số liệu | File dữ liệu thô |
|---|---|---|
| 1. API scale to zero | số pod theo thời gian (0 → 1 → 0) | `docs/evidence/scale-to-zero/timeline.csv` |
| | mốc gửi request và nhận phản hồi | `docs/evidence/scale-to-zero/markers.csv`, `request.txt` |
| | cấu hình `scaledownPeriod=300` | `docs/evidence/scale-to-zero/config.txt` |
| | sự kiện KEDA và Kubernetes (bật, tắt, pod chạy) | `docs/evidence/scale-to-zero/events.json` |
| 2. Cold start (20 lượt) | tổng, kết nối TCP, thời gian pod sẵn sàng của từng lượt | `docs/results/cold-start-4-api32-positions.csv` |
| 3. Phần mạng của máy đo | thời gian kết nối TCP tới ALB và đích đối chứng | **không có file**: đo trực tiếp lúc viết log, chưa lưu |
| 4. 200 user, bài A | tổng hợp k6 (số request, lỗi, p95, p99) | `docs/results/load-200vus-5m-via-alb-summary.json` |
| | pod, node, CPU theo thời gian trong bài A | `docs/results/load-200vus-5m-cluster-monitor.csv` |
| 4. 200 user, bài B (`/db`) | tổng hợp k6 | `docs/results/load-200vus-3m-with-db-summary.json` |
| 4. 200 user, bài C (domain) | tổng hợp k6 | `docs/results/load-200vus-3m-domain-endpoints-summary.json` |
| 4. Aurora lúc có tải | ACU, CPU, kết nối (CloudWatch) | **không có file**: truy vấn CloudWatch lúc viết log, chưa lưu |
| 5.1 HPA frontend | số pod, CPU, đầu ra `get hpa` | `docs/evidence/scaling/timeline.csv`, `markers.csv`, `snap_hpa_*.txt`, `k6_hpa.txt` |
| 5.1b HPA tốn CPU | số pod, CPU, sự kiện `SuccessfulRescale` | `docs/evidence/hpa-demo/timeline.csv`, `markers.csv`, `events.json`, `snap_*.txt` |
| 5.2 Karpenter | số node, NodeClaim, nhật ký Karpenter | `docs/evidence/scaling/timeline.csv`, `markers.csv`, `karpenter.log`, `snap_karpenter_*.txt` |
| 5.3 KEDA với SQS | hàng đợi, worker, node worker theo thời gian | `docs/evidence/scaling/timeline.csv`, `markers.csv`, `sqs_send.txt` |
| | sự kiện KEDA/HPA của worker | `docs/evidence/scaling/events.json` |
| | bộ đếm SQS (gửi, nhận, xoá, DLQ) | `docs/evidence/scaling/sqs_cloudwatch.txt`, `snap_sqs_*.txt` |
| 6. Tổng kết | số node, giới hạn, over-provisioning | không phải số đo: lấy từ cấu hình (`charts/cluster-config/values.yaml`, `charts/app/values.yaml`, `apps/*.values.yaml`, `terraform/`) và từ cluster lúc viết |

**Hai chỗ không kiểm chứng lại được bằng file** (mục 3 và phần Aurora ở mục 4): số liệu lấy trực tiếp lúc viết log và chưa được lưu. Cluster cũ đã huỷ, nên không truy lại được. Nếu cần, chạy lại bài đo trên cluster hiện tại để có số mới kèm file.

**Ghi chú về `docs/evidence/scaling/timeline.csv`:** file này chứa cả ba bài (HPA, Karpenter, SQS) cùng một chuỗi thời gian. Cột `epoch` đối chiếu được với `markers.csv` để biết mỗi bài bắt đầu và kết thúc lúc nào. Giờ trong log là giờ máy (UTC+7); `events.json` và `karpenter.log` ghi giờ UTC.

## 1. API scale to zero: tự về 0 pod khi không dùng, tự bật lại khi có request

> **Mục đích:** Chứng minh API **thật sự về 0 pod** khi không có request (không tốn tài nguyên) và **tự bật lại** khi có request đầu tiên, với cấu hình thật (thời gian chờ về 0 là **300 giây**). Mục 2 sau đó đo xem việc bật lại nhanh đến đâu.
>
> **Cách test:** Trên cluster vừa dựng lại, chờ API ở 0 pod, gửi **đúng một** request qua ALB thật, rồi **không gửi gì nữa** và ghi số pod của API mỗi ~5 giây cho đến khi về 0. Đối chiếu với sự kiện KEDA và Kubernetes ghi lại (các sự kiện này do chính KEDA tự ghi, độc lập với script đo).
>
> **Kết quả cho thấy:** API đi **0 → 1 → 0** và khoảng cách giữa hai lần đổi trạng thái đúng bằng thời gian chờ đã cấu hình. Không có thao tác tay nào.
>
> **Kết quả KHÔNG chứng minh:** Đây là một lần đo chi tiết, cộng với một chu kỳ trước đó trong sự kiện KEDA. Không chứng minh việc về 0 vẫn đúng khi có lưu lượng thất thường, ví dụ request đến đúng lúc đang tắt.

Dữ liệu thô: `docs/evidence/scale-to-zero/`. Cấu hình lúc đo (`kubectl get httpscaledobject api`): `scaledownPeriod=300 min=0 max=2`.

Lệnh:

```bash
kubectl -n demo get deploy api -o jsonpath='{.status.replicas}'     # 0 hoặc rỗng: đang ở 0 pod
curl -s -H "Host: api.demo.local" "http://$ALB/positions?limit=1"   # request duy nhất: HTTP 200, 2,375 giây
kubectl -n demo get pods -l app.kubernetes.io/name=api -w           # xem pod xuất hiện rồi biến mất sau 300 giây
```

| Giờ (giờ máy) | Số pod API | Ghi chú |
|---|---|---|
| 16:41:39 | 0 | API đang ở 0 pod |
| 16:41:45 | 0 | gửi request (HTTP 200, tổng 2,375 giây) |
| 16:41:51 | 1 | pod đã chạy, không gửi thêm request nào |
| 16:42:27 đến 16:46:47 | 1 | giữ nguyên 1 pod trong lúc chờ |
| 16:46:53 | 0 | **về 0 pod** |

**Sự kiện do KEDA và Kubernetes ghi (giờ máy = giờ UTC + 7):**

| Giờ | Đối tượng | Sự kiện | Ý nghĩa |
|---|---|---|---|
| 16:41:46 | ScaledObject api | `KEDAScaleTargetActivated` | bật API từ 0 lên 1 vì có request |
| 16:41:46 | Pod | `Scheduled`, `Pulled` (ảnh đã có sẵn trên node), `Started` | pod chạy trong chưa đầy 1 giây kể từ lúc được xếp |
| 16:46:46 | ScaledObject api | `KEDAScaleTargetDeactivated` | đưa API từ 1 về 0 |
| 16:46:46 | Pod | `Killing` | pod bị tắt |

**Kết luận:** từ lúc bật (16:41:46) đến lúc về 0 (16:46:46) là **đúng 300 giây**, bằng giá trị `scaledownPeriod` đã đặt. Một chu kỳ trước đó trên cùng cluster (bật 16:29:43, về 0 16:34:46, khoảng 303 giây) cho kết quả tương tự. Lần request này mất **2,375 giây** để nhận phản hồi, nằm dưới 3 giây.

## 2. API cold start: thời gian bật lại từ 0 pod (kết quả cuối cùng, từng lượt)

> **Mục đích:** Kiểm tra hai yêu cầu cùng lúc: (a) khi không có ai dùng, API **về 0 pod** (không tốn tài nguyên); (b) khi có request đầu tiên sau đó, API **bật lại và trả lời đủ nhanh** (cold start). Hai yêu cầu này đối nghịch nhau: về 0 càng triệt để thì lần gọi đầu càng phải chờ pod khởi động.
>
> **Cách test:** Lặp 20 lượt với cấu hình cuối cùng đang chạy. Mỗi lượt: chờ API về 0 pod, gửi **đúng một** request qua ALB thật, đo thời gian. Không có pod nào chạy sẵn trước request đó, nên thời gian đo được là **toàn bộ chuỗi** khởi động.
>
> **Kết quả cho thấy:** Cột "Tổng" là thứ người dùng cảm nhận: càng nhỏ càng tốt, và ngưỡng 3 s là mục tiêu cần đối chiếu. Bảng tóm tắt cho biết một cấu hình **thường** nhanh đến đâu (trung vị) và **tệ nhất** thế nào (p95, lớn nhất). Từng lượt cho biết cú chậm xảy ra đơn lẻ hay thường xuyên.
>
> **Kết quả KHÔNG chứng minh:** Không đại diện cho mọi vị trí địa lý của người dùng (đo từ một máy), và không đo trường hợp chỗ trống trên node đã hết (lúc đó phải tạo node mới).

### Cách đo

Mỗi **lượt** làm đúng các bước sau (script `scripts/test-cold-start.sh`):
1. Chờ cho Deployment `api` có **0 replica** (KEDA đã đưa API về 0 pod), rồi chờ thêm 5 giây cho ổn định.
2. Gửi **một** request qua **ALB thật** (từ máy ngoài AWS, đi qua internet), đo `curl -w` tổng thời gian, thời gian kết nối TCP, thời gian tới byte đầu.
3. Chỉ chấp nhận nếu phản hồi là JSON thật của API (một mã 200 từ trang frontend **không** được tính, vì từng có sự cố ALB chuyển nhầm).
4. Đọc dấu thời gian do API server Kubernetes ghi của pod vừa được tạo để biết pod mất bao lâu từ lúc tạo đến lúc sẵn sàng (độ phân giải 1 giây).

Để đo nhiều lượt trong thời gian hợp lý, thời gian chờ về 0 được hạ tạm xuống 15 giây trong lúc đo (mặc định 300 giây), rồi khôi phục.
Đường đi của mỗi request: máy đo -> ALB -> interceptor của KEDA (giữ request) -> KEDA bật API từ 0 lên 1 -> pod khởi động -> interceptor chuyển tiếp -> phản hồi.

Lệnh đã chạy (chạy lại được):

```bash
API_SCALEDOWN=15 scripts/deploy-apps.sh install            # hạ thời gian chờ về 0 xuống 15 giây để đo nhanh
COLD_PATH='/positions?limit=10' COLD_JQ='has("items")' \
  scripts/test-cold-start.sh 20 direct /tmp/cold4.csv       # 20 lượt, API 32 endpoint, có kết nối database
scripts/deploy-apps.sh install                              # trả thời gian chờ về 300 giây
column -t -s, /tmp/cold4.csv                                # xem từng lượt
```

### Bảng tóm tắt (giây)

| Lần chạy | API | Endpoint | Số lượt | Nhỏ nhất | Trung vị | p90 | p95 | Lớn nhất | **Dưới 3 s** | Dưới 3 s (bỏ thời gian kết nối TCP) |
|---|---|---|---|---|---|---|---|---|---|---|
| 4 | API mới 32 endpoint | `GET /positions?limit=10` | 20 | 1,44 | 2,16 | 2,57 | 2,60 | 3,07 | **19/20** | 20/20 |

Chú thích: "bỏ thời gian kết nối TCP" trừ đi thời gian `connect` của từng lượt, để loại phần mạng của máy đo (xem mục 3).

### Kết quả cuối cùng: API 32 endpoint (image 0.3.0), cấu hình đang chạy, endpoint có kết nối database

- API: API mới 32 endpoint; endpoint: `GET /positions?limit=10`; cấu hình probe: chỉ readinessProbe (1 giây).
- Dữ liệu thô: `docs/results/cold-start-4-api32-positions.csv`.

> **Mục đích:** Đảm bảo **API mới 32 endpoint** không làm cold start xấu đi. Lần gọi đầu của API mới phải kết nối database và kiểm tra/tạo bảng, nên tôi đo bằng endpoint **có chạm database** (`/positions`), là trường hợp xấu hơn endpoint `GET /` trước đó.
>
> **Cách test:** Image `0.3.0`, cấu hình chỉ có readinessProbe, endpoint `GET /positions?limit=10`, 20 lượt. Phản hồi phải có trường `items` (JSON thật của API).
>
> **Kết quả cho thấy:** Trung vị **2,16 s**: đã tính cả kết nối database. 19/20 lượt dưới 3 s; **một lượt 3,07 s với kết nối TCP bình thường**, tức là hệ thống thỉnh thoảng thật sự vượt 3 s.
>
> **Kết quả KHÔNG chứng minh:** Không chứng minh được "luôn dưới 3 s": 20 mẫu chỉ cho ước lượng thô, và đã thấy ít nhất một lượt vượt.

Cột "Pod tạo sau request" là giờ tạo pod trừ giờ gửi request, sai số khoảng 1 giây vì dấu thời gian của Kubernetes chỉ chính xác đến giây (giá trị âm nhỏ đã được đưa về 0). Cột kiến trúc node để "-" khi endpoint được gọi không trả về kiến trúc (lần 4).

| Lượt | HTTP | Tổng (s) | Kết nối TCP (s) | Sau kết nối (s) | Kiến trúc node | Pod tạo sau request (s) | Pod tạo đến sẵn sàng (s) | Dưới 3 s |
|---|---|---|---|---|---|---|---|---|
| 1 | 200 | 2,23 | 0,19 | 2,04 | - | 0,73 | 1 | có |
| 2 | 200 | 2,15 | 0,10 | 2,05 | - | 0,59 | 1 | có |
| 3 | 200 | 2,15 | 0,19 | 1,96 | - | 0,52 | 1 | có |
| 4 | 200 | 2,16 | 0,11 | 2,05 | - | 0,40 | 1 | có |
| 5 | 200 | 1,76 | 0,20 | 1,56 | - | 0,00 | 1 | có |
| 6 | 200 | 2,33 | 0,11 | 2,23 | - | 0,49 | 1 | có |
| 7 | 200 | 1,95 | 0,11 | 1,85 | - | 0,05 | 1 | có |
| 8 | 200 | 2,55 | 0,10 | 2,45 | - | 0,53 | 1 | có |
| 9 | 200 | 2,60 | 0,10 | 2,50 | - | 0,59 | 1 | có |
| 10 | 200 | 2,22 | 0,11 | 2,11 | - | 0,03 | 1 | có |
| 11 | 200 | 1,87 | 0,10 | 1,76 | - | 0,65 | 0 | có |
| 12 | 200 | 1,92 | 0,10 | 1,83 | - | 0,64 | 1 | có |
| 13 | 200 | 1,84 | 0,11 | 1,74 | - | 0,45 | 1 | có |
| 14 | 200 | 3,07 | 0,11 | 2,96 | - | 0,58 | 1 | **không** |
| 15 | 200 | 2,57 | 0,10 | 2,47 | - | 0,03 | 2 | có |
| 16 | 200 | 1,44 | 0,10 | 1,34 | - | 0,00 | 1 | có |
| 17 | 200 | 2,16 | 0,20 | 1,96 | - | 0,51 | 1 | có |
| 18 | 200 | 2,21 | 0,10 | 2,11 | - | 0,50 | 1 | có |
| 19 | 200 | 2,26 | 0,16 | 2,10 | - | 0,44 | 1 | có |
| 20 | 200 | 1,96 | 0,08 | 1,89 | - | 0,14 | 1 | có |

Lượt từ 3 giây trở lên: lượt 14 (3,07 s, kết nối 0,11 s, kết nối bình thường nên là chậm thật của hệ thống).

### Thời gian đi đâu trong một lượt

Lần 4 (cấu hình đang chạy): pod từ lúc được tạo đến lúc sẵn sàng là **1 giây** ở 18/20 lượt (và 1 lượt 0 giây, 1 lượt 2 giây trở lên; dấu thời gian Kubernetes chỉ chính xác đến 1 giây). Trung vị tổng là 2,16 giây, nên **khoảng 1 giây còn lại nằm ngoài pod**: KEDA phát hiện request và bật API, interceptor chuyển tiếp, và mạng (round trip tới ALB khoảng 0,07 giây). Hai đoạn đầu không đo riêng được bằng dấu thời gian 1 giây, nên **chưa tách được** giữa chúng.

## 3. Phần mạng của máy đo (vì sao có lượt chậm không do hệ thống)

> **Mục đích:** **Tách phần chậm do mạng của máy đo** ra khỏi phần chậm do hệ thống. Nếu không tách, một lượt chậm vì mất gói tin sẽ bị tính nhầm là lỗi của cold start.
>
> **Cách test:** Đo riêng thời gian thiết lập kết nối TCP (`curl -w %{time_connect}`) tới ALB, tới từng địa chỉ của ALB, và tới một dịch vụ AWS **không liên quan đến hệ thống** (`checkip.amazonaws.com`) làm đối chứng. Việc này không dính gì đến API hay KEDA.
>
> **Kết quả cho thấy:** Khoảng 5 đến 10% kết nối tốn hơn 1 giây trên cả ba địa chỉ ALB lẫn đích đối chứng, nên đó là đặc tính **đường mạng của máy đo** (gói SYN bị mất rồi gửi lại), không phải lỗi hệ thống. Vì vậy các bảng ở mục 1 có cột "Kết nối TCP" và "Sau kết nối" để nhìn thời gian khi đã loại phần mạng này.
>
> **Kết quả KHÔNG chứng minh:** Không chứng minh người dùng thật ở nơi khác sẽ không gặp lỗi tương tự; họ có đường mạng riêng của họ.

Lệnh và kết quả đã ghi (tính thời gian kết nối TCP, không liên quan đến cold start):

```bash
for i in $(seq 1 100); do curl -s -o /dev/null -w '%{time_connect}\n' http://$ALB/; done | sort -n | tail -8
# và riêng từng địa chỉ của ALB bằng: curl --resolve $ALB:80:<ip> ...  cùng một đích đối chứng không thuộc hệ thống: checkip.amazonaws.com
```

| Đích | Số kết nối | Trung vị (s) | Lớn nhất (s) | Số kết nối trên 0,5 s |
|---|---|---|---|---|
| ALB (cả 3 địa chỉ) | 100 | 0,078 | 2,094 | 8/100 |
| ALB 3.1.98.202 | 40 | 0,076 | 1,069 | 2/40 |
| ALB 54.251.190.47 | 40 | 0,072 | 3,180 | 5/40 |
| ALB 54.254.201.5 | 40 | 0,076 | 1,090 | 4/40 |
| `checkip.amazonaws.com` (đối chứng, không phải hệ thống của ta) | 40 | 0,076 | 1,054 | 2/40 |

Khoảng 5 đến 10% kết nối từ máy đo tốn hơn 1 giây (mất gói SYN rồi gửi lại), kể cả khi gọi một dịch vụ AWS không liên quan, nên những lượt chậm có cột "Kết nối TCP" lớn là do đường mạng của máy đo, không phải do cold start.

## 4. 200 user cùng lúc

Dữ liệu thô: `docs/results/load-200vus-5m-via-alb-summary.json` (bài A), `load-200vus-3m-with-db-summary.json` (bài B), `load-200vus-3m-domain-endpoints-summary.json` (bài C), `load-200vus-5m-cluster-monitor.csv` (mẫu cluster trong bài A).

> **Mục đích:** Kiểm tra yêu cầu **chịu được 200 user cùng lúc** trên hạ tầng thật, với các giới hạn đã đặt (tối đa 2 pod cho API và frontend).
>
> **Cách test:** k6 chạy 200 người dùng ảo song song; mỗi người lặp: tải trang frontend, gọi API, nghỉ khoảng 1 giây. Chạy ba kịch bản tăng dần độ nặng (bài A, B, C). Đo độ trễ (trung vị, p90, p95, p99, lớn nhất) và tỉ lệ lỗi, đối chiếu với ngưỡng nghiệm thu. Song song, ghi số pod, số node và CPU của cluster và của Aurora để biết **thành phần nào chịu tải**.
>
> **Kết quả cho thấy:** Ba bài đều không có lỗi và độ trễ nằm trong ngưỡng, nên hệ thống phục vụ được 200 user đồng thời. Bảng pod/node cho thấy API và frontend không vượt 2 pod và không cần thêm node. Số liệu Aurora cho thấy database là chỗ **gần chạm giới hạn nhất**.
>
> **Kết quả KHÔNG chứng minh:** Không chứng minh được số user lớn hơn 200, tải kéo dài hàng giờ, hay người dùng ở nhiều nơi (đo từ một máy).

Công cụ: k6 (`scripts/loadtest.js`), **200 user ảo chạy đồng thời**, mỗi user lặp lại: tải trang frontend, gọi API, nghỉ khoảng 1 giây. Chạy từ một máy ngoài AWS qua ALB thật, nên mọi độ trễ đã gồm round trip mạng của máy đó (trung vị khoảng 70 ms).
Ngưỡng nghiệm thu (do tôi đề xuất, bạn chưa xác nhận): lỗi dưới 1%, p95 dưới 500 ms, p99 dưới 3 giây.

### Bảng tóm tắt

| Bài | Số user | Thời lượng | Tổng request | Lỗi | Trung vị (ms) | p90 (ms) | p95 (ms) | p99 (ms) | Lớn nhất (ms) | Kết quả |
|---|---|---|---|---|---|---|---|---|---|---|
| Bài A | 200 | 5 phút | 102.188 | 0 (0.00%) | 69,0 | 76,4 | 314,4 | 327,4 | 2160 | **đạt cả ba ngưỡng** |
| Bài B | 200 | 3 phút | 85.953 | 0 (0.00%) | 70,7 | 79,5 | 306,0 | 322,3 | 2050 | **đạt cả ba ngưỡng** |
| Bài C | 200 | 3 phút | 131.694 | 0 (0.00%) | 71,7 | 82,0 | 119,9 | 306,3 | 2270 | **đạt cả ba ngưỡng** |

Ở cả ba bài, p99 chỉ 306 đến 327 ms, nghĩa là ít nhất 99% request nhanh hơn 0,33 giây. Request chậm nhất là 2,05 đến 2,27 giây (tức là có một số ít request vượt 2 giây); tôi **chưa tách riêng** nguyên nhân của chúng, nhiều khả năng là đợt đầu khi API còn ở 0 pod hoặc một lần mất gói SYN của mạng máy đo.

### Bài A: 5 phút, trang frontend + API (`/api/`), API bắt đầu ở 0 pod

> **Mục đích:** Kiểm tra hệ thống phục vụ được 200 user **ngay từ trạng thái nguội**: API bắt đầu ở 0 pod nên 200 request đầu tiên phải chờ API khởi động.
>
> **Cách test:** 5 phút, mỗi vòng: tải trang frontend rồi gọi `/api/` (qua proxy của frontend, interceptor của KEDA, tới API).
>
> **Kết quả cho thấy:** Không lỗi, p95 314 ms, p99 327 ms. API lên 2 pod ngay lập tức (200 request chia cho mục tiêu 100 request mỗi pod) và frontend lên 2 pod nhờ HPA; số node không đổi, vì chỗ trống (headroom) đã có sẵn. Request chậm nhất (khoảng 2 s) là đợt đầu khi API còn ở 0 pod.
>
> **Kết quả KHÔNG chứng minh:** Không cho biết hệ thống xử lý được bao nhiêu user tối đa; chỉ cho biết 200 là an toàn.

Lệnh (chạy lúc 2026-10-26 13:07 (giờ máy)):

```bash
docker run --rm -v "$PWD/scripts:/s:ro" grafana/k6 run -e BASE_URL=http://$ALB -e VUS=200 -e DURATION=5m /s/loadtest.js
```

| Đối tượng | Trung vị (ms) | p90 (ms) | p95 (ms) | Lớn nhất (ms) |
|---|---|---|---|---|
| Tất cả request | 69,0 | 76,4 | 314,4 | 2160 |
| Trang frontend | 67,8 | 74,4 | 102,1 | 2040 |
| API (qua proxy của frontend) | 70,5 | 77,5 | 316,5 | 2160 |

Phần tổng kết gốc của k6 (nguyên văn):

```
# THRESHOLDS
    http_req_duration{kind:light}
    [đạt] 'p(95)<500' p(95)=314.41ms
    [đạt] 'p(99)<3000' p(99)=327.4ms
    http_req_duration{target:api}
    [đạt] 'p(95)<500' p(95)=316.48ms
    http_req_duration{target:frontend}
    [đạt] 'p(95)<500' p(95)=102.12ms
    http_req_failed
    [đạt] 'rate<0.01' rate=0.00%
  # TOTAL RESULTS
    checks_total.......: 102188  338.587531/s
    checks_succeeded...: 100.00% 102188 out of 102188
    checks_failed......: 0.00%   0 out of 102188
    [đạt] frontend 200
    [đạt] api / 200
    HTTP
    http_req_duration..............: avg=87.24ms min=60.08ms  med=69.02ms max=2.16s p(90)=76.43ms p(95)=314.41ms
      { expected_response:true }...: avg=87.24ms min=60.08ms  med=69.02ms max=2.16s p(90)=76.43ms p(95)=314.41ms
      { kind:light }...............: avg=87.24ms min=60.08ms  med=69.02ms max=2.16s p(90)=76.43ms p(95)=314.41ms
      { target:api }...............: avg=93.3ms  min=61.6ms   med=70.47ms max=2.16s p(90)=77.48ms p(95)=316.48ms
      { target:frontend }..........: avg=81.17ms min=60.08ms  med=67.85ms max=2.04s p(90)=74.36ms p(95)=102.12ms
    http_req_failed................: 0.00%  0 out of 102188
    http_reqs......................: 102188 338.587531/s
    EXECUTION
    iteration_duration.............: avg=1.17s   min=624.95ms med=1.17s   max=3.83s p(90)=1.57s   p(95)=1.62s
    iterations.....................: 51094  169.293766/s
    vus............................: 32     min=32          max=200
    vus_max........................: 200    min=200         max=200
    NETWORK
    data_received..................: 56 MB  187 kB/s
    data_sent......................: 12 MB  41 kB/s
```

### Bài B: 3 phút, thêm `GET /db` (truy vấn Aurora)

> **Mục đích:** Kiểm tra **tầng database** dưới tải: Aurora có co giãn không, pool kết nối của API có đủ không.
>
> **Cách test:** Như bài A (3 phút), thêm mỗi vòng một lần gọi `GET /db` (tăng bộ đếm trong Aurora).
>
> **Kết quả cho thấy:** Không lỗi, p95 306 ms. Aurora tự tăng dung lượng từ 0,5 lên 2,0 ACU và pool giữ ở mức 10 kết nối; không truy vấn nào thất bại.
>
> **Kết quả KHÔNG chứng minh:** Chưa nói gì về truy vấn phức tạp hơn một lệnh đếm.

Lệnh (chạy lúc 2026-10-26 13:55):

```bash
docker run --rm -v "$PWD/scripts:/s:ro" grafana/k6 run -e BASE_URL=http://$ALB -e VUS=200 -e DURATION=3m -e WITH_DB=1 /s/loadtest.js
```

| Đối tượng | Trung vị (ms) | p90 (ms) | p95 (ms) | Lớn nhất (ms) |
|---|---|---|---|---|
| Tất cả request | 70,7 | 79,5 | 306,0 | 2050 |
| Trang frontend | 68,1 | 75,7 | 84,8 | 2030 |
| API (qua proxy của frontend) | 70,4 | 78,0 | 307,8 | 2050 |

Phần tổng kết gốc của k6 (nguyên văn):

```
# THRESHOLDS
    http_req_duration{kind:light}
    [đạt] 'p(95)<500' p(95)=305.99ms
    [đạt] 'p(99)<3000' p(99)=322.3ms
    http_req_duration{target:api}
    [đạt] 'p(95)<500' p(95)=307.76ms
    http_req_duration{target:frontend}
    [đạt] 'p(95)<500' p(95)=84.83ms
    http_req_failed
    [đạt] 'rate<0.01' rate=0.00%
  # TOTAL RESULTS
    checks_total.......: 85953   472.839014/s
    checks_succeeded...: 100.00% 85953 out of 85953
    checks_failed......: 0.00%   0 out of 85953
    [đạt] frontend 200
    [đạt] api / 200
    [đạt] api /db 200
    HTTP
    http_req_duration..............: avg=84.83ms min=60.34ms  med=70.66ms max=2.05s p(90)=79.52ms p(95)=305.99ms
      { expected_response:true }...: avg=84.83ms min=60.34ms  med=70.66ms max=2.05s p(90)=79.52ms p(95)=305.99ms
      { kind:light }...............: avg=84.83ms min=60.34ms  med=70.66ms max=2.05s p(90)=79.52ms p(95)=305.99ms
      { target:api }...............: avg=85.13ms min=61.51ms  med=70.37ms max=2.05s p(90)=78.04ms p(95)=307.76ms
      { target:frontend }..........: avg=78.71ms min=60.34ms  med=68.12ms max=2.03s p(90)=75.67ms p(95)=84.83ms
    http_req_failed................: 0.00% 0 out of 85953
    http_reqs......................: 85953 472.839014/s
    EXECUTION
    iteration_duration.............: avg=1.26s   min=691.43ms med=1.26s   max=3.31s p(90)=1.65s   p(95)=1.71s
    iterations.....................: 28651 157.613005/s
    vus............................: 46    min=46         max=200
    vus_max........................: 200   min=200        max=200
    NETWORK
    data_received..................: 42 MB 233 kB/s
    data_sent......................: 11 MB 58 kB/s
```

### Bài C: 3 phút, API mới 32 endpoint, thêm đọc vị trí/ứng viên/thống kê và ghi ứng viên mới (`WITH_DOMAIN=1`)

> **Mục đích:** Kiểm tra **API thật 32 endpoint** với truy vấn đọc và ghi vào các bảng tuyển dụng, không chỉ endpoint demo.
>
> **Cách test:** Như bài A (3 phút), mỗi vòng thêm: đọc danh sách vị trí, danh sách ứng viên, thống kê; cứ 10 vòng tạo thêm một ứng viên mới. Trước khi chạy, tạo dữ liệu mẫu bằng `POST /seed`.
>
> **Kết quả cho thấy:** Không lỗi, p95 120 ms. Tuy nhiên CPU của database lên tới **92,6%** ở mức dung lượng tối đa, nên đây là **bài sát giới hạn nhất** của cấu hình hiện tại.
>
> **Kết quả KHÔNG chứng minh:** Không cho thấy hệ thống còn chịu được bao nhiêu nữa; ngược lại, nó báo trước database là điểm nghẽn tiếp theo.

Lệnh (chạy lúc 2026-10-26 14:41):

```bash
docker run --rm -v "$PWD/scripts:/s:ro" grafana/k6 run -e BASE_URL=http://$ALB -e VUS=200 -e DURATION=3m -e WITH_DOMAIN=1 /s/loadtest.js
```

| Đối tượng | Trung vị (ms) | p90 (ms) | p95 (ms) | Lớn nhất (ms) |
|---|---|---|---|---|
| Tất cả request | 71,7 | 82,0 | 119,9 | 2270 |
| Trang frontend | 68,9 | 77,2 | 81,0 | 1080 |
| API (qua proxy của frontend) | 70,9 | 76,9 | 79,8 | 1030 |

Phần tổng kết gốc của k6 (nguyên văn):

```
# THRESHOLDS
    http_req_duration{kind:light}
    [đạt] 'p(95)<500' p(95)=119.93ms
    [đạt] 'p(99)<3000' p(99)=306.26ms
    http_req_duration{target:api}
    [đạt] 'p(95)<500' p(95)=79.76ms
    http_req_duration{target:frontend}
    [đạt] 'p(95)<500' p(95)=81.05ms
    http_req_failed
    [đạt] 'rate<0.01' rate=0.00%
  # TOTAL RESULTS
    checks_total.......: 131694  714.352208/s
    checks_succeeded...: 100.00% 131694 out of 131694
    checks_failed......: 0.00%   0 out of 131694
    [đạt] seed 201
    [đạt] frontend 200
    [đạt] api / 200
    [đạt] positions 200
    [đạt] applicants 200
    [đạt] stats 200
    [đạt] create applicant 201
    HTTP
    http_req_duration..............: avg=78.19ms min=60.3ms   med=71.69ms max=2.27s p(90)=82.01ms p(95)=119.93ms
      { expected_response:true }...: avg=78.19ms min=60.3ms   med=71.69ms max=2.27s p(90)=82.01ms p(95)=119.93ms
      { kind:light }...............: avg=78.17ms min=60.3ms   med=71.69ms max=1.09s p(90)=82ms    p(95)=119.93ms
      { target:api }...............: avg=75.49ms min=61.72ms  med=70.9ms  max=1.03s p(90)=76.89ms p(95)=79.76ms
      { target:frontend }..........: avg=73.17ms min=60.3ms   med=68.9ms  max=1.08s p(90)=77.19ms p(95)=81.05ms
    http_req_failed................: 0.00%  0 out of 131694
    http_reqs......................: 131694 714.352208/s
    EXECUTION
    iteration_duration.............: avg=1.4s    min=823.46ms med=1.4s    max=3s    p(90)=1.79s   p(95)=1.85s
    iterations.....................: 25804  139.969508/s
    vus............................: 9      min=0           max=200
    vus_max........................: 200    min=200         max=200
    NETWORK
    data_received..................: 166 MB 901 kB/s
    data_sent......................: 18 MB  96 kB/s
```

### Hệ thống phản ứng thế nào trong bài A (mỗi ~17 giây một mẫu)

| Giờ | Pod FE | Pod API | Số node | CPU FE (m, tổng) | CPU API (m, tổng) |
|---|---|---|---|---|---|
| 13:07:16 | 1 | 2 | 4 | 1 | 0 |
| 13:07:33 | 1 | 2 | 4 | 52 | 46 |
| 13:07:50 | 1 | 2 | 4 | 50 | 43 |
| 13:08:08 | 1 | 2 | 4 | 53 | 44 |
| 13:08:24 | 1 | 2 | 4 | 56 | 44 |
| 13:08:41 | 2 | 2 | 4 | 51 | 43 |
| 13:08:58 | 2 | 2 | 4 | 65 | 42 |
| 13:09:16 | 2 | 2 | 4 | 61 | 40 |
| 13:09:34 | 2 | 2 | 4 | 62 | 42 |
| 13:09:50 | 2 | 2 | 4 | 61 | 40 |
| 13:10:07 | 2 | 2 | 4 | 60 | 41 |
| 13:10:26 | 2 | 2 | 4 | 60 | 41 |
| 13:10:44 | 2 | 2 | 4 | 61 | 40 |
| 13:11:01 | 2 | 2 | 4 | 62 | 40 |
| 13:11:17 | 2 | 2 | 4 | 60 | 42 |
| 13:11:34 | 2 | 2 | 4 | 60 | 41 |
| 13:11:53 | 2 | 2 | 4 | 59 | 41 |
| 13:12:11 | 2 | 2 | 4 | 60 | 24 |
| 13:12:30 | 2 | 1 | 4 | 46 | 24 |

- Số pod **không vượt 2** ở cả FE lẫn API (giới hạn đã đặt), số node giữ nguyên 4: không cần tạo thêm node nhờ chỗ trống (headroom) đã giữ sẵn.
- API lên 2 pod ngay từ đầu vì 200 request đồng thời chia cho mục tiêu 100 request mỗi pod; FE tăng từ 1 lên 2 pod nhờ HPA sau khoảng 90 giây.

### Aurora trong lúc có tải (CloudWatch, giá trị lớn nhất mỗi phút)

Nhãn giờ ở đầu mỗi cột là **giờ máy (UTC+7)**; cửa sổ truy vấn ghi theo UTC. Mỗi bài chỉ lấy các phút nằm trong cửa sổ chạy tải.

**Bài B (có `/db`)**, cửa sổ UTC 2026-10-26T06:57:43Z đến 2026-10-26T07:00:45Z:

```
ServerlessDatabaseCapacity: 13:57=2.00  13:58=1.50  13:59=1.50
DatabaseConnections: 13:57=2.00  13:58=10.00  13:59=10.00
CPUUtilization: 13:57=69.47  13:58=56.47  13:59=60.78
```

**Bài C (endpoint domain)**, cửa sổ UTC 2026-10-26T07:41:18Z đến 2026-10-26T07:44:23Z:

```
ServerlessDatabaseCapacity: 14:41=2.00  14:42=2.00  14:43=1.50
DatabaseConnections: 14:41=10.00  14:42=10.00  14:43=10.00
CPUUtilization: 14:41=92.61  14:42=74.07  14:43=67.89
```

- Bài B (có `/db`): đỉnh dung lượng **2,00 ACU** (mức tối đa đã đặt là 2,0), tối đa **10 kết nối**, CPU của database lên tới **69,5%**.
- Bài C (endpoint domain): đỉnh dung lượng **2,00 ACU** (mức tối đa đã đặt là 2,0), tối đa **10 kết nối**, CPU của database lên tới **92,6%**.
- Nghĩa là: dung lượng tăng từ mức tối thiểu 0,5 ACU lên mức tối đa khi có 200 user, và CPU của database rất cao (bài C gần chạm giới hạn). Database **chịu được 200 user nhưng không còn dư địa**; tải cao hơn cần tăng `db_max_acu`, và tôi chưa thử.

## 5. Bằng chứng khả năng scale: HPA, Karpenter, KEDA với SQS

Dữ liệu thô: `docs/evidence/scaling/` (bài frontend HPA, Karpenter, KEDA với SQS: chuỗi thời gian, sự kiện, nhật ký Karpenter, bộ đếm SQS) và `docs/evidence/hpa-demo/` (bài HPA tốn CPU).

> **Mục đích:** Chứng minh bằng số đo và nhật ký của chính cluster rằng ba tầng co giãn đều **hoạt động thật**: HPA thêm/bớt **pod** theo CPU, Karpenter thêm/bớt **node** theo pod không có chỗ chạy, KEDA bật/tắt **worker** theo độ dài hàng đợi SQS.
>
> **Cách test:** Cho mỗi tầng: tạo đúng điều kiện kích hoạt, rồi ghi lại trạng thái cluster **mỗi khoảng 8 giây** (số pod, đầu ra của HPA, số node, độ dài hàng đợi) kèm **sự kiện Kubernetes** và **nhật ký của Karpenter** sinh ra trong lúc đó. Sự kiện và nhật ký do các thành phần tự ghi, nên là bằng chứng độc lập với script đo.
>
> **Kết quả cho thấy:** Mỗi tiểu mục dưới đây có (1) bảng thay đổi theo thời gian, (2) các sự kiện/nhật ký tương ứng, (3) kết luận cho tầng đó. Một tầng được coi là chứng minh khi **cả hai chiều** (tăng khi có tải, giảm về mức tối thiểu khi hết tải) đều xuất hiện.
>
> **Kết quả KHÔNG chứng minh:** Không chứng minh ngưỡng cụ thể ở mọi mức tải; mỗi bài chỉ là một lần chạy.

### 5.1 HPA: tầng pod

> **Mục đích:** Chứng minh HPA **tăng số pod khi CPU vượt mục tiêu (50% so với mức pod xin)** và **giảm khi hết tải**. Với frontend, HPA có giới hạn 1 đến 2 pod.
>
> **Cách test:** Bài 1 (frontend thật): k6 200 user trong 4 phút vào trang frontend và `/api/`, rồi chờ HPA tự giảm (cửa sổ giảm 5 phút). Bài 2 (workload tốn CPU có chủ đích): xem 5.1b nếu có.
>
> **Kết quả cho thấy:** Xem kết luận ngay dưới bảng.
>
> **Kết quả KHÔNG chứng minh:** Một lần chạy không chứng minh HPA tính đúng ở mọi mức CPU.

Lệnh:

```bash
docker run --rm -v "$PWD/scripts:/s:ro" grafana/k6 run -e BASE_URL=http://$ALB -e VUS=200 -e DURATION=4m /s/loadtest.js
kubectl -n demo get hpa fe -w          # xem HPA phản ứng
```

**Bài 1: frontend dưới 200 user (các thời điểm có thay đổi)**

| Giờ | Pod frontend | HPA đang đặt | CPU so với mức pod xin (%) | Ghi chú |
|---|---|---|---|---|
| 14:55:49 | 1 | 1 | 1 | bắt đầu tải |
| 14:56:37 | 1 | 1 | 46 |  |
| 14:57:25 | 1 | 1 | 46 |  |
| 14:58:13 | 1 | 1 | 45 |  |
| 14:59:01 | 1 | 1 | 45 |  |
| 14:59:49 | 1 | 1 | 47 | kết thúc tải |

**Kết luận (lần chạy này):** CPU của frontend chỉ đạt đỉnh **47%**, thấp hơn mục tiêu 50%, nên HPA **đúng** là không thêm pod (số pod luôn 1). Lần chạy này **không** là bằng chứng scale-out; nó chỉ cho thấy HPA không scale khi chưa vượt ngưỡng. Ở bài A (mục 4) cùng tải này đã làm CPU nhỉnh qua 50% và frontend lên 2 pod, nên frontend nằm sát ngưỡng và không phải bằng chứng ổn định. Bằng chứng chắc chắn hơn nằm ở 5.1b (workload tốn CPU rõ rệt).

Sự kiện co giãn (`SuccessfulRescale`) của HPA frontend trong bài này: **không có**, HPA không đổi số pod lần nào.

Đầu ra `kubectl -n demo get hpa` ngay sau tải:

```
$ kubectl -n demo get hpa
NAME              REFERENCE           TARGETS             MINPODS   MAXPODS   REPLICAS   AGE
fe                Deployment/fe       cpu: 47%/50%        1         2         1          3h42m
keda-hpa-api      Deployment/api      0/100 (avg)         1         2         1          3h42m
keda-hpa-worker   Deployment/worker   <unknown>/5 (avg)   1         20        0          74m
```

#### 5.1b HPA với workload tốn CPU có chủ đích

> **Mục đích:** Frontend nginx dùng rất ít CPU nên nằm sát ngưỡng và không cho bằng chứng ổn định. Cần một workload **chắc chắn vượt 50% CPU** để quan sát HPA tăng rồi giảm.
>
> **Cách test:** Triển khai một bản sao của API bằng đúng chart `app` ở chế độ HPA (tối thiểu 1, tối đa 3 pod, mục tiêu CPU 50%, pod xin 250m CPU, cửa sổ giảm 60 giây). Từ **bên trong cluster**, vài vòng lặp gọi `GET /burn?ms=4000` (endpoint đốt CPU) tạo tải CPU lớn; sau đó dừng tải và chờ HPA giảm. Ghi trạng thái mỗi ~6 giây.
>
> **Kết quả cho thấy:** Xem bảng dưới: số pod tăng khi CPU vượt mục tiêu và giảm về 1 khi hết tải.
>
> **Kết quả KHÔNG chứng minh:** Đây là workload thử nghiệm, không phải frontend hay API thật; cửa sổ giảm 60 giây ngắn hơn giá trị thật (300 giây) để bài chạy gọn.

Ghi chú: cột "Pod đang chạy" đếm cả pod tạo tải (`load`) và pod đang tắt dần; số pod của HPA là cột "HPA hiện có". Hai sự kiện `Failed...` đầu tiên là HPA chưa kịp có số đo CPU của pod mới tạo, không phải lỗi co giãn.

| Giờ | Pod đang chạy | HPA hiện có | HPA đang đặt | CPU so với mức pod xin (%) | Số node default |
|---|---|---|---|---|---|
| 15:12:12 | 1 |  | 0 | - | 2 |
| 15:12:29 | 1 | 1 | 0 | - | 2 |
| 15:12:46 | 2 | 1 | 1 | 0 | 2 |
| 15:12:54 | 2 | 1 | 1 | 0 | 2 |
| 15:13:27 | 3 | 1 | 2 | 779 | 2 |
| 15:13:36 | 3 | 1 | 2 | 779 | 2 |
| 15:13:44 | 4 | 2 | 3 | 780 | 2 |
| 15:14:01 | 4 | 3 | 3 | 642 | 2 |
| 15:14:18 | 4 | 3 | 3 | 455 | 2 |
| 15:15:00 | 4 | 3 | 3 | 489 | 2 |
| 15:15:42 | 3 | 3 | 3 | 392 | 2 |
| 15:16:24 | 3 | 3 | 3 | 87 | 2 |
| 15:17:06 | 3 | 3 | 3 | 0 | 3 |
| 15:17:47 | 3 | 3 | 3 | 0 | 3 |
| 15:17:55 | 1 | 3 | 1 | 0 | 3 |
| 15:18:12 | 1 | 1 | 1 | 0 | 3 |
| 15:18:20 | 1 | 1 | 1 | 0 | 3 |

- 15:12:22 `FailedComputeMetricsReplicas`: invalid metrics (1 invalid out of 1), first error is: failed to get cpu resource metric value: failed to get cpu utilization: unable to get metrics for resource cpu: no metrics returned from resource metrics API
- 15:12:22 `FailedGetResourceMetric`: failed to get cpu utilization: unable to get metrics for resource cpu: no metrics returned from resource metrics API
- 15:13:22 `SuccessfulRescale`: New size: 2; reason: cpu resource utilization (percentage of request) above target
- 15:13:37 `SuccessfulRescale`: New size: 3; reason: cpu resource utilization (percentage of request) above target
- 15:17:53 `SuccessfulRescale`: New size: 1; reason: All metrics below target

**Kết luận:** HPA tăng từ 1 lên **3 pod** (mức tối đa đã đặt) khi CPU vượt xa 50%, mỗi bước đều có sự kiện `SuccessfulRescale` ghi nguyên nhân, rồi sau khi hết tải và hết cửa sổ ổn định 60 giây thì đưa về **1 pod**. Cả scale-out và scale-in đều có bằng chứng.

### 5.2 Karpenter: tầng node

> **Mục đích:** Chứng minh Karpenter **tạo node mới khi có pod không còn chỗ chạy** và **dọn node thừa khi không còn cần**, bằng node spot đúng loại.
>
> **Cách test:** Tạo một Deployment gồm 3 pod, mỗi pod xin 1200m CPU (tổng 3600m, lớn hơn chỗ trống trên các node hiện có), chỉ định chạy trên pool `default`. Chờ cả 3 pod sẵn sàng. Sau đó xoá Deployment và chờ Karpenter thu hồi node. Ghi số node, `NodeClaim`, và nhật ký của Karpenter.
>
> **Kết quả cho thấy:** Nhật ký Karpenter bên dưới là chuỗi sự kiện do Karpenter tự ghi: thấy pod chưa xếp được, tính node cần, tạo `NodeClaim`, node đăng ký và sẵn sàng, rồi khi hết cần thì gom và xoá node.
>
> **Kết quả KHÔNG chứng minh:** Không chứng minh Karpenter xử lý đúng khi spot hết hàng hoặc bị thu hồi.

Lệnh:

```bash
kubectl create namespace tier-test
# Deployment 'karpenter-probe': 3 replicas, nodeSelector nodepool=default, cpu request 1200m, image registry.k8s.io/pause:3.10
kubectl get nodeclaim -w
kubectl delete namespace tier-test       # rồi chờ node biến mất
```

| Giờ | Số node pool default | Số NodeClaim | Ghi chú |
|---|---|---|---|
| 15:00:21 | 2 | 4 | tạo 3 pod xin 3600m CPU |
| 15:00:37 | 3 | 4 |  |
| 15:00:45 | 4 | 4 |  |
| 15:03:01 | 3 | 3 |  |
| 15:04:13 | 2 | 2 |  |

- Từ lúc tạo pod đến khi cả 3 pod sẵn sàng: **44 giây** (gồm Karpenter tạo node spot mới).
- Từ lúc xoá bài thử đến khi số node về mức ban đầu: **191 giây** (gồm thời gian chờ gom node).

**Pod của bài thử và node chúng chạy (`kubectl get pods -o wide`):**

```
$ kubectl -n tier-test get pods -o wide
NAME                               READY   STATUS    RESTARTS   AGE   IP            NODE                                             NOMINATED NODE   READINESS GATES
karpenter-probe-6474d9dfdf-bj7sr   1/1     Running   0          43s   10.0.5.139    ip-10-0-7-166.ap-southeast-1.compute.internal    <none>           <none>
karpenter-probe-6474d9dfdf-r42sv   1/1     Running   0          43s   10.0.6.155    ip-10-0-3-182.ap-southeast-1.compute.internal    <none>           <none>
karpenter-probe-6474d9dfdf-wxfnz   1/1     Running   0          43s   10.0.39.112   ip-10-0-46-236.ap-southeast-1.compute.internal   <none>           <none>
```

**Các NodeClaim lúc đó (`kubectl get nodeclaim -o wide`):**

```
$ kubectl get nodeclaim -o wide
NAME            TYPE        CAPACITY   ZONE              NODE                                             READY   AGE     IMAGEID                 ID                                           NODEPOOL   NODECLASS   DRIFTED
default-6cwjh   t3.medium   spot       ap-southeast-1c   ip-10-0-46-236.ap-southeast-1.compute.internal   True    3h52m   ami-0b1988268abc044b3   aws:///ap-southeast-1c/i-0413228afd3d7d2b0   default    default     
default-fjpzt   c7g.large   spot       ap-southeast-1a   ip-10-0-7-166.ap-southeast-1.compute.internal    True    55m     ami-0cfb5d0ee50ea6e01   aws:///ap-southeast-1a/i-09d9899e26d31efe6   default    default     
default-t484t   t3.medium   spot       ap-southeast-1a   ip-10-0-3-182.ap-southeast-1.compute.internal    True    43s     ami-0b1988268abc044b3   aws:///ap-southeast-1a/i-027015b754e78a226   default    default     
default-t9f2c   t4g.large   spot       ap-southeast-1c   ip-10-0-35-242.ap-southeast-1.compute.internal   True    43s     ami-0cfb5d0ee50ea6e01   aws:///ap-southeast-1c/i-072ec2622d52c7aaa   default    default
```

**Nhật ký của Karpenter trong bài này (lọc các sự kiện chính; giờ máy):**

| Giờ | Sự kiện | Chi tiết |
|---|---|---|
| 15:00:20 | computed new nodeclaim(s) to fit pod(s) |  |
| 15:00:20 | found provisionable pod(s) |  |
| 15:00:20 | created nodeclaim | NodeClaim=default-t9f2c |
| 15:00:20 | created nodeclaim | NodeClaim=default-t484t |
| 15:00:23 | launched nodeclaim | NodeClaim=default-t9f2c, instance-type=t4g.large, capacity-type=spot, zone=ap-southeast-1c |
| 15:00:24 | launched nodeclaim | NodeClaim=default-t484t, instance-type=t3.medium, capacity-type=spot, zone=ap-southeast-1a |
| 15:00:41 | registered nodeclaim | NodeClaim=default-t484t |
| 15:00:44 | registered nodeclaim | NodeClaim=default-t9f2c |
| 15:00:55 | initialized nodeclaim | NodeClaim=default-t484t |
| 15:00:56 | initialized nodeclaim | NodeClaim=default-t9f2c |
| 15:02:31 | disrupting node(s) |  |
| 15:02:31 | tainted node | NodeClaim=default-fjpzt |
| 15:03:03 | deleted node | NodeClaim=default-fjpzt |
| 15:03:03 | deleted nodeclaim | NodeClaim=default-fjpzt |
| 15:03:47 | tainted node | NodeClaim=default-6cwjh |
| 15:04:14 | deleted node | NodeClaim=default-6cwjh |
| 15:04:14 | deleted nodeclaim | NodeClaim=default-6cwjh |
| 15:05:01 | created nodeclaim | NodeClaim=worker-s6h2l |
| 15:05:06 | launched nodeclaim | NodeClaim=worker-s6h2l, instance-type=t3.medium, capacity-type=spot, zone=ap-southeast-1b |
| 15:05:14 | created nodeclaim | NodeClaim=worker-4mx4s |
| 15:05:14 | created nodeclaim | NodeClaim=worker-xqrrq |

**Kết luận:** số node pool `default` tăng từ 2 lên 4 ngay sau khi 3 pod không còn chỗ chạy, cả 3 pod sẵn sàng sau 44 giây, rồi sau khi xoá pod, Karpenter tự gom và xoá node thừa, đưa về 2 node sau 191 giây. Cả hai chiều đều có bằng chứng, kèm nhật ký do chính Karpenter ghi. Các NodeClaim đều là `spot`, gồm cả amd64 (t3) và arm64 (t4g, c7g).

### 5.3 KEDA với SQS: tầng worker

> **Mục đích:** Chứng minh KEDA **bật worker khi hàng đợi có message** (từ 0 pod), **tăng số worker theo độ dài hàng đợi**, **xử lý hết message không mất cái nào**, rồi **đưa worker về 0** khi hàng đợi rỗng.
>
> **Cách test:** Gửi 500 message vào SQS qua `POST /jobs` (5 lần × 100). KEDA đọc độ dài hàng đợi để quyết định số worker (mục tiêu 5 message mỗi worker, tối đa 20). Ghi độ dài hàng đợi, số worker và số node worker; sau đó đối chiếu **bộ đếm của chính SQS** (gửi, nhận, xoá) và hàng đợi lỗi (DLQ).
>
> **Kết quả cho thấy:** Số message gửi, nhận và xoá bằng nhau và DLQ bằng 0 nghĩa là mọi message đều được worker xử lý thành công. Chuỗi 0 → N → 0 pod của worker là bằng chứng KEDA scale theo hàng đợi cả hai chiều.
>
> **Kết quả KHÔNG chứng minh:** Worker chỉ giả lập công việc (ngủ 2 giây mỗi message); không chứng minh khối lượng xử lý nặng thật.

Lệnh:

```bash
Q=$(terraform -chdir=terraform output -raw queue_url)
for i in 1 2 3 4 5; do curl -s -XPOST -H "Host: api.demo.local" http://$ALB/jobs -d '{"count":100}'; echo; done
watch -n 5 "aws sqs get-queue-attributes --queue-url $Q --region ap-southeast-1 --attribute-names ApproximateNumberOfMessages ApproximateNumberOfMessagesNotVisible --query Attributes --output text"
kubectl -n demo get pods -l app.kubernetes.io/name=worker -w ; kubectl -n demo get hpa keda-hpa-worker
```

| Giờ | Message chờ | Đang xử lý | Worker (đang chạy / tổng) | HPA của KEDA đặt | Node worker | DLQ |
|---|---|---|---|---|---|---|
| 15:04:37 | 0 | 0 | 0 / 0 | 0 | 0 | 0 |
| 15:05:01 | 500 | 0 | 0 / 1 | 0 | 0 | 0 |
| 15:05:17 | 500 | 0 | 0 / 4 | 4 | 0 | 0 |
| 15:05:25 | 500 | 0 | 0 / 4 | 4 | 1 | 0 |
| 15:05:33 | 500 | 0 | 0 / 8 | 8 | 3 | 0 |
| 15:05:41 | 500 | 0 | 5 / 8 | 16 | 3 | 0 |
| 15:05:49 | 500 | 0 | 11 / 16 | 16 | 3 | 0 |
| 15:05:57 | 0 | 0 | 11 / 20 | 20 | 3 | 0 |
| 15:06:05 | 0 | 0 | 19 / 20 | 20 | 3 | 0 |
| 15:06:21 | 0 | 0 | 20 / 20 | 20 | 3 | 0 |
| 15:07:01 | 0 | 0 | 0 / 3 | 20 | 3 | 0 |
| 15:07:09 | 0 | 0 | 0 / 0 | 20 | 3 | 0 |
| 15:07:17 | 0 | 0 | 0 / 0 | 0 | 3 | 0 |
| 15:07:33 | 0 | 0 | 0 / 0 | 0 | 3 | 0 |

Mốc thời gian kể từ lúc gửi message (độ phân giải khoảng 8 đến 14 giây):

- worker pod đầu tiên được tạo (KEDA kích hoạt): **+25 giây**
- node worker đầu tiên có mặt (Karpenter): **+49 giây**
- worker đầu tiên chạy: **+65 giây**
- hàng đợi hết message: **+81 giây**
- worker về 0 pod: **+153 giây**
- Đỉnh: **20 pod worker**, **3 node worker**, DLQ lớn nhất **0**.

**Sự kiện co giãn do KEDA/HPA ghi cho worker (giờ máy):**

| Giờ | Đối tượng | Lý do | Nội dung |
|---|---|---|---|
| 15:05:00 | ScaledObject/worker | KEDAScaleTargetActivated | Scaled apps/v1.Deployment demo/worker from 0 to 1, triggered by s0-aws-sqs-demo-jobs |
| 15:05:13 | HorizontalPodAutoscaler/keda-hpa-worker | SuccessfulRescale | New size: 4; reason: external metric s0-aws-sqs-demo-jobs(&LabelSelector{MatchLabels:map[string]string{scaledobject.keda.sh/name: worker,},M |
| 15:05:28 | HorizontalPodAutoscaler/keda-hpa-worker | SuccessfulRescale | New size: 8; reason: external metric s0-aws-sqs-demo-jobs(&LabelSelector{MatchLabels:map[string]string{scaledobject.keda.sh/name: worker,},M |
| 15:05:43 | HorizontalPodAutoscaler/keda-hpa-worker | SuccessfulRescale | New size: 16; reason: external metric s0-aws-sqs-demo-jobs(&LabelSelector{MatchLabels:map[string]string{scaledobject.keda.sh/name: worker,}, |
| 15:05:58 | HorizontalPodAutoscaler/keda-hpa-worker | SuccessfulRescale | New size: 20; reason: All metrics below target |
| 15:07:00 | ScaledObject/worker | KEDAScaleTargetDeactivated | Deactivated apps/v1.Deployment demo/worker from 20 to 0 |

**Bộ đếm của chính SQS (CloudWatch, cộng cả cửa sổ 40 phút của bài chạy; gồm 500 message của bài này, có thể cộng thêm vài message của các lần thử khác trong cùng cửa sổ):**

```
NumberOfMessagesSent 504.0
NumberOfMessagesReceived 504.0
NumberOfMessagesDeleted 504.0
DLQ_MessagesSent 0.0
```

Trạng thái ScaledObject của worker **trước** khi gửi message:

```
$ kubectl -n demo get scaledobject worker
NAME     SCALETARGETKIND      SCALETARGETNAME   MIN   MAX   READY   ACTIVE   FALLBACK   PAUSED   TRIGGERS        AUTHENTICATIONS   AGE
worker   apps/v1.Deployment   worker            0     20    True    False    False      False    aws-sqs-queue   worker-aws        79m
```

**Kết luận:** worker đi từ **0 pod** lên **20 pod** (tối đa đã đặt) khi có 500 message, mỗi bước tăng đều có sự kiện `SuccessfulRescale` của KEDA, Karpenter tạo thêm node worker để chứa chúng, SQS ghi nhận số message gửi = nhận = xoá và DLQ = 0 (không mất, không lỗi), rồi khi hàng đợi rỗng KEDA đưa worker **về 0**. **Lưu ý trung thực:** mẫu lấy mỗi ~8 giây nên cột "Đang xử lý" có thể bỏ sót khoảng thời gian ngắn; bộ đếm CloudWatch là con số đáng tin hơn. Các node worker không bị xoá ngay lúc worker về 0 vì Karpenter còn chờ gom (consolidateAfter) và có thể giữ làm chỗ trống cho pod khác.

## 6. Tổng kết: hệ thống và các phương pháp đã áp dụng

### 6.1 Hệ thống gồm gì

- **Ứng dụng:** API viết bằng Go (32 endpoint, miền tuyển dụng: vị trí, ứng viên, hồ sơ...), frontend React (nginx), worker Go xử lý hàng đợi.
- **Hạ tầng AWS (vùng `ap-southeast-1`):** EKS 1.33, ALB đứng trước (một ALB dùng chung cho API và frontend), SQS (kèm hàng đợi lỗi DLQ), Aurora PostgreSQL Serverless v2 (0,5 đến 2 ACU, không bao giờ tạm dừng), ECR chứa image. Tất cả dựng bằng Terraform.
- **Chưa làm:** Route 53 (chưa có tên miền); hiện truy cập bằng địa chỉ DNS của ALB.
- **Chart Helm dùng chung `charts/app`:** thay image và cấu hình là chạy được API, frontend hay ứng dụng khác (đã kiểm chứng bằng `scripts/test-plug-image.sh`); chọn chế độ co giãn bằng một giá trị (`hpa`, `http`, `sqs`, `none`).

### 6.2 Ba tầng co giãn

| Tầng | Công cụ | Áp dụng cho | Co giãn theo | Giới hạn |
|---|---|---|---|---|
| Node | Karpenter | mọi pod không còn chỗ chạy | pod ở trạng thái chưa xếp được | pool `default` tối đa 16 vCPU, pool `worker` tối đa 12 vCPU |
| Pod | KEDA HTTP Add-on | API | số request đồng thời (mục tiêu 100 mỗi pod) | **0 đến 2 pod** (về 0 sau 300 giây không có request) |
| Pod | HPA | Frontend | CPU 50% so với mức pod xin | **1 đến 2 pod** |
| Pod | KEDA + SQS | Worker | độ dài hàng đợi (mục tiêu 5 message mỗi pod) | **0 đến 20 pod** |

### 6.3 Các phương pháp để giảm thời gian chờ (over-provisioning và liên quan)

1. **Chỗ trống dự phòng bằng pod giữ chỗ (placeholder), tức over-provisioning có kiểm soát:**
   - Luôn có **2 node spot** thuộc pool `default` chạy sẵn: **1 node amd64 và 1 node arm64**, mỗi node có **1 pod giữ chỗ** (image `pause`, xin 500m CPU và 512 MiB RAM, độ ưu tiên -10, không bị Karpenter gom).
   - Node là loại 2 vCPU, nên sau khi trừ phần pod giữ chỗ và hệ thống thì **mỗi node còn trống khoảng 1,28 vCPU** để pod API/frontend mới xếp vào ngay, **không phải chờ tạo node** (tạo node mất khoảng 40 đến 50 giây).
   - Khi chỗ trống hết, pod thật (độ ưu tiên 0) đẩy pod giữ chỗ ra; pod giữ chỗ chưa xếp được sẽ khiến Karpenter tạo node thay thế, nên chỗ trống tự được bù lại.
   - Chi phí của phương pháp này: **2 node spot chạy liên tục** kể cả khi không ai dùng.
2. **Tải sẵn image (pre-pull):** một DaemonSet chạy các image API/frontend trên mọi node (init container chạy `-version` rồi thoát) để image đã nằm trong bộ nhớ đệm của node, pod mới không mất thời gian kéo image.
3. **Bỏ `startupProbe` của API:** đo được trung vị giảm từ 2,97 xuống 2,06 giây vì kubelet chạy hai probe nối tiếp. Giữ `readinessProbe` 1 giây vì bỏ nó không nhanh hơn (đã đo).
4. **Cấu hình VPC CNI `WARM_IP_TARGET=3` và `MINIMUM_IP_TARGET=6`:** node giữ sẵn địa chỉ IP, pod mới không phải chờ cấp IP.
5. **Interceptor của KEDA giữ request** trong lúc API bật từ 0 lên 1, nên người dùng chỉ thấy chậm hơn chứ không nhận lỗi.
6. **Pool riêng cho worker:** pool `worker` có taint, chỉ worker chạy ở đó, không tranh chỗ với API/frontend.

### 6.4 Số lượng tài nguyên lúc nhàn rỗi và lúc cao tải

| Thành phần | Nhàn rỗi | Cao tải (tối đa đã đặt) |
|---|---|---|
| Node hệ thống (nhóm node có sẵn của EKS, spot) | 2 node `t3.medium` | 2 (cố định) |
| Node `default` (spot) | 2 (1 amd64, 1 arm64, mỗi node có 1 pod giữ chỗ) | tăng theo nhu cầu, tối đa 16 vCPU |
| Node `worker` (spot) | 0 | tối đa 12 vCPU (đo được 3 node khi 20 worker chạy) |
| Pod API | **0** | 2 |
| Pod frontend | 1 | 2 |
| Pod worker | **0** | 20 |
| Aurora | 0,5 ACU | 2 ACU |

**Về nguồn tính toán:** toàn bộ node đều là **spot** theo yêu cầu của bạn (chấp nhận rủi ro bị thu hồi). Đã thử mất node đột ngột (xem `docs/results/`), nhưng chưa thử mất cả hai node hệ thống cùng lúc.

### 6.5 Đánh giá theo yêu cầu

| Yêu cầu | Trạng thái |
|---|---|
| API Go 20 đến 50 endpoint | **Đạt** (32 endpoint, đã có kiểm thử tự động và kiểm thử qua ALB) |
| Frontend React | **Đạt** |
| ALB | **Đạt** |
| Route 53 | **Chưa làm** (chưa có tên miền) |
| Ba tầng co giãn (Karpenter, KEDA với SQS, HPA) | **Đạt**, có bằng chứng ở mục 5 |
| API scale to zero | **Đạt** |
| Cold start dưới 3 giây | **Phần lớn đạt:** 19/20 lượt, có 1 lượt 3,07 giây; chưa thể nói "luôn dưới 3 giây" |
| 200 user cùng lúc | **Đạt**, nhưng database gần hết dư địa (xem mục 4) |
| Toàn bộ node là spot, arm64 và amd64 | **Đạt** |
| Aurora Serverless v2 không tạm dừng | **Đạt** (tối thiểu 0,5 ACU) |
| Chart dùng được với image bất kỳ | **Đạt** |
| Huỷ toàn bộ hạ tầng | Có sẵn `scripts/down.sh` (đã thử chế độ `--dry-run`); **chưa chạy thật** |

## 7. Hạn chế của các số đo trên

- Đo từ **một máy** ngoài AWS, nên gồm round trip mạng của máy đó và các lần mất gói SYN ngẫu nhiên (mục 3).
- Cold start mỗi lần chỉ 20 lượt; p95 của 20 mẫu là ước lượng thô.
- Dấu thời gian pod của Kubernetes chỉ chính xác đến 1 giây, nên các cột "Pod tạo sau request" và "Pod tạo đến sẵn sàng" có sai số khoảng 1 giây.
- Pod API luôn rơi vào node đã có chỗ trống (headroom). Chưa đo cold start khi chỗ trống đã hết (lúc đó phải tạo node mới, đo được khoảng 36 giây ở một lần thử riêng).
- Bài tải 200 user dùng kết nối giữ lâu (keep-alive) và nghỉ 1 giây giữa các lượt; chưa thử tải cao hơn 200 user, và chưa thử 200 user mới kết nối cùng một giây khi API đang ở 0 pod ngoài lượt đầu của bài A.

