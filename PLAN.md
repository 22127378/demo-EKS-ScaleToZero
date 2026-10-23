# Plan demo: EKS cắm image bất kỳ, toàn spot, API scale-to-zero

> Trạng thái: **chỉ là plan, chưa implement**. Bắt đầu làm khi được cho phép.

## 1. Mục tiêu và nghiệm thu

**Mục tiêu:** lấy một image API hoặc FE bất kỳ, chỉ sửa vài dòng values (image, port, host) rồi chạy được trên hệ thống có:

- Route 53 → ALB → EKS.
- 3 tầng scale: Karpenter (node), KEDA (SQS và HTTP), HPA (pod).
- API scale về 0 pod, cold start thấp nhờ placeholder.
- Toàn bộ node là spot, NodePool dùng cả amd64 và arm64.

**Ngoài phạm vi:** WAF, CloudFront, multi-env, CI/CD, observability đầy đủ.

**Ngân sách:** chạy trên tài khoản AWS đã nâng cấp (trả phí), **không** ràng buộc free tier. EKS, NAT gateway, Aurora đều tính phí; dùng `down.sh` để huỷ sau mỗi lần demo.

**Giả định mặc định** (sửa nếu chưa đúng):

| Mục | Giá trị |
|---|---|
| Region | `ap-southeast-1` |
| Domain | Không có hosted zone, dùng DNS của ALB (xem 3.5) |
| Image mẫu | nginx (FE), một echo server công khai (API) |
| FE | Không về 0, `min: 1`, `max: 2` |
| API | Về 0, `max: 2` |
| Giới hạn pod | **Tối đa 2 pod cho mỗi app API và FE** (worker không bị giới hạn này) |
| Database | Aurora Serverless v2, engine **PostgreSQL**, một writer, scale 0..2 ACU (xem 3.7). Đổi sang MySQL nếu bạn muốn |
| App demo | Tự viết: API + worker (Go), FE (React + Vite + TypeScript), image multi-arch, push lên ECR. Vẫn test thêm với nginx và echo server công khai |
| Worker | Về 0 qua SQS |
| Placeholder | Một Deployment mỗi kiến trúc (amd64, arm64), mỗi cái 1 replica; chỉnh được (xem 3.6) |

## 2. Kiến trúc

```
Client ─► Route 53 ─► ALB (1 ALB dùng chung, group.name=demo)
                       ├─ app.<domain> ─► Pods FE        (HPA, 1..2)
                       └─ api.<domain> ─► Interceptor ─► Pods API (KEDA HTTP, 0..2)
                                          (giữ request khi API đang ở 0)

API ─► SQS (+DLQ) ─► KEDA ─► Pods worker (0..N, spot, taint workload=worker)

Karpenter ─► tạo/xoá node spot (amd64 + arm64) theo pod Pending
Placeholder (pause, priority -10), mỗi kiến trúc một Deployment
System node group (spot, 2 node, 2 AZ): Karpenter, CoreDNS, LB Controller,
                                         KEDA, HTTP Add-on, metrics-server
```

## 3. Quyết định thiết kế

### 3.1 Điều kiện để "image nào cũng chạy"

| Vấn đề | Xử lý trong chart `app` |
|---|---|
| Khác port | `containerPort` là tham số |
| Không có endpoint health | Probe mặc định là TCP. Chỉ dùng HTTP khi khai báo `probe.httpPath` |
| Khác kiến trúc CPU | Tham số `arch` + script `detect-arch.sh` (xem 3.2) |
| Entrypoint riêng | Cho override `command`, `args` |
| Biến môi trường, secret | `env`, `envFrom` |
| Image chạy root hoặc ghi file | Không ép `runAsNonRoot`, có `securityContext` tuỳ chọn |
| Private registry | `imagePullSecrets`, hoặc ECR qua IAM node role |
| Thêm app mới | Một lệnh `helm install <tên> charts/app -f values.yaml`, dùng chung ALB |
| App dưới sub-path | Không hỗ trợ (ALB không cắt prefix). Dùng routing theo host |

### 3.2 Đa kiến trúc (amd64 + arm64)

