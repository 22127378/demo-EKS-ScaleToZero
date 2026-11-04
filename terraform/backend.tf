# State lives in S3 so it survives a lost laptop and can be locked. The bucket is created by
# scripts/bootstrap-state.sh and is deliberately NOT managed here: `terraform destroy` must never delete
# the state it is running from. The settings are in backend.hcl:
#   terraform init -backend-config=backend.hcl
terraform {
  backend "s3" {}
}
