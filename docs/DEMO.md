# Quy trình triển khai và kiểm thử hệ thống tuyển dụng co giãn tự động trên Amazon EKS

Tài liệu dành cho nhóm thực hiện lại hệ thống và các bài kiểm thử. Kết quả tham chiếu nằm trong [BAO_CAO.md](BAO_CAO.md). Lệnh giữ nguyên tiếng Anh.

## 1. Mục đích và cách đọc tài liệu

Tài liệu mô tả cách dựng hệ thống từ đầu, kiểm thử từng yêu cầu, ghi nhận số liệu và dọn dẹp, để một nhóm không tham gia phát triển có thể lặp lại và so sánh kết quả với báo cáo.

Mỗi bài kiểm thử có sáu mục: mục tiêu, điều kiện trước, các bước, kết quả mong đợi, dữ liệu cần ghi, lưu ý và dọn dẹp. Giá trị tham chiếu lấy từ báo cáo. Đó là số đo của một máy đo, một ngày, 20 lượt mỗi chuỗi, nên kết quả của nhóm có thể lệch. Tiêu chí đạt của từng bài là yêu cầu của đề tài, không phải con số tham chiếu.

| Bài | Nội dung | Thời lượng | Tác động lên hệ thống | Mức |
|---|---|---|---|---|
| K1 | Kiểm tra chức năng API và giao diện | 10 phút | ghi dữ liệu mẫu vào database | bắt buộc |
| K2 | API về 0 pod và cold start | 10 phút (một lần), 30 đến 40 phút (20 lượt) | không | bắt buộc |
| K3 | Karpenter thêm và bớt node | 5 phút | tạo node tạm | bắt buộc |
| K4 | HPA và trigger CPU của API | 15 phút | tạo namespace tạm | bắt buộc |
| K5 | KEDA ScaledJob với SQS | 5 phút | gửi 500 message | bắt buộc |
| K6 | Cold start của worker | 30 đến 40 phút | cập nhật một hồ sơ nhiều lần | bắt buộc |
| K7 | Chart Helm với image bất kỳ | 5 phút | tạo namespace tạm | bắt buộc |
| K8 | Mất một node spot | 5 phút | tắt một máy EC2 thật | bắt buộc |
| K9 | 200 user cùng lúc và Aurora | 10 phút | ghi dữ liệu thật | bắt buộc |
| K10 | Stress test Aurora | 15 phút | ghi hàng chục nghìn bản ghi | tùy chọn |
| K11 | Giám sát bằng Grafana | 10 phút | không | tùy chọn |

## 2. Điều kiện tiên quyết

### 2.1 Công cụ trên máy chạy lệnh

`aws` (CLI v2, đã `aws login`), `terraform` (cần phiên bản có khóa gốc của S3; dự án dùng 1.14.6), `kubectl`, `helm` phiên bản 4 (các script dùng cờ `--force-conflicts` của Helm 4; CI cố định 4.1.3), `docker`, `jq`, `curl`, `python3`, `watch`. Riêng `scripts/worker-cold-start.sh` cần thêm `bc`.

### 2.2 Tài khoản AWS

- Quyền tạo EKS, VPC, RDS, SQS, ECR, IAM, S3.
- Hạn mức đủ cho cụm: khoảng 32 vCPU spot, 1 Elastic IP, 1 VPC. `scripts/check-prereqs.sh` kiểm tra các mục này và việc có sẵn vai trò dịch vụ của EC2 Spot.
- Vùng triển khai là `ap-southeast-1`. Vùng mặc định của AWS CLI trên máy có thể khác (ví dụ `us-east-1`), nên mọi lệnh AWS trong tài liệu này giả định đã đặt `AWS_DEFAULT_REGION` ở mục 3.

### 2.3 Mạng

ALB chỉ nhận địa chỉ trong biến `INBOUND_CIDRS`. Mặc định `scripts/deploy-apps.sh` lấy IP công cộng của máy chạy script và dùng dạng `<ip>/32`. Máy đo phải nằm trong danh sách này, nếu không `curl` tới ALB báo `http=000` hoặc hết thời gian chờ. Nếu máy ra internet bằng nhiều IP khác nhau (ví dụ qua một dịch vụ proxy), đặt cả hai: `INBOUND_CIDRS=ip1/32,ip2/32`.

### 2.4 Chi phí và thời gian

Cụm tính tiền theo giờ khi chạy: phí điều khiển EKS, NAT gateway, ALB, Aurora tối thiểu 0,5 ACU, 2 node hệ thống và các node dự phòng. Dựng mất khoảng 25 đến 40 phút, hủy mất khoảng 15 đến 25 phút. Hủy cụm ngay sau khi kiểm thử xong (mục 8).

## 3. Chuẩn bị môi trường làm việc

Chạy các lệnh sau ở mọi cửa sổ terminal dùng cho kiểm thử, tại thư mục gốc của dự án:

```bash
export AWS_DEFAULT_REGION=ap-southeast-1
export AWS_REGION=ap-southeast-1
aws login                      # chỉ khi phiên AWS hết hạn
aws eks update-kubeconfig --region ap-southeast-1 --name demo --kubeconfig /tmp/demo.kubeconfig
export KUBECONFIG=/tmp/demo.kubeconfig
export ALB=$(kubectl -n demo get ingress fe -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')
export Q=$(aws sqs get-queue-url --queue-name demo-jobs --query QueueUrl --output text)
export DLQ=$(aws sqs get-queue-url --queue-name demo-jobs-dlq --query QueueUrl --output text)
export TAG=0.5.2               # tag image đã deploy (mặc định của scripts/deploy-apps.sh)
```

- `aws eks update-kubeconfig ... --kubeconfig`: ghi cấu hình kết nối cụm `demo` vào file riêng, không đụng `~/.kube/config`. Địa chỉ cụm đổi sau mỗi lần dựng lại, nên chạy lại lệnh này.
- `ALB`: địa chỉ DNS của ALB, đổi sau mỗi lần dựng lại.
- `Q`, `DLQ`: địa chỉ hàng đợi chính và hàng đợi lỗi.

Ba cửa sổ quan sát nên chạy suốt thời gian kiểm thử:

| Cửa sổ | Lệnh | Cho biết |
|---|---|---|
| O1 (pod) | `watch -n 2 'kubectl -n demo get pods,hpa,scaledjob -o wide'` | pod ứng dụng, HPA, ScaledJob và node mà mỗi pod chạy |
| O2 (node) | `watch -n 3 'kubectl get nodes -L karpenter.sh/nodepool,kubernetes.io/arch,karpenter.sh/capacity-type; echo; kubectl get nodeclaim'` | node, pool Karpenter, kiến trúc, kiểu thuê, các NodeClaim. Node không có pool là node hệ thống |
| O3 (SQS) | `watch -n 3 "aws sqs get-queue-attributes --queue-url $Q --attribute-names ApproximateNumberOfMessages ApproximateNumberOfMessagesNotVisible --query Attributes --output text"` | message đang chờ và đang được xử lý |

## 4. Dựng hệ thống

### 4.1 Bucket lưu state Terraform

State Terraform lưu trong S3. Bucket nằm ngoài Terraform để `destroy` không xóa nó.

```bash
scripts/bootstrap-state.sh          # tạo bucket demo-tfstate-<mã tài khoản>, bật version, mã hóa, chặn truy cập công khai
```

Mở `terraform/backend.hcl` và sửa `bucket` thành `demo-tfstate-<mã tài khoản của bạn>`. File trong kho mã trỏ tới bucket của tài khoản đã dựng ban đầu.

### 4.2 Giá trị cục bộ

```bash
cp terraform/terraform.tfvars.example terraform/terraform.tfvars    # file này không đưa lên git
```

- Đặt `create_spot_service_linked_role = true` nếu `scripts/check-prereqs.sh` báo chưa có vai trò dịch vụ của EC2 Spot.
- Giữ `prepull_images = []` ở lần dựng đầu. Khi ECR còn trống mà danh sách này khác rỗng, bước cài `cluster-config` của Terraform hết thời gian chờ vì không có image để tải sẵn (mục 4.5).

### 4.3 Kiểm tra điều kiện và xem kế hoạch

```bash
scripts/check-prereqs.sh            # chỉ đọc; báo hạn mức, vai trò Spot, cụm trùng tên
scripts/up.sh --plan                # chạy terraform plan rồi dừng, không tạo gì
```

Đọc kế hoạch: số tài nguyên cần tạo phải khớp mô tả ở báo cáo (mục 3). Kết quả `check-prereqs.sh` phải không có dòng `[FAIL]`.

### 4.4 Dựng

```bash
scripts/up.sh                       # gõ "apply" để xác nhận
```

Script chạy năm bước: kiểm tra điều kiện, `terraform plan`, `terraform apply` (15 đến 25 phút), build và đẩy image `demo-api`, `demo-worker`, `demo-web` lên ECR (tag `TAG`, hai kiến trúc), rồi deploy ba ứng dụng bằng Helm. Cuối cùng in địa chỉ frontend. Để theo dõi: `kubectl -n demo get pods -w` ở một cửa sổ khác sau khi bước 5 bắt đầu.

### 4.5 Bật tải sẵn image (khuyến nghị trước khi đo cold start)

Sau khi image đã nằm trong ECR, bật `image-prepuller` để mọi node Karpenter giữ sẵn ba image. Sửa `terraform/terraform.tfvars` (thay `<registry>` bằng `<mã tài khoản>.dkr.ecr.ap-southeast-1.amazonaws.com` và tag khớp `TAG`):

```hcl
prepull_images = [
  { name = "api",    image = "<registry>/demo-api:0.5.2",    command = ["/app", "-version"] },
  { name = "worker", image = "<registry>/demo-worker:0.5.2", command = ["/app", "-version"] },
  { name = "web",    image = "<registry>/demo-web:0.5.2",    command = ["nginx", "-v"] },
]
```

Terraform không đọc được thông tin đăng nhập do `aws login` tạo, nên chạy qua hàm `tf` của `scripts/_common.sh`:

```bash
source scripts/_common.sh           # nạp hàm tf; đóng shell này khi xong (nó dọn thư mục tạm lúc thoát)
tf init -input=false -backend-config=backend.hcl
tf plan -input=false -out=/tmp/tfplan
tf apply /tmp/tfplan
```

### 4.6 Kiểm tra sau khi dựng

Chờ khoảng 5 phút cho cụm ổn định (pod giữ chỗ chạy, node dự phòng sẵn sàng), rồi kiểm tra:

| Kiểm tra | Lệnh | Kết quả mong đợi |
|---|---|---|
| Node | `kubectl get nodes -L karpenter.sh/nodepool,kubernetes.io/arch` | 5 node: 2 hệ thống (không có pool), 2 thuộc pool `default` (1 amd64, 1 arm64), 1 thuộc pool `worker` |
| Pod ứng dụng | `kubectl -n demo get pods` | frontend 1 pod; API và worker 0 pod |
| Đối tượng co giãn | `kubectl -n demo get hpa,scaledjob; kubectl -n demo get httpscaledobject` | HPA của frontend, một ScaledJob `worker` ở trạng thái READY, một HTTPScaledObject của API |
| Frontend | `curl -s -o /dev/null -w '%{http_code}\n' http://$ALB/` | `200` |
| Hàng đợi | `aws sqs get-queue-attributes --queue-url $DLQ --attribute-names ApproximateNumberOfMessages --query Attributes --output text` | `0` |

Nạp dữ liệu mẫu (20 ứng viên, 5 vị trí, 40 hồ sơ); lệnh này cũng đánh thức API lần đầu:

```bash
curl -s -X POST -H "Host: api.demo.local" "http://$ALB/seed"
```

## 5. Các bài kiểm thử

### Bài K1: kiểm tra chức năng API và giao diện

Mục tiêu: xác nhận 32 endpoint và giao diện hoạt động trên cụm.

Điều kiện trước: hệ thống đã dựng, đã nạp dữ liệu mẫu. API có thể đang ở 0 pod.

Các bước:
1. Chạy bộ kiểm tra tự động:
   ```bash
   EXPECT_QUEUE=1 scripts/smoke-api.sh http://$ALB api.demo.local
   ```
   Script gọi lần lượt mọi route và kiểm tra mã trả về cùng các trường chính. Tham số thứ hai là header `Host` để ALB chuyển request tới API thay vì frontend. `EXPECT_QUEUE=1` cho script biết API có hàng đợi, nên `POST /jobs` và `POST /applications/{id}/screen` phải trả 202. Không đặt biến này, script kỳ vọng 503 và hai kiểm tra đó báo `[FAIL]` dù hệ thống đúng.
2. Mở `http://$ALB/` bằng trình duyệt (từ máy nằm trong `INBOUND_CIDRS`) và thực hiện các thao tác ở Phụ lục A.