- NodePool cho `kubernetes.io/arch: [amd64, arm64]`. Family amd64: `c5, c6i, m5, m6i, t3`. Family arm64: `c6g, c7g, m6g, m7g, t4g`.
- Node group system chạy cả hai kiến trúc (`t3.medium` kèm `t4g.medium` dự phòng). Các add-on đều có image multi-arch.
- **Vấn đề:** Karpenter chọn node rẻ nhất (thường là Graviton spot) và không biết image hỗ trợ kiến trúc nào. Image chỉ có amd64 mà lên node arm64 sẽ lỗi `exec format error`.
- Chart `app` có tham số `arch: auto | amd64 | arm64`:
  - `amd64` / `arm64`: thêm `nodeSelector kubernetes.io/arch`.
  - `auto`: không ràng buộc, chỉ dùng cho image multi-arch.
- `scripts/detect-arch.sh <image:tag>` đọc manifest (`docker buildx imagetools inspect` hoặc `crane`):
  - Có cả hai kiến trúc → `auto`.
  - Chỉ một → ép đúng kiến trúc đó.
  - Không đọc được manifest → mặc định `amd64` và cảnh báo.

### 3.3 Toàn spot

- Node group system: `capacity_type = SPOT`, 2 node ở 2 AZ, khai báo nhiều instance type để giảm nguy cơ thiếu hàng.
- NodePool: `capacity-type: [spot]` duy nhất. Spot hết hàng thì pod Pending (đã chấp nhận).
- Rủi ro đã biết:
  - Node chứa Karpenter bị thu hồi: ASG cần vài phút thay. Trong lúc đó không scale được, pod đang chạy vẫn sống.
  - Cả 2 node system bị thu hồi cùng lúc: API ngừng nhận traffic (interceptor chạy ở đó) cho đến khi ASG thay node.
- Giảm thiểu: queue interruption của Karpenter, add-on 2 replica rải AZ, `terminationGracePeriod`, PDB.
- Vì API và FE tối đa 2 pod nên khi chỉ còn 1 pod, node spot của pod đó bị thu hồi sẽ gián đoạn ngắn. Đã chấp nhận cho demo. Với `min: 1`, PDB dùng `maxUnavailable: 1` (không dùng `minAvailable: 1`, vì sẽ chặn việc drain node); chart chỉ bật PDB `minAvailable: 1` khi replica tối thiểu ≥ 2.

### 3.4 Ba tầng scale và API scale-to-zero

| Tầng | Công cụ | Áp dụng | Tín hiệu |
|---|---|---|---|
| 1 | HPA | FE (và API khi KEDA tạo HPA bên dưới) | CPU 60% / số request đồng thời |
| 2 | KEDA | Worker (SQS), API (HTTP Add-on) | Độ dài queue; request đang chờ |
| 3 | Karpenter | Node | Pod Pending, consolidation khi trống |

Mỗi workload chỉ có **một** bộ điều khiển scale (HPA hoặc KEDA), vì KEDA đã tạo HPA bên dưới.

**API về 0:** HPA không scale về 0 được, và ALB trả 503 khi target group rỗng. KEDA HTTP Add-on đặt một interceptor trước API để giữ request trong lúc pod khởi động rồi chuyển tiếp.

- Interceptor chạy 2 replica trên node system, định tuyến theo header Host.
- `HTTPScaledObject`: `min: 0`, `max: 2`, `scaledownPeriod` (mặc định 5 phút), `targetPendingRequests`.
- Ingress của API phải nằm cùng namespace với service interceptor. Chart `app` che chi tiết này.
- ALB `idle_timeout` tăng lên 120s, client cần timeout đủ dài.
- **Cần xác minh khi implement:** cấu hình health check của ALB cho interceptor (port, path) và khả năng tương thích target-type `ip` với service interceptor.
- HTTP Add-on còn ở trạng thái beta, phù hợp cho demo.

### 3.5 Routing và domain

- **Có hosted zone:** Route 53 alias trỏ vào ALB, ACM cert cho HTTPS (ARN truyền vào values). `external-dns` là tuỳ chọn.
- **Không có domain (mặc định):** truy cập bằng DNS của ALB, phân biệt app bằng header Host (`curl -H "Host: api.demo.local"`). Hai app không thể cùng ở `/` nếu không có host khác nhau.

### 3.6 Placeholder và cold start

PriorityClass `-10` (`preemptionPolicy: Never`) cùng Deployment `pause` xin sẵn CPU/RAM. Pod thật xuất hiện thì scheduler đuổi placeholder, pod chạy ngay trên node đã có. Placeholder chuyển Pending nên Karpenter tạo node mới bù lại.

