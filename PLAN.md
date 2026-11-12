# Plan demo: EKS cắm image bất kỳ, toàn spot, API scale-to-zero, 200 user, cold start < 3s

> Trạng thái: đây là kế hoạch thiết kế ban đầu, giữ lại để tham khảo. Các bước đã được thực hiện và đo xong; số liệu cuối cùng nằm ở [docs/BAO_CAO.md](docs/BAO_CAO.md), nên một số con số trong tài liệu này là của các lần đo trước. Mỗi lần `apply` lên AWS cần người dùng xác nhận.

## 1. Mục tiêu và nghiệm thu

Mục tiêu: lấy một image API hoặc FE bất kỳ, chỉ sửa vài dòng values (image, port, host) rồi chạy được trên hệ thống có:

- Route 53 → ALB → EKS.
- 3 tầng scale: Karpenter (node), KEDA (SQS và HTTP), HPA (pod).
- API scale về 0 pod (yêu cầu cứng) và request đầu tiên từ trạng thái 0 phải được trả lời dưới 3 giây (yêu cầu cứng), nhờ headroom node, placeholder, pre-pull image và probe nhanh (xem 3.4, 3.6).
- Chịu được 200 user cùng lúc (xem 3.8).
- Toàn bộ node là spot, NodePool dùng cả amd64 và arm64.
- Database Aurora Serverless v2 luôn sẵn sàng (không tạm dừng).

Ràng buộc: giữ nguyên hệ thống và bộ công nghệ ban đầu: Go (API), React (FE), Route 53, ALB, EKS, Karpenter, KEDA + SQS, HPA, placeholder node. Không thay thế bằng công nghệ khác (ví dụ Lambda). Thành phần bổ sung so với yêu cầu ban đầu: Aurora Serverless v2 (do bạn yêu cầu) và KEDA HTTP Add-on (một thành phần của KEDA, cần cho API scale về 0 phía sau ALB; xem mục 10).

Ngoài phạm vi: WAF, CloudFront, multi-env, observability đầy đủ. CI/CD được thêm sau bằng GitHub Actions (xem README).

Ngân sách: chạy trên tài khoản AWS đã nâng cấp (trả phí), không ràng buộc free tier. EKS, NAT gateway, Aurora đều tính phí; dùng `down.sh` để huỷ sau mỗi lần demo.

### Các quyết định đã chốt

| Quyết định | Hệ quả |
|---|---|
| API scale-to-zero và cold start < 3s, cả hai là yêu cầu cứng | KEDA HTTP Add-on + thiết kế cold start tối ưu (3.4, 3.6). Không thể cam kết trước: phải đo ở Bước 6b (cổng quyết định); nếu p95 ≥ 3s thì tinh chỉnh tiếp trong cùng stack, và nếu vẫn không đạt thì bạn quyết định nới yêu cầu nào (3.4) |
| Aurora không cần tạm dừng | `min_capacity = 0.5`, không auto-pause; connection pool được giữ kết nối (xem 3.7) |
| Tối đa 2 pod cho API và FE | HPA 1..2; 200 user vẫn đủ nhờ API Go nhẹ (xem 3.8) |
| Ngân sách có trả phí | Không bị ràng buộc free tier |

Giả định mặc định (sửa nếu chưa đúng):

| Mục | Giá trị |
|---|---|
| Region | `ap-southeast-1` |
| Domain | Không có hosted zone. Truy cập FE bằng DNS của ALB; FE proxy `/api/` tới interceptor của API (xem 3.5) |
| Image mẫu | nginx (FE), một echo server công khai (API) |
| FE | HPA `min: 1`, `max: 2` |
| API | KEDA HTTP Add-on, `min: 0`, `max: 2` (xem 3.4) |
| Giới hạn pod | Tối đa 2 pod cho mỗi app API và FE (worker không bị giới hạn này) |
| Database | Aurora Serverless v2, engine PostgreSQL, một writer, scale 0.5..2 ACU (xem 3.7) |
| App demo | Tự viết: API + worker (Go), FE (React + Vite + TypeScript), image multi-arch, push lên ECR. Vẫn test thêm với nginx và echo server công khai |
| Worker | Về 0 qua SQS. Độ trễ khởi động của worker là best-effort, không thuộc yêu cầu 3s (xem 3.6) |
| Placeholder | Một Deployment mỗi kiến trúc (amd64, arm64); chỉnh được (xem 3.6) |

## 2. Kiến trúc