Kết quả mong đợi: mọi dòng của script là `[ok]`, không có `[FAIL]` (khoảng 41 kiểm tra trên 33 đường dẫn). Giao diện hiển thị dữ liệu từ Aurora. Bấm Score ở một hồ sơ thì điểm xuất hiện sau khi worker chạy (thời gian phụ thuộc cấu hình KEDA, xem K6).

Dữ liệu cần ghi: toàn bộ đầu ra của script, số kiểm tra đạt trên tổng.

Lưu ý: script và nút "Add sample data" tạo thêm dữ liệu, chỉ dùng trên database của môi trường kiểm thử.

### Bài K2: API về 0 pod và cold start

Mục tiêu: đo thời gian từ request đầu tiên khi API ở 0 pod đến phản hồi (yêu cầu dưới 3 giây), và xác nhận API về 0 sau thời gian rảnh.

Điều kiện trước: API ở 0 pod (O1 không có pod `api`). Nếu vừa chạy K1, chờ `scaledownPeriod` (mặc định 300 giây).

K2a, một lần đo:
```bash
curl -s -o /dev/null -H "Host: api.demo.local" -w "http=%{http_code} connect=%{time_connect}s total=%{time_total}s\n" "http://$ALB/positions?limit=1"
curl -s -o /dev/null -H "Host: api.demo.local" -w "http=%{http_code} connect=%{time_connect}s total=%{time_total}s\n" "http://$ALB/positions?limit=1"
```
- Lệnh đầu gặp API ở 0 pod nên chờ pod khởi động. Lệnh thứ hai gặp API đã chạy.
- `-w` in mã HTTP, thời gian mở kết nối TCP (`time_connect`) và tổng thời gian. Ghi cả hai số, vì thời gian kết nối là độ trễ mạng của máy đo, không phải của hệ thống.
- Sau lệnh thứ hai, ghi giờ và chờ `scaledownPeriod` (300 giây). Quan sát O1: pod `api` biến mất. Ghi số giây từ request cuối đến lúc về 0.

Giao diện web cho cách đo thứ hai: khi API ở 0 pod, bấm một thao tác thật (ví dụ Add position) mà không bấm Load data trước (Load data cũng gọi API). Khung "API response time" ở đầu trang hiện thời gian và nhãn Cold start hoặc Warm.

K2b, chuỗi 20 lượt:
```bash
helm upgrade api charts/app -n demo --reset-then-reuse-values --set scaling.http.scaledownPeriod=10 --force-conflicts
COLD_PATH='/positions?limit=10' COLD_JQ='has("items")' \
  scripts/test-cold-start.sh 20 direct docs/results/api/cold-start-api.csv
helm upgrade api charts/app -n demo --reset-then-reuse-values --set scaling.http.scaledownPeriod=300 --force-conflicts
```
- Lệnh `helm upgrade` đầu hạ thời gian chờ về 0 xuống 10 giây để mỗi lượt đỡ chờ 5 phút. Lệnh cuối trả lại giá trị thật.
- `test-cold-start.sh <số lượt> <chế độ> <file>`: mỗi lượt chờ API về 0 pod, gửi một request qua ALB, ghi thời gian, rồi đọc dấu thời gian của pod mới để biết giai đoạn nào chậm. `COLD_PATH` chọn đường gọi (đường có chạm database cho kết quả sát thực tế); `COLD_JQ` là điều kiện jq mà nội dung JSON phải thỏa để tính là phản hồi của API.
- Không dùng `scripts/deploy-apps.sh` để đổi giá trị này, vì nó cài lại cả ba ứng dụng theo biến `TAG`.

Kết quả mong đợi: yêu cầu là cold start dưới 3 giây. Báo cáo hai cách tính: đầu cuối (`total_s`) và phía hệ thống (`total_s` trừ `connect_s`). Giá trị tham chiếu ở [BAO_CAO.md](BAO_CAO.md) mục 5.1.

Dữ liệu cần ghi: file CSV (cột `total_s`, `connect_s`, `ttfb_s`, `pod_created_after_request_s`, `created_to_scheduled_s`, `created_to_ready_s`), thống kê script in cuối, và số lượt có `connect_s` từ 0,5 giây trở lên.

Lưu ý: lượt có `connect_s` lớn là mất gói ở đường mạng của máy đo, cần tách riêng khi báo cáo. `created_to_*` có độ phân giải 1 giây. Cold start đo trên cụm vừa dựng có thể chậm hơn bất thường; chờ cụm ổn định rồi đo lại và ghi chú.

### Bài K3: Karpenter thêm và bớt node

Mục tiêu: xác nhận Karpenter tạo node spot khi có pod không còn chỗ và dọn node thừa khi hết tải.

Điều kiện trước: cụm ổn định (5 node, mục 4.6). Mở O2.

Bước 1, tạo 5 pod xin 1500m CPU mỗi pod (lớn hơn chỗ trống trên các node sẵn có):
```bash
kubectl create namespace tier-test
cat <<'EOF' | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata: {name: karpenter-probe, namespace: tier-test}
spec:
  replicas: 5
  selector: {matchLabels: {app: karpenter-probe}}
  template:
    metadata: {labels: {app: karpenter-probe}}
    spec:
      nodeSelector: {karpenter.sh/nodepool: default}
      containers:
        - name: pause
          image: registry.k8s.io/pause:3.10
          resources: {requests: {cpu: 1500m}}
EOF
time kubectl -n tier-test rollout status deployment/karpenter-probe --timeout=240s
```
Pod chạy `pause` (container rỗng) chỉ để chiếm chỗ; `nodeSelector` ép chúng vào pool `default`; `rollout status` chờ đủ 5 pod và `time` in thời gian chờ.

Bước 2, chỉ chạy sau khi bước 1 báo xong (nếu chạy cùng lúc, pod bị xóa trước khi Karpenter kịp tạo node):
```bash
kubectl delete namespace tier-test
```

Kết quả mong đợi: O2 cho thấy số node pool `default` tăng (thường 1 đến 3 node, tùy loại máy Karpenter chọn), cả 5 pod sẵn sàng, rồi sau khi xóa, số node giảm về 2 trong vài phút (Karpenter gom node thừa sau 1 phút). Giá trị tham chiếu ở [BAO_CAO.md](BAO_CAO.md) mục 5.7.