- Pod amd64 không đuổi được placeholder nằm trên node arm64 (và ngược lại), nên có **`placeholder-amd64` và `placeholder-arm64`** riêng. Chi phí cố định tăng gấp đôi so với một Deployment.
- Biến `placeholder.amd64.replicas` và `placeholder.arm64.replicas` cho phép đặt một bên về 0. Ví dụ ép app multi-arch chạy arm64 (rẻ hơn) và chỉ giữ placeholder arm64.

| Tình huống | Request đầu tiên chờ (ước lượng, sẽ đo thật) |
|---|---|
| API ở 0, có placeholder đúng kiến trúc | vài giây |
| API ở 0, không placeholder (phải tạo node spot) | khoảng 60-90 giây |

### 3.7 Database: Aurora Serverless v2 (PostgreSQL)

- **Cấu hình:** một cluster `aurora-postgresql`, một instance writer `db.serverless`, không có reader (tiết kiệm). `serverless_v2_scaling_configuration`: `min_capacity = 0`, `max_capacity = 2`, `seconds_until_auto_pause = 300`. Nhờ đó DB **tự tạm dừng về 0 ACU** khi không có kết nối, cùng triết lý scale-to-zero với API và worker. Chỉ còn tính phí lưu trữ khi đang tạm dừng.
- **Điều kiện:** scale-to-zero chỉ có ở một số phiên bản engine và region. Phiên bản cụ thể cần xác minh với AWS ở Bước 3b trước khi chốt (biến `db_engine_version`).
- **Mạng:** DB subnet group dùng các subnet private. Security group chỉ cho cổng 5432 từ security group của node (`module.eks.node_security_group_id`; node Karpenter cũng dùng nhóm này nhờ tag discovery). Không public.
- **Mật khẩu:** `manage_master_user_password = true`, AWS tự tạo và quản lý secret trong Secrets Manager (có tính phí nhỏ cho mỗi secret). API đọc secret bằng IAM của Pod Identity (`secretsmanager:GetSecretValue`), không đặt mật khẩu vào values hay biến môi trường.
- **Cấu hình demo:** mã hoá bằng key mặc định `aws/rds`, backup giữ 1 ngày, `skip_final_snapshot = true`, `deletion_protection = false` để `destroy` sạch. Tắt Performance Insights và Enhanced Monitoring để giảm chi phí.
- **Ảnh hưởng tới cold start (quan trọng):**
  - Khi DB đang tạm dừng, kết nối đầu tiên sẽ đánh thức nó; thời gian resume khoảng vài chục giây (AWS ghi nhận khoảng 15 giây, cần đo thực tế). Chuỗi cold start từ trạng thái "tất cả về 0" là: interceptor giữ request → pod API khởi động → DB resume.
  - API phải đặt timeout kết nối đủ dài (≥ 30-60s) và retry khi DB đang resume. Timeout của interceptor (`requestTimeout`) và `idle_timeout` của ALB phải lớn hơn tổng chuỗi này.
  - **Connection pool phải không giữ kết nối nhàn rỗi** (`MinConns = 0`, `MaxConnIdleTime` ngắn), nếu không DB sẽ không bao giờ đủ điều kiện tạm dừng.
- **Chi phí:** tính theo ACU-giờ khi đang chạy (khoảng $0.12/ACU-giờ ở nhiều region, cần đối chiếu bảng giá của `ap-southeast-1`), cộng lưu trữ và I/O. Nếu đặt `min_capacity = 0.5` thay vì 0 thì DB không bao giờ tạm dừng và tốn tối thiểu khoảng $40/tháng. Đây là lý do mặc định là 0.
- **App demo dùng DB:** API thêm `GET /db` (chạy truy vấn đơn giản, tăng bộ đếm trong bảng `visits`, trả về độ trễ và cho biết kết nối có phải vừa đánh thức DB không). FE thêm thẻ "Database" hiển thị kết quả. Bảng được tạo tự động (`CREATE TABLE IF NOT EXISTS`) ở lần dùng đầu.

## 4. Cấu trúc repo

