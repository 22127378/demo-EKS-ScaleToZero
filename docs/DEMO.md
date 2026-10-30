# Kịch bản demo (khoảng 30 phút, bỏ phần 7 và 8 nếu cần rút còn 20 phút)

Kịch bản dành cho người trình bày. Mỗi phần có: **nói gì**, **chạy gì và chạy ở đâu**, **người xem sẽ thấy gì**.
Lệnh giữ nguyên tiếng Anh. Bài tải ở phần 9 **phải bật** `WITH_DB=1 WITH_DOMAIN=1`, nếu không database không được dùng và ACU đứng ở 0,5. Số liệu tham chiếu lấy từ [TEST_LOG.md](TEST_LOG.md); khi demo, số thật có thể lệch vài phần trăm.

**Địa chỉ hệ thống (sau lần deploy này):**

```
http://k8s-demo-49ec67e7ad-1895331656.ap-southeast-1.elb.amazonaws.com/
```

Địa chỉ ALB **đổi sau mỗi lần dựng lại hạ tầng**. Nếu bạn dựng lại, lấy địa chỉ mới bằng:
`kubectl -n demo get ingress fe -o jsonpath='{.status.loadBalancer.ingress[0].hostname}'`, rồi thay vào mọi chỗ bên dưới.
ALB chỉ cho phép địa chỉ IP công cộng của máy đã chạy `up.sh`, nên mở link từ cùng máy đó.

## Quy ước: 1 trình duyệt và 4 terminal

| Tên | Dùng để | Chạy ở đâu |
|---|---|---|
| **Trình duyệt** | mở giao diện web | tab trình duyệt trên máy trình bày |
| **Terminal 1 (Pod)** | theo dõi pod và HPA, **để chạy suốt buổi** | thư mục bất kỳ |
| **Terminal 2 (Node)** | theo dõi node và NodeClaim của Karpenter, **để chạy suốt buổi** | thư mục bất kỳ |
| **Terminal 3 (SQS)** | theo dõi độ dài hàng đợi, **để chạy suốt buổi** | **bắt buộc ở thư mục gốc dự án** (cần `terraform output`) |
| **Terminal 4 (Lệnh)** | gõ các lệnh demo | **thư mục gốc dự án** |

Thư mục gốc dự án: `/home/tan.le@rikai.technology/Demo-test`.

## 0. Chuẩn bị (trước giờ G khoảng 10 phút)

Hạ tầng phải đang chạy. Nếu chưa, dựng trước 30 đến 45 phút bằng `scripts/up.sh` (xem `scripts/up.sh --plan` để xem kế hoạch;
kho ECR trống thì dựng hai lượt như ghi trong [BAO_CAO.md](BAO_CAO.md)).

**Chạy ở cả 4 terminal, các dòng đầu tiên (cài biến môi trường):**

```bash
cd /home/tan.le@rikai.technology/Demo-test
aws login                                  # chỉ khi phiên AWS đã hết hạn
aws eks update-kubeconfig --region ap-southeast-1 --name demo --kubeconfig /tmp/demo.kubeconfig
export KUBECONFIG=/tmp/demo.kubeconfig
export ALB=k8s-demo-49ec67e7ad-1895331656.ap-southeast-1.elb.amazonaws.com
```

**Từng lệnh làm gì:**
- `cd ...`: vào thư mục dự án, vì một số lệnh sau dùng đường dẫn tương đối (`scripts/`, `terraform/`).
- `aws login`: đăng nhập AWS; chỉ cần khi phiên cũ hết hạn (lỗi `session has expired`).
- `aws eks update-kubeconfig ...`: tạo file cấu hình kết nối `kubectl` tới cluster `demo` hiện tại, ghi vào `/tmp/demo.kubeconfig` (không đụng `~/.kube/config`). Cluster được dựng lại thì địa chỉ đổi, nên chạy lại lệnh này.
- `export KUBECONFIG=...`: bảo `kubectl` trong **terminal này** dùng file vừa tạo. Mỗi terminal phải chạy riêng.
- `export ALB=...`: lưu địa chỉ ALB vào biến `$ALB` để các lệnh `curl` và `k6` phía sau dùng lại.

**Terminal 1 (Pod):**
```bash
watch -n 2 'kubectl -n demo get pods,hpa -o wide'
```

Lệnh `watch -n 2` chạy lại `kubectl get pods,hpa` mỗi 2 giây: liệt kê pod của namespace `demo` (API, frontend, worker) và các HPA, kèm node mà mỗi pod đang chạy. Dùng để thấy pod xuất hiện, biến mất, và HPA đổi số bản sao.

**Terminal 2 (Node):**
```bash
watch -n 3 'kubectl get nodes -L karpenter.sh/nodepool,kubernetes.io/arch,karpenter.sh/capacity-type; echo; kubectl get nodeclaim'
```

Cũng chạy mỗi 3 giây: danh sách node, cột NODEPOOL (pool Karpenter), kiến trúc CPU, kiểu thuê (spot), cộng danh sách `nodeclaim` (yêu cầu node mà Karpenter đã tạo). Node nào không có NODEPOOL là node hệ thống.