Dữ liệu cần ghi: số node trước, đỉnh và sau; thời gian `rollout status` báo; thời gian về số node ban đầu; loại và kiến trúc node được tạo. Nhật ký Karpenter (Karpenter chạy 2 pod, chỉ pod leader ghi sự kiện nên phải đọc cả hai bằng nhãn, không dùng `logs deploy/karpenter`):
```bash
kubectl -n karpenter logs -l app.kubernetes.io/name=karpenter --tail=-1 \
  | grep -E '"created nodeclaim"|"deleted nodeclaim"' \
  | sed -E 's/.*"time":"([^"]*)".*"message":"([^"]*)".*"NodeClaim":\{"name":"([^"]*)"\}.*/\1  \2  \3/'
```
Mỗi dòng gồm giờ UTC, sự kiện, tên NodeClaim (tiền tố `default-` hoặc `worker-` cho biết pool).

### Bài K4: HPA và trigger CPU của API

Mục tiêu: xác nhận HPA thêm pod khi CPU vượt ngưỡng và giảm khi hết tải; xác nhận API thêm pod theo CPU qua trigger của KEDA.

K4a, HPA. Frontend dùng quá ít CPU để kích hoạt HPA, nên dùng một bản sao của API có endpoint `/burn` (đốt CPU theo yêu cầu):
```bash
ACCOUNT=$(aws sts get-caller-identity --query Account --output text)
kubectl create namespace hpa-demo
helm upgrade --install hpa-demo charts/app -n hpa-demo \
  --set image.repository=$ACCOUNT.dkr.ecr.ap-southeast-1.amazonaws.com/demo-api --set image.tag=$TAG --set arch=auto \
  --set containerPort=8080 --set probe.httpPath=/healthz --set probe.startup.enabled=false \
  --set scaling.mode=hpa --set scaling.hpa.min=1 --set scaling.hpa.max=3 --set scaling.hpa.cpu=50 \
  --set scaling.hpa.scaleDownStabilizationSeconds=60 --set ingress.enabled=false \
  --set resources.requests.cpu=250m --set resources.requests.memory=64Mi --set resources.limits.memory=256Mi \
  --set nodePool=default --set serviceAccountName=default --set serviceAccount.create=false --wait
kubectl -n hpa-demo run load --image=curlimages/curl:8.10.1 --restart=Never --command -- sh -c \
  'end=$(( $(date +%s) + 150 )); while [ $(date +%s) -lt $end ]; do curl -s -m 10 "http://hpa-demo/burn?ms=1000" >/dev/null; sleep 2; done'
watch -n 3 'kubectl -n hpa-demo get hpa,pods'
```
- Bản sao dùng `scaling.mode=hpa`, từ 1 đến 3 pod, mục tiêu CPU 50% so với mức xin 250m, chờ 60 giây ổn định trước khi giảm, không mở ra ngoài (`ingress.enabled=false`).
- Pod `load` chạy trong cụm, gọi `GET /burn?ms=1000` (đốt CPU 1 giây) rồi nghỉ 2 giây, lặp 150 giây. Nó gọi thẳng Service nên không qua ALB. O1 chỉ theo dõi namespace `demo`, nên cần lệnh `watch` riêng ở cuối.

Kết quả mong đợi: cột `TARGETS` của HPA vượt 50%, số pod tăng từng bước (có sự kiện `SuccessfulRescale`), tối đa 3 pod. Sau khi tải kết thúc và hết cửa sổ ổn định 60 giây, số pod về 1.

Dọn dẹp: `helm -n hpa-demo uninstall hpa-demo; kubectl delete namespace hpa-demo`.

K4b, trigger CPU của API. Điều kiện trước: API đang có 1 pod (gọi API một lần). Tạo tải CPU qua ALB bằng 10 vòng lặp song song, mỗi vòng gọi `/burn?ms=1000` liên tục 120 giây. Số request đồng thời chỉ khoảng 10, xa mức 100 của trigger request, nên chỉ trigger CPU thêm được pod:
```bash
end=$(( $(date +%s) + 120 ))
for i in $(seq 1 10); do
  ( while [ $(date +%s) -lt $end ]; do curl -s -o /dev/null -m 15 -H "Host: api.demo.local" "http://$ALB/burn?ms=1000"; done ) &
done; wait
kubectl -n demo get hpa
```
Kết quả mong đợi: O1 cho thấy API lên 2 pod trong vòng khoảng nửa phút sau khi bắt đầu tải; HPA do KEDA tạo ra (`keda-hpa-api`) báo `New size: 2` với lý do CPU vượt mục tiêu. Sau khi hết tải và hết `scaledownPeriod`, API về 0 pod. Giá trị tham chiếu ở [BAO_CAO.md](BAO_CAO.md) mục 5.8.

Dữ liệu cần ghi: giờ bắt đầu và kết thúc tải; giờ có pod thứ hai; sự kiện HPA (`kubectl -n demo describe hpa`); số pod theo thời gian.

### Bài K5: KEDA ScaledJob với SQS

Mục tiêu: xác nhận worker tăng từ 0 đến tối đa 20 Job theo độ dài hàng đợi, không mất message, rồi về 0.

Điều kiện trước: worker ở 0 pod, hàng đợi và DLQ rỗng. Mở O1, O2, O3.

Các bước:
```bash
for i in 1 2 3 4 5; do curl -s -XPOST -H "Host: api.demo.local" "http://$ALB/jobs" -d '{"count":100}'; echo; done
kubectl -n demo get pods -l app.kubernetes.io/name=worker -w
```
- Mỗi lần `POST /jobs` đẩy 100 message vào hàng đợi (đây là mức tối đa một lần), tổng 500 message.
- `-w` in thêm một dòng mỗi khi pod đổi trạng thái; có nhiều dòng là bình thường. Muốn đếm theo trạng thái: `watch -n 2 'kubectl -n demo get pods -l app.kubernetes.io/name=worker --no-headers | awk "{print \$3}" | sort | uniq -c'`.

Kiểm tra không mất message (hàng đợi lỗi phải bằng 0):
```bash
aws sqs get-queue-attributes --queue-url $DLQ --attribute-names ApproximateNumberOfMessages --query Attributes --output text
```

Kết quả mong đợi: O3 cho thấy hàng đợi tăng lên 500 rồi về 0; O1 cho thấy số pod worker đi từ 0 lên tối đa 20 rồi hoàn tất (`Completed`) và biến mất dần; DLQ bằng 0. Thường không có node mới vì node `worker` sẵn có đủ chỗ cho 20 pod.