```
Demo-test/
├─ PLAN.md
├─ terraform/
│  ├─ main.tf        VPC (2-3 AZ, 1 NAT), EKS, system node group (spot), Karpenter + IAM
│  ├─ addons.tf      Helm: LB Controller, Karpenter, KEDA, KEDA HTTP Add-on, metrics-server
│  ├─ sqs.tf         Queue + DLQ cho worker, IAM cho KEDA, IAM (Pod Identity) cho api/worker
│  ├─ ecr.tf         3 repository: demo-api, demo-worker, demo-web
│  ├─ rds.tf         Aurora Serverless v2 (PostgreSQL), security group, secret, IAM đọc secret
│  ├─ dns.tf         Route 53 + ACM (tuỳ chọn, bật khi có domain)
│  ├─ variables.tf, outputs.tf, versions.tf
├─ charts/
│  ├─ app/            Chart generic (mục 5)
│  └─ cluster-config/ EC2NodeClass, NodePool (default, worker), PriorityClass, placeholder x2
├─ src/                      # app demo tự viết (mục 11, Bước 2b)
│  ├─ api/                   Go: main.go, Dockerfile
│  ├─ worker/                Go: main.go, Dockerfile
│  └─ web/                   React + Vite + TS, Dockerfile (build -> nginx)
├─ apps/
│  ├─ api.values.yaml     scaling.mode: http
│  ├─ fe.values.yaml      scaling.mode: hpa
│  └─ worker.values.yaml  scaling.mode: sqs
├─ scripts/
│  ├─ up.sh / down.sh     dựng và huỷ toàn bộ
│  ├─ detect-arch.sh      đọc manifest image, in ra giá trị arch
│  ├─ test-plug-image.sh  đổi image, kiểm tra chạy được
│  ├─ test-scale.sh       tải cho HPA, đẩy message SQS
│  └─ test-cold-start.sh  đo thời gian từ 0, có và không có placeholder
└─ README.md             Cách cắm image mới, cách chạy kiểm chứng, cách huỷ
```

## 5. Chart `app` (cốt lõi)

Templates: Deployment, Service, Ingress (ALB), PDB, và một trong ba chế độ scale.

```yaml
image: {repository, tag}
arch: auto                      # auto | amd64 | arm64
containerPort: 80
probe: {httpPath: ""}           # rỗng = TCP
env: {}
scaling:
  mode: hpa | http | sqs
  hpa:  {min: 1, max: 2, cpu: 60}                 # FE
  http: {min: 0, max: 2, scaledownPeriod: 300, targetPendingRequests: 10}   # API
  sqs:  {queueURL, queueLength: 5, min: 0, max: 20}
nodePool: default | worker
ingress: {host, path, groupName, certificateArn}
```

Mọi app dùng chung ALB nhờ `group.name`. Có `topologySpread` theo AZ.

## 6. Kịch bản kiểm chứng (nghiệm thu)

| # | Kiểm tra | Mong đợi |
|---|---|---|
| 1 | Deploy image API mẫu và FE mẫu | Truy cập được qua ALB |
| 2 | Đổi sang image khác (khác port, không có `/healthz`) | Chạy chỉ sau khi sửa values |
| 3 | Deploy image chỉ có amd64 | Lên đúng node amd64, không `exec format error` |
| 4 | Deploy image multi-arch với `arch: auto` | Karpenter chọn node rẻ nhất, chạy bình thường |
| 4b | So sánh image demo với image ngoài (nginx, echo server) | Cả hai loại đều chạy bằng cùng chart, chỉ khác values |
| 5 | API idle sau cooldown | Số pod API = 0 |
| 6 | Gửi 1 request khi API ở 0 | Nhận 200 (không phải 503), ghi lại thời gian |
| 7 | Lặp lại bước 6 khi tắt placeholder | Chậm hơn rõ rệt, ghi số liệu so sánh (theo từng kiến trúc) |
| 8 | Bắn tải vào API và FE | Pod tăng 1 → 2 rồi dừng (không vượt 2 dù tải tăng thêm); node mới được tạo khi cần |
| 9 | Đẩy 500 message vào SQS | Worker tăng, queue về 0, worker về 0 |
| 10 | Kill một node spot đang chạy API/FE | FE không gián đoạn kéo dài, node được thay |
| 11 | Hết tải | Pod và node giảm về mức tối thiểu |
| 11b | Để DB không có kết nối quá 5 phút | Metric `ServerlessDatabaseCapacity` về 0 (DB tạm dừng) |
| 11c | Để API về 0 và DB tạm dừng, rồi gọi `GET /db` qua ALB | Nhận 200 sau khi chờ toàn chuỗi (pod + DB resume); ghi lại thời gian; không bị 502/504 |
| 12 | `down.sh` | Không còn tài nguyên phát sinh phí |

## 7. Chi phí và an toàn