```
Client ─► Route 53 ─► ALB (1 ALB dùng chung, group.name=demo)
                       └─ app.<domain> hoặc DNS của ALB ─► Pods FE (nginx)  HPA 1..2
                                                              │ /api/  (proxy nội bộ)
                                                              ▼
              (có domain: api.<domain> ─► ALB ─►)  Interceptor (luôn chạy, 2 replica)
                                                   giữ request khi API ở 0, báo KEDA kích hoạt
                                                              ▼
                                                   Pods API  KEDA HTTP 0..2
                                                              ▼
                                                  Aurora Serverless v2 (0.5..2 ACU)

API ─► SQS (+DLQ) ─► KEDA ─► Pods worker (0..N, spot, taint workload=worker)

Karpenter ─► tạo/xoá node spot (amd64 + arm64) theo pod Pending
Headroom node + placeholder (pause, priority -10), mỗi kiến trúc một Deployment
Image pre-puller (DaemonSet) ─► image demo luôn có sẵn trên node
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
- Node group system chỉ chạy amd64 (một managed node group có một loại AMI). Các add-on đều có image multi-arch.
- Vấn đề: Karpenter chọn node rẻ nhất (thường là Graviton spot) và không biết image hỗ trợ kiến trúc nào. Image chỉ có amd64 mà lên node arm64 sẽ lỗi `exec format error`.
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
  - Cả 2 node system bị thu hồi cùng lúc: interceptor (đường vào duy nhất của API) ngừng chạy cho tới khi ASG thay node, nên API không nhận được request. Chấp nhận cho demo; kịch bản 10 đo thời gian phục hồi.
  - Thu hồi node đang chạy pod API: Karpenter drain ngay khi nhận thông báo; vì API đang có 1-2 pod nên có thể có một khoảng gián đoạn ngắn cho tới khi pod thay thế Ready.
- Giảm thiểu: queue interruption của Karpenter, add-on 2 replica rải AZ, `terminationGracePeriod`, PDB.
- PDB: với `min: 1` dùng `maxUnavailable: 1` (không dùng `minAvailable: 1`, vì sẽ chặn việc drain node); chart chỉ bật `minAvailable: 1` khi số pod tối thiểu ≥ 2.

### 3.4 Ba tầng scale và API scale-to-zero

| Tầng | Công cụ | Áp dụng | Tín hiệu |
|---|---|---|---|
| 1 | HPA | FE (1..2) | CPU 50-60% |
| 2 | KEDA | Worker (SQS), API (HTTP Add-on) | Độ dài queue; request đang chờ. Cả hai có thể về 0 |
| 3 | Karpenter | Node | Pod Pending, consolidation khi trống |

Mỗi workload chỉ có một bộ điều khiển scale (HPA hoặc KEDA), vì KEDA đã tạo HPA bên dưới.

API về 0 pod. HPA không scale về 0 được, và ALB trả 503 khi target group rỗng. KEDA HTTP Add-on đặt một interceptor luôn chạy trước API: nó giữ request trong lúc pod khởi động rồi chuyển tiếp, nên target group của ALB (trỏ vào interceptor) không bao giờ rỗng.

- Interceptor chạy 2 replica trên node system, định tuyến theo header Host.
- `HTTPScaledObject`: `min: 0`, `max: 2`, `scaledownPeriod` mặc định 300s.
- Ingress của API phải nằm cùng namespace với service interceptor. Chart `app` che chi tiết này.
- ALB `idle_timeout` 120s; `requestTimeout` của interceptor đủ dài.
- Cần xác minh khi implement: health check của ALB cho interceptor (port, path), tương thích target-type `ip`, tên và tham số của `HTTPScaledObject` theo phiên bản add-on đã cài (đặc biệt tham số độ trễ kích hoạt).
- HTTP Add-on còn ở trạng thái beta.

Vì sao khó đạt dưới 3s từ 0, và cách rút ngắn. Từ lúc request tới interceptor đến lúc có phản hồi, chuỗi thời gian là (ngân sách mục tiêu, chưa đo):

| Giai đoạn | Cách rút ngắn | Mục tiêu |
|---|---|---|
| Interceptor báo, KEDA kích hoạt 0 → 1 | Đặt chu kỳ polling/độ trễ kích hoạt ngắn nhất add-on cho phép | ≤ 1s |
| Scheduler xếp pod | Headroom node: node luôn còn chỗ trống đủ cho pod API, nên không cần đuổi pod nào (xem 3.6) | ≤ 0.2s |
| Cấp IP cho pod | VPC CNI giữ sẵn IP (`WARM_IP_TARGET` / `MINIMUM_IP_TARGET`) | ≤ 0.3s |
| Kéo image | DaemonSet pre-pull; image Go distroless ~20MB | 0s |
| Container khởi động | Go khởi động vài chục ms; `STARTUP_DELAY` mặc định 0 | ≤ 0.3s |
| Readiness | `periodSeconds: 1`, `initialDelaySeconds: 0` | ≤ 1s |
| Endpoint tới interceptor | Interceptor theo dõi EndpointSlice | ≤ 0.3s |
| Tổng | | ≈ 2-3s, biên an toàn rất mỏng |

Cổng quyết định (Bước 6b): đo thật 20 lần "API về 0 → request đầu tiên" trên cluster đã dựng.
- Nếu p95 < 3s: giữ thiết kế này.
- Nếu p95 ≥ 3s: tinh chỉnh tiếp trong cùng stack rồi đo lại. Các hướng còn lại, theo thứ tự thử: rút ngắn độ trễ kích hoạt của HTTP Add-on; tăng headroom để chắc chắn không bao giờ phải đuổi placeholder; giảm thời gian khởi động và kích thước image; điều chỉnh probe và `startupProbe`; kiểm tra thời gian cấp IP của VPC CNI.
- Nếu đã tinh chỉnh hết mà vẫn ≥ 3s: đây là giới hạn của việc khởi động pod trên Kubernetes, không phải lỗi cấu hình. Khi đó tôi không tự đổi công nghệ; bạn quyết định nới một trong hai yêu cầu: (a) API `min: 1`, mất scale-to-zero của API, hoặc (b) nâng ngưỡng cold start lên con số đo được.

Tinh chỉnh HPA cho FE: `behavior.scaleUp.stabilizationWindowSeconds: 0`, thêm tối đa 1 pod mỗi 15s (đủ vì tối đa chỉ 2 pod); `scaleDown` 300s để tránh dao động.

### 3.5 Routing và domain

- Có hosted zone: Route 53 alias trỏ vào ALB, ACM cert cho HTTPS (ARN truyền vào values). FE ở `app.<domain>`, API ở `api.<domain>` (Ingress của API trỏ vào interceptor). `external-dns` là tuỳ chọn.
- Không có domain (mặc định): truy cập FE bằng DNS của ALB. API không cần Ingress riêng: nginx trong image FE proxy `/api/` tới Service của interceptor (`UPSTREAM=http://keda-add-ons-http-interceptor-proxy.keda.svc.cluster.local:8080`; phải dùng tên đầy đủ vì resolver của nginx không dùng search domain của Kubernetes) và gắn header Host của API (`UPSTREAM_HOST`). Trình duyệt chỉ nói chuyện với một origin nên không cần CORS. Lưu lượng test vào API qua đường này đi qua 2 pod FE trước, cần tính vào kết quả.

### 3.6 Headroom node, placeholder, pre-pull và cold start

Hai nghĩa của "cold start" cần đo riêng:

1. API từ 0 pod: từ lúc request tới interceptor đến lúc nhận phản hồi 200, kể cả khi gọi `/db`. Mục tiêu p95 < 3s (xem ngân sách ở 3.4; Aurora luôn chạy nên không cộng thêm thời gian đánh thức).
2. Pod mới khi đang có tải (HPA thêm pod FE, pod thay thế khi spot bị thu hồi): pod Ready < 3s. ALB chỉ gửi traffic sau khi pod qua health check (`interval 5s × healthy threshold 2` ≈ 10s); khoảng này không tính vào 3s vì pod cũ vẫn phục vụ, nhưng được ghi riêng khi đo. (API đi qua interceptor nên không chịu độ trễ này.)

Ba lớp giữ chỗ cho pod mới:

- Headroom node (đường nhanh). Node do Karpenter tạo luôn còn chỗ trống chưa ai xin: đủ cho 2 pod API và 1 pod FE. Scheduler xếp pod vào chỗ trống trong vài chục ms, không phải đuổi ai.
- Placeholder (giữ node sống). PriorityClass `-10` (`preemptionPolicy: Never`) cùng Deployment `pause` xin sẵn CPU/RAM, kèm annotation `karpenter.sh/do-not-disrupt` để Karpenter không dọn node nhàn rỗi. Khi phần chỗ trống bị dùng hết, pod thật đuổi placeholder (`terminationGracePeriodSeconds: 0`, ≤ 1s), placeholder chuyển Pending và Karpenter tạo node mới bù lại.
- Hết cả hai: phải tạo node spot mới, khoảng 60-90s, không đạt 3s. Đây là kịch bản dùng để chứng minh giá trị của headroom/placeholder.

Pod amd64 không dùng được chỗ trống trên node arm64 (và ngược lại), nên có `placeholder-amd64` và `placeholder-arm64` riêng. Biến `placeholder.amd64.replicas` và `placeholder.arm64.replicas` cho phép đặt một bên về 0.

Pre-pull image: DaemonSet `image-prepuller` chạy trên mọi node (kể cả node placeholder), kéo trước image demo bằng init container. Node mới của Karpenter cũng có sẵn image trước khi pod thật tới. Danh sách image là tham số, thêm image khác khi cắm vào.

Worker (SQS): về 0, độ trễ từ lúc có message đến lúc xử lý gồm: KEDA polling (đặt 5s) + khởi động pod. Ước lượng 4-6s, best-effort, không nằm trong yêu cầu 3s. Cần đạt 3s cho worker thì đặt `min: 1`.

### 3.7 Database: Aurora Serverless v2 (PostgreSQL)