Dữ liệu cần ghi: thời gian từ lúc gửi đến lúc hàng đợi về 0; số pod worker lớn nhất; số node tạo thêm; giá trị DLQ; số Job đã tạo (`kubectl -n demo get jobs --no-headers | wc -l`).

Lưu ý: nếu O1 báo pod worker kẹt ở `ContainerCreating` hơn 10 giây, xem mục 7 (kho IP của node).

### Bài K6: cold start của worker

Mục tiêu: đo thời gian từ lúc bấm Score đến lúc có điểm khi worker ở 0 pod (yêu cầu dưới 3 giây), và số Job tạo ra cho mỗi lần Score.

Điều kiện trước: đã nạp dữ liệu mẫu (mục 4.6); API đang nóng (script tự gọi `/healthz` để giữ nóng); worker ở 0 pod. Xác định cấu hình KEDA hiện tại và ghi vào kết quả:
```bash
kubectl -n demo get scaledjob -o custom-columns=NAME:.metadata.name,POLL:.spec.pollingInterval,TRIG:.spec.triggers[*].type,READY:.status.conditions[0].status
```

Các bước:
```bash
scripts/score-cold-start.sh 20 docs/results/worker/<tên-file>.csv
```
Mỗi lượt script chờ worker về 0 pod, nghỉ ngẫu nhiên tối đa `JITTER_MAX` giây (mặc định 60) để lần bấm rơi vào mọi điểm của chu kỳ hỏi của KEDA, gửi `POST /applications/{id}/screen` qua ALB, hỏi `/screening` mỗi 0,25 giây đến khi có điểm, đợi `AFTER_WAIT` giây (mặc định 25) rồi đếm Job được tạo. Khi chu kỳ hỏi lớn hơn 25 giây, đặt `AFTER_WAIT` lớn hơn chu kỳ hỏi dài nhất để không bỏ sót Job dư tạo ở nhịp hỏi kế tiếp, và `JITTER_MAX` không nhỏ hơn chu kỳ hỏi.

Các cột của file CSV:
- `api_reply_s`: thời gian API trả lời lệnh Score (202), đo ở máy khách.
- `total_s`: từ lúc bấm đến lúc điểm hiện, đồng hồ máy đo, gồm mạng.
- `server_s`: `doneAt` trừ `requestedAt` trong database, không gồm mạng của máy đo.
- `keda_job_after_s`: từ lúc bấm đến lúc Job đầu tiên xuất hiện (độ phân giải 1 giây, có thể hơi âm).
- `jobs_created`: số Job cho một lần Score. 1 là lý tưởng, lớn hơn nghĩa là có Job rỗng dư.

Đổi chu kỳ hỏi để so sánh (cấu hình trong kho mã là một ScaledJob chỉ có trigger SQS, `pollingInterval: 1`, vì là bản demo):
```bash
helm -n demo upgrade worker charts/app --reset-then-reuse-values --set scaling.sqsjob.pollingInterval=30 --force-conflicts --wait --timeout 3m
```
Đo lại (chu kỳ 30 giây cho ít Job dư hơn, BAO_CAO.md mục 5.2 và 5.4), rồi đặt lại giá trị cũ bằng lệnh tương tự với `pollingInterval=1`. Đề xuất tách thành hai ScaledJob (trigger PostgreSQL hỏi 1 giây, trigger SQS hỏi 60 giây) chưa được duyệt và chỉ có mã tham chiếu trong `docs/reference/worker-split/`. Mã đó chỉ để đọc: không bài kiểm thử nào áp dụng nó lên cụm, và không nên áp dụng. Mô tả và rủi ro của cấu hình này nằm ở [BAO_CAO.md](BAO_CAO.md) mục 3.6, 6.3 và 6.4.

Đối chiếu số Job bằng log của KEDA. KEDA chạy hai pod operator, nên phải đọc cả hai:
```bash
for p in $(kubectl -n keda get pods -l app.kubernetes.io/name=keda-operator -o name); do
  kubectl -n keda logs "$p" --since=2h
done | grep 'Created jobs'
```
Số dòng `Created jobs` trong khoảng đo nên khớp với tổng `jobs_created`.

Kết quả mong đợi: yêu cầu là `server_s` dưới 3 giây. Báo cáo trung vị, p95, số lượt dưới 3 giây ở cả `server_s` và `total_s`, và số lượt đúng 1 Job. Giá trị tham chiếu của hệ thống (chỉ SQS) ở [BAO_CAO.md](BAO_CAO.md) mục 5.2; của hai đề xuất cải tiến A và B, chưa được duyệt, ở mục 5.5. Phân tích vì sao các cấu hình cho thời gian khác nhau: mục 5.3 của báo cáo (tách `server_s` thành thời gian KEDA phát hiện việc và thời gian từ lúc Job được tạo đến lúc có điểm).

Dữ liệu cần ghi: file CSV, thống kê script in, cấu hình KEDA đã đặt, số Job theo log operator.

Lưu ý: các phân vị script in dùng cách chọn hạng gần nhất, nên có thể lệch nhẹ so với nội suy tuyến tính trong báo cáo. Script tạo ra khoảng nghỉ nên 20 lượt mất khoảng 25 đến 40 phút.

Tùy chọn: `scripts/worker-cold-start.sh <nhãn> [số lượt] [file]` đo thời gian từ lúc gửi một message trực tiếp vào SQS đến lúc pod worker Ready và hàng đợi cạn (không qua API). Cần `bc` và thông tin đăng nhập AWS.

### Bài K7: chart Helm với image bất kỳ

Mục tiêu: xác nhận cùng một chart chạy được image của bên thứ ba, chỉ đổi image, cổng và tham số.

Các bước:
```bash
scripts/test-plug-image.sh
```
Script cài ba image (`nginx`, `hashicorp/http-echo`, `hello-app` chỉ có amd64) bằng `helm upgrade --install` với cùng chart. `scripts/detect-arch.sh` đọc manifest của từng image để đặt `arch` (`auto`, `amd64` hay `arm64`). Sau khi pod Ready, script gọi HTTP từ một pod `curl` trong cụm. Cuối cùng chạy phép thử đối chứng: ép `hello-app` chạy trên node arm64 thì pod phải không Ready. Script xóa namespace `demo-plug` khi xong (dùng `--keep` để giữ lại).

Kết quả mong đợi: ba dòng `[ ok ]` cho ba image, một dòng `[ ok ] wrong-arch pod is not Ready`, và dòng cuối `RESULT: all checks passed`.

Dữ liệu cần ghi: toàn bộ đầu ra của script.