**Terminal 3 (SQS):**
```bash
Q=$(terraform -chdir=terraform output -raw queue_url)
watch -n 3 "aws sqs get-queue-attributes --region ap-southeast-1 --queue-url $Q --attribute-names ApproximateNumberOfMessages ApproximateNumberOfMessagesNotVisible --query Attributes --output text"
```

- `Q=$(terraform ... queue_url)`: lấy địa chỉ hàng đợi SQS từ Terraform (nên phải đứng ở thư mục dự án).
- `aws sqs get-queue-attributes`: hỏi SQS hai con số, làm mới mỗi 3 giây: `ApproximateNumberOfMessages` (message đang chờ) và `ApproximateNumberOfMessagesNotVisible` (message đang được worker xử lý).

**Terminal 4 (Lệnh):** để trống, dùng cho các phần bên dưới.

**Kiểm tra trước giờ G:**
- [ ] **Trình duyệt:** mở `http://k8s-demo-49ec67e7ad-1895331656.ap-southeast-1.elb.amazonaws.com/` thấy trang Recruitment.
- [ ] **Terminal 2:** có 4 node: 2 hệ thống, 2 thuộc pool `default` (1 amd64, 1 arm64) đang giữ chỗ trống.
- [ ] **Terminal 1:** frontend 1 pod; API và worker **0 pod** (đúng thiết kế).
- [ ] Docker đang chạy (cần cho bài 200 user bằng k6, ở **Terminal 4**).

## 1. Giới thiệu hệ thống (2 phút)

**Nói:** Hệ thống gồm API Go (32 endpoint, miền tuyển dụng), frontend React, worker xử lý hàng đợi SQS, ALB phía trước,
Aurora Serverless v2 làm cơ sở dữ liệu. Chạy trên EKS, **toàn bộ node là spot**, hỗ trợ cả amd64 và arm64.
Ba tầng co giãn: **Karpenter** (node), **KEDA** (API về 0 pod; worker theo hàng đợi SQS), **HPA** (frontend).

**Chạy ở Terminal 4:**
```bash
kubectl get nodes -L karpenter.sh/nodepool,kubernetes.io/arch,karpenter.sh/capacity-type
kubectl -n kube-system get pods | grep -E "placeholder|prepull"
```

- Lệnh 1: liệt kê node kèm pool, kiến trúc và kiểu thuê, để cho thấy toàn bộ là spot và có cả amd64 lẫn arm64.
- Lệnh 2: tìm các pod `placeholder` (pod giữ chỗ trống dự phòng) và `prepull` (tải sẵn image) trong namespace `kube-system`.

**Người xem thấy:** có sẵn 2 node spot (1 amd64, 1 arm64) với pod giữ chỗ (`placeholder`). Đây là phần **dự phòng chỗ trống**:
mỗi node còn khoảng 1,28 vCPU trống để pod mới xếp vào ngay, không phải chờ tạo node (tạo node mất 40 đến 50 giây).
Chỉ sang **Terminal 2** đang hiển thị cùng thông tin.

## 2. Frontend và API hoạt động (2 phút)

