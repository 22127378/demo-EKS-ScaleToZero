# EKS scale-to-zero demo

Một hệ thống mẫu trên AWS để chứng minh: **API Go và frontend React chạy trên EKS, toàn bộ node là spot,
tự co giãn qua 3 tầng (Karpenter, KEDA, HPA), API về 0 pod mà vẫn phản hồi dưới 3 giây, và cắm image bất kỳ vẫn chạy.**

> Cluster đang chạy **tính phí** (EKS, NAT gateway, Aurora, ALB, node spot). Dùng xong hãy chạy
> `scripts/down.sh`.

## Kiến trúc

```
Client ─► ALB ─┬─ trang FE ──► Pods FE (nginx)    HPA 1..2
               └─ /api/  ────► KEDA HTTP interceptor ─► Pods API (Go)   KEDA 0..2
                                                          │
                                       Aurora Serverless v2 (0.5..2 ACU, PostgreSQL)

Pods API ─► SQS (+DLQ) ─► KEDA ─► Pods worker (Go)   0..20, node spot riêng

Karpenter:  tạo/xoá node spot (amd64 + arm64) theo pod Pending
Headroom:   placeholder + pre-pull image giữ sẵn chỗ và image, nên pod mới lên trong 1-2 giây
```

Chi tiết thiết kế, quyết định và các đánh đổi: [PLAN.md](PLAN.md).

## Kết quả đã đo

| Yêu cầu | Kết quả |
|---|---|
| API về 0 pod, request đầu tiên | trung vị **2,06 s**; 20/20 lần dưới 2,5 s tính từ lúc kết nối xong; đầu-cuối 18/20 lần dưới 3 s (các lần còn lại mất >1 s ở kết nối mạng của máy đo) |
| 200 user cùng lúc qua ALB (FE + API + `/db`) | 85.953 request, **0 lỗi**, p95 306 ms, p99 322 ms |
| Giới hạn tối đa 2 pod cho API và FE | giữ đúng dưới tải 200 user |
| SQS: 500 message | worker 0 → 20 → 0, 500/500 xử lý, 0 vào DLQ |
| Mất node chứa pod FE duy nhất | 1,3% request lỗi trong 8 giây, tự hồi phục |
| Cắm image bất kỳ | nginx (cổng 80), echo server, image chỉ amd64 đều chạy bằng cùng chart |

Số liệu thô: [docs/results/](docs/results/) và [docs/evidence/](docs/evidence/).

**Giới hạn cần biết:** đo từ một máy, nên số đo đầu-cuối gồm cả độ trễ mạng của máy đó; mất node mới thử 1 lần và ở
dạng kill đột ngột; chưa thử mất node system (nơi chạy interceptor); Route 53 chưa làm (cần domain). API đã có **32
endpoint** (domain tuyển dụng) và đã qua test cục bộ với Postgres thật, nhưng **chưa được triển
khai lên cluster** nên các số đo ở trên là của phiên bản API cũ (5 endpoint).

## Chạy thử

Cần: `aws` (đã `aws login`), `terraform`, `kubectl`, `helm`, `docker`, `jq`.

```bash
scripts/up.sh --plan      # kiểm tra + xem kế hoạch, KHÔNG tạo gì
scripts/up.sh             # dựng hạ tầng, build/push image, deploy ứng dụng (khoảng 20-30 phút)
scripts/down.sh --dry-run # chỉ liệt kê những gì đang tồn tại
scripts/down.sh           # huỷ toàn bộ theo đúng thứ tự (xoá cả database)
```
`up.sh` in địa chỉ ALB ở cuối. ALB **chỉ nhận traffic từ IP của máy chạy script**; đổi bằng `INBOUND_CIDRS=<ip>/32`.

## Cắm image của bạn vào

Chart [charts/app](charts/app) chạy được image bất kỳ; chỉ cần đổi vài giá trị.

1. Xem image hỗ trợ kiến trúc nào: `scripts/detect-arch.sh <image:tag>` (in `auto`, `amd64` hoặc `arm64`).
2. Viết một file values, ví dụ:
   ```yaml
   image: { repository: my/image, tag: "1.0" }
   arch: auto            # kết quả bước 1
   containerPort: 8080
   scaling: { mode: hpa, hpa: { min: 1, max: 2 } }   # hoặc http (về 0 pod), sqs (theo hàng đợi)
   ingress: { host: "", path: / }
   ```
3. `helm upgrade --install myapp charts/app -n demo -f my-values.yaml`

Kiểm tra nhanh với 3 image bên ngoài: `scripts/test-plug-image.sh`.

## Cấu trúc thư mục

| Thư mục | Nội dung |
|---|---|
| `terraform/` | VPC, EKS, Karpenter, add-on, Aurora, SQS, ECR, IAM; có test offline (`terraform test`) |
| `charts/app/` | chart dùng chung cho mọi workload |
| `charts/cluster-config/` | NodePool của Karpenter, placeholder (headroom), pre-pull image |
| `apps/` | values cho api, fe, worker |
| `src/` | API và worker (Go), frontend (React + Vite + TypeScript) |
| `scripts/` | up/down, deploy, build image, kiểm tra điều kiện, các bài đo |
| `docs/` | nhật ký kết quả kiểm thử và số liệu thô |

## Kiểm thử

- **Nhật ký kết quả, từng lượt đo cold start của API scale-to-zero và các bài 200 user (tiếng Việt): [docs/TEST_LOG.md](docs/TEST_LOG.md).**
- Không cần AWS: `go test` (qua Docker), `npm run build`, `helm lint`, `terraform test`.
- Đo cold start: `scripts/test-cold-start.sh`. Đo tải: `scripts/loadtest.js` (k6).

## Lưu ý an toàn và chi phí

- Hạ tầng tính phí theo giờ; đặt **Budget cảnh báo** trong AWS Billing và nhớ chạy `scripts/down.sh`.
- Terraform lưu state **cục bộ** (`terraform/terraform.tfstate`, không đưa lên git). Đừng xoá file này khi hạ tầng còn
  chạy, nếu không `destroy` không biết cần xoá gì.
- Nên dùng một IAM user/role riêng thay vì tài khoản root, và bật MFA.
- Mật khẩu database do AWS tự tạo trong Secrets Manager, ứng dụng đọc bằng quyền riêng (EKS Pod Identity); không có
  mật khẩu nào trong mã hay giá trị cấu hình.