Lưu ý: chỉ kiểm tra dịch vụ HTTP; chưa thử volume, TCP, gRPC, registry riêng, container non-root.

### Bài K8: mất một node spot

Mục tiêu: đo người dùng thấy gián đoạn bao lâu và cụm hồi phục sau bao lâu khi một node bị thu hồi đột ngột.

Cảnh báo: bài này tắt một máy EC2 thật. Chỉ chạy khi cụm ổn định và không dùng chung với người khác.

Các bước:
```bash
scripts/spot-loss-test.sh docs/results/spot-loss
```
Script chọn node đang chạy pod frontend và lấy mã instance, đánh thức API, chạy nền hai vòng `curl` mỗi 0,5 giây (một vào frontend, một vào `/healthz` của API), sau 5 giây gọi `aws ec2 terminate-instances` (mất máy đột ngột, không có cảnh báo hai phút của spot), rồi ghi trạng thái cụm mỗi 3 giây. Script dừng khi cụm hồi phục ba lần kiểm tra liên tiếp (tối đa 4 phút) và lưu nhật ký Karpenter.

Kết quả mong đợi: Karpenter nhận biết việc mất node và tạo node thay; pod frontend chạy lại trên node khác. Frontend chỉ có 1 pod nền nên có thể có một khoảng ngắn ALB trả 502 hoặc 503; số request lỗi và độ dài khoảng lỗi phải được báo cáo đúng như đo được. API không nằm trên node bị tắt thì không lỗi. Aurora và SQS không nằm trên node nên dữ liệu không mất. Giá trị tham chiếu ở [BAO_CAO.md](BAO_CAO.md) mục 5.11.

Dữ liệu cần ghi: thư mục kết quả (`probe.csv`, `timeline.csv`, `summary.txt`, `nodes_before.txt`, nhật ký Karpenter); `summary.txt` đếm số request không trả HTTP 200 theo từng đường.

Lưu ý: kết quả không đại diện cho thông báo gián đoạn spot hai phút của AWS; bài chỉ thử mất máy đột ngột và chỉ thử với node chứa frontend.

### Bài K9: 200 user cùng lúc và Aurora

Mục tiêu: xác nhận hệ thống chịu 200 người dùng ảo cùng lúc, API và frontend không vượt 2 pod, và quan sát dung lượng Aurora.

Điều kiện trước: API đang có 1 pod (gọi API một lần), để tăng pod nếu có là do tải chứ không do cold start. Docker đang chạy. Máy chạy k6 nằm trong `INBOUND_CIDRS`.

Các bước, tải 2 phút (dùng `DURATION=3m` để so sánh với báo cáo):
```bash
docker run --rm -v "$PWD/scripts:/s:ro" grafana/k6 run -e BASE_URL=http://$ALB \
  -e VUS=200 -e DURATION=2m -e WITH_SLOW=1 -e WITH_DB=1 -e WITH_DOMAIN=1 /s/loadtest.js
```
- k6 chạy trong container tạm nên máy không cần cài k6. `-v` gắn thư mục `scripts/` vào container ở chế độ chỉ đọc.
- `VUS=200` là số người dùng ảo cùng lúc; `DURATION` là thời lượng.
- `WITH_SLOW=1` làm mỗi người dùng ảo gọi thêm endpoint chờ 2 giây. Cần như vậy vì request nhanh chỉ tạo khoảng 10 request đồng thời, xa mức 100 mà bộ co giãn HTTP đòi để thêm pod.
- `WITH_DB=1` và `WITH_DOMAIN=1` làm k6 chạm database (đọc dữ liệu tuyển dụng, thỉnh thoảng tạo ứng viên). Thiếu hai cờ này thì Aurora gần như không có tải và ACU đứng ở 0,5.

Xem ACU của Aurora ngay sau khi k6 kết thúc (CloudWatch ghi mỗi phút một điểm):
```bash
aws cloudwatch get-metric-statistics --namespace AWS/RDS --metric-name ServerlessDatabaseCapacity \
  --dimensions Name=DBClusterIdentifier,Value=demo-aurora \
  --start-time $(date -u -d '-8 minutes' +%FT%TZ) --end-time $(date -u +%FT%TZ) \
  --period 60 --statistics Maximum --query 'sort_by(Datapoints,&Timestamp)[].[Timestamp,Maximum]' --output text
```
Đổi `--metric-name` thành `CPUUtilization` hoặc `DatabaseConnections` để xem hai chỉ số còn lại.

Kết quả mong đợi: không có request lỗi (`http_req_failed` dưới 1%); các ngưỡng p95 và p99 trong `scripts/loadtest.js` đạt; O1 cho thấy API và frontend tối đa 2 pod; ACU tăng từ 0,5 lên gần hoặc chạm 2,0 trong lúc có tải rồi giảm dần. Chạm trần 2,0 nghĩa là database là điểm nghẽn tiếp theo. Giá trị tham chiếu ở [BAO_CAO.md](BAO_CAO.md) mục 5.9 và 5.10.

Dữ liệu cần ghi: kết quả k6 (số request, request mỗi giây, trung vị, p95, p99, lỗi), số pod tối đa mỗi bên, ACU, CPU và số kết nối theo phút.

### Bài K10: stress test Aurora (tùy chọn)

Mục tiêu: tăng tải từng bậc (100, 200, 300, 400, 500 request mỗi giây, mỗi bậc 2 phút) với các endpoint tốn database nhất, để tìm điểm API hoặc database bắt đầu không theo kịp.

Điều kiện trước: API đang có 1 pod. Mở một cửa sổ theo dõi Aurora:
```bash
watch -n 30 'for m in ServerlessDatabaseCapacity CPUUtilization DatabaseConnections; do echo "== $m"; aws cloudwatch get-metric-statistics --namespace AWS/RDS --metric-name $m --dimensions Name=DBClusterIdentifier,Value=demo-aurora --start-time $(date -u -d "-8 minutes" +%FT%TZ) --end-time $(date -u +%FT%TZ) --period 60 --statistics Maximum --query "sort_by(Datapoints,&Timestamp)[].[Timestamp,Maximum]" --output text; done'
```

Các bước:
```bash
docker run --rm --network host -v "$PWD/scripts:/s:ro" grafana/k6 run -e ALB=$ALB /s/stress-aurora.js
```
Tùy chỉnh bằng `-e START=100 -e STEP=100 -e STEPS=5 -e STEP_SECONDS=120` và `-e WRITE_SHARE=0.5` (tỷ lệ yêu cầu ghi). Bài dùng tốc độ cố định (`ramping-arrival-rate`) nên k6 giữ đúng số request mỗi giây dù server chậm đi. `dropped_iterations` trong kết quả nghĩa là k6 không đủ người dùng ảo hoặc hệ thống không đáp ứng kịp.

