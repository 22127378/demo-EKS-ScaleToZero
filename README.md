# EKS scale-to-zero demo

Một hệ thống mẫu trên AWS để chứng minh bốn điều: API Go và frontend React chạy trên EKS với toàn bộ node là spot; hệ thống
tự co giãn qua 3 tầng (Karpenter, KEDA, HPA); API về 0 pod mà vẫn phản hồi dưới 3 giây; và image bất kỳ cắm vào đều chạy được.

> Cụm chạy tính phí (EKS, NAT gateway, Aurora, ALB, node spot). Dùng xong hãy chạy
> `scripts/down.sh`.

## Kiến trúc

```
Client ─► ALB ─┬─ trang FE ──► Pods FE (nginx)    HPA 1..2
               └─ /api/  ────► KEDA HTTP interceptor ─► Pods API (Go)   KEDA 0..2
                                                          │
                                       Aurora Serverless v2 (0.5..2 ACU, PostgreSQL)

Pods API ─► SQS (+DLQ) ─► KEDA ScaledJob ─► Jobs worker (Go)   0..20, node spot riêng

Karpenter:  tạo/xoá node spot (amd64 + arm64) theo pod Pending
Headroom:   placeholder + pre-pull image giữ sẵn chỗ và image, nên pod mới lên trong 1-2 giây
```

Thiết kế, các quyết định và đánh đổi nằm trong [PLAN.md](PLAN.md).

## Kết quả đã đo

Số liệu đầy đủ, cách đo và phần giải thích nằm trong [docs/BAO_CAO.md](docs/BAO_CAO.md).

| Yêu cầu | Kết quả |
|---|---|
| API về 0 pod, request đầu tiên | 20 lượt: trung vị 2,40 s đầu cuối (17/20 dưới 3 s); bỏ thời gian mở kết nối ở máy đo thì 20/20 dưới 3 s, trung vị 2,08 s |
| Worker (KEDA với SQS) cold start dưới 3 s | chưa đạt: trung vị 10,18 s khi hỏi hàng đợi mỗi 1 giây (cấu hình demo), 0/20 lượt dưới 3 s, 40 Job dư. Hai đề xuất cải tiến (chưa duyệt, chỉ đo để tham khảo) cho khoảng 1,7 s |
| 200 user cùng lúc qua ALB | 70.111 request, 0 lỗi, tối đa 2 pod mỗi bên cho API và frontend |
| Mất một máy spot | frontend lỗi 5/151 request (khoảng 6 giây, vì chỉ có một pod), API 0/143 |
| Cắm image bất kỳ | nginx, http-echo, hello-app chạy bằng cùng chart; image API, web, worker có bản arm64 |

Số liệu thô: [docs/results/](docs/results/). Quy trình đo cho người khác lặp lại: [docs/DEMO.md](docs/DEMO.md).

Giới hạn: đo từ một máy, nên số đo đầu cuối gồm cả độ trễ mạng của máy đó; mất node mới thử một lần và ở dạng tắt đột ngột; chưa thử mất node system (nơi chạy interceptor); Route 53 chưa làm (cần domain).

## Chạy thử

Cần: `aws` (đã `aws login`), `terraform`, `kubectl`, `helm`, `docker`, `jq`.

```bash
scripts/up.sh --plan      # kiểm tra + xem kế hoạch, KHÔNG tạo gì
scripts/up.sh             # dựng hạ tầng, build/push image, deploy ứng dụng (khoảng 20-30 phút)
scripts/down.sh --dry-run # chỉ liệt kê những gì đang tồn tại
scripts/down.sh           # huỷ toàn bộ theo đúng thứ tự (xoá cả database)
```
`up.sh` in địa chỉ ALB ở cuối. ALB chỉ nhận traffic từ IP của máy chạy script; đổi bằng `INBOUND_CIDRS=<ip>/32`.

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
| `docs/` | báo cáo, quy trình đo, số liệu thô (`results/`) và mã tham chiếu chưa áp dụng (`reference/`) |

## Kiểm thử

- Báo cáo kết quả (tiếng Việt), gồm từng lượt đo cold start của API và worker, các bài 200 user và Aurora: [docs/BAO_CAO.md](docs/BAO_CAO.md).
- Không cần AWS: `go test` (qua Docker), `npm run build`, `helm lint`, `terraform test`.
- Đo cold start: `scripts/test-cold-start.sh`. Đo tải: `scripts/loadtest.js` (k6).

## Lưu ý an toàn và chi phí

- Hạ tầng tính phí theo giờ; đặt cảnh báo Budget trong AWS Billing và nhớ chạy `scripts/down.sh`.
- Terraform lưu state trong bucket S3 riêng (có khóa gốc của S3), tạo bởi `scripts/bootstrap-state.sh` và không do Terraform quản lý nên `destroy` không xóa nó. Cấu hình nằm ở `terraform/backend.hcl`.
- Workflow CI chạy trên pull request và push vào main. Workflow CD (`cd.yml`) deploy lên cụm bằng role OIDC và cần các biến `AWS_ROLE_ARN`, `INBOUND_CIDRS` của kho mã; chỉ có tác dụng khi cụm đang tồn tại.
- Nên dùng một IAM user/role riêng thay vì tài khoản root, và bật MFA.
- Mật khẩu database do AWS tự tạo trong Secrets Manager, ứng dụng đọc bằng quyền riêng (EKS Pod Identity); không có
  mật khẩu nào trong mã hay giá trị cấu hình.