**Chạy ở Trình duyệt:** mở `http://k8s-demo-49ec67e7ad-1895331656.ap-southeast-1.elb.amazonaws.com/`.
- Trang không tự gọi API khi mở (để API còn ở 0 pod nếu đang rảnh): xem phần 3. Khung trên cùng của trang hiện **thời gian API trả lời cho mỗi thao tác** bạn bấm.
- Bấm **Add sample data** (nạp dữ liệu mẫu), rồi xem tổng quan, thêm một vị trí và một ứng viên, tạo hồ sơ ứng tuyển. Mỗi nút làm gì: xem [phần "Các nút trên giao diện"](#các-nút-trên-giao-diện) cuối file.
- Bấm **Score** ở một hồ sơ: việc chấm điểm được đẩy vào hàng đợi SQS; worker bật từ 0 pod rồi ghi điểm về, điểm tự hiện sau vài chục giây (lần đo đầu mất khoảng 56 giây, gồm cả thời gian bật worker).

**Chạy ở Terminal 4:**
```bash
EXPECT_QUEUE=1 scripts/smoke-api.sh http://$ALB api.demo.local        # gọi mọi route, kiểm tra mã trả về
```

`EXPECT_QUEUE=1` báo cho script biết API trên cụm **có hàng đợi SQS**, nên `POST /jobs` và `POST /applications/{id}/screen` phải trả 202. Không đặt biến này thì script mặc định kỳ vọng 503 (kiểu chạy không có hàng đợi) và hai dòng đó báo `[FAIL]` dù hệ thống đúng.

`scripts/smoke-api.sh <địa chỉ> <host>`: script gọi lần lượt **mọi route** của API (32 route) và kiểm tra mã trả về cùng các trường chính trong phản hồi. Tham số thứ hai `api.demo.local` là header `Host`, để ALB chuyển request tới API thay vì frontend. In `[ ok ]` hoặc `[FAIL]` cho từng kiểm tra.

**Người xem thấy:** tab Recruitment hiển thị dữ liệu thật từ Aurora; **Terminal 1** cho thấy pod `worker` xuất hiện khi bấm Score;
smoke test báo đạt (khoảng 41 kiểm tra, 33 đường dẫn). Lần gọi API đầu tiên sau khi API về 0 sẽ chậm hơn một chút (phần 3).

> Lưu ý: smoke test và nút "Add sample data" tạo thêm dữ liệu trong cơ sở dữ liệu demo.

## 3. API scale to zero và cold start (4 phút)

**Nói:** API không có ai dùng thì về **0 pod**, không tốn tài nguyên. Request đầu tiên bật nó lên, yêu cầu là **dưới 3 giây**.
Thời gian chờ về 0 mặc định là 300 giây (5 phút). Để demo nhanh có thể hạ xuống (xem "Phương án nhanh" bên dưới).

**Cách 1: bấm trên giao diện web (dễ thấy nhất).** Khi API đang ở 0 pod (xem **Terminal 1**: không có pod `api`), mở **Trình duyệt** và
bấm một thao tác thật, ví dụ **Add position** hoặc **Add applicant** (điền ô rồi bấm nút). **Đừng bấm "Load data" trước**, vì nó cũng gọi API và làm API bật lên.
Khung trên cùng của trang hiện đồng hồ chạy theo thời gian thực cùng thanh tiến độ so với mục tiêu 3 giây; khi xong hiện "xong sau X giây" và nhãn **Cold start**
(API vừa bật từ 0 pod cho request này) hoặc **Warm** (API đã chạy sẵn). Dữ liệu vừa thêm hiện trong bảng. Bấm thêm một thao tác nữa: nhãn đổi thành Warm, thời gian chỉ vài chục mili giây.
Bảng **Activity** cuối trang giữ 10 thao tác gần nhất. Lúc này **Terminal 1** cho thấy pod `api` vừa xuất hiện.

**Cách 2: bằng lệnh. Chạy ở Terminal 4, khi API đang ở 0 pod:**
```bash
curl -s -o /dev/null -H "Host: api.demo.local" -w "http=%{http_code} total=%{time_total}s\n" \
  "http://$ALB/positions?limit=1"                               # request đầu tiên: khoảng 2 đến 3 giây
curl -s -o /dev/null -H "Host: api.demo.local" -w "http=%{http_code} total=%{time_total}s\n" \
  "http://$ALB/positions?limit=1"                               # request thứ hai: vài chục mili giây
```

- `curl -s -o /dev/null`: gọi API, im lặng (`-s`) và bỏ nội dung phản hồi (`-o /dev/null`); ta chỉ cần thời gian.
- `-H "Host: api.demo.local"`: header để ALB định tuyến tới API (không phải frontend).
- `-w "http=%{http_code} total=%{time_total}s\n"`: sau khi xong, in mã HTTP và **tổng thời gian** của request.
- Lệnh đầu gặp API ở 0 pod nên chờ pod khởi động (cold start); lệnh thứ hai gặp API đã chạy nên rất nhanh. So hai con số là thấy cold start.

**Người xem thấy:** **Terminal 1** cho thấy pod `api` xuất hiện rồi chạy; request đầu chậm hơn các request sau (số đo ở [TEST_LOG.md](TEST_LOG.md) bài 2), request sau gần như tức thì.
Sau 5 phút không dùng, pod tự biến mất (nhìn **Terminal 1**).

**Phương án nhanh (không phải chờ 5 phút), chạy ở Terminal 4 trước buổi demo:**
```bash
helm upgrade api charts/app -n demo --reuse-values --set scaling.http.scaledownPeriod=10 --force-conflicts
helm upgrade worker charts/app -n demo --reuse-values --set scaling.sqs.cooldownPeriod=10
helm upgrade fe charts/app -n demo --reuse-values --set scaling.hpa.scaleDownStabilizationSeconds=10
# ... demo xong thì trả lại giá trị thật:
helm upgrade api charts/app -n demo --reuse-values --set scaling.http.scaledownPeriod=300 --force-conflicts
helm upgrade worker charts/app -n demo --reuse-values --set scaling.sqs.cooldownPeriod=60
helm upgrade fe charts/app -n demo --reuse-values --set scaling.hpa.scaleDownStabilizationSeconds=300
```
- `helm upgrade <tên> charts/app`: cập nhật bản cài đặt đang chạy bằng chart chung.
- `--reuse-values`: giữ nguyên mọi cấu hình cũ, chỉ đổi giá trị ghi sau `--set`. **Quan trọng:** đừng dùng `scripts/deploy-apps.sh` ở đây, vì nó đặt lại frontend về image cũ.
- `scaledownPeriod`: số giây API không có request thì về 0 pod. `cooldownPeriod`: số giây hàng đợi rỗng thì worker về 0. `scaleDownStabilizationSeconds`: số giây HPA chờ trước khi giảm pod frontend.
- `--force-conflicts` (chỉ ở `api`): cho phép Helm ghi đè trường mà KEDA cũng sửa; thiếu nó Helm báo xung đột.
Nói rõ với khán giả là đã hạ thời gian chờ để demo cho nhanh.

**Trung thực khi trả lời câu hỏi:** xem số đo bài 2 trong TEST_LOG.md; chỉ nói "luôn dưới 3 giây" nếu mọi lượt đo đều dưới 3 giây.

## 4. Karpenter: thêm và bớt node (3 phút)

**Nói:** khi có pod không còn chỗ, Karpenter tạo node spot mới đúng cỡ; hết tải thì dọn bớt.

**Bước 1, tạo 5 pod xin 1500m CPU mỗi pod, chạy ở Terminal 4** (lớn hơn chỗ trống trên các node có sẵn, nên Karpenter phải tạo thêm node):
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
time kubectl -n tier-test rollout status deployment/karpenter-probe --timeout=240s   # lệnh này CHỜ đến khi đủ 5 pod, in ra thời gian
```

- `kubectl create namespace tier-test`: tạo vùng riêng cho bài thử, để dọn một lần bằng cách xoá cả vùng.
- Khối `cat <<'EOF' | kubectl apply -f -`: tạo một Deployment `karpenter-probe` gồm **5 pod**, mỗi pod xin **1500m CPU** (nhiều hơn chỗ trống trên các node có sẵn). Pod chạy `pause` (container rỗng, không làm gì), chỉ để chiếm chỗ. `nodeSelector` ép chúng vào pool `default`.
- `kubectl ... rollout status`: **chờ** đến khi đủ 5 pod sẵn sàng; trong lúc chờ Karpenter tạo node mới. `time` in tổng thời gian chờ.
Nhìn **Terminal 2** trong lúc chờ: node mới xuất hiện (thời gian cho 5 pod sẵn sàng: xem TEST_LOG.md bài 4).

**Bước 2, chỉ chạy SAU khi bước 1 báo xong** (nếu dán cả hai bước một lượt thì pod bị xoá trước khi Karpenter kịp tạo node và không có gì để xem):
```bash
kubectl delete namespace tier-test               # rồi chờ vài phút nhìn Terminal 2: node thừa được dọn
```

Xoá cả namespace thì 5 pod biến mất, các node không còn việc. Karpenter đợi khoảng 1 phút rồi dọn node thừa (có thể dồn pod còn lại sang node khác). Nhìn **Terminal 2** để thấy node giảm.

**Người xem thấy (ở Terminal 2):** số node pool `default` tăng thêm (thường 1 đến 3 node tuỳ chỗ trống lúc đó), sau đó giảm về 2 node dự phòng.
Số chính xác phụ thuộc vào loại máy Karpenter chọn (có khi chọn máy 4 vCPU, chứa được nhiều pod hơn).

**Bằng chứng log, chạy ở Terminal 4** (Karpenter chạy 2 pod, chỉ pod leader có sự kiện, nên phải đọc cả hai bằng nhãn; không dùng `logs deploy/karpenter`). Mỗi dòng: giờ UTC, sự kiện, tên NodeClaim (`default-...` hoặc `worker-...` cho biết pool). Sự kiện `default-` mới của bài thử chỉ có sau khi chạy các lệnh tạo 3 pod ở trên:
```bash
kubectl -n karpenter logs -l app.kubernetes.io/name=karpenter --tail=-1 \
  | grep -E '"created nodeclaim"|"deleted nodeclaim"' \
  | sed -E 's/.*"time":"([^"]*)".*"message":"([^"]*)".*"NodeClaim":\{"name":"([^"]*)"\}.*/\1  \2  \3/'
```

- `kubectl logs -l app.kubernetes.io/name=karpenter --tail=-1`: đọc toàn bộ nhật ký của **cả hai** pod Karpenter (chọn bằng nhãn).
- `grep -E '"created nodeclaim"|"deleted nodeclaim"'`: giữ các dòng ghi lúc Karpenter tạo hoặc xoá một node.
- `sed -E 's/.../\1  \2  \3/'`: rút từ dòng JSON dài ra ba trường: giờ, sự kiện, tên NodeClaim.

## 5. KEDA với SQS: worker từ 0 lên 20 (3 phút)

**Nói:** worker mặc định 0 pod. Có message trong hàng đợi thì KEDA bật worker, tối đa 20, mỗi Job xử lý tối đa 10 message.

**Chạy ở Terminal 4** (nhìn Terminal 1, 2, 3 trong lúc chạy):
```bash
for i in 1 2 3 4 5; do curl -s -XPOST -H "Host: api.demo.local" "http://$ALB/jobs" -d '{"count":100}'; echo; done
kubectl -n demo get pods -l app.kubernetes.io/name=worker -w
```

- Vòng `for ... curl -XPOST .../jobs -d '{"count":100}'`: gọi `POST /jobs` 5 lần; mỗi lần API đẩy 100 message vào hàng đợi SQS (100 là mức tối đa một lần). Tổng **500 message**. `-XPOST` là phương thức POST, `-d` là nội dung gửi, `echo` xuống dòng để dễ đọc.
- `kubectl get pods -l ... worker -w`: theo dõi riêng các pod `worker`; `-w` (watch) in thêm một dòng mỗi khi pod đổi trạng thái. Nhiều dòng là bình thường (16 đến 20 pod, mỗi pod đổi trạng thái vài lần). Muốn gọn, dùng lệnh đếm theo trạng thái: `watch -n 2 'kubectl -n demo get pods -l app.kubernetes.io/name=worker --no-headers | awk "{print \$3}" | sort | uniq -c'`.

**Người xem thấy:** **Terminal 3** hiển thị hàng đợi vọt lên 500 rồi về 0; **Terminal 1** cho thấy worker đi từ 0 lên 20 pod;
**Terminal 2** cho thấy Karpenter tạo thêm node `worker` (khoảng 3 node); sau đó hết việc, worker về 0 (khoảng 2,5 phút kể từ lúc gửi).

**Kiểm tra không mất message, chạy ở Terminal 4** (hàng đợi lỗi phải bằng 0):
```bash
aws sqs get-queue-attributes --region ap-southeast-1 --queue-url "$(terraform -chdir=terraform output -raw dlq_url)" \
  --attribute-names ApproximateNumberOfMessages --query Attributes --output text
```

Đọc số message trong **hàng đợi lỗi** (DLQ). Message nào worker xử lý hỏng nhiều lần sẽ bị chuyển vào đó. Kết quả **0** nghĩa là cả 500 message đều xử lý xong, không mất cái nào.

## 6. HPA: thêm pod khi CPU cao (3 phút)

**Nói:** HPA của frontend thêm pod khi CPU vượt 50%. Frontend dùng quá ít CPU nên khó kích hoạt, vì vậy demo bằng một bản sao
của API tốn CPU có chủ đích (endpoint `/burn`).

**Chạy ở Terminal 4:**
```bash
ACCOUNT=$(aws sts get-caller-identity --query Account --output text)
kubectl create namespace hpa-demo
helm upgrade --install hpa-demo charts/app -n hpa-demo \
  --set image.repository=$ACCOUNT.dkr.ecr.ap-southeast-1.amazonaws.com/demo-api --set image.tag=0.3.0 --set arch=auto \
  --set containerPort=8080 --set probe.httpPath=/healthz --set probe.startup.enabled=false \
  --set scaling.mode=hpa --set scaling.hpa.min=1 --set scaling.hpa.max=3 --set scaling.hpa.cpu=50 \
  --set scaling.hpa.scaleDownStabilizationSeconds=60 --set ingress.enabled=false \
  --set resources.requests.cpu=250m --set resources.requests.memory=64Mi --set resources.limits.memory=256Mi \
  --set nodePool=default --set serviceAccountName=default --set serviceAccount.create=false --wait
kubectl -n hpa-demo run load --image=curlimages/curl:8.10.1 --restart=Never --command -- sh -c \
  'end=$(( $(date +%s) + 150 )); while [ $(date +%s) -lt $end ]; do curl -s -m 10 "http://hpa-demo/burn?ms=1000" >/dev/null; sleep 2; done'
```

- `ACCOUNT=$(aws sts get-caller-identity ...)`: lấy mã tài khoản AWS để ghép địa chỉ kho image.
- `kubectl create namespace hpa-demo`: vùng riêng cho bài thử.
- `helm upgrade --install hpa-demo charts/app ...`: cài một **bản sao của API** bằng chart chung. Các tuỳ chọn chính: `scaling.mode=hpa` (dùng HPA), `min=1`/`max=3` (1 đến 3 pod), `cpu=50` (mục tiêu CPU 50% so với mức pod xin), `requests.cpu=250m` (mỗi pod xin 250m CPU), `scaleDownStabilizationSeconds=60` (chờ 60 giây rồi mới giảm), `ingress.enabled=false` (không mở ra ngoài, chỉ dùng trong cluster), `--wait` (chờ cài xong).
- `kubectl run load ... curlimages/curl`: chạy một pod tạm tên `load` bên trong cluster, gọi `GET /burn?ms=1000` (endpoint đốt CPU 1 giây) rồi nghỉ 2 giây, lặp trong 150 giây. Tổng khoảng 330m CPU.
- Pod `load` gọi thẳng `http://hpa-demo/...` (tên Service trong cluster), không qua ALB.

**Theo dõi, chạy ở một terminal phụ (Terminal 5) hoặc ở Terminal 4 sau lệnh trên:**
```bash
watch -n 3 'kubectl -n hpa-demo get hpa,pods'
```

Làm mới mỗi 3 giây: cột `TARGETS` của HPA là CPU hiện tại so với mục tiêu (ví dụ `80%/50%`), cột `REPLICAS` là số pod; danh sách pod bên dưới cho thấy pod mới xuất hiện.
(Terminal 1 chỉ theo dõi namespace `demo`, không thấy `hpa-demo`, nên cần lệnh này; nhớ chạy `export KUBECONFIG=/tmp/demo.kubeconfig` trước ở terminal phụ.)

Tải này nhẹ có chủ đích: một vòng lặp đốt CPU 1 giây rồi nghỉ 2 giây (khoảng 330m CPU), vừa đủ để HPA dừng ở **3 pod** thay vì chạm trần.

**Người xem thấy:** CPU vượt ngưỡng 50% so với mức pod xin, HPA thêm pod từng bước (mỗi bước có sự kiện `SuccessfulRescale`), sau đó CPU rơi về quanh mức mục tiêu 50% và số pod giữ nguyên 3.
Sau khi hết tải (150 giây) và hết cửa sổ ổn định 60 giây, HPA giảm về 1.

**Dọn sau demo, chạy ở Terminal 4:**
```bash
helm -n hpa-demo uninstall hpa-demo; kubectl delete namespace hpa-demo
```

Gỡ bản sao `hpa-demo` rồi xoá namespace, trả cluster về như cũ.

## 7. Chart Helm dùng được với image bất kỳ (3 phút)

**Nói:** cùng một chart `charts/app` chạy được image của bên thứ ba, chỉ đổi image, cổng và tham số. Script cài 3 image khác nhau
(`nginx`, `hashicorp/http-echo`, `hello-app` chỉ có amd64), gọi từng cái bằng HTTP trong cụm, rồi làm một phép thử đối chứng:
ép image chỉ có amd64 chạy trên node arm64 thì **phải thất bại**.

**Chạy ở Terminal 4:**
```bash
scripts/test-plug-image.sh
```

- Mỗi image được cài bằng `helm upgrade --install` với cùng chart, chỉ khác `image.repository`, `image.tag`, `containerPort`, `args`.
- `scripts/detect-arch.sh` đọc manifest của image để biết nó có amd64, arm64 hay cả hai, rồi đặt `arch` cho đúng (`auto`, `amd64`, `arm64`).
- Sau khi pod Ready, script gọi HTTP từ một pod `curl` trong cụm và in `[ ok ]` hoặc `[FAIL]` cho từng image.
- Phép thử đối chứng: `hello-app` bị ép `arch=arm64`; pod phải **không** Ready (lỗi `exec format error`). Có bước này để chứng minh việc chọn kiến trúc có tác dụng.
- Cuối script xoá namespace `demo-plug`.

**Người xem thấy:** ở **Terminal 4** ba dòng `[ ok ]` cho ba image, một dòng `[ ok ] wrong-arch pod is not Ready`, và dòng cuối
`RESULT: all checks passed`. **Terminal 1 và 2** cho thấy các pod `plug-*` xuất hiện rồi biến mất.
Giới hạn cần nói thật: chỉ thử dịch vụ HTTP; chưa thử image dùng volume, TCP thuần hay gRPC.

## 8. Mất một node spot đột ngột (3 phút)

**Nói:** toàn bộ node là spot nên có thể bị AWS thu hồi. Bài này tắt hẳn máy đang chạy pod frontend (không báo trước) trong khi
liên tục gửi request qua ALB, để đo người dùng thấy gì và cụm tự hồi phục sau bao lâu.

**Chạy ở Terminal 4** (mất khoảng 1,5 đến 3 phút, dừng ngay khi cụm hồi phục, **tắt một EC2 thật**, chỉ chạy khi cụm đang ổn định):
```bash
scripts/spot-loss-test.sh
```

- Script chọn node đang chạy pod frontend, lấy mã instance, rồi chạy nền hai vòng `curl` mỗi 0,5 giây: một vào frontend `/`, một vào API `/healthz`.
- Sau 5 giây chạy `aws ec2 terminate-instances` lên instance đó (mất máy đột ngột, không có cảnh báo 2 phút của spot).
- Mỗi 3 giây ghi số node Ready, pod frontend, pod API, pod giữ chỗ; dừng khi cụm hồi phục 3 lần liên tiếp (tối đa 4 phút), rồi lưu nhật ký Karpenter.
- Kết quả lưu ở `docs/evidence/spot-loss/`; `summary.txt` đếm số request không trả HTTP 200 theo từng đường.

**Số đo:** xem TEST_LOG.md bài 9 (`docs/evidence/spot-loss/`) sau khi đo.

**Người xem thấy:** **Terminal 2** node mất đi rồi Karpenter tạo node thay; **Terminal 1** pod frontend chạy lại ở node khác.
Frontend chỉ có 1 pod nên **có thể có một khoảng ngắn ALB trả lỗi 502/503** trước khi pod mới sẵn sàng; hãy đọc `summary.txt` và
`probe.csv` để nói đúng số lượng và độ dài khoảng lỗi, không nói "không có gián đoạn". Aurora và SQS không nằm trên node nên dữ liệu không mất.

## 9. 200 user cùng lúc (3 phút)

**Nói:** k6 giả lập 200 user vào trang web và API qua ALB. Số đo: xem TEST_LOG.md bài 7.
Cơ sở dữ liệu gần chạm giới hạn 2 ACU ở bài nặng nhất, nên 200 user là gần trần của cấu hình hiện tại.

**Chạy ở Terminal 4** (bản rút gọn 2 phút để khỏi chờ lâu):
```bash
docker run --rm -v "$PWD/scripts:/s:ro" grafana/k6 run -e BASE_URL=http://$ALB -e VUS=200 -e DURATION=2m -e WITH_SLOW=1 -e WITH_DB=1 -e WITH_DOMAIN=1 /s/loadtest.js
```

- `docker run --rm grafana/k6 run`: chạy công cụ k6 trong một container tạm (xoá khi xong, `--rm`), nên máy không cần cài k6.
- `-v "$PWD/scripts:/s:ro"`: gắn thư mục `scripts/` vào container ở đường dẫn `/s` (chỉ đọc), để k6 đọc được `loadtest.js`.
- `-e BASE_URL=http://$ALB`: địa chỉ mà người dùng ảo truy cập. `-e VUS=200`: **200 người dùng ảo** cùng lúc. `-e DURATION=2m`: chạy 2 phút. `-e WITH_SLOW=1`: mỗi người dùng ảo còn gọi thêm endpoint chờ 2 giây (lý do ở dưới).

`WITH_SLOW=1` làm mỗi người dùng ảo còn gọi một endpoint chờ 2 giây. Cần như vậy vì request nhanh chỉ tạo khoảng 10 request đồng thời, xa mức 100 mà bộ co giãn HTTP đòi để thêm pod.
Nên **đảm bảo API đang có 1 pod chạy sẵn** trước khi bắt đầu (gọi API một lần), để thấy tăng do tải chứ không do đợt cold start.

**Xem ACU của Aurora** (chạy ở **Terminal 4** ngay sau khi k6 kết thúc; CloudWatch ghi mỗi phút một điểm):
```bash
aws cloudwatch get-metric-statistics --region ap-southeast-1 --namespace AWS/RDS \
  --metric-name ServerlessDatabaseCapacity --dimensions Name=DBClusterIdentifier,Value=demo-aurora \
  --start-time $(date -u -d '-8 minutes' +%FT%TZ) --end-time $(date -u +%FT%TZ) \
  --period 60 --statistics Maximum --query 'sort_by(Datapoints,&Timestamp)[].[Timestamp,Maximum]' --output text
```

- `ServerlessDatabaseCapacity` là số ACU Aurora đang dùng. Giá trị phải nằm trong khoảng **0,5 đến 2,0** (giới hạn đã đặt).
- `--start-time $(date -u -d '-8 minutes' ...)` lấy 8 phút gần nhất, đủ phủ bài k6 2 phút cộng thời gian chờ số liệu; `--period 60` mỗi điểm một phút.
- `--statistics Maximum` lấy đỉnh trong từng phút. `sort_by(...)` xếp theo thời gian, `--output text` in mỗi dòng một phút.
- Muốn xem sống: mở thêm một tab, bọc lệnh trong `watch -n 30 '...'` trước khi chạy k6 (CloudWatch có độ trễ khoảng 1 phút).

**Người xem thấy ở lệnh trên:** ACU tăng từ khoảng 0,5 lên gần hoặc chạm 2,0 trong lúc có tải rồi hạ dần sau đó. Chạm trần 2,0 nghĩa là database là điểm nghẽn tiếp theo.

**Người xem thấy:** **Terminal 1** cho thấy API lên 2 pod sau khoảng 15 giây (xem số pod đo được ở TEST_LOG.md bài 7), frontend có thể lên 2 pod; kết quả k6 ở **Terminal 4** cuối bài có
`http_req_failed` và p95 để đối chiếu với TEST_LOG.md.

## 10. Tổng kết (1 phút)

| Yêu cầu | Trạng thái |
|---|---|
| API Go 32 endpoint, frontend React, ALB | Đạt |
| Ba tầng co giãn (Karpenter, KEDA với SQS, HPA) | Đạt, có bằng chứng |
| API scale to zero, cold start dưới 3 giây | Xem TEST_LOG.md bài 1 và 2 |
| 200 user cùng lúc | Đạt, database gần hết dư địa |
| Toàn bộ node spot, amd64 và arm64 | Đạt |
| Chart dùng được với image bất kỳ | Đạt với dịch vụ HTTP (phần 7) |
| Chịu mất node spot | Xem số đo ở phần 8 |
| Aurora 0,5 đến 2 ACU | Đạt, đỉnh xem ở phần 9 |
| Route 53 | **Chưa làm** (chưa có tên miền) |

## 11. Câu hỏi hay gặp

- **"Sao không luôn giữ 1 pod API cho nhanh?"** Yêu cầu là scale to zero. Đặt `min: 1` sẽ hết cold start nhưng mất tính tiết kiệm.
- **"Spot bị thu hồi thì sao?"** Xem phần 8. Số đo: xem TEST_LOG.md bài 9. Chưa thử mất cả hai node hệ thống cùng lúc.
- **"Route 53 đâu?"** Chưa có tên miền; hiện dùng DNS của ALB. Có tên miền thì gắn chỉ mất một bước Terraform.
- **"Sao cold start 2 đến 3 giây?"** Khoảng 1 giây là pod khởi động, phần còn lại là KEDA phát hiện request, interceptor chuyển tiếp và mạng.
  Đã bỏ startupProbe của API (số đo: TEST_LOG.md bài 2).

## 12. Dọn dẹp sau demo (bắt buộc, để ngừng tốn tiền)

**Chạy ở Terminal 4:**
```bash
scripts/down.sh --dry-run           # xem sẽ xoá gì (không xoá)
scripts/down.sh                     # huỷ toàn bộ; gõ "demo" để xác nhận. Aurora và dữ liệu bị xoá vĩnh viễn
scripts/down.sh --dry-run           # kiểm tra còn sót gì (mọi dòng phải là none hoặc 0)
```

- `down.sh --dry-run`: chỉ **liệt kê** tài nguyên đang tồn tại và các bước sẽ làm, không xoá gì.
- `down.sh`: **huỷ toàn bộ**: gỡ ứng dụng (để ALB biến mất), tắt các node Karpenter, rồi `terraform destroy` (Aurora, SQS, EKS, NAT, VPC, ECR, IAM). Phải gõ `demo` để xác nhận. Mất khoảng 15 đến 25 phút; Aurora và dữ liệu bị xoá vĩnh viễn.
- Nếu bạn đã hạ thời gian chờ ở phần 3 mà **không** huỷ hạ tầng, nhớ chạy các lệnh `helm upgrade` trả giá trị thật ở phần 3. Nếu sắp huỷ thì không cần.

Sau khi huỷ, 3 terminal theo dõi sẽ báo lỗi kết nối; bấm Ctrl+C để đóng chúng.

## Các nút trên giao diện

| Nút / ô | Làm gì |
|---|---|
| **Load data** | Tải mọi dữ liệu đang hiển thị (tổng quan, vị trí, ứng viên, hồ sơ). Cũng là lệnh đầu tiên đánh thức API nếu nó đang ở 0 pod. |
| **Add sample data** | Nạp dữ liệu mẫu: 20 ứng viên, 5 vị trí, 40 hồ sơ. Mỗi lần bấm thêm một bộ mới. |
| **Add position** | Thêm vị trí tuyển dụng từ ba ô Title, Department, Location (cả ba bắt buộc). |
| **Close** (ở mỗi vị trí đang mở) | Đóng vị trí (`open` sang `closed`). Vị trí đã đóng không còn trong danh sách chọn khi tạo hồ sơ. |
| **Add applicant** | Thêm ứng viên từ Name, Email (bắt buộc) và Skills (tuỳ chọn, cách nhau dấu phẩy). |
| **Apply** | Tạo hồ sơ nối một ứng viên với một vị trí đang mở. Hồ sơ mới ở trạng thái `applied`. |
| Ô chọn trạng thái (mỗi hồ sơ) | Đổi trạng thái hồ sơ theo quy trình hợp lệ (`applied` sang `screening`, `interview`, `offer`, `hired`; `rejected` hoặc `withdrawn` bất cứ lúc nào trước bước cuối). Chọn bước nhảy không hợp lệ thì API từ chối và trang hiện lỗi đỏ. |
| **Score** | Đẩy việc chấm điểm sàng lọc vào hàng đợi SQS. Worker bật từ 0 pod, ghi điểm về sau vài chục giây. Điểm là giả lập (công thức cố định theo mã hồ sơ). |
| **Delete** (vị trí, ứng viên, hồ sơ) | Xoá bản ghi sau khi xác nhận. Xoá vị trí hoặc ứng viên cũng xoá các hồ sơ liên quan. |

Đầu trang có **hai khung đo thời gian riêng**:
- **API response time** (bên trái): thời gian API trả lời mỗi thao tác, kèm nhãn Cold start hoặc Warm.
- **Worker scoring time** (bên phải): bấm Score thì đồng hồ chạy từ lúc bấm đến lúc điểm xuất hiện (chờ hàng đợi, khởi động worker nếu đang 0 pod, chấm điểm). Không gồm lời gọi API.

Mọi thao tác đều ghi vào bảng **Activity** ở cuối trang, cột **Source** cho biết là `API (cold start)`, `API (warm)` hay `Worker`.

## Giám sát và log (Prometheus, Grafana, Loki)

Chạy trên hai node hệ thống, không mở ra internet. Mở bằng port-forward ở một terminal riêng:
```bash
kubectl -n monitoring port-forward svc/grafana 3000:80
kubectl -n monitoring get secret grafana -o jsonpath='{.data.admin-password}' | base64 -d; echo
```
Mở http://localhost:3000, đăng nhập user `admin`.

- **Explore > Prometheus:** số liệu pod, node, KEDA (`keda_scaler_metrics_value`, `keda_scaler_active`), Karpenter (`karpenter_nodes_*`, `karpenter_nodeclaims_*`), HPA (`kube_horizontalpodautoscaler_*`).
- **Explore > Loki:** log của pod ứng dụng, KEDA, Karpenter và sự kiện Kubernetes. Ví dụ LogQL: `{namespace="demo", app="worker"}`, `{namespace="karpenter"} |= "nodeclaim"`, `{job="loki.source.kubernetes_events"}`.
- Log nằm trong bucket S3 `demo-logs-<mã tài khoản>`, tự xoá sau 7 ngày, nên vẫn tìm lại được log của pod API và worker đã về 0 pod. Loki ghi lên S3 theo lô, nên log rất mới có thể chưa lên S3 trong lúc pod Loki còn giữ.