Cách đọc kết quả: ACU chạm 2,0 và CPU trên 90% ở bậc nào là trần của database. p95 trên 1 giây hoặc lỗi 5xx ở bậc nào là hệ thống không theo kịp. Nếu CPU database còn thấp mà lỗi tăng thì nghẽn ở API (tối đa 2 pod); nếu CPU database đã sát trần thì nghẽn ở Aurora. Số kết nối ổn định ở khoảng 20 (pool của 2 pod API) cho thấy database không cạn kết nối.

Dọn dữ liệu sau bài: bài này tạo hàng chục nghìn ứng viên có e-mail kết thúc `@stress.test`. Xóa bằng một pod `psql` tạm; mật khẩu đi qua đầu vào chuẩn và không được in ra:
```bash
ARN=$(kubectl -n demo get deploy api -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="DB_SECRET_ARN")].value}')
HOST=$(kubectl -n demo get deploy api -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="DB_HOST")].value}')
SEC=$(aws secretsmanager get-secret-value --secret-id "$ARN" --query SecretString --output text)
U=$(echo "$SEC" | python3 -c 'import sys,json;print(json.load(sys.stdin)["username"])'); P=$(echo "$SEC" | python3 -c 'import sys,json;print(json.load(sys.stdin)["password"])')
{ echo "$P"; echo "DELETE FROM applicants WHERE email LIKE '%@stress.test';"; } | kubectl -n demo run psql-clean --rm -i --restart=Never --image=postgres:16-alpine --command -- sh -c "read PGPASSWORD; export PGPASSWORD; PGSSLMODE=require psql -h $HOST -U $U -d demo"
unset P SEC
```
`ON DELETE CASCADE` xóa luôn hồ sơ ứng tuyển của các ứng viên này. Câu lệnh chỉ xóa các dòng có e-mail `@stress.test`.

### Bài K11: giám sát bằng Grafana (tùy chọn)

Prometheus, Grafana và Loki chạy trên hai node hệ thống, không mở ra internet. Truy cập bằng port-forward ở một cửa sổ riêng:
```bash
kubectl -n monitoring port-forward svc/grafana 3000:80
kubectl -n monitoring get secret grafana -o jsonpath='{.data.admin-password}' | base64 -d; echo
```
Mở `http://localhost:3000`, đăng nhập với người dùng `admin` và mật khẩu vừa in.

- Explore, nguồn Prometheus: số liệu pod, node, KEDA (`keda_scaler_metrics_value`, `keda_scaler_active`), Karpenter (`karpenter_nodes_*`, `karpenter_nodeclaims_*`), HPA (`kube_horizontalpodautoscaler_*`).
- Explore, nguồn Loki: log của pod ứng dụng, KEDA, Karpenter và sự kiện Kubernetes. Ví dụ LogQL: `{namespace="demo", app="worker"}`, `{namespace="karpenter"} |= "nodeclaim"`, `{job="loki.source.kubernetes_events"}`.
- Log nằm trong bucket S3 `demo-logs-<mã tài khoản>` và tự xóa sau 7 ngày, nên vẫn tìm lại được log của pod API và worker đã về 0. Loki ghi lên S3 theo lô, nên log rất mới có thể chưa lên S3.

## 6. Tổng hợp và tiêu chí đạt

Điền bảng sau khi kết thúc các bài. Cột "Tiêu chí" là yêu cầu của đề tài; cột cuối chỉ chỉ ra nơi có giá trị tham chiếu.

| Yêu cầu | Bài | Tiêu chí | Tham chiếu |
|---|---|---|---|
| API Go 20 đến 50 endpoint, frontend React, ALB | K1 | mọi kiểm tra của `smoke-api.sh` là `[ok]` | BAO_CAO mục 3.2 |
| API về 0 pod, cold start dưới 3 giây | K2 | request đầu tiên khi API ở 0 pod dưới 3 giây (báo cáo cả đầu cuối và phía hệ thống) | BAO_CAO mục 5.1 |
| Worker bắt đầu xử lý dưới 3 giây | K6 | `server_s` dưới 3 giây | BAO_CAO mục 5.2 và 5.3 |
| Karpenter thêm và bớt node spot | K3 | node tăng khi thiếu chỗ, về số ban đầu sau khi hết tải | BAO_CAO mục 5.7 |
| HPA và trigger CPU | K4 | số pod tăng khi CPU vượt ngưỡng, giảm khi hết tải, không vượt trần | BAO_CAO mục 5.8 |
| KEDA với SQS | K5 | worker lên tối đa 20 Job, hàng đợi về 0, DLQ bằng 0 | BAO_CAO mục 5.2 |
| Chart dùng được với image bất kỳ | K7 | `RESULT: all checks passed` | BAO_CAO mục 5.12 |
| Chịu mất node spot | K8 | cụm hồi phục; báo cáo đúng số request lỗi và độ dài khoảng lỗi | BAO_CAO mục 5.11 |
| Chịu 200 user, API và frontend tối đa 2 pod | K9 | không có lỗi; không vượt 2 pod mỗi bên | BAO_CAO mục 5.9 |
| Aurora 0,5 đến 2 ACU | K9, K10 | ACU nằm trong khoảng đã đặt; ghi lại điểm chạm trần | BAO_CAO mục 5.10 |
| Route 53 | không có | chưa làm, cần tên miền | BAO_CAO mục 1.2 |

## 7. Sự cố thường gặp

