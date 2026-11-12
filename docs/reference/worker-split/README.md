# Cấu hình tách hai ScaledJob (chỉ để tham chiếu)

Thư mục này chỉ để đọc. Mã trong đây không được chạy thật và không được áp dụng lên cụm.

- Không script nào (`up.sh`, `deploy-apps.sh`, `down.sh`), không workflow CI/CD và không Helm release nào đọc thư mục `docs/reference/`.
- Chart đang dùng là `charts/app`, với một ScaledJob chỉ có trigger SQS (hệ thống trong [BAO_CAO.md](../../BAO_CAO.md)). Thư mục này không thay đổi điều đó. Đây là đề xuất B, chưa được duyệt.
- Đừng chép các file này vào `charts/` hoặc `apps/`.

## Nội dung

| File | Là gì |
|---|---|
| `scaledjob-sqs.split.yaml.tpl` | Bản thay thế cho `charts/app/templates/scaledjob-sqs.yaml` trong thiết kế tách hai ScaledJob. Chỉ dùng để so sánh với template đang chạy |
| `worker.split.values.yaml` | Các giá trị `scaling.sqsjob.split.*` mà template trên đọc, kèm chu kỳ hỏi của lần đo ngày 3/11/2026 |

## Thiết kế

Thay vì một ScaledJob giữ cả hai trigger, mã này tạo hai ScaledJob:

| ScaledJob | Trigger | Chu kỳ hỏi | Vai trò |
|---|---|---|---|
| `worker` | PostgreSQL (đếm hồ sơ `queued` trong 5 phút gần nhất) | 1 giây | đường chính |
| `worker-sqs` | SQS (độ dài hàng đợi) | 60 giây | lưới an toàn cho message mà database không biết |

So với template đang chạy, khác biệt là: vòng lặp `range` tạo một hoặc hai ScaledJob; ở dạng tách mỗi ScaledJob chỉ có một trigger nên không còn `multipleScalersCalculation`; tên của ScaledJob thứ hai có hậu tố `-sqs`; `safetyMax` cho phép đặt trần Job riêng cho `worker-sqs` (0 nghĩa là bằng `max`). Hai ScaledJob không biết nhau, nên với `max` là 20 mỗi bên, tổng số Job có thể lên 40. Khi `split.enabled` là `false`, template cho đầu ra giống hệt template đang chạy (đã so sánh bằng `helm template`).

## Trạng thái kiểm tra

- Đã kiểm tra bằng `helm lint` và `helm template`: ra đúng hai ScaledJob với trigger, chu kỳ và trần như bảng trên.
- Chưa từng được triển khai lên cụm. Lần đo ngày 3/11/2026 (`docs/results/worker/worker-cold-start-keda-postgres-split.csv`) dùng một cài đặt tạm tương đương; cài đặt đó không còn lưu. Mã trong thư mục này được viết lại từ mô tả của nó. Vì vậy số đo trong báo cáo là của cài đặt tạm, không phải của mã này.

## Cách đọc kết quả render mà không chạy thật

Lệnh dưới chỉ in YAML ra màn hình. Nó làm việc trên một bản sao tạm của chart và không gửi gì tới cụm:

```bash
tmp=$(mktemp -d) && cp -r charts/app "$tmp/app"
cp docs/reference/worker-split/scaledjob-sqs.split.yaml.tpl "$tmp/app/templates/scaledjob-sqs.yaml"
helm template worker "$tmp/app" -f apps/worker.values.yaml -f docs/reference/worker-split/worker.split.values.yaml \
  --set image.repository=example/app --set image.tag=ci \
  --set-string scaling.sqsjob.queueURL=https://sqs.ap-southeast-1.amazonaws.com/111111111111/demo-jobs \
  --set-string scaling.sqsjob.postgres.host=db.example.internal \
  --set-string scaling.sqsjob.postgres.secretARN=arn:aws:secretsmanager:ap-southeast-1:111111111111:secret:db
rm -rf "$tmp"
```

## Vì sao không dùng

Rủi ro của thiết kế này (trần Job nhân đôi, đường nhanh phụ thuộc database, lưới dự phòng chậm, vận hành phức tạp hơn) và lý do chưa áp dụng nằm ở [BAO_CAO.md](../../BAO_CAO.md) mục 6.3 và 6.4.