- Cấu hình: một cluster `aurora-postgresql`, một instance writer `db.serverless`, không có reader. `serverless_v2_scaling_configuration`: `min_capacity = 0.5`, `max_capacity = 2`. Không auto-pause, nên kết nối luôn đến được ngay, không có độ trễ đánh thức.
- Mạng: DB subnet group dùng các subnet private. Security group chỉ cho cổng 5432 từ security group của node (`module.eks.node_security_group_id`; node Karpenter cũng dùng nhóm này nhờ tag discovery). Không public.
- Mật khẩu: `manage_master_user_password = true`, AWS tự tạo và quản lý secret trong Secrets Manager. API đọc secret bằng IAM của Pod Identity (`secretsmanager:GetSecretValue`), không đặt mật khẩu vào values hay biến môi trường.
- Cấu hình demo: mã hoá bằng key mặc định `aws/rds`, backup giữ 1 ngày, `skip_final_snapshot = true`, `deletion_protection = false` để `destroy` sạch. Tắt Performance Insights và Enhanced Monitoring.
- Connection pool của API: giữ sẵn kết nối (`MinConns = 1-2`, `MaxConns` vừa phải). `MaxConns` của tất cả pod cộng lại phải nhỏ hơn `max_connections` mà Aurora cho ở mức ACU tối đa (chưa xác minh con số này cho 2 ACU, sẽ đo ở Bước 8; tăng `db_max_acu` nếu thiếu).
- Scale lên khi tải: Aurora tăng ACU liên tục theo tải, thường trong vài giây. Kịch bản 8b kiểm tra DB không thành điểm nghẽn ở 200 user.
- Chi phí: vì không tạm dừng, DB tốn tối thiểu khoảng 0.5 ACU × số giờ chạy (khoảng $40/tháng nếu chạy liên tục, con số cần đối chiếu bảng giá của `ap-southeast-1`) cộng lưu trữ và I/O. `down.sh` xoá cả cluster sau mỗi lần demo nên chỉ tính theo giờ chạy.
- App demo dùng DB: API thêm `GET /db` (chạy truy vấn đơn giản, tăng bộ đếm trong bảng `visits`, trả về độ trễ). FE thêm thẻ "Database". Bảng được tạo tự động (`CREATE TABLE IF NOT EXISTS`) ở lần dùng đầu.

### 3.8 Chịu tải 200 user

- API Go với 2 pod xử lý được hàng trăm request mỗi giây cho endpoint nhẹ; 200 user thường tương ứng khoảng 200 RPS hoặc ít hơn. Giới hạn 2 pod vẫn đủ. Không dùng `/burn` khi đo tải này (nó cố tình đốt CPU).
- Đặt `requests.cpu` thực tế cho API và FE (ví dụ 250m) để HPA tính CPU đúng và Karpenter xếp node đúng.
- Tiêu chí nghiệm thu đề xuất (bạn có thể đổi ngưỡng): k6 với 200 VUs trong 5 phút vào FE và `GET /`, `GET /db` của API; tỉ lệ lỗi < 1%; p95 < 500ms cho endpoint nhẹ; p99 < 3s; số pod API và FE không vượt 2.
- Nếu không đạt, thứ tự điều tra: CPU pod (tăng `requests`/giới hạn), kết nối DB (tăng ACU hoặc giảm pool), hiệu năng nginx proxy ở FE.

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
│  └─ cluster-config/ EC2NodeClass, NodePool (default, worker), PriorityClass, placeholder x2, image-prepuller
├─ src/                      # app demo tự viết (mục 11, Bước 2b)
│  ├─ api/                   Go: main.go, Dockerfile
│  ├─ worker/                Go: main.go, Dockerfile
│  └─ web/                   React + Vite + TS, Dockerfile (build -> nginx)
├─ apps/
│  ├─ api.values.yaml     scaling.mode: http (0..2)
│  ├─ fe.values.yaml      scaling.mode: hpa (1..2)
│  └─ worker.values.yaml  scaling.mode: sqs
├─ scripts/
│  ├─ up.sh / down.sh     dựng và huỷ toàn bộ
│  ├─ detect-arch.sh      đọc manifest image, in ra giá trị arch
│  ├─ build-images.sh     build đa kiến trúc, push lên ECR
│  ├─ check-prereqs.sh    kiểm tra credentials, quota, service-linked role
│  ├─ test-plug-image.sh  đổi image, kiểm tra chạy được
│  ├─ test-cold-start.sh  đo thời gian pod mới Ready và request đầu tiên sau thời gian rảnh
│  └─ loadtest.js         kịch bản k6: 200 VUs (FE + API + DB)
└─ README.md             Cách cắm image mới, cách chạy kiểm chứng, cách huỷ
```

## 5. Chart `app` (cốt lõi)

Templates: Deployment, Service, Ingress (ALB), PDB, và một trong các chế độ scale.

```yaml
image: {repository, tag}
arch: auto                      # auto | amd64 | arm64
containerPort: 80
probe: {httpPath: "", periodSeconds: 1}   # rỗng = TCP; có startupProbe
env: {}
scaling:
  mode: hpa | http | sqs | none
  hpa:  {min: 1, max: 2, cpu: 50}         # FE
  http: {min: 0, max: 2, scaledownPeriod: 300, targetConcurrency: 100}   # API
  hpa.behavior: scaleUp không chờ (stabilization 0), scaleDown 300s
  sqs:  {queueURL, queueLength: 5, min: 0, max: 20, pollingInterval: 5}