- Chi phí ước lượng khi chạy: EKS control plane ~$0.10/giờ, NAT gateway, ALB, 2 node system spot, 2 node placeholder (mỗi kiến trúc một). Khoảng $4-8/ngày. Cần `down.sh` để huỷ ngay sau demo.
- Aurora Serverless v2: tính theo ACU-giờ khi chạy; khi tạm dừng về 0 ACU chỉ còn phí lưu trữ. `down.sh` phải xoá cả cluster Aurora.
- Giới hạn tổng CPU của từng NodePool để tránh phát sinh ngoài ý muốn.
- Kiểm tra quota vCPU spot của tài khoản trước khi chạy.
- Ở máy dev chỉ validate được bằng `terraform validate`, `helm lint`, `helm template`. Apply lên AWS và chạy kịch bản kiểm chứng cần credentials của người dùng. Các số liệu cold start ở trên là ước lượng, chưa phải số đo.

## 8. Lộ trình (khoảng 2-3 ngày)

| Bước | Nội dung | Ước lượng |
|---|---|---|
| 1 | Chart `app` (3 chế độ scale, tham số `arch`), `helm lint`/`template`, `detect-arch.sh` | 0.75 ngày |
| 2 | Terraform: VPC, EKS, system node group spot, Karpenter | 1 ngày |
| 3 | Add-on: LB Controller, KEDA, HTTP Add-on, metrics-server | 0.5 ngày |
| 4 | `cluster-config`: NodePool, placeholder kép; SQS + IAM | 0.5 ngày |
| 5 | Values mẫu, script kiểm chứng, README, rà soát các điểm cần xác minh (3.4) | 0.5 ngày |

## 9. Rủi ro chính

1. **KEDA HTTP Add-on (beta) với ALB:** các chi tiết ở 3.4 có thể cần chỉnh khi thực tế khác kỳ vọng.
2. **Toàn spot:** node chứa Karpenter hoặc interceptor bị thu hồi gây gián đoạn ngắn; spot hết hàng làm pod Pending.
3. **Đa kiến trúc:** image chỉ có amd64 mà không được ép `arch` sẽ lỗi. Script `detect-arch.sh` giảm rủi ro nhưng không đọc được manifest của registry riêng chưa đăng nhập.
4. **Image lạ:** image có hành vi đặc biệt (port động, yêu cầu quyền cao) cần chỉnh values thủ công. Chart cố ý giữ cấu hình tối thiểu để không làm hỏng image.
5. **Không có domain:** demo nhiều app cùng lúc phải giả lập header Host.
6. **Aurora scale-to-zero:** chỉ có ở một số phiên bản engine và region (cần xác minh). Kết nối đầu tiên vào DB đang tạm dừng mất vài chục giây, làm cold start dài hơn đáng kể; connection pool giữ kết nối sẽ chặn DB tạm dừng.

## 10. Việc cần chốt trước khi bắt đầu

- Cho phép bắt đầu implement.
- Xác nhận các giả định ở mục 1 (region, domain, image mẫu, FE có về 0 không, số replica placeholder cho từng kiến trúc).

## 11. Các bước thực hiện chi tiết

Quy ước: mỗi bước có **Việc làm**, **Kết quả (file)** và **Kiểm tra xong** (điều kiện để sang bước tiếp). Bước `[local]` chạy được ở máy dev; bước `[AWS]` cần credentials và có phát sinh chi phí, do người dùng chạy.

### Bước 0 - Chuẩn bị `[local]`
- Chốt các giả định ở mục 1.
- Kiểm tra công cụ: `terraform`, `helm`, `kubectl`, `aws`, `docker` (đã có), cài thêm `crane` (tuỳ chọn, cho `detect-arch.sh`).
- Tạo khung thư mục theo mục 4 và `.gitignore` (state Terraform, `.terraform/`, `*.tfvars`).
- **Kiểm tra xong:** cấu trúc thư mục đúng, `git status` sạch trừ file mới.