| Triệu chứng | Nguyên nhân | Cách xử lý |
|---|---|---|
| `session has expired` hoặc lệnh AWS báo không có thông tin đăng nhập | Phiên `aws login` hết hạn giữa chừng | Chạy lại `aws login`. Kiểm tra bằng `aws sts get-caller-identity` trước mỗi bài dài |
| Lệnh `aws` báo hàng đợi hoặc repository không tồn tại, hoặc trả rỗng | Vùng mặc định của AWS CLI khác `ap-southeast-1` | Đặt `AWS_DEFAULT_REGION=ap-southeast-1` hoặc thêm `--region` |
| `curl` tới ALB báo `http=000` hoặc hết thời gian chờ | IP máy đo không nằm trong `INBOUND_CIDRS` | Deploy lại với `INBOUND_CIDRS=<danh sách>`, hoặc đặt biến GitHub `INBOUND_CIDRS` nếu dùng CD |
| `terraform` báo lỗi thông tin đăng nhập | Terraform không đọc được thông tin do `aws login` tạo | Chạy qua hàm `tf` (`source scripts/_common.sh`) |
| `terraform init` báo bucket không tồn tại | Chưa chạy `scripts/bootstrap-state.sh`, hoặc `backend.hcl` còn trỏ bucket của tài khoản khác | Chạy script và sửa `bucket` trong `terraform/backend.hcl` |
| Terraform hết thời gian chờ ở `cluster-config`, hoặc pod `image-prepuller` ở `ImagePullBackOff` | `prepull_images` khác rỗng khi ECR còn trống, hoặc tag không khớp ECR | Đặt `prepull_images = []`, đẩy image, rồi bật lại (mục 4.5) |
| Helm báo xung đột quyền sở hữu trường (field ownership) | KEDA cũng sửa trường đó | Thêm `--force-conflicts` |
| `smoke-api.sh` báo `[FAIL]` ở `POST /jobs` và `screen` | Quên `EXPECT_QUEUE=1` | Chạy lại với biến này |
| Cold start rất chậm (hàng chục giây) ở request đầu tiên trên cụm mới dựng | Cụm chưa ổn định (node, pod giữ chỗ, kho IP) | Chờ khoảng 5 phút rồi đo lại, và ghi chú lần đo đầu |
| Pod worker kẹt `ContainerCreating` 10 đến 40 giây khi nhiều Job tạo cùng lúc (sự kiện `FailedCreatePodSandBox`, `failed to assign an IP address`) | Kho IP trống của node cạn | Chờ pod thử lại; pool `worker` đã dùng node 4 vCPU và `WARM_IP_TARGET=10`, `MINIMUM_IP_TARGET=30` để giảm hiện tượng này |
| `kubectl logs deploy/keda-operator` thiếu dòng `Created jobs` | KEDA chạy hai pod operator, lệnh chỉ đọc một pod | Đọc cả hai pod (K6) |
| `watch` hoặc `bc` không có | Thiếu công cụ | Cài gói tương ứng |

## 8. Dọn dẹp

Nếu giữ cụm và chỉ muốn trả cấu hình về mặc định: đặt lại `scaledownPeriod` của API về 300 và `pollingInterval` của worker về giá trị trong `apps/worker.values.yaml` bằng các lệnh `helm upgrade` ở K2 và K6.

Để hủy toàn bộ và ngừng tính tiền:
```bash
scripts/down.sh --dry-run           # liệt kê những gì đang tồn tại, không xóa
scripts/down.sh                     # gõ "demo" để xác nhận; xóa cả Aurora và dữ liệu
scripts/down.sh --dry-run           # kiểm tra còn sót gì
```
`down.sh` làm theo thứ tự: gỡ ứng dụng (ALB biến mất), xóa pod giữ chỗ và NodePool rồi chờ Karpenter tắt hết node, `terraform destroy` (Aurora, SQS, EKS, NAT, VPC, ECR, IAM), rồi liệt kê phần còn lại. Mất khoảng 15 đến 25 phút. Sau khi xong, mọi dòng của lần `--dry-run` cuối phải là `none` hoặc `0` (cụm EKS, load balancer, Aurora, node EC2, NAT gateway, hàng đợi SQS, ảnh ECR). Hạ tầng cũng nên được đối chiếu trên console AWS để chắc không còn Elastic IP hoặc volume EBS mồ côi.

Bucket state `demo-tfstate-<mã tài khoản>` được giữ lại có chủ ý và chỉ tốn một khoản nhỏ. Xóa bằng tay nếu không dùng lại (bucket bật version nên phải xóa cả các phiên bản).

Các cửa sổ quan sát sẽ báo lỗi kết nối sau khi cụm bị hủy; dùng Ctrl+C để đóng.

## Phụ lục A. Các thao tác trên giao diện

| Nút hoặc ô | Chức năng |
|---|---|
| Load data | Tải mọi dữ liệu đang hiển thị (tổng quan, vị trí, ứng viên, hồ sơ). Cũng là lệnh đầu tiên đánh thức API nếu nó đang ở 0 pod |
| Add sample data | Nạp dữ liệu mẫu: 20 ứng viên, 5 vị trí, 40 hồ sơ. Mỗi lần bấm thêm một bộ mới |
| Add position | Thêm vị trí tuyển dụng từ ba ô Title, Department, Location (cả ba bắt buộc) |
| Close (ở mỗi vị trí đang mở) | Đóng vị trí (`open` sang `closed`). Vị trí đã đóng không còn trong danh sách chọn khi tạo hồ sơ |
| Add applicant | Thêm ứng viên từ Name, Email (bắt buộc) và Skills (tùy chọn, cách nhau dấu phẩy) |
| Apply | Tạo hồ sơ nối một ứng viên với một vị trí đang mở. Hồ sơ mới ở trạng thái `applied` |
| Ô chọn trạng thái (mỗi hồ sơ) | Đổi trạng thái hồ sơ theo quy trình hợp lệ (`applied` sang `screening`, `interview`, `offer`, `hired`; `rejected` hoặc `withdrawn` bất cứ lúc nào trước bước cuối). Bước nhảy không hợp lệ bị API từ chối và trang hiện lỗi đỏ |
| Score | Đẩy việc chấm điểm vào hàng đợi SQS. Worker bật từ 0 pod rồi ghi điểm. Thời gian chờ phụ thuộc chu kỳ hỏi của KEDA (BAO_CAO mục 5.2). Điểm là giả lập theo công thức cố định theo mã hồ sơ |
| Delete (vị trí, ứng viên, hồ sơ) | Xóa bản ghi sau khi xác nhận. Xóa vị trí hoặc ứng viên cũng xóa các hồ sơ liên quan |

Đầu trang có hai khung đo thời gian:
- API response time (bên trái): thời gian API trả lời mỗi thao tác, kèm nhãn Cold start hoặc Warm.
- Worker scoring time (bên phải): bấm Score thì đồng hồ chạy từ lúc bấm đến lúc điểm xuất hiện (chờ hàng đợi, chấm điểm), không gồm lời gọi API.

Mọi thao tác đều ghi vào bảng Activity ở cuối trang. Cột Source cho biết nguồn là `API (cold start)`, `API (warm)` hay `Worker`.