nodePool: default | worker
ingress: {host, path, groupName, certificateArn}   # host bắt buộc khi mode: http
```

Mọi app dùng chung ALB nhờ `group.name`. Có `topologySpread` theo AZ. Health check ALB: `interval 5s`, `healthy threshold 2`. `targetConcurrency` của HTTP mode đặt cao (khoảng 100) vì mỗi pod Go chịu được nhiều request đồng thời và tối đa chỉ 2 pod.

## 6. Kịch bản kiểm chứng (nghiệm thu)

| # | Kiểm tra | Mong đợi |
|---|---|---|
| 1 | Deploy image API mẫu và FE mẫu | Truy cập được qua ALB |
| 2 | Đổi sang image khác (khác port, không có `/healthz`) | Chạy chỉ sau khi sửa values |
| 3 | Deploy image chỉ có amd64 | Lên đúng node amd64, không `exec format error` |
| 4 | Deploy image multi-arch với `arch: auto` | Karpenter chọn node rẻ nhất, chạy bình thường |
| 4b | So sánh image demo với image ngoài (nginx, echo server) | Cả hai loại đều chạy bằng cùng chart, chỉ khác values |
| 5 | Đợi API về 0 pod (qua `scaledownPeriod`), gửi 1 request `/` rồi `/db`; lặp 20 lần | Luôn nhận 200 (không 502/504); p95 < 3s |
| 5b | Khi API đang ở 0, gửi 200 request đồng thời | Cả 200 đều thành công, thời gian chờ lâu nhất < 3s (mục tiêu); ghi số liệu |
| 6 | Tăng tải để HPA thêm pod FE (có headroom, image đã pre-pull); đo thời gian từ lúc pod tạo đến Ready, và đến lúc ALB gửi traffic | Pod Ready < 3s (mục tiêu); ghi riêng thời gian ALB nhận pod |
| 7 | Lặp lại 5 và 6 khi tắt headroom/placeholder | Chậm hơn rõ rệt (dự kiến 60-90s); ghi số liệu so sánh theo từng kiến trúc |
| 8 | k6 200 VUs, 5 phút, vào FE và API (không dùng `/burn`) | Lỗi < 1%, p95 < 500ms, p99 < 3s; pod API và FE ≤ 2 |
| 8b | Cùng tải, gọi `/db` | Không lỗi kết nối; Aurora ACU tăng trên 0.5 rồi giảm lại; số kết nối không chạm `max_connections` |
| 9 | Đẩy 500 message vào SQS | Worker tăng 0 → N, queue về 0, worker về 0; ghi độ trễ khởi động của worker |
| 10 | Kill một node spot đang chạy API/FE; kill luôn một node system | Ghi số request lỗi và thời gian phục hồi |
| 11 | Hết tải | Pod về `min`, node dư được Karpenter dọn |
| 12 | `down.sh` | Không còn tài nguyên phát sinh phí |

## 7. Chi phí và an toàn

- Chi phí ước lượng khi chạy: EKS control plane ~$0.10/giờ, NAT gateway, ALB, 2 node system spot, node placeholder (mỗi kiến trúc một), Aurora tối thiểu 0.5 ACU. Khoảng $4-8/ngày. Cần `down.sh` để huỷ ngay sau demo.
- Aurora Serverless v2: tính theo ACU-giờ; vì không tạm dừng nên luôn có chi phí tối thiểu trong lúc chạy. `down.sh` phải xoá cả cluster Aurora.
- Giới hạn tổng CPU của từng NodePool để tránh phát sinh ngoài ý muốn.
- Kiểm tra quota vCPU spot của tài khoản trước khi chạy.
- Ở máy dev chỉ validate được bằng `terraform validate`, `terraform test`, `helm lint`, `helm template`. Apply lên AWS và chạy kịch bản kiểm chứng cần credentials của người dùng. Các số liệu cold start và tải ở trên là ước lượng/mục tiêu, chưa phải số đo.

## 8. Lộ trình

Chi tiết từng bước ở mục 11. Tổng khoảng 7 ngày công, trong đó phần cần AWS (apply, kiểm chứng, đo) khoảng 2,5 ngày.

## 9. Rủi ro chính

1. Yêu cầu "scale-to-zero và dưới 3s" không thể cam kết trước: ngân sách ở 3.4 cộng lại khoảng 2-3s, biên rất mỏng; chỉ số đo thật ở Bước 6b mới quyết định. Nếu đo ra không đạt thì bạn phải chọn nới yêu cầu (3.4); không có phương án thay thế công nghệ.
2. KEDA HTTP Add-on (beta) với ALB: các chi tiết ở 3.4 có thể cần chỉnh; interceptor là điểm nằm trên đường dữ liệu. Toàn spot nên nếu cả 2 node system bị thu hồi thì API ngừng nhận request cho tới khi ASG thay node.
3. Đa kiến trúc: image chỉ có amd64 mà không được ép `arch` sẽ lỗi. Script `detect-arch.sh` giảm rủi ro nhưng không đọc được manifest của registry riêng chưa đăng nhập.
4. Image lạ: image có hành vi đặc biệt (port động, yêu cầu quyền cao) cần chỉnh values thủ công. Chart cố ý giữ cấu hình tối thiểu để không làm hỏng image.
5. Không có domain: API chỉ truy cập được qua proxy của FE; đo tải vào API đi qua cả nginx của FE.
6. Aurora 2 ACU có thể không đủ ở 200 user: giới hạn kết nối và CPU của DB chưa được xác minh; điều chỉnh `db_max_acu` và kích thước pool theo kết quả kịch bản 8b.
7. Tiêu chí hiệu năng (p95 < 500ms, lỗi < 1%) là đề xuất của tôi, chưa được bạn xác nhận.

## 10. Việc cần chốt

- Xác nhận dùng KEDA HTTP Add-on cho API scale-to-zero. Đây là thành phần thêm so với bộ công nghệ ban đầu (KEDA + SQS); nếu không chấp nhận thì API không thể về 0 phía sau ALB (HPA không scale về 0, ALB trả 503 khi không có target).
- Nếu Bước 6b đo p95 ≥ 3s sau khi tinh chỉnh: bạn muốn nới yêu cầu nào (3.4).
- Xác nhận worker không thuộc yêu cầu 3s (xem 3.6).
- Xác nhận các ngưỡng hiệu năng ở 3.8.
- Region, domain, image mẫu ở mục 1.

## 11. Các bước thực hiện chi tiết

Quy ước: mỗi bước có Việc làm, Kết quả (file) và Kiểm tra xong (điều kiện để sang bước tiếp). Bước `[local]` chạy được ở máy dev; bước `[AWS]` cần credentials và có phát sinh chi phí, do người dùng chạy.

### Bước 0 - Chuẩn bị `[local]` (đã xong)
- Kiểm tra công cụ: `terraform`, `helm`, `kubectl`, `aws`, `docker`.
- Tạo khung thư mục theo mục 4 và `.gitignore`.
- Kiểm tra xong: cấu trúc thư mục đúng.

### Bước 1 - Chart `app` `[local]` (đã xong)
1. `Chart.yaml`, `values.yaml`, `_helpers.tpl`.
2. `deployment.yaml`: image, port, `command`/`args`, `env`/`envFrom`, probe TCP/HTTP, `topologySpread`, `terminationGracePeriod`, `nodeSelector` theo `arch` và `nodePool`, `POD_NAME`/`NODE_NAME` qua downward API.
3. `service.yaml`, `pdb.yaml`, `ingress.yaml`.
4. Các chế độ scale: `hpa.yaml`, `httpscaledobject.yaml` (tuỳ chọn, không dùng), `scaledobject-sqs.yaml`.
- Kiểm tra xong: `helm lint` và `helm template` pass.

### Bước 1b - Tinh chỉnh chart cho cold start và tải `[local]` (đã xong)
1. Probe: `readinessProbe.periodSeconds: 1` và thêm `startupProbe` (`periodSeconds: 1`, `failureThreshold: 30`) để pod Ready nhanh nhưng không bị giết khi khởi động chậm.
2. HPA: thêm `behavior` (scaleUp không chờ, +1 pod mỗi 15s; scaleDown 300s), mục tiêu CPU mặc định 50%.
3. Ingress: annotation health check `interval 5s`, `healthy-threshold 2`, `unhealthy-threshold 2`; giữ yêu cầu `ingress.host` cho chế độ `http`.
4. KEDA SQS: `pollingInterval` mặc định 5s.
5. Đặt `requests.cpu` hợp lý trong `apps/api.values.yaml` và `apps/fe.values.yaml`; `apps/api.values.yaml` dùng `scaling.mode: http` (0..2), `targetConcurrency` cao, timeout của HTTPScaledObject đủ dài.
- Kiểm tra xong: `helm lint`/`template` pass; manifest có `startupProbe`, `behavior`, annotation health check mới; API (mode `http`) có HTTPScaledObject và Ingress trỏ vào interceptor.
- Kết quả: readiness probe 1 giây (trước là 5 giây) cùng `startupProbe` (tối đa 30 giây để khởi động); HPA `behavior` (tăng ngay, +1 pod mỗi 15 giây, giảm sau 300 giây), mục tiêu CPU 50%; health check ALB 5 giây, ngưỡng 2/2; KEDA SQS polling 5 giây; HTTP mode `targetConcurrency` 100; rollout `maxUnavailable: 0` để không mất pod khi cập nhật; `requests.cpu` thực tế cho API (250m) và FE (100m). API server của cluster đã xác nhận manifest của cả 3 chế độ (`--dry-run=server`), và đối chiếu với giá trị probe sai bị từ chối đúng. Chưa kiểm chứng: cấu hình health check ALB cho interceptor (cổng 9090, `/livez`), vẫn đánh dấu `TODO` đến Bước 6.

### Bước 2 - Script `detect-arch.sh` `[local]` (đã xong)
- Đọc manifest bằng `docker buildx imagetools inspect` (fallback `crane`), in `auto` / `amd64` / `arm64`; không đọc được thì in `amd64` kèm cảnh báo.
- Kiểm tra xong: thử với image multi-arch, image một kiến trúc, và tag không tồn tại.

### Bước 2b - App demo: API, worker, FE React `[local]` (đã xong)
1. API (Go): `GET /`, `GET /healthz`, `GET /burn?ms=`, `POST /jobs`, CORS, tắt gọn khi nhận SIGTERM, `STARTUP_DELAY`.
2. Worker (Go): long-poll SQS, xử lý giả lập, xoá message, tắt gọn.
3. FE (React + Vite + TypeScript): thẻ hiển thị phản hồi API, nút gọi tự động, Burn CPU, Send jobs; địa chỉ API đọc lúc chạy từ `/config.json`; nginx proxy `/api/` tới `UPSTREAM`.
4. Dockerfile đa kiến trúc; `scripts/build-images.sh`.
- Kiểm tra xong: `go vet`, `go test`, `npm run build` pass; chạy thử bằng Docker; image có cả amd64 và arm64.
- Việc còn lại theo quyết định mới: khi cắm vào cluster, `UPSTREAM` của FE trỏ vào Service của interceptor và `UPSTREAM_HOST` là host của API (xem 3.5).

### Bước 2c - API và FE dùng database `[local]` (đã xong)
1. API: thêm `GET /db` theo 3.7, dùng `pgx` với pool giữ sẵn kết nối (`MinConns = 1-2`), timeout và retry hợp lý. Biến môi trường: `DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USER`, `DB_PASSWORD` (chạy local) hoặc `DB_SECRET_ARN` (đọc từ Secrets Manager khi chạy trên AWS). Nếu không cấu hình DB thì `/db` trả 503 giống `/jobs`.
2. FE: thẻ "Database" (độ trễ, bộ đếm).
3. Test: unit test cho phần DB (dùng interface giả), và chạy thử với container Postgres cục bộ.
- Kiểm tra xong: `go vet`, `go test` pass; chạy API với Postgres cục bộ, `GET /db` tăng bộ đếm; dừng Postgres thì `/db` trả lỗi rõ ràng, API vẫn sống; FE hiển thị đúng.

### Bước 2d - Mở rộng API lên 30+ endpoint (domain tuyển dụng) `[local]` (đã viết và kiểm thử cục bộ; chưa triển khai)
Lý do: yêu cầu gốc là API Go có 20-50 endpoint; bản demo chỉ có 5 vì plan trước đó thu hẹp phạm vi mà không có bước nào bổ sung lại. Bước này sửa điều đó.
1. Domain "applicant job" (ứng viên, vị trí tuyển, hồ sơ ứng tuyển), lưu trong Aurora, khoảng 32 endpoint:
   - Hệ thống: `GET /`, `/healthz`, `/readyz`, `/version`, `/burn`, `/db`, `POST /jobs` (giữ nguyên các endpoint demo cũ).
   - Ứng viên: tạo, liệt kê (phân trang, tìm), xem, sửa, xoá, xem hồ sơ của ứng viên.
   - Vị trí tuyển: tạo, liệt kê (lọc theo phòng ban/trạng thái), xem, sửa, xoá, đóng, xem hồ sơ của vị trí.
   - Hồ sơ: nộp, liệt kê (lọc), xem, đổi trạng thái (có kiểm tra chuyển trạng thái hợp lệ), xoá, gửi sàng lọc vào SQS và xem kết quả sàng lọc.
   - Thống kê: tổng quan, theo trạng thái, vị trí nhiều hồ sơ nhất. Tìm kiếm chung. `POST /seed` sinh dữ liệu mẫu để thử tải.
2. Worker sàng lọc thật: nhận message `screen` từ SQS, ghi điểm vào Aurora (cần worker đọc secret DB: thêm một câu lệnh vào policy IAM của worker, apply Terraform lần nữa). Message demo cũ (`POST /jobs`) vẫn chạy.
3. Mã dùng chung (kết nối DB, schema, hàm sàng lọc) tách vào `src/internal/db`. Tạo bảng dưới khoá advisory của Postgres để nhiều pod khởi động cùng lúc không đụng nhau.
4. Kiểm thử: test đơn vị (kiểm tra đầu vào, chuyển trạng thái, lỗi khi không có DB) và test tích hợp với Postgres thật (CRUD đầy đủ, trùng email, khoá ngoại, xoá dây chuyền, phân trang, tìm kiếm, thống kê, seed, sàng lọc từ API đến worker).
5. Sau khi triển khai (bước riêng, cần duyệt): đo lại cold start 20 lần và tải 200 user với kịch bản có endpoint domain, để chắc việc thêm endpoint không làm xấu các số đo.
- Kiểm tra xong: `go vet`, `go test` (kể cả test tích hợp với Postgres) pass; số route `grep` ≥ 20.
- Kết quả (cục bộ): 32 route (xem `src/api/`). `go vet` và `gofmt` sạch; 26 test của API (19 test đơn vị, 7 test tích hợp với Postgres 16 thật: CRUD đầy đủ, trùng email, khoá ngoại, xoá dây chuyền, luật chuyển trạng thái, phân trang, tìm kiếm có ký tự đại diện, thống kê, seed, sàng lọc đầu-cuối, hoàn tác khi hàng đợi lỗi, nhiều kết nối khởi tạo schema cùng lúc) và 6 test của worker đều pass. `scripts/smoke-api.sh` chạy trên đúng image sẽ triển khai: 41/41 kiểm tra, 33 đường dẫn khác nhau. Kịch bản k6 mới (`WITH_DOMAIN=1`) chạy tốt cục bộ.
- Lỗi đã gặp khi viết: pgx không tự đổi số nguyên sang kiểu `text` (lỗi chỉ lộ ra ở test tích hợp, không lộ ở test đơn vị); pgx mới nhất (v5.11) đòi Go 1.25 nên ghim v5.7.6 cho khớp Go 1.24 của môi trường build.
- Chưa kiểm chứng (cần triển khai): đường sàng lọc qua SQS thật và worker thật trong cluster; cold start và tải 200 user với API mới (migration lúc khởi động có thể làm lần gọi đầu chậm hơn: cần đo lại); quyền IAM mới của worker.
- Việc triển khai tiếp theo (cần duyệt): build/push image `0.3.0`; `terraform apply` thêm quyền đọc secret DB cho worker (1 policy sửa tại chỗ) và đổi tag pre-pull; `scripts/deploy-apps.sh`; rồi `smoke-api.sh` qua ALB, sàng lọc đầu-cuối, đo lại cold start 20 lần và tải 200 user có `WITH_DOMAIN=1`.

### Bước 3 - Terraform nền: VPC + EKS `[local viết, AWS apply]` (đã viết xong)
1. `versions.tf`, `variables.tf`, `main.tf`: VPC, EKS với managed node group system `SPOT`, security group node gắn tag discovery, Karpenter IAM + SQS interruption queue.
2. `ecr.tf`: 3 repository kèm lifecycle policy.
3. Bổ sung: VPC endpoint S3 (gateway), tắt KMS key riêng và log group của EKS, `api_public_access_cidrs`, `admin_principal_arns`, disk gp3 mã hoá, `terraform.tfvars.example`.
4. `scripts/check-prereqs.sh` và `terraform/tests/main.tftest.hcl` (test offline bằng `mock_provider`).
- Lệch so với mục 3.2: nhóm system chỉ chạy amd64.
- Kiểm tra xong: `terraform fmt`, `validate`, `terraform test` pass `[local]` (đã đạt). Sau `terraform apply` `[AWS]`: `kubectl get nodes` thấy 2 node system Ready.

### Bước 3b - Aurora Serverless v2 `[local viết, AWS apply]` (đã xong)
1. `rds.tf`: cluster + instance `db.serverless` theo 3.7, DB subnet group (subnet private), security group cho phép 5432 từ security group của node, secret do AWS quản lý.
2. Biến: `db_engine_version`, `db_min_acu` (mặc định 0.5), `db_max_acu` (mặc định 2), `db_name`. Output: endpoint, ARN secret.
3. IAM Pod Identity cho service account của API: `secretsmanager:GetSecretValue` trên đúng ARN secret.
4. Mở rộng `terraform/tests/main.tftest.hcl` (min ACU ≥ 0.5 mặc định, không public, chỉ cho 5432 từ node SG, `db_min_acu` không lớn hơn `db_max_acu`).
- Kiểm tra xong: `terraform validate` và `terraform test` pass. Sau khi apply: `aws rds describe-db-clusters` cho thấy `ServerlessV2ScalingConfiguration` đúng (min 0.5, max 2); một pod tạm với `psql` kết nối được từ trong cluster.

### Bước 4 - Add-on cơ bản `[local viết, AWS apply]`
- `addons.tf`: Helm release cho AWS Load Balancer Controller (IAM Pod Identity), Karpenter, KEDA, metrics-server. KEDA HTTP Add-on. Tất cả 2 replica, rải AZ, ghim vào node system. Cấu hình VPC CNI giữ sẵn IP (`WARM_IP_TARGET`, `MINIMUM_IP_TARGET`) để pod mới nhận IP nhanh.
- `external-dns` và `dns.tf` (Route 53 + ACM) chỉ bật khi có biến `domain`.
- Đã xong (đã apply). Phiên bản đã chọn và kiểm tra: LB Controller chart 3.6.2, Karpenter 1.14.1, KEDA 2.21.0, KEDA HTTP Add-on 0.16.0, metrics-server 3.14.0. Schema `HTTPScaledObject` và tên/cổng service của interceptor đã đối chiếu với đúng phiên bản add-on, khớp với chart `app`.
- Hai lỗi đã gặp và cách sửa (ghi lại để lần sau khỏi lặp):
  1. Module Karpenter mặc định `create_pod_identity_association = false`; thiếu nó thì Karpenter dùng quyền của node và bị từ chối mọi lời gọi EC2. Đã đặt `true`.
  2. Module 20.37.2 chưa cấp 4 quyền mà Karpenter 1.14 cần (`ec2:DescribeCapacityReservations`, `ec2:DescribeInstanceStatus`, `ec2:DescribePlacementGroups`, `iam:ListInstanceProfiles`). Đã thêm qua `iam_policy_statements`, sau khi so từng action với policy chính thức của đúng phiên bản. Khi nâng phiên bản Karpenter cần so lại policy.
- Chart mặc định của HTTP Add-on đòi 7 pod × 250m CPU nên đã đặt lại replica và tài nguyên để vừa 2 node `t3.medium`.
- Kiểm tra xong: mọi pod trong `kube-system`, `keda`, `karpenter` Running; 5 Helm release `deployed`; log Karpenter không còn ERROR; `terraform plan` không còn thay đổi.

### Bước 5 - Chart `cluster-config` `[local viết, AWS apply]`
- `EC2NodeClass` (AL2023, subnet/SG theo discovery tag, node role).
- `NodePool default`: spot, `arch: [amd64, arm64]`, family theo mục 3.2, giới hạn CPU, consolidation.
- `NodePool worker`: như trên, thêm taint `workload=worker`.
- `PriorityClass` `-10` (`preemptionPolicy: Never`), `placeholder-amd64` và `placeholder-arm64`: `terminationGracePeriodSeconds: 0`, annotation `karpenter.sh/do-not-disrupt`, kích thước chọn sao cho node còn chỗ trống không thuộc placeholder đủ cho 2 pod API và 1 pod FE (headroom, xem 3.6); replica chỉnh được.
- DaemonSet `image-prepuller`: init container kéo trước các image trong danh sách (mặc định gồm image demo), container chính là `pause`.
- Đã xong (đã apply, 1 tạo mới). Kết quả đo thật (độ phân giải 1 giây; hướng dẫn kiểm tra ở `docs/DEMO.md`):
  - Karpenter tạo được node spot cho cả hai kiến trúc (`c7g.large` arm64 và `t3.medium` amd64), `Ready` sau khoảng 73 giây kể từ lúc apply.
  - Pre-puller đã kéo đủ 3 image trên cả hai kiến trúc; image arm64 chạy thật trên node arm64.
  - Headroom đúng thiết kế: còn khoảng 1280m CPU trống trên mỗi node 2 vCPU. Pod vừa phần trống lên Ready sau 0-2 giây (12 lần đo, mostly 1s), placeholder không bị đuổi.
  - Pod phải đuổi placeholder vẫn Ready sau 1 giây; Karpenter bù node mới trong khoảng 36 giây (1 lần đo, nhanh hơn ước lượng 60-90s).
  - Chưa đo: KEDA activation và interceptor (cần Bước 6), nên cold start từ 0 pod của API vẫn chưa được kiểm chứng.
- Kiểm tra xong: `helm template` pass, API server xác nhận manifest hợp lệ, `kubectl get nodepool` Ready, placeholder và pre-puller Running, image demo đã có trên node.

### Bước 6 - Deploy API + FE mẫu và kiểm tra cắm image `[AWS]` (đã xong)
0. Push image demo (Bước 2b) lên ECR bằng `scripts/build-images.sh`.
1. Chạy `detect-arch.sh` cho từng image, điền `arch` vào `apps/api.values.yaml`, `apps/fe.values.yaml` (image demo là multi-arch nên dùng `auto`; image ngoài thì theo kết quả script).
2. `helm install` API (`scaling.mode: http`) và FE (`scaling.mode: hpa`). FE đặt `UPSTREAM` trỏ vào Service của interceptor và `UPSTREAM_HOST` là host của API.
3. Xác minh ALB và interceptor (các điểm mở ở 3.4): health check ALB cho interceptor, target-type `ip`, namespace của Ingress, tên và tham số `HTTPScaledObject` theo phiên bản add-on đã cài. Ghi kết quả và chỉnh chart nếu cần.
4. Chạy `test-plug-image.sh`: đổi sang image khác (khác port, không có `/healthz`, image chỉ amd64, image multi-arch).
- Kiểm tra xong: kịch bản 1-4 ở mục 6 đạt.

- Tiến độ Bước 6 (đã deploy, chưa đóng bước):
  - Đã cài `api` (HTTP, 0..2) và `fe` (HPA 1..2) bằng `scripts/deploy-apps.sh`; ALB tạo xong, giới hạn nguồn theo IP máy chạy script; FE, `/api/` qua proxy và API trực tiếp qua header Host đều trả 200.
  - Điểm mở ở 3.4 đã giải quyết: health check ALB cho interceptor dùng `/livez` cổng 9090, cả 2 pod interceptor `healthy`; target-type `ip` hoạt động với service interceptor; schema `HTTPScaledObject` đúng phiên bản.
  - Lỗi 1 (đã sửa): `UPSTREAM` của FE phải là FQDN (resolver của nginx không dùng search domain), nếu không `/api/` trả 502.
  - Lỗi 2 (đã sửa và áp dụng): security group node chỉ cho node-tới-node qua TCP 53 và 1025-65535, nên pod nghe cổng dưới 1025 (nginx ở cổng 80) không nhận được traffic từ pod ở node khác. Đã thêm quy tắc `ingress_self_all` trong `terraform/main.tf`; `plan` chỉ thêm 1 security group rule. Cần được bạn duyệt `apply`, rồi chạy lại `scripts/test-plug-image.sh` (kết quả trước khi sửa: echo server cổng 5678 và image cổng 8080 chạy tốt, nginx cổng 80 timeout; đối chứng sai kiến trúc bị từ chối đúng).
  - Lần gọi `/api/` đầu tiên khi API ở 0 pod (1 mẫu, chưa phải phép đo chính thức): 3,79 giây, vượt mốc 3 giây. Phép đo chính thức làm ở Bước 6b.

### Bước 6b - Cổng quyết định: đo cold start từ 0 `[AWS]` (đã đo; xem kết quả cuối bước)
Làm ngay sau Bước 6 và trước khi đầu tư thêm vào các bước sau, vì đây là điểm rủi ro lớn nhất của plan.
1. Để API về 0 pod (đợi qua `scaledownPeriod`), dùng `scripts/test-cold-start.sh` gửi 1 request `/` và 1 request `/db`; lặp 20 lần, ghi thời gian phản hồi từng lần.
2. Ghi riêng từng giai đoạn nếu vượt ngân sách (thời điểm KEDA kích hoạt, thời điểm pod Scheduled / Ready, thời điểm interceptor chuyển tiếp) bằng `kubectl get events` và log của interceptor để biết đoạn nào chậm.
3. Tinh chỉnh các tham số ở 3.4 (độ trễ kích hoạt, probe, IP ấm, headroom) rồi đo lại.
- Kiểm tra xong: p95 < 3s thì tiếp tục. Ngược lại, tinh chỉnh tiếp trong cùng stack và đo lại (3.4); nếu vẫn không đạt thì dừng và chờ bạn quyết định nới yêu cầu nào trước khi làm tiếp. Kịch bản 5 và 5b ở mục 6 là tiêu chí.

- Kết quả đo (20 lần mỗi cấu hình, API từ 0 pod, qua ALB, mỗi phản hồi đã kiểm tra là JSON của API; dữ liệu ở `docs/results/`):

  | Cấu hình | Trung vị | p95 | Lớn nhất | Dưới 3s | Sau khi đã kết nối TCP: lớn nhất |
  |---|---|---|---|---|---|
  | 1. startupProbe + readiness (mốc) | 2,97 s | 3,37 s | 4,82 s | 12/20 | 4,39 s |
  | 2. chỉ readiness (đã chọn) | 2,06 s | 3,01 s | 3,09 s | 18/20 | 2,40 s |
  | 3. bỏ cả hai probe | 2,13 s | 2,64 s | 3,34 s | 19/20 | 2,46 s |

  - Thay đổi tạo ra khác biệt: tắt `startupProbe` của API (kubelet chạy nó trước readiness, hai probe nối tiếp tự cộng thêm khoảng 1 giây). Pod tạo → Ready giảm từ 2-4 giây xuống 1-2 giây.
  - Bỏ nốt readiness probe (cấu hình 3) không mang lại lợi ích đo được nhưng mất lớp bảo vệ, nên đã hoàn tác. Chart vẫn có tuỳ chọn `probe.readiness.enabled`.
  - Mọi lần vượt 3 giây ở cấu hình 2 và 3 đều có thời gian kết nối TCP khoảng 1,1-1,3 giây, là độ trễ mạng của máy đo, không phải cold start: khoảng 8% kết nối từ máy này tới ALB mất hơn 1 giây, trên cả 3 địa chỉ ALB và cả với một đích AWS không liên quan. Loại phần kết nối, cả 20 lần của cấu hình 2 đều dưới 2,5 giây.
  - Kết luận trung thực: API về 0 pod phản hồi trong trung vị 2,06 giây; 18/20 lần dưới 3 giây đo đầu-cuối; 20/20 lần dưới 2,5 giây tính từ lúc kết nối xong. Yêu cầu "luôn dưới 3 giây" đạt ở phía hệ thống nhưng không thể cam kết tuyệt đối đầu-cuối vì mạng từ phía client có thể thêm hơn 1 giây (mất gói SYN) nằm ngoài hệ thống.
  - Giới hạn của phép đo: 1 địa điểm đo, độ phân giải dấu thời gian Kubernetes 1 giây, API trên node amd64 có sẵn headroom. Chưa đo đường qua FE proxy (thêm 1 hop nginx), chưa đo khi headroom đã hết (khi đó phải tạo node mới, khoảng 36 giây theo lần đo ở Bước 5), chưa đo dưới tải.

### Bước 7 - SQS + worker `[local viết, AWS apply]` (đã xong)
- `sqs.tf`: queue + DLQ (maxReceiveCount 3-5), IAM cho KEDA đọc độ dài queue, IAM Pod Identity cho api (gửi) và worker (nhận/xoá).
- `apps/worker.values.yaml` (`scaling.mode: sqs`, `nodePool: worker`).
- Kiểm tra xong: `test-scale.sh` đẩy 500 message → worker tăng, queue về 0, worker về 0 (kịch bản 9).

- Kết quả Bước 2c, 3b, 7 (đã apply: 19 tạo mới, 1 sửa; dữ liệu ở `docs/results/`):
  - Test cục bộ với Postgres thật: đếm đúng, 200 request song song không mất cập nhật (tăng đúng 201), DB chết thì `/db` trả 503 trong 59 ms còn các endpoint khác vẫn 200, DB sống lại thì tự nối lại.
  - Trên cluster: pod API đọc secret Aurora bằng Pod Identity và nối được DB. Lần `/db` đầu tiên khi API ở 0 pod mất 2,54 giây gồm cả cold start, đọc secret, nối DB và tạo bảng; các lần sau 150 ms, truy vấn DB khoảng 2 ms.
  - 200 user trên FE + API + `/db` (3 phút): 85.953 request, 0 lỗi, p95 306 ms, p99 322 ms. Aurora tăng từ 0,5 lên 2,0 ACU (chạm mức tối đa), tối đa 10 kết nối, CPU DB tới 69%: đủ cho 200 user nhưng hết dư địa; tải cao hơn cần tăng `db_max_acu`.
  - SQS + KEDA (kịch bản 9): gửi 500 message; SQS báo gửi 500, nhận 500, xoá 500, 0 vào DLQ. Worker 0 → 20 pod (đúng mức tối đa), 3 node spot; pod đầu tiên được tạo sau ≤ 16 giây, worker chạy sau ≤ 55 giây (gồm thời gian Karpenter tạo node), queue hết sau ≤ 71 giây, worker về 0 pod sau ≤ 151 giây (mốc có độ phân giải khoảng 14 giây).
  - Hai lỗi đã gặp: (1) KEDA operator ban đầu dùng quyền của node vì Pod Identity chỉ tiêm thông tin xác thực khi pod được tạo, mà operator có trước khi association tồn tại: phải khởi động lại operator sau khi tạo association; (2) chart chỉ tham chiếu ServiceAccount mà không tạo nó, đã thêm `serviceAccount.create`.
  - Chưa kiểm chứng: Aurora chỉ chịu tải ở mức 2 ACU; chưa thử tải cao hơn; chưa đo `/db` khi hàng chục pod; worker chỉ thử với xử lý giả lập (sleep 2 giây).

### Bước 8 - Kiểm chứng tải và cold start `[AWS]`
- `scripts/loadtest.js` (k6, 200 VUs): FE, `GET /`, `GET /db` của API (kịch bản 8, 8b).
- `scripts/test-cold-start.sh` (đã dùng ở Bước 6b): thêm kịch bản 5b (200 request đồng thời khi API ở 0), đo thời gian pod FE mới Ready và thời gian ALB nhận pod, có và không có headroom/placeholder, theo từng kiến trúc (kịch bản 6, 7). Số liệu lưu ở `docs/results/`.
- Kill một node spot đang chạy API/FE, ghi số request lỗi và thời gian phục hồi (kịch bản 10). Nếu có khoảng gián đoạn đáng kể thì ghi lại để cân nhắc tăng số replica add-on hoặc dùng thêm một node system.
- Kiểm tra xong: kịch bản 5-11 ở mục 6 đạt; số liệu thật thay thế ước lượng trong 3.6 và 3.8.

- Kết quả thử mất node (kịch bản 10, đã chạy 1 lần; dữ liệu ở `docs/results/node-loss-*`): tải nhẹ liên tục (khoảng 60 request/giây, FE + `/api/`), ở giây thứ 40 kết thúc đột ngột instance spot arm64 (`c7g.large`) đang chứa pod FE duy nhất (`min: 1`). Không đụng node system.
  - 11.996 request, 153 lỗi (1,3%), tất cả nằm trong cửa sổ 8,3 giây sau khi kill (FE 75 lỗi, API 77 lỗi vì API đi qua proxy của FE; API không mất pod). Từ giây +9 trở đi, 9.229 request đều thành công.
  - Diễn biến: pod FE thay thế được tạo sau 2 giây (Kubernetes xoá node ngay khi instance biến mất), Ready sau 4 giây, rồi ALB cần thêm vài giây để nhận pod mới; đó là toàn bộ cửa sổ lỗi. Pod mới được xếp ngay vào chỗ trống của node amd64 nhờ headroom.
  - Karpenter bù node: NodeClaim chỉ được tạo sau 108 giây (phát hiện chậm vì đây là kill đột ngột, không có cảnh báo spot), node Ready sau 139 giây; placeholder arm64 trở lại cùng lúc. Trong khoảng đó kiến trúc arm64 không có headroom.
  - Kết luận: với `min: 1` mất node chứa pod duy nhất gây khoảng 8 giây lỗi; đúng rủi ro đã nêu ở 3.3. Đặt FE (và API) `min: 2`, rải node khác nhau, sẽ loại bỏ cửa sổ này nhưng làm HPA của FE không còn khoảng để co giãn do giới hạn 2 pod. Quyết định thuộc về bạn.
  - Hạn chế: chỉ 1 lần đo; đây là kill đột ngột (xấu hơn thu hồi spot thật, vốn báo trước 2 phút để Karpenter drain), cách thu hồi spot thật chưa được thử; tải nhẹ dùng kết nối mới cho mỗi request nên độ trễ thành công cao hơn (p95 khoảng 1,1 giây) so với k6; chưa thử mất node system (nơi chạy interceptor).
- Kết quả đo tải 200 user (đã chạy, dữ liệu ở `docs/results/load-200vus-5m-*`): k6, 200 user, 5 phút, từ một máy qua ALB công khai (trang FE và `/api/` qua proxy FE, interceptor, API). API bắt đầu ở 0 pod.
  - 102.188 request, 0 lỗi. Độ trễ: trung vị 69ms, p95 314ms, p99 327ms, lớn nhất 2,17s. Đạt cả ba ngưỡng đã đề xuất (lỗi < 1%, p95 < 500ms, p99 < 3s); FE p95 102ms, API p95 316ms.
  - Số pod không vượt giới hạn: API lên 2 pod ngay từ đầu (200 user chia cho `targetConcurrency` 100), FE từ 1 lên 2 nhờ HPA sau khoảng 90 giây; không phải tạo thêm node nào. CPU thực tế thấp (FE ~65m, API ~46m cả hai pod).
  - Lần chậm nhất 2,17s nhiều khả năng là đợt đầu khi API còn ở 0 pod (kịch bản 5b), nhưng chưa tách riêng để xác nhận. Có một nhóm khoảng 5% request chậm quanh 315ms ở đường API (không có ở FE), chưa tìm nguyên nhân; không ảnh hưởng ngưỡng.
  - Hạn chế: đo từ một máy, đã gồm round trip mạng của máy đó; chưa có `/db` (chưa có Aurora); chưa thử tải dài hơn hay cao hơn.

### Bước 9 - Hoàn thiện và dọn dẹp `[local + AWS]`
- `scripts/up.sh` và `down.sh` (down: gỡ Helm release có ALB/Ingress trước rồi mới `terraform destroy`, tránh ALB mồ côi).
- `README.md`: cách cắm image mới (3 bước), cách chạy kiểm chứng, cách huỷ, bảng xử lý sự cố.
- Cập nhật `PLAN.md` theo thực tế (các điểm đã xác minh, số liệu đo).
- Kiểm tra xong: chạy `down.sh` thành công (kịch bản 12), không còn tài nguyên tính phí (kiểm tra EKS, NAT, ALB, EC2, ENI, RDS trong console/CLI).

### Tổng hợp thứ tự và phụ thuộc

| Bước | Phụ thuộc | Ước lượng | Ai chạy |
|---|---|---|---|
| 0 | - | 0.25 ngày | Claude (xong) |
| 1 | 0 | 0.5 ngày | Claude (xong) |
| 1b | 1 | 0.25 ngày | Claude |
| 2 | 0 | 0.25 ngày | Claude (xong) |
| 2b | 0 (push image cần 3) | 0.75-1 ngày | Claude viết, build thử cục bộ (xong); người dùng push lên ECR |
| 2c | 2b | 0.5 ngày | Claude |
| 3 | 0 | 1 ngày | Claude viết (xong), người dùng apply |
| 3b | 3 | 0.5 ngày | Claude viết, người dùng apply |
| 4 | 3 | 0.5 ngày | Claude viết, người dùng apply |
| 5 | 4 | 0.75 ngày | Claude viết, người dùng apply |
| 6 | 1b, 2, 5 | 0.5 ngày | Người dùng chạy, Claude chỉnh |
| 6b | 6 (+3b nếu đo `/db`) | 0.5 ngày | Người dùng chạy, Claude phân tích; quyết định tiếp tục hay nới yêu cầu |
| 7 | 4, 5 | 0.25 ngày | Claude viết, người dùng apply |
| 8 | 6, 7, 3b | 0.75 ngày | Người dùng chạy, Claude phân tích |
| 9 | 8 | 0.25 ngày | Claude, người dùng chạy `down.sh` |

Bước 1b và 2c độc lập với hạ tầng nên làm trước và kiểm tra được ngay ở máy dev. Từ Bước 3 trở đi mỗi lần `apply` lên AWS đều cần người dùng xác nhận, vì có phát sinh chi phí.