### Bước 1 - Chart `app` `[local]`
1. `Chart.yaml`, `values.yaml` theo mục 5, `_helpers.tpl`.
2. `deployment.yaml`: image, port, `command`/`args`, `env`/`envFrom`, probe TCP/HTTP, `topologySpread`, `terminationGracePeriod`, `nodeSelector` theo `arch` và `nodePool` (kèm toleration cho `worker`).
3. `service.yaml`, `pdb.yaml`.
4. `ingress.yaml`: annotation ALB, `group.name`, `target-type: ip`, healthcheck, HTTP/HTTPS theo `certificateArn`. Với `scaling.mode: http`, trỏ backend vào service interceptor (xem Bước 6).
5. Ba chế độ scale: `hpa.yaml`, `httpscaledobject.yaml`, `scaledobject-sqs.yaml` (+ `TriggerAuthentication`). Chỉ render một chế độ theo `scaling.mode`, và bỏ `replicas` cứng khi có scaler.
- **Kiểm tra xong:** `helm lint` và `helm template` pass cho cả 3 chế độ với `apps/*.values.yaml`; không còn trường thừa/trống trong manifest.

### Bước 2 - Script `detect-arch.sh` `[local]`
- Đọc manifest bằng `docker buildx imagetools inspect` (fallback `crane manifest`), in `auto` / `amd64` / `arm64`; không đọc được thì in `amd64` kèm cảnh báo.
- **Kiểm tra xong:** thử với 3 image công khai: một multi-arch (nginx), một chỉ amd64, và một tag không tồn tại.

### Bước 2b - App demo: API, worker, FE React `[local]`
Mục đích: có app kiểm soát được để nhìn thấy scale, đo cold start và làm "image gốc" so sánh với image ngoài.

1. **API (Go):** cấu hình bằng biến môi trường (`PORT`, `STARTUP_DELAY`, `QUEUE_URL`, `AWS_REGION`).
   - `GET /`: trả JSON gồm tên pod, node, kiến trúc (`runtime.GOARCH`), thời điểm khởi động.
   - `GET /healthz`.
   - `GET /burn?ms=`: đốt CPU để kích HPA.
   - `POST /jobs`: gửi message vào SQS (AWS SDK v2).
   - Bật CORS; xử lý SIGTERM gọn (graceful shutdown).
2. **Worker (Go, cùng module, binary thứ hai):** long-poll SQS, xử lý giả lập (sleep có cấu hình), xoá message, log thời gian xử lý, tắt gọn khi nhận SIGTERM.
3. **FE (React + Vite + TypeScript):** một trang gồm
   - Thẻ hiển thị phản hồi từ API (pod, node, kiến trúc, thời gian phản hồi) kèm nút gọi lại.
   - Nút "Burn CPU" và nút "Send jobs" (gửi N message).
   - Cấu hình địa chỉ API lúc chạy qua `/config.json` (sinh khi container khởi động từ biến môi trường), để cùng một image dùng được ở mọi môi trường.
   - Khi chưa có domain, trình duyệt không đặt được header Host riêng. Cách xử lý: nginx trong image FE có chế độ proxy `/api/` tới service interceptor và tự gắn header Host của API. Nhờ đó vẫn đi qua interceptor và vẫn scale-to-zero được, không cần CORS. Chế độ này bật bằng biến môi trường, tắt khi có domain thật.
4. **Dockerfile:** Go build tĩnh (`CGO_ENABLED=0`), image `scratch` hoặc `distroless`; FE build nhiều giai đoạn rồi chạy bằng nginx. Build đa kiến trúc bằng `docker buildx build --platform linux/amd64,linux/arm64`.
5. `Makefile` hoặc `scripts/build-images.sh`: build, kiểm tra từng kiến trúc, push lên ECR (push chạy sau Bước 3, khi ECR đã tồn tại).
- **Kiểm tra xong:** `go vet` và `go build` pass; `npm run build` pass; chạy thử cục bộ bằng Docker (`/`, `/healthz`, `/burn`, FE gọi được API); `docker buildx imagetools inspect` (sau khi push) thấy cả amd64 và arm64. Phần `/jobs` và worker kiểm thử đầy đủ ở Bước 7 khi đã có SQS.

### Bước 2c - API và FE dùng database `[local]`
1. API: thêm `GET /db` theo 3.7, dùng `pgx` với pool không giữ kết nối nhàn rỗi, timeout kết nối và retry đủ dài. Biến môi trường: `DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USER`, `DB_PASSWORD` (chạy local) hoặc `DB_SECRET_ARN` (đọc từ Secrets Manager khi chạy trên AWS). Nếu không cấu hình DB thì `/db` trả 503 giống `/jobs`.
2. FE: thẻ "Database" (độ trễ, bộ đếm, có vừa đánh thức DB không).
3. Test: unit test cho phần DB (dùng interface giả), và chạy thử với container Postgres cục bộ.
- **Kiểm tra xong:** `go vet`, `go test` pass; chạy API với Postgres cục bộ, `GET /db` tăng bộ đếm; dừng Postgres thì `/db` trả lỗi rõ ràng, API vẫn sống; FE hiển thị đúng.

### Bước 3 - Terraform nền: VPC + EKS `[local viết, AWS apply]`
1. `versions.tf`, `variables.tf` (region, cluster name, domain tuỳ chọn, giới hạn CPU NodePool).
2. `main.tf`: VPC 2-3 AZ, 1 NAT; subnet private gắn tag `karpenter.sh/discovery`; EKS với managed node group system `SPOT`, 2 node, nhiều instance type (`t3`/`t4g`/`m5`), label cho add-on; security group node gắn tag discovery.
3. Module Karpenter: IAM (Pod Identity), SQS interruption queue.
4. `ecr.tf`: 3 repository (`demo-api`, `demo-worker`, `demo-web`) kèm lifecycle policy giữ ít image.
5. Bổ sung: VPC endpoint S3 dạng gateway (miễn phí, image layer từ ECR không đi qua NAT); tắt KMS key riêng và log group của EKS để giảm chi phí; biến `api_public_access_cidrs` và `admin_principal_arns`; disk gp3 mã hoá cho node system; `terraform.tfvars.example`.
6. `scripts/check-prereqs.sh` (chỉ đọc): kiểm tra credentials, quota spot/EIP/VPC, service-linked role của Spot, cluster trùng tên.
7. `terraform/tests/main.tftest.hcl`: test offline bằng `mock_provider`, chạy `terraform test` không cần credentials.
- **Lệch so với mục 3.2:** managed node group chỉ có một loại AMI nên nhóm system chỉ chạy amd64 (spot). Workload vẫn nhận cả hai kiến trúc từ NodePool của Karpenter.
- **Kiểm tra xong:** `terraform fmt`, `terraform validate` pass `[local]`. Sau đó `terraform apply` `[AWS]`: `kubectl get nodes` thấy 2 node system Ready ở 2 AZ.

### Bước 3b - Aurora Serverless v2 `[local viết, AWS apply]`
1. `rds.tf`: cluster + instance `db.serverless` theo 3.7, DB subnet group (subnet private), security group cho phép 5432 từ security group của node, secret do AWS quản lý.
2. Biến: `db_engine_version`, `db_min_acu` (mặc định 0), `db_max_acu` (mặc định 2), `db_seconds_until_auto_pause` (mặc định 300), `db_name`. Output: endpoint, ARN secret.
3. IAM Pod Identity cho service account của API: `secretsmanager:GetSecretValue` trên đúng ARN secret.
4. Mở rộng `terraform/tests/main.tftest.hcl` (min ACU = 0 khi mặc định, không public, chỉ cho 5432 từ node SG, `min_acu` không được lớn hơn `max_acu`) và `scripts/check-prereqs.sh` (phiên bản engine hỗ trợ scale-to-zero tại region).
- **Kiểm tra xong:** `terraform validate` và `terraform test` pass. Sau khi apply: `aws rds describe-db-clusters` cho thấy `ServerlessV2ScalingConfiguration` đúng; chạy một pod tạm với `psql` kết nối được từ trong cluster; sau 5 phút không kết nối, metric `ServerlessDatabaseCapacity` về 0.

### Bước 4 - Add-on cơ bản `[local viết, AWS apply]`
- `addons.tf`: Helm release cho AWS Load Balancer Controller (IAM Pod Identity), Karpenter, KEDA, metrics-server, KEDA HTTP Add-on. Tất cả 2 replica, rải AZ, ghim vào node system.
- `external-dns` và `dns.tf` (Route 53 + ACM) chỉ bật khi có biến `domain`.
- **Kiểm tra xong:** `terraform validate` pass; sau apply, mọi pod trong `kube-system`, `keda`, `karpenter` Running; `kubectl top nodes` có số liệu.

### Bước 5 - Chart `cluster-config` `[local viết, AWS apply]`
- `EC2NodeClass` (AL2023, subnet/SG theo discovery tag, node role).
- `NodePool default`: spot, `arch: [amd64, arm64]`, family theo mục 3.2, giới hạn CPU, consolidation.
- `NodePool worker`: như trên, thêm taint `workload=worker`.
- `PriorityClass` `-10` (`preemptionPolicy: Never`), `placeholder-amd64` và `placeholder-arm64` (replica và kích thước chỉnh được).
- **Kiểm tra xong:** `helm template` pass. Sau apply: placeholder Running, Karpenter đã tạo node cho mỗi kiến trúc, `kubectl get nodepool` Ready.

### Bước 6 - Deploy API + FE mẫu và kiểm tra cắm image `[AWS]`
0. Push image demo (Bước 2b) lên ECR bằng `scripts/build-images.sh`.
1. Chạy `detect-arch.sh` cho từng image, điền `arch` vào `apps/api.values.yaml`, `apps/fe.values.yaml` (image demo là multi-arch nên dùng `auto`; image ngoài thì theo kết quả script).
2. `helm install` FE (`scaling.mode: hpa`) và API (`scaling.mode: http`).
3. **Xác minh các điểm mở ở 3.4:** health check ALB cho interceptor, target-type `ip`, namespace của Ingress. Ghi kết quả và chỉnh chart nếu cần.
4. Chạy `test-plug-image.sh`: đổi sang image khác (khác port, không có `/healthz`, image chỉ amd64, image multi-arch).
- **Kiểm tra xong:** kịch bản 1-4 ở mục 6 đạt.

### Bước 7 - SQS + worker `[local viết, AWS apply]`
- `sqs.tf`: queue + DLQ (maxReceiveCount 3-5), IAM cho KEDA đọc độ dài queue, IAM Pod Identity cho api (gửi) và worker (nhận/xoá).
- `apps/worker.values.yaml` (`scaling.mode: sqs`, `nodePool: worker`).
- **Kiểm tra xong:** `test-scale.sh` đẩy 500 message → worker tăng, queue về 0, worker về 0 (kịch bản 9).

### Bước 8 - Kiểm chứng scale và cold start `[AWS]`
- `test-scale.sh`: bắn tải vào API (kịch bản 8).
- `test-cold-start.sh`: để API về 0, đo request đầu tiên, có và không có placeholder, theo từng kiến trúc (kịch bản 5-7). Lưu số liệu vào `docs/results.md`.
- Kill một node spot (kịch bản 10), kiểm tra hồi phục.
- **Kiểm tra xong:** kịch bản 5-11 ở mục 6 đạt; số liệu cold start thật thay thế ước lượng trong 3.6.

### Bước 9 - Hoàn thiện và dọn dẹp `[local + AWS]`
- `scripts/up.sh` và `down.sh` (down: gỡ Helm release có ALB/Ingress trước rồi mới `terraform destroy`, tránh ALB mồ côi).
- `README.md`: cách cắm image mới (3 bước), cách chạy kiểm chứng, cách huỷ, bảng xử lý sự cố.
- Cập nhật `PLAN.md` theo thực tế (các điểm đã xác minh, số liệu đo).
- **Kiểm tra xong:** chạy `down.sh` thành công (kịch bản 12), không còn tài nguyên tính phí (kiểm tra EKS, NAT, ALB, EC2, ENI trong console/CLI).

### Tổng hợp thứ tự và phụ thuộc

| Bước | Phụ thuộc | Ước lượng | Ai chạy |
|---|---|---|---|
| 0 | - | 0.25 ngày | Claude |
| 1 | 0 | 0.5 ngày | Claude |
| 2 | 0 | 0.25 ngày | Claude |
| 2b | 0 (push image cần 3) | 0.75-1 ngày | Claude viết, build thử cục bộ; người dùng push lên ECR |
| 2c | 2b | 0.5 ngày | Claude |
| 3 | 0 | 1 ngày | Claude viết, người dùng apply |
| 3b | 3 | 0.5 ngày | Claude viết, người dùng apply |
| 4 | 3 | 0.5 ngày | Claude viết, người dùng apply |
| 5 | 4 | 0.5 ngày | Claude viết, người dùng apply |
| 6 | 1, 2, 5 | 0.5 ngày | Người dùng chạy, Claude chỉnh |
| 7 | 4, 5 | 0.25 ngày | Claude viết, người dùng apply |
| 8 | 6, 7 | 0.5 ngày | Người dùng chạy, Claude phân tích |
| 9 | 8 | 0.25 ngày | Claude, người dùng chạy `down.sh` |

Bước 1-2 độc lập với hạ tầng nên làm trước và validate được ngay ở máy dev. Từ bước 3 trở đi mỗi lần `apply` lên AWS đều cần người dùng xác nhận, vì có phát sinh chi phí.
